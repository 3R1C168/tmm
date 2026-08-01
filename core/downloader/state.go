package downloader

import (
	"encoding/base64"
	"encoding/json"
	"math/bits"
	"os"
	"sync"

	"github.com/go-faster/errors"
)

// stateExt is the suffix of the per-file control file that records which
// pieces of a partial download have already been written, so an interrupted
// download can be resumed block-by-block instead of from scratch.
const stateExt = ".tdlstate"

// stateVersion is the on-disk schema version of the control file.
const stateVersion = 1

// State tracks per-piece completion of a single file download and persists it
// to a control file next to the data file (like aria2's .aria2 file).
//
// Pieces are partSize bytes each. The bitmap is the source of truth: the data
// file's size alone is not reliable because pieces may be written out of order
// by concurrent workers, leaving zero-filled holes.
//
// A piece is only marked complete once it has been FULLY covered by written
// ranges. Callers may write sub-piece blocks (e.g. the streamer fetches 512KiB
// halves of a 1MiB piece, possibly out of order or twice); partial coverage is
// tracked per piece so a half-written piece is never reported as done (which
// would otherwise serve zero-filled holes to the reader).
type State struct {
	mu sync.Mutex

	path     string // path of the control file
	size     int64  // total file size in bytes
	partSize int    // bytes per piece
	bits     []uint64
	done     int // number of completed pieces
	dirty    bool

	// partial tracks byte coverage of pieces that are written but not yet
	// complete. Key is the piece index; entry is dropped when the piece
	// becomes fully covered and its bit is set.
	partial map[int]*intervalSet
}

type stateFile struct {
	Version  int    `json:"version"`
	Size     int64  `json:"size"`
	PartSize int    `json:"part_size"`
	Bitmap   string `json:"bitmap"` // base64-encoded little-endian []uint64
}

func numPieces(size int64, partSize int) int {
	return int((size + int64(partSize) - 1) / int64(partSize))
}

// NewState creates a State for a fresh download (no pieces completed yet).
// The control file is only written on Flush.
func NewState(path string, size int64, partSize int) *State {
	n := numPieces(size, partSize)
	return &State{
		path:     path,
		size:     size,
		partSize: partSize,
		bits:     make([]uint64, (n+63)/64),
	}
}

// StatePath returns the control file path for a given data file path.
func StatePath(dataPath string) string {
	return dataPath + stateExt
}

// LoadState reads the control file for dataPath. It returns (nil, nil) when no
// control file exists. It returns an error when the file exists but is corrupt
// or does not match the expected size/partSize, in which case the caller should
// restart the download from scratch.
func LoadState(dataPath string, size int64, partSize int) (*State, error) {
	path := StatePath(dataPath)

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "read state")
	}

	var sf stateFile
	if err = json.Unmarshal(raw, &sf); err != nil {
		return nil, errors.Wrap(err, "decode state")
	}

	if sf.Version != stateVersion {
		return nil, errors.Errorf("unsupported state version %d", sf.Version)
	}
	if sf.Size != size || sf.PartSize != partSize {
		return nil, errors.Errorf("state mismatch (size %d/%d, part %d/%d)",
			sf.Size, size, sf.PartSize, partSize)
	}

	bm, err := base64.StdEncoding.DecodeString(sf.Bitmap)
	if err != nil {
		return nil, errors.Wrap(err, "decode bitmap")
	}

	n := numPieces(size, partSize)
	words := (n + 63) / 64
	if len(bm) != words*8 {
		return nil, errors.Errorf("bitmap length %d, want %d", len(bm), words*8)
	}

	s := &State{
		path:     path,
		size:     size,
		partSize: partSize,
		bits:     make([]uint64, words),
	}
	for i := range s.bits {
		s.bits[i] = uint64(bm[i*8]) |
			uint64(bm[i*8+1])<<8 |
			uint64(bm[i*8+2])<<16 |
			uint64(bm[i*8+3])<<24 |
			uint64(bm[i*8+4])<<32 |
			uint64(bm[i*8+5])<<40 |
			uint64(bm[i*8+6])<<48 |
			uint64(bm[i*8+7])<<56
	}
	for _, w := range s.bits {
		s.done += bits.OnesCount64(w)
	}

	return s, nil
}

// Mark records the byte range [off, off+len) as written. A piece is marked
// complete only once the union of all written ranges fully covers it, so
// partial sub-piece writes and duplicate writes are both safe.
func (s *State) Mark(off int64, n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	end := off + int64(n) // exclusive
	first := int(off / int64(s.partSize))
	last := int((end - 1) / int64(s.partSize))
	for p := first; p <= last; p++ {
		w := uint(p) / 64
		b := uint(p) % 64
		if s.bits[w]&(1<<b) != 0 {
			continue // already complete
		}
		pStart := int64(p) * int64(s.partSize)
		pEnd := pStart + int64(s.partSize)
		if pEnd > s.size {
			pEnd = s.size
		}
		// coverage contributed by this write, clamped to the piece
		cStart := off
		if cStart < pStart {
			cStart = pStart
		}
		cEnd := end
		if cEnd > pEnd {
			cEnd = pEnd
		}
		if s.partial == nil {
			s.partial = make(map[int]*intervalSet)
		}
		iv := s.partial[p]
		if iv == nil {
			iv = &intervalSet{}
			s.partial[p] = iv
		}
		iv.add(cStart, cEnd)
		if iv.covers(pStart, pEnd) {
			s.bits[w] |= 1 << b
			s.done++
			delete(s.partial, p)
		}
	}
	s.dirty = true
}

// Complete reports whether every piece has been written.
func (s *State) Complete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done == numPieces(s.size, s.partSize)
}

// markMissingFrom 把从 offset（含）开始的 piece 全部标记为未完成，
// 让后续 Missing() 返回这些 piece 以便重新下载。
// 用于「磁盘实际字节数 < 声明 size」时强制补齐尾部——弥补 state 与磁盘
// 不同步、以及 gotd plain 模式提前退出导致的缺块。
func (s *State) markMissingFrom(offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	startPiece := int(offset / int64(s.partSize))
	total := numPieces(s.size, s.partSize)
	for p := startPiece; p < total; p++ {
		w := uint(p) / 64
		b := uint(p) % 64
		if s.bits[w]&(1<<b) != 0 {
			s.bits[w] &^= 1 << b // 清除完成位
			s.done--
		}
		delete(s.partial, p)
	}
	s.dirty = true
}

// pieceRange is a half-open byte range [Off, End) of one piece.
type pieceRange struct {
	Off int64
	End int64
}

// Missing returns the byte ranges of all incomplete pieces, in order.
func (s *State) Missing() []pieceRange {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := numPieces(s.size, s.partSize)
	out := make([]pieceRange, 0, n-s.done)
	for p := 0; p < n; p++ {
		if s.bits[uint(p)/64]&(1<<(uint(p)%64)) != 0 {
			continue
		}
		off := int64(p) * int64(s.partSize)
		end := off + int64(s.partSize)
		if end > s.size {
			end = s.size
		}
		out = append(out, pieceRange{Off: off, End: end})
	}
	return out
}

// Flush writes the current bitmap to the control file if it changed since the
// last flush. The write is atomic (temp file + rename) so a crash never
// leaves a half-written control file behind. It is a no-op when nothing new
// was marked.
func (s *State) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}

	bm := make([]byte, len(s.bits)*8)
	for i, w := range s.bits {
		bm[i*8] = byte(w)
		bm[i*8+1] = byte(w >> 8)
		bm[i*8+2] = byte(w >> 16)
		bm[i*8+3] = byte(w >> 24)
		bm[i*8+4] = byte(w >> 32)
		bm[i*8+5] = byte(w >> 40)
		bm[i*8+6] = byte(w >> 48)
		bm[i*8+7] = byte(w >> 56)
	}

	sf := stateFile{
		Version:  stateVersion,
		Size:     s.size,
		PartSize: s.partSize,
		Bitmap:   base64.StdEncoding.EncodeToString(bm),
	}

	raw, err := json.Marshal(sf)
	if err != nil {
		return errors.Wrap(err, "encode state")
	}

	tmp := s.path + ".tmp"
	if err = os.WriteFile(tmp, raw, 0o644); err != nil {
		return errors.Wrap(err, "write state")
	}
	if err = os.Rename(tmp, s.path); err != nil {
		return errors.Wrap(err, "rename state")
	}

	s.dirty = false
	return nil
}

// Remove deletes the control file. Called after the download completes and the
// data file has been finalized.
func (s *State) Remove() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "remove state")
	}
	return nil
}

// --- interval set (sorted, non-overlapping half-open ranges) ---

type interval struct{ start, end int64 }

// intervalSet keeps the union of added intervals so coverage queries are
// exact. Inserts merge overlapping/adjacent intervals.
type intervalSet struct {
	ranges []interval
}

func (s *intervalSet) add(start, end int64) {
	if start >= end {
		return
	}
	r := s.ranges
	// find insertion window: all intervals touching [start, end)
	i := 0
	for i < len(r) && r[i].end < start {
		i++
	}
	j := i
	for j < len(r) && r[j].start <= end {
		j++
	}
	merged := interval{start: start, end: end}
	if i < len(r) && r[i].start < merged.start {
		merged.start = r[i].start
	}
	if j > i && r[j-1].end > merged.end {
		merged.end = r[j-1].end
	}
	out := make([]interval, 0, len(r)-(j-i)+1)
	out = append(out, r[:i]...)
	out = append(out, merged)
	out = append(out, r[j:]...)
	s.ranges = out
}

// covers reports whether the union of intervals fully covers [start, end).
func (s *intervalSet) covers(start, end int64) bool {
	cur := start
	for _, r := range s.ranges {
		if r.start > cur {
			return false
		}
		if r.end > cur {
			cur = r.end
		}
		if cur >= end {
			return true
		}
	}
	return false
}
