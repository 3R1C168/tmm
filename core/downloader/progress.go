package downloader

import (
	"sync"
	"time"

	"go.uber.org/atomic"
)

type Progress interface {
	OnAdd(elem Elem)
	OnDownload(elem Elem, state ProgressState)
	OnDone(elem Elem, err error)
	// TODO: OnLog to log something that is not an error but should be sent to the user
}

type ProgressState struct {
	Downloaded int64
	Total      int64
}

// flushInterval is how often the resume state is persisted, so even a hard
// kill loses at most this much progress.
const flushInterval = 5 * time.Second

// writeAt wrapper for file to use progress bar.
//
// It also feeds every written piece to the resume State (when present) and
// flushes it periodically, so an interrupted download can be resumed later.
//
// do not need mutex for the underlying file because gotd has use syncio.WriteAt
type writeAt struct {
	elem     Elem
	progress Progress
	partSize int
	state    *State

	downloaded *atomic.Int64

	flushMu  sync.Mutex
	lastFlush time.Time
}

func newWriteAt(elem Elem, progress Progress, partSize int) *writeAt {
	return &writeAt{
		elem:       elem,
		progress:   progress,
		partSize:   partSize,
		state:      elem.Resume(),
		downloaded: atomic.NewInt64(0),
		lastFlush:  time.Now(),
	}
}

func (w *writeAt) WriteAt(p []byte, off int64) (int, error) {
	at, err := w.elem.To().WriteAt(p, off)
	if err != nil {
		return 0, err
	}

	if w.state != nil {
		w.state.Mark(off, at)
		w.maybeFlush()
	}

	// some small files may finish too fast, terminal history may not be overwritten
	// this is just a simple way to avoid the problem
	if at < w.partSize { //  last part(every file only exec once)
		time.Sleep(time.Millisecond * 200) // to ensure the progress render next time
	}
	w.progress.OnDownload(w.elem, ProgressState{
		Downloaded: w.downloaded.Add(int64(at)),
		Total:      w.elem.File().Size(),
	})
	return at, nil
}

func (w *writeAt) maybeFlush() {
	w.flushMu.Lock()
	defer w.flushMu.Unlock()
	if time.Since(w.lastFlush) < flushInterval {
		return
	}
	// best-effort; a failed flush only loses a bit of resume progress
	_ = w.state.Flush()
	w.lastFlush = time.Now()
}
