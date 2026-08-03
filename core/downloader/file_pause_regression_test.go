package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/dcpool"
)

// TestFilePauseDoesNotCancelTask 回归验证 spec §2.1「文件级暂停不打断任务」：
//
// 场景：任务里有 2 个文件，第 1 个下载中被用户暂停（PauseCheck 返回非
// context.Canceled 的哨兵错误，即 server 的 errFilePaused）。修复前：
//   - 若 PauseCheck 返回 context.Canceled → downloader 把单文件暂停当成
//     「用户取消整任务」→ errgroup cancel wgctx → 其他文件全停、任务 StatusPaused；
//   - 若返回其他非 canceled 错误 → OnDone 收到 nil → 上层误判 FileDone。
//
// 本测试用假 dcpool.Pool + 假 MTProto invoker 完整跑通 downloader.Download：
//  1. file-a 下载中 PauseCheck 命中哨兵 → download 失败但**不取消任务**
//  2. 断言：Download 不返回错误、file-a 的 OnDone 收到哨兵（上层据此标 FilePaused）、
//     file-b 正常下载完成（OnDone 收 nil）
func TestFilePauseDoesNotCancelTask(t *testing.T) {
	// 假 MTProto invoker：两个文件都返回单块数据（len < partSize → last()），
	// PauseCheck 在 WriteAt 时拦截 file-a（volID==1）。
	var inv tg.Invoker = invokerFunc(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.UploadGetFileRequest)
		if !ok {
			return fmt.Errorf("unexpected request %T", input)
		}
		loc, ok := req.Location.(*tg.InputFileLocation)
		if !ok {
			return fmt.Errorf("unexpected location %T", req.Location)
		}
		box := output.(*tg.UploadFileBox)
		box.File = &tg.UploadFile{
			Type:  &tg.StorageFilePartial{},
			Mtime: 0,
			Bytes: []byte("abcd"), // len 4 < MaxPartSize → last()
		}
		_ = loc
		return nil
	})

	pool := &fakePool{inv: inv}
	pauseCheck := func(e Elem) error {
		loc := e.File().Location().(*tg.InputFileLocation)
		if loc.VolumeID == 1 { // file-a：用户手动暂停
			return errSentinelPause
		}
		return nil
	}

	var (
		mu       sync.Mutex
		done     []int64 // VolumeID 顺序
		doneErrs []error
	)
	progress := &recordingProgress{
		onDone: func(e Elem, err error) {
			mu.Lock()
			defer mu.Unlock()
			done = append(done, e.File().Location().(*tg.InputFileLocation).VolumeID)
			doneErrs = append(doneErrs, err)
		},
	}

	it := &staticIter{
		elems: []Elem{newFakeElem(1, "file-a"), newFakeElem(2, "file-b")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d := New(Options{Pool: pool, Threads: 8, Iter: it, Progress: progress, PauseCheck: pauseCheck})
	err := d.Download(ctx, 1)

	// 1) 整任务不被哨兵错误打断
	require.NoError(t, err, "file pause must not cancel the task")
	// 2) 两个文件都走到 OnDone
	mu.Lock()
	require.Len(t, done, 2)
	require.Len(t, doneErrs, 2)
	mu.Unlock()
	require.Equal(t, []int64{1, 2}, done)
	// 3) file-a 的 OnDone 收到哨兵错误（上层据此标 FilePaused），而不是 nil
	require.True(t, errors.Is(doneErrs[0], errSentinelPause), "file-a should be finalized with the pause error, got %v", doneErrs[0])
	// 4) file-b 正常完成：OnDone 收到 nil
	require.NoError(t, doneErrs[1], "file-b should complete cleanly, got %v", doneErrs[1])
}

// TestFileFailureDoesNotCancelTask 回归验证：单文件下载失败（非 paused、非
// context.Canceled）同样不取消整任务，错误传给 OnDone（上层据此标 FileFailed）。
func TestFileFailureDoesNotCancelTask(t *testing.T) {
	sentinel := errors.New("network failure")

	var inv tg.Invoker = invokerFunc(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.UploadGetFileRequest)
		if !ok {
			return fmt.Errorf("unexpected request %T", input)
		}
		loc := req.Location.(*tg.InputFileLocation)
		if loc.VolumeID == 1 { // file-a 失败
			return sentinel
		}
		box := output.(*tg.UploadFileBox)
		box.File = &tg.UploadFile{
			Type:  &tg.StorageFilePartial{},
			Mtime: 0,
			Bytes: []byte("abcd"),
		}
		return nil
	})

	pool := &fakePool{inv: inv}

	var (
		mu       sync.Mutex
		doneErrs []error
	)
	progress := &recordingProgress{
		onDone: func(e Elem, err error) {
			mu.Lock()
			defer mu.Unlock()
			doneErrs = append(doneErrs, err)
		},
	}

	it := &staticIter{
		elems: []Elem{newFakeElem(1, "file-a"), newFakeElem(2, "file-b")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d := New(Options{Pool: pool, Threads: 8, Iter: it, Progress: progress})
	err := d.Download(ctx, 1)

	require.NoError(t, err, "single-file failure must not cancel the task")
	mu.Lock()
	require.Len(t, doneErrs, 2)
	mu.Unlock()
	require.True(t, errors.Is(doneErrs[0], sentinel), "file-a error should reach OnDone, got %v", doneErrs[0])
	require.NoError(t, doneErrs[1], "file-b should complete cleanly, got %v", doneErrs[1])
}

// TestFileCanceledStillCancelsTask 回归验证：context.Canceled 的整任务取消语义
// 保持不变——download 返回错误、errgroup 取消 wgctx、Download 返回 wrapped error。
func TestFileCanceledStillCancelsTask(t *testing.T) {
	var inv tg.Invoker = invokerFunc(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		return context.Canceled
	})

	pool := &fakePool{inv: inv}

	var (
		mu       sync.Mutex
		doneErrs []error
	)
	progress := &recordingProgress{
		onDone: func(e Elem, err error) {
			mu.Lock()
			defer mu.Unlock()
			doneErrs = append(doneErrs, err)
		},
	}

	// 只产出 1 个文件，避免 errgroup 取消后第二个 goroutine 抢跑
	it := &staticIter{
		elems: []Elem{newFakeElem(1, "file-a")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d := New(Options{Pool: pool, Threads: 8, Iter: it, Progress: progress})
	err := d.Download(ctx, 1)

	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled), "task cancel should surface context.Canceled, got %v", err)
	mu.Lock()
	require.Len(t, doneErrs, 1)
	mu.Unlock()
	require.True(t, errors.Is(doneErrs[0], context.Canceled), "OnDone should receive the canceled error, got %v", doneErrs[0])
}

// --- fakes ---

// errSentinelPause simulates server/dl.go's errFilePaused (a non-context.Canceled
// sentinel returned by PauseCheck for a user-paused file).
var errSentinelPause = errors.New("file paused by user")

// fakePool implements dcpool.Pool against a fake invoker (no real network).
type fakePool struct {
	inv tg.Invoker
}

func (f *fakePool) Client(ctx context.Context, dc int) *tg.Client  { return tg.NewClient(f.inv) }
func (f *fakePool) Takeout(ctx context.Context, dc int) *tg.Client { return tg.NewClient(f.inv) }
func (f *fakePool) Default(ctx context.Context) *tg.Client         { return tg.NewClient(f.inv) }
func (f *fakePool) Close() error                                   { return nil }

var _ dcpool.Pool = (*fakePool)(nil)

type invokerFunc func(ctx context.Context, input bin.Encoder, output bin.Decoder) error

func (f invokerFunc) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	return f(ctx, input, output)
}

var _ tg.Invoker = invokerFunc(nil)

// recordingProgress records OnDone calls.
type recordingProgress struct {
	onDone func(e Elem, err error)
}

func (r *recordingProgress) OnAdd(e Elem)                       {}
func (r *recordingProgress) OnDownload(e Elem, s ProgressState) {}
func (r *recordingProgress) OnDone(e Elem, err error) {
	if r.onDone != nil {
		r.onDone(e, err)
	}
}

var _ Progress = (*recordingProgress)(nil)

// staticIter yields a fixed set of elements once.
type staticIter struct {
	elems []Elem
	idx   int
}

func (s *staticIter) Next(ctx context.Context) bool {
	if s.idx >= len(s.elems) {
		return false
	}
	s.idx++
	return true
}

func (s *staticIter) Value() Elem { return s.elems[s.idx-1] }
func (s *staticIter) Err() error  { return nil }

var _ Iter = (*staticIter)(nil)

// fakeDownloadElem implements Elem for the fake download path.
type fakeDownloadElem struct {
	volID int64
	name  string
}

func newFakeElem(volID int64, name string) Elem {
	return &fakeDownloadElem{volID: volID, name: name}
}

func (f *fakeDownloadElem) File() File {
	return &fakeFile{
		loc: &tg.InputFileLocation{
			VolumeID: f.volID,
			LocalID:  int(f.volID),
			Secret:   0,
		},
		size: 4, // 实际返回块字节数
		dc:   2,
	}
}

func (f *fakeDownloadElem) To() io.WriterAt { return &memWriterAt{} }
func (f *fakeDownloadElem) AsTakeout() bool { return false }
func (f *fakeDownloadElem) Resume() *State  { return nil }

type fakeFile struct {
	loc  *tg.InputFileLocation
	size int64
	dc   int
}

func (f *fakeFile) Location() tg.InputFileLocationClass { return f.loc }
func (f *fakeFile) Size() int64                         { return f.size }
func (f *fakeFile) DC() int                             { return f.dc }

var _ File = (*fakeFile)(nil)

type memWriterAt struct {
	mu sync.Mutex
	b  []byte
}

func (m *memWriterAt) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	end := int(off) + len(p)
	if len(m.b) < end {
		nb := make([]byte, end)
		copy(nb, m.b)
		m.b = nb
	}
	copy(m.b[off:], p)
	return len(p), nil
}

var _ io.WriterAt = (*memWriterAt)(nil)
