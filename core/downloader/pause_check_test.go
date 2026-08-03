package downloader

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeElem 满足 Elem 接口的最小实现（只测 WriteAt 中断路径，download 不真的跑）。
type fakeElem struct {
	written int64
}

func (f *fakeElem) File() File                 { return nil }
func (f *fakeElem) To() io.WriterAt            { return f }
func (f *fakeElem) AsTakeout() bool            { return false }
func (f *fakeElem) Resume() *State             { return nil }
func (f *fakeElem) WriteAt(p []byte, off int64) (int, error) {
	f.written += int64(len(p))
	return len(p), nil
}

// TestWriteAtPauseCheckAborts 验证：PauseCheck 返回错误时 WriteAt 不再写入，
// 返回该错误；PauseCheck 为 nil 时正常写入。
func TestWriteAtPauseCheckAborts(t *testing.T) {
	t.Run("nil PauseCheck writes normally", func(t *testing.T) {
		e := &fakeElem{}
		w := newWriteAt(e, nil, MaxPartSize, nil)
		n, err := w.WriteAt([]byte("hello"), 0)
		require.NoError(t, err)
		require.Equal(t, 5, n)
		require.Equal(t, int64(5), e.written)
	})

	t.Run("PauseCheck error aborts without writing", func(t *testing.T) {
		e := &fakeElem{}
		w := newWriteAt(e, nil, MaxPartSize, nil)
		// 给 writeAt 塞一个 pauseCheck（测试里通过字段注入）
		w.pauseCheck = func(elem Elem) error { return errors.New("abort") }
		n, err := w.WriteAt([]byte("hello"), 0)
		require.Error(t, err)
		require.Equal(t, "abort", err.Error())
		require.Equal(t, 0, n)
		require.Equal(t, int64(0), e.written) // 没写
	})

	t.Run("PauseCheck nil-safe and only checked when set", func(t *testing.T) {
		var calls atomic.Int32
		e := &fakeElem{}
		w := newWriteAt(e, nil, MaxPartSize, nil)
		w.pauseCheck = func(elem Elem) error { calls.Add(1); return nil }
		n, err := w.WriteAt([]byte("hi"), 0)
		require.NoError(t, err)
		require.Equal(t, 2, n)
		require.Equal(t, int32(1), calls.Load())
		require.Equal(t, int64(2), e.written)
	})
}
