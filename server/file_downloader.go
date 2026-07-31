package server

import (
	"context"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	tdldl "github.com/iyear/tdl/core/downloader"
)

// streamSession is a per-file streaming engine that downloads Telegram media
// chunks on-demand near the browser's playhead (TDLib's streaming-window model).
type streamSession struct {
	a          *account
	t          *mediaTarget
	dataPath   string
	state      *tdldl.State
	f          *os.File
	useTakeout bool

	mu         sync.Mutex
	playhead   int64 // furthest byte served across all requests
	prefCancel context.CancelFunc
	prefActive bool
	closed     bool
}

// newStreamSession opens the preview file and loads/creates resume state.
func newStreamSession(a *account, t *mediaTarget, dataPath string, useTakeout bool) (*streamSession, error) {
	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, errors.Wrap(err, "open preview file")
	}
	state, err := tdldl.LoadState(dataPath, t.size, int(chunkSize))
	if err != nil || state == nil {
		if err := f.Truncate(0); err != nil {
			f.Close()
			return nil, err
		}
		state = tdldl.NewState(tdldl.StatePath(dataPath), t.size, int(chunkSize))
	}
	return &streamSession{a: a, t: t, dataPath: dataPath, state: state, f: f, useTakeout: useTakeout}, nil
}

func (ss *streamSession) api(ctx context.Context) *tg.Client {
	if ss.useTakeout {
		return ss.a.pool.Takeout(ctx, ss.t.dc)
	}
	return ss.a.pool.Client(ctx, ss.t.dc)
}

// --- serving ---

// ServeRange serves [start, end] to w progressively. For each chunk:
// 1. If on disk, serve immediately.
// 2. If missing, download JUST THAT CHUNK, then serve it.
// 3. Move to next chunk.
//
// This ensures the browser receives the first chunk as fast as possible,
// instead of waiting for the entire range to download.
func (ss *streamSession) ServeRange(ctx context.Context, w http.ResponseWriter, start, end int64) (fetched int64, firstByte time.Duration, err error) {
	flusher, _ := w.(http.Flusher)
	begin := time.Now()
	writeOff := start
	chunks := alignedChunks(start, end, ss.t.size, chunkSize)

	for i := 0; i < len(chunks) && writeOff <= end; {
		if ctx.Err() != nil {
			return fetched, firstByte, ctx.Err()
		}
		p := chunks[i]

		if !ss.chunkReady(p.off, p.end) {
			// Find the run of consecutive missing chunks starting at i.
			j := i
			for j+1 < len(chunks) && !ss.chunkReady(chunks[j+1].off, chunks[j+1].end) {
				j++
			}
			missing := chunks[i : j+1]

			// Seek 预缓冲：把整段缺失并行下载，同时"边下边写"——每个 chunk
			// 一就绪就立即写给浏览器，而不是等整段下完。拖动进度到远端未缓冲
			// 区域时，第一个 chunk（~1s RPC）一到视频就有数据可播，视觉上
			// 不再整段同步等待卡死。
			if err := ss.downloadAndServe(ctx, w, missing, &writeOff, &fetched, &firstByte, flusher, begin, end); err != nil {
				return fetched, firstByte, err
			}
			if ctx.Err() != nil {
				return fetched, firstByte, ctx.Err()
			}
			// 已下载+已写给浏览器的数据覆盖了 [start, writeOff)，跳过已被
			// 消费的 chunk，避免重复读。skip 循环已把 i 放到下一个待服务
			// chunk 的位置，这里不能再用 continue（会触发已删除的 for i++，
			// 多跳一块，导致响应里漏一个 chunk —— 视频随机自动暂停的根因）。
			for i < len(chunks) && chunks[i].end <= writeOff {
				i++
			}
			if i >= len(chunks) {
				break
			}
			continue
		}

		// Serve from disk.
		sStart := writeOff
		if sStart < p.off {
			sStart = p.off
		}
		sEnd := p.end - 1
		if sEnd > end {
			sEnd = end
		}
		if sStart <= sEnd {
			buf := make([]byte, sEnd-sStart+1)
			n, rerr := ss.f.ReadAt(buf, sStart)
			if rerr != nil && rerr != io.EOF {
				return fetched, firstByte, errors.Wrap(rerr, "read preview")
			}
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return fetched, firstByte, werr
				}
				if flusher != nil {
					flusher.Flush()
				}
				if firstByte == 0 {
					firstByte = time.Since(begin)
				}
				fetched += int64(n)
				writeOff = sStart + int64(n)
			}
		}

		ss.notePlayhead(writeOff)
		ss.kickPrefetch()
		i++ // for 无自增，disk-serve 分支手动推进
	}
	return fetched, firstByte, nil
}

// downloadAndServe downloads the missing chunks in parallel and, as each chunk
// becomes ready on disk, immediately serves the contiguous prefix to the
// browser (progressive serving). This replaces the old "download the whole run
// synchronously, then serve" behaviour for seek gaps: a seek to an un-buffered
// position gets its first bytes as soon as the first chunk lands (~1 RPC),
// instead of stalling the video until every chunk in the range is downloaded.
//
// Chunks land out of order (parallel workers). We serve only the ordered
// prefix from writeOff: a chunk that lands ahead of the current position is
// left on disk — the outer loop's chunkReady check serves it on a later
// request and prefetch reuses it. downloadSync's ctx is used for the batch;
// onReady fires from workers, so all serving happens on worker goroutines.
func (ss *streamSession) downloadAndServe(ctx context.Context, w http.ResponseWriter, missing []piece, writeOff *int64, fetched *int64, firstByte *time.Duration, flusher http.Flusher, begin time.Time, end int64) error {
	if len(missing) == 0 {
		return nil
	}
	var mu sync.Mutex
	onReady := func(p piece) {
		// Only a chunk contiguous with the current writeOff is servable now.
		mu.Lock()
		defer mu.Unlock()
		if p.off != *writeOff {
			return
		}
		sEnd := p.end - 1
		if sEnd > end {
			sEnd = end
		}
		buf := make([]byte, sEnd-*writeOff+1)
		n, rerr := ss.f.ReadAt(buf, *writeOff)
		if rerr != nil && rerr != io.EOF {
			return // leave it; a later request will surface the error
		}
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // client gone; nothing more to serve
			}
			if flusher != nil {
				flusher.Flush()
			}
			if *firstByte == 0 {
				*firstByte = time.Since(begin)
			}
			*fetched += int64(n)
			*writeOff += int64(n)
		}
	}
	if err := ss.downloadSync(ctx, missing, onReady); err != nil {
		// 部分 chunk 下载失败：不中断整个响应——已下载/已写的字节照常返回
		// （浏览器拿到后会继续请求下一个块），失败只记日志。若完全没写出
		// 任何字节，返回错误让上层处理。
		mu.Lock()
		written := *fetched
		mu.Unlock()
		if written == 0 {
			return err
		}
		return nil
	}
	// Serve any remaining contiguous tail that landed but wasn't flushed by
	// onReady (e.g. the final chunk was already contiguous).
	mu.Lock()
	defer mu.Unlock()
	for _, p := range missing {
		if *writeOff >= p.end {
			continue
		}
		if *writeOff < p.off {
			break
		}
		sEnd := p.end - 1
		if sEnd > end {
			sEnd = end
		}
		buf := make([]byte, sEnd-*writeOff+1)
		n, rerr := ss.f.ReadAt(buf, *writeOff)
		if rerr != nil && rerr != io.EOF {
			break
		}
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher != nil {
				flusher.Flush()
			}
			if *firstByte == 0 {
				*firstByte = time.Since(begin)
			}
			*fetched += int64(n)
			*writeOff += int64(n)
		}
	}
	ss.notePlayhead(*writeOff)
	ss.kickPrefetch()
	return nil
}

// downloadOne downloads a single chunk to disk.
func (ss *streamSession) downloadOne(ctx context.Context, p piece) error {
	return ss.fetchOne(ctx, p)
}

// WaitForRange blocks until every chunk covering [start, end] is on disk.
// Used for serveProbe and prefetchTail where we need specific bytes.
func (ss *streamSession) WaitForRange(ctx context.Context, start, end int64) error {
	chunks := alignedChunks(start, end, ss.t.size, chunkSize)
	for _, p := range chunks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !ss.chunkReady(p.off, p.end) {
			if err := ss.downloadOne(ctx, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- download engine ---

// downloadWorkers is the number of parallel chunk downloads for prefetch.
// A single Telegram connection tops out ~100-150KB/s, so to actually use the
// available bandwidth we need many workers. 32 workers ≈ pool size 32 (one
// connection each, the account pool dials up to 32): 32 × ~150KB/s ≈ 4.8MB/s,
// comfortably ahead of high-bitrate (HEVC 4K) playback so prefetch never lags
// the playhead during steady streaming.
const downloadWorkers = 32

// downloadSync downloads multiple chunks in parallel (for prefetch).
// If onReady is non-nil it is called with each chunk right after that chunk
// lands on disk (out of order). Callers use it to serve bytes progressively
// (seek prefetch) instead of waiting for the whole batch.
func (ss *streamSession) downloadSync(ctx context.Context, chunks []piece, onReady func(piece)) error {
	if len(chunks) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	queue := make(chan piece)
	errCh := make(chan error, 1)

	nw := downloadWorkers
	if len(chunks) < nw {
		nw = len(chunks)
	}
	var wg sync.WaitGroup
	for k := 0; k < nw; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range queue {
				if err := ss.fetchOne(ctx, p); err != nil {
					select {
					case errCh <- err:
						cancel()
					default:
					}
					return
				}
				if onReady != nil {
					onReady(p)
				}
			}
		}()
	}
	for _, p := range chunks {
		queue <- p
	}
	close(queue)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

// fetchOne downloads one aligned chunk via UploadGetFile and writes it to
// the preview file. State is marked immediately at chunkSize grain.
// Always fetches the full chunk to match state granularity — no partial marks.
func (ss *streamSession) fetchOne(ctx context.Context, p piece) error {
	if ss.chunkReady(p.off, p.end) {
		return nil
	}
	client := ss.api(ctx)

	// Always request the full chunk (aligned to chunkSize) so state.Mark
	// covers the entire piece. No adaptive part size — it causes partial
	// chunk downloads and state mismatches.
	limit := int64(chunkSize)
	if rem := p.end - p.off; rem < limit {
		limit = rem
	}
	if limit%4096 != 0 {
		limit = (limit/4096 + 1) * 4096
	}

	res, err := fetchUploadFile(ctx, client, &tg.UploadGetFileRequest{
		Precise:  true,
		Location: ss.t.loc,
		Offset:   p.off,
		Limit:    int(limit),
	})
	if err != nil {
		return errors.Wrap(err, "get file")
	}
	file, ok := res.(*tg.UploadFile)
	if !ok {
		return errors.Errorf("unexpected response %T", res)
	}
	if len(file.Bytes) == 0 {
		return nil
	}

	// Write to preview file.
	ss.mu.Lock()
	if _, err := ss.f.WriteAt(file.Bytes, p.off); err != nil {
		ss.mu.Unlock()
		return errors.Wrap(err, "write preview")
	}
	ss.state.Mark(p.off, len(file.Bytes))
	_ = ss.state.Flush()
	ss.mu.Unlock()
	return nil
}

// --- prefetch ---

// prefetchWindow is how far ahead of the playhead we keep downloaded.
// 8MB starves at high playback rates: once it's filled the loop idles at
// 500ms polling and the bandwidth sits unused. 64MB keeps the workers busy
// and lets fast-forward / high-bitrate playback stay ahead.
const prefetchWindow = 64 * 1024 * 1024

// kickPrefetch starts a background prefetch if not already running.
// Single-flight: only one prefetch loop per session.
func (ss *streamSession) kickPrefetch() {
	ss.mu.Lock()
	if ss.prefActive || ss.closed {
		ss.mu.Unlock()
		return
	}
	ss.prefActive = true
	ctx, cancel := context.WithCancel(context.Background())
	ss.prefCancel = cancel
	ss.mu.Unlock()

	go func() {
		defer func() {
			ss.mu.Lock()
			ss.prefActive = false
			ss.prefCancel = nil
			ss.mu.Unlock()
		}()
		for {
			ss.mu.Lock()
			base := ss.playhead + chunkSize
			closed := ss.closed
			ss.mu.Unlock()
			if closed || ctx.Err() != nil {
				return
			}
			end := base + prefetchWindow
			if end > ss.t.size {
				end = ss.t.size
			}
			if base >= ss.t.size || end <= base {
				return
			}
			all := alignedChunks(base, end-1, ss.t.size, chunkSize)
			var todo []piece
			for _, p := range all {
				if !ss.chunkReady(p.off, p.end) {
					todo = append(todo, p)
				}
			}
			if len(todo) == 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(500 * time.Millisecond):
					continue
				}
			}
			batchCtx, batchCancel := context.WithCancel(ctx)
			_ = ss.downloadSync(batchCtx, todo, nil)
			batchCancel()
			if ctx.Err() != nil {
				return
			}
		}
	}()
}

// notePlayhead records the furthest byte served. On seek (> prefetchWindow/2),
// cancels in-flight prefetch so it re-anchors at the new position.
func (ss *streamSession) notePlayhead(off int64) {
	ss.mu.Lock()
	jump := off - ss.playhead
	if jump < 0 {
		jump = -jump
	}
	ss.playhead = off
	if jump > prefetchWindow/2 && ss.prefCancel != nil {
		ss.prefCancel()
	}
	ss.mu.Unlock()
}

// --- state ---

// chunkReady reports whether [off, end) is fully on disk.
func (ss *streamSession) chunkReady(off, end int64) bool {
	for _, m := range ss.state.Missing() {
		if m.Off < end && off < m.End {
			return false
		}
	}
	return true
}

func (ss *streamSession) Close() error {
	ss.mu.Lock()
	ss.closed = true
	if ss.prefCancel != nil {
		ss.prefCancel()
	}
	ss.mu.Unlock()
	_ = ss.state.Flush()
	return ss.f.Close()
}

// --- registry ---

type ssEntry struct {
	ss        *streamSession
	refs      int
	idleTimer *time.Timer
}

func (s *Server) acquireSession(a *account, t *mediaTarget, dataPath string) (*streamSession, error) {
	s.streamerMu.Lock()
	defer s.streamerMu.Unlock()

	if e, ok := s.streamers[dataPath]; ok {
		if e.idleTimer != nil {
			e.idleTimer.Stop()
			e.idleTimer = nil
		}
		e.refs++
		return e.ss, nil
	}

	useTakeout := s.settings.useTakeoutFor(t.size)
	ss, err := newStreamSession(a, t, dataPath, useTakeout)
	if err != nil {
		return nil, err
	}
	if s.streamers == nil {
		s.streamers = make(map[string]*ssEntry)
	}
	s.streamers[dataPath] = &ssEntry{ss: ss, refs: 1}
	return ss, nil
}

func (s *Server) releaseSession(dataPath string) {
	s.streamerMu.Lock()
	e, ok := s.streamers[dataPath]
	if !ok {
		s.streamerMu.Unlock()
		return
	}
	e.refs--
	if e.refs > 0 {
		s.streamerMu.Unlock()
		return
	}
	timer := time.AfterFunc(60*time.Second, func() {
		s.streamerMu.Lock()
		if e2, ok2 := s.streamers[dataPath]; ok2 && e2 == e && e2.refs <= 0 {
			_ = e2.ss.Close()
			delete(s.streamers, dataPath)
		}
		s.streamerMu.Unlock()
	})
	e.idleTimer = timer
	s.streamerMu.Unlock()
}

// --- piece type ---

type piece struct{ off, end int64 }

func alignedChunks(start, end int64, size int64, part int64) []piece {
	var out []piece
	for off := (start / part) * part; off <= end; off += part {
		pe := off + part
		if pe > size {
			pe = size
		}
		out = append(out, piece{off, pe})
	}
	return out
}
