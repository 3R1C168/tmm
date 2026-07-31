package server

import (
	"context"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gorilla/mux"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/chat"
	tdldl "github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/tutil"
)

// chunkSize is the alignment/state/streaming granularity for media (512KiB).
const chunkSize = 512 * 1024

// streamResponseChunks is how many chunks each video/audio Range response
// covers. Larger = more buffered playback per request (smoother high-bitrate
// streaming); first-byte latency stays low because downloadAndServe streams
// the first ready chunk immediately.
const streamResponseChunks = 4 // 2 MiB per response

// noThumbCacheTTL is how long a "message has no thumbnail" answer is trusted
// in memory. The answer is a property of the message and never changes; the
// TTL only bounds stale state after an account switch.
const noThumbCacheTTL = 10 * time.Minute

// mediaTarget resolves a (chat, message) to its downloadable file location.
type mediaTarget struct {
	loc  tg.InputFileLocationClass
	size int64
	dc   int
	name string

	width             int
	height            int
	duration          int    // seconds (video)
	mime              string // document mime type, or "image/jpeg" for photos
	supportsStreaming bool   // moov atom at front (Telegram server-transcoded)
}

// thumbTarget resolves the small preview image for a media message.
type thumbTarget struct {
	loc  tg.InputFileLocationClass
	size int64
	dc   int
}

// resolveMedia fetches the message and extracts its media location.
func (s *Server) resolveMedia(ctx context.Context, a *account, chatID string, msgID int) (*mediaTarget, *tg.Message, error) {
	peer, err := tutil.GetInputPeer(ctx, a.peers, chatID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "resolve chat")
	}
	msg, err := tutil.GetSingleMessage(ctx, a.pool.Default(ctx), peer.InputPeer(), msgID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "get message")
	}
	media, ok := tmedia.GetMedia(msg)
	if !ok {
		return nil, nil, errors.New("该消息没有可下载的媒体")
	}
	mt := &mediaTarget{
		loc:  media.InputFileLoc,
		size: media.Size,
		dc:   media.DC,
		name: media.Name,
	}
	if m, ok := msg.GetMedia(); ok {
		switch mm := m.(type) {
		case *tg.MessageMediaDocument:
			if doc, ok := mm.Document.(*tg.Document); ok {
				mt.mime = doc.MimeType
				for _, attr := range doc.Attributes {
					if v, ok := attr.(*tg.DocumentAttributeVideo); ok {
						mt.width, mt.height, mt.duration = v.W, v.H, int(v.Duration)
						mt.supportsStreaming = v.SupportsStreaming
					}
					if a, ok := attr.(*tg.DocumentAttributeAudio); ok {
						mt.duration = int(a.Duration)
					}
				}
			}
		case *tg.MessageMediaPhoto:
			if photo, ok := mm.Photo.(*tg.Photo); ok {
				mt.mime = "image/jpeg"
				for _, sz := range photo.Sizes {
					if s, ok := sz.(*tg.PhotoSize); ok {
						if s.W > mt.width {
							mt.width, mt.height = s.W, s.H
						}
					} else if s, ok := sz.(*tg.PhotoSizeProgressive); ok {
						if s.W > mt.width {
							mt.width, mt.height = s.W, s.H
						}
					}
				}
			}
		}
	}
	return mt, msg, nil
}

// resolveMediaCached is like resolveMedia but caches the result per (account, chat, msg).
func (s *Server) resolveMediaCached(ctx context.Context, a *account, chatID string, msgID int) (*mediaTarget, *tg.Message, error) {
	key := a.ns + ":" + chatID + ":" + itoa(int64(msgID))
	s.mediaMu.Lock()
	if t, ok := s.mediaTargets[key]; ok && t != nil {
		s.mediaMu.Unlock()
		return t, nil, nil
	}
	if s.mediaTargetNS != a.ns {
		s.mediaTargets = make(map[string]*mediaTarget)
		s.mediaTargetNS = a.ns
	}
	s.mediaMu.Unlock()

	t, msg, err := s.resolveMedia(ctx, a, chatID, msgID)
	if err != nil {
		return nil, nil, err
	}
	s.mediaMu.Lock()
	s.mediaTargets[key] = t
	s.mediaMu.Unlock()
	return t, msg, nil
}

// resolveThumb extracts a small thumbnail location from a message.
func resolveThumb(msg *tg.Message) (*thumbTarget, bool) {
	m, ok := msg.GetMedia()
	if !ok {
		return nil, false
	}
	switch mm := m.(type) {
	case *tg.MessageMediaPhoto:
		mi, ok := tmedia.GetPhotoInfo(mm)
		if !ok {
			return nil, false
		}
		return &thumbTarget{loc: mi.InputFileLoc, size: mi.Size, dc: mi.DC}, true
	case *tg.MessageMediaDocument:
		if doc, ok := mm.Document.(*tg.Document); ok {
			if th, ok := tmedia.GetDocumentThumb(doc); ok {
				return &thumbTarget{loc: th.InputFileLoc, size: th.Size, dc: th.DC}, true
			}
		}
	}
	return nil, false
}

// pickPhotoThumb chooses a small photo size for list previews.
func pickPhotoThumb(sizes []tg.PhotoSizeClass) (string, int, bool) {
	want := map[string]bool{"m": true, "s": true, "x": true}
	var fallback *tg.PhotoSize
	var prog *tg.PhotoSizeProgressive
	for _, sc := range sizes {
		switch s := sc.(type) {
		case *tg.PhotoSize:
			if want[s.Type] {
				return s.Type, s.Size, true
			}
			fallback = s
		case *tg.PhotoSizeProgressive:
			prog = s
		}
	}
	if fallback != nil {
		return fallback.Type, fallback.Size, true
	}
	if prog != nil && len(prog.Sizes) > 0 {
		return prog.Type, prog.Sizes[len(prog.Sizes)-1], true
	}
	return "", 0, false
}

// --- handlers ---

func (s *Server) handleMediaInfo(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}
	chatID, msgID, err := parseMediaVars(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, _, err := s.resolveMedia(r.Context(), a, chatID, msgID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":     t.name,
		"size":     t.size,
		"mime":     t.mime,
		"width":    t.width,
		"height":   t.height,
		"duration": t.duration,
	})
}

// handleMediaThumb serves the small preview image, cached. If a download is
// already in flight for this (chat,msg), waits for it and serves the result.
func (s *Server) handleMediaThumb(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}
	chatID, msgID, err := parseMediaVars(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	peerID := peerIDFromChat(chatID)

	// Fast path: already cached.
	if p, ok := s.cache.thumbExists(peerID, msgID); ok {
		serveFileWithMime(w, r, p, "t.jpg")
		return
	}

	// Negative cache: 视频无服务端缩略图的消息（很常见）不会凭空长出缩略图，
	// 重复探测（LazyThumb 的 fetch 状态检查）命中这里直接 404，省一次
	// resolveMedia RPC（~1.5-2s）。10 分钟 TTL，防账号切换后 ID 复用。
	ntKey := a.ns + ":" + chatID + ":" + itoa(int64(msgID))
	s.noThumbMu.Lock()
	if t, ok := s.noThumb[ntKey]; ok && time.Since(t) < noThumbCacheTTL {
		s.noThumbMu.Unlock()
		writeErr(w, http.StatusNotFound, errString("无缩略图"))
		return
	}
	s.noThumbMu.Unlock()

	// Single-flight: wait if another goroutine (handler or warmup) is fetching.
	// Bounded wait: if the in-flight download takes too long (RPC to Telegram
	// can be 1.5-2s each), return 503 so the browser's connection is freed for
	// other thumbs instead of blocking all 6 concurrent connections on slow
	// downloads. 3s covers most warmup downloads (16 concurrent, ~1.5s each);
	// beyond that the browser retries via LazyThumb's 503 backoff.
	key := chatID + ":" + itoa(int64(msgID))
	s.thumbMu.Lock()
	if ch, ok := s.thumbInflight[key]; ok {
		s.thumbMu.Unlock()
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			writeErr(w, http.StatusServiceUnavailable, errString("缩略图准备中"))
			return
		case <-r.Context().Done():
			return
		}
		if p, ok := s.cache.thumbExists(peerID, msgID); ok {
			serveFileWithMime(w, r, p, "t.jpg")
			return
		}
		// Fetcher 完成（ch 关闭）但缓存里没图：要么 fetcher 被取消了（切频道
		// 取消 warmup，warmup 正是注册 inflight 的那一方，HTTP 等待者等的是
		// 它的 ch），要么真正下载失败。两者都不是「永久损坏」——前端
		// LazyThumb 的 503 退避会重试，下一次多半已缓存或由新 warmup 补上。
		// 返回 503 而非 500，避免误报并触发自动重试。
		writeErr(w, http.StatusServiceUnavailable, errString("缩略图准备中"))
		return
	}
	s.thumbMu.Unlock()

	// We are the fetcher: resolve the thumb location then download.
	_, msg, err := s.resolveMedia(r.Context(), a, chatID, msgID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	th, ok := resolveThumb(msg)
	if !ok {
		s.noThumbMu.Lock()
		if len(s.noThumb) >= 8192 {
			for k, v := range s.noThumb {
				if time.Since(v) >= noThumbCacheTTL {
					delete(s.noThumb, k)
				}
			}
		}
		s.noThumb[ntKey] = time.Now()
		s.noThumbMu.Unlock()
		writeErr(w, http.StatusNotFound, errString("无缩略图"))
		return
	}

	err = s.fetchThumb(r.Context(), a, chatID, msgID, th)
	if err != nil {
		s.log().Warn("thumb download failed", zap.String("chat", chatID), zap.Int("msg", msgID), zap.Error(err))
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.evictCache()
	if p, ok := s.cache.thumbExists(peerID, msgID); ok {
		serveFileWithMime(w, r, p, "t.jpg")
		return
	}
	writeErr(w, http.StatusInternalServerError, errString("缩略图下载失败"))
}

// fetchThumb downloads a thumbnail to the cache, holding the single-flight
// slot for (chat,msg) so concurrent handlers/warmup wait instead of
// double-downloading. The caller must NOT already hold the slot.
//
// The download runs on a detached context, NOT the caller's ctx: warmup passes
// a warmupSession ctx that gets cancelled on channel switch, but a thumbnail
// already in flight should finish (it's tiny). Cancelling in-flight fetches on
// every channel switch was the root cause of thumb 500s: HTTP waiters pinning
// the inflight slot saw their fetcher abort with "context canceled" → cache
// miss → 500. Detached ctx means once a thumb download starts it completes and
// populates the cache, so the next request hits cache regardless of warmup.
func (s *Server) fetchThumb(ctx context.Context, a *account, chatID string, msgID int, th *thumbTarget) error {
	peerID := peerIDFromChat(chatID)
	key := chatID + ":" + itoa(int64(msgID))

	s.thumbMu.Lock()
	if _, ok := s.thumbInflight[key]; ok {
		// Already in flight — nothing to do, the waiter path handles serving.
		s.thumbMu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	s.thumbInflight[key] = ch
	s.thumbMu.Unlock()

	defer func() {
		s.thumbMu.Lock()
		delete(s.thumbInflight, key)
		close(ch)
		s.thumbMu.Unlock()
	}()

	// Another goroutine may have cached it while we registered.
	if _, ok := s.cache.thumbExists(peerID, msgID); ok {
		return nil
	}
	if err := s.cache.ensure(); err != nil {
		return err
	}
	// Detached ctx: see comment above. A thumbnail download, once started,
	// runs to completion so the cache is populated for the next request.
	dlCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dst := s.cache.thumbPath(peerID, msgID)
	return s.downloadToFile(dlCtx, a, th.loc, th.size, th.dc, dst, false)
}

// handleMediaFile serves the full-resolution file (images on zoom), cached.
// If the video was already previewed (streamed), we reuse the preview file
// if it's complete — no re-download needed.
func (s *Server) handleMediaFile(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}
	chatID, msgID, err := parseMediaVars(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, _, err := s.resolveMedia(r.Context(), a, chatID, msgID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	ext := strings.ToLower(filepath.Ext(t.name))
	peerID := peerIDFromChat(chatID)

	// 1. Check media cache (already fully downloaded)
	if p, ok := s.cache.mediaExists(peerID, msgID, ext); ok {
		serveFileWithMime(w, r, p, t.name)
		return
	}

	// 2. Check preview cache (partial download from streaming) — if complete, use it
	previewPath := s.cache.previewDataPath(peerID, msgID)
	if state, err := tdldl.LoadState(previewPath, t.size, int(chunkSize)); err == nil && state != nil && state.Complete() {
		// Preview is complete — rename to media cache
		mediaPath := s.cache.mediaPath(peerID, msgID, ext)
		if err := os.Rename(previewPath, mediaPath); err == nil {
			// Also remove the .tdlstate since it's no longer needed
			_ = os.Remove(tdldl.StatePath(previewPath))
			serveFileWithMime(w, r, mediaPath, t.name)
			return
		}
	}

	// 3. Download fresh
	if err := s.cache.ensure(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	dst := s.cache.mediaPath(peerID, msgID, ext)
	start := time.Now()
	if err := s.downloadToFile(r.Context(), a, t.loc, t.size, t.dc, dst, s.settings.useTakeoutFor(t.size)); err != nil {
		s.log().Warn("media file download failed", zap.String("chat", chatID), zap.Int("msg", msgID), zap.Error(err))
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log().Info("media file cached",
		zap.String("chat", chatID), zap.Int("msg", msgID),
		zap.Int64("size", t.size), zap.Duration("cost", time.Since(start)))
	s.evictCache()
	serveFileWithMime(w, r, dst, t.name)
}

// handleMediaStream serves a Telegram media file as a byte-range stream. The
// actual download runs in the background using gotd's proven-fast Parallel
// engine (>10 MB/s). The HTTP layer reads from the growing local file
// chunk-by-chunk — download and serving are completely decoupled.
func (s *Server) handleMediaStream(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}
	chatID, msgID, err := parseMediaVars(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, _, err := s.resolveMediaCached(r.Context(), a, chatID, msgID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	peerID := peerIDFromChat(chatID)
	if err := s.cache.ensure(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	dataPath := s.cache.previewDataPath(peerID, msgID)

	start, end, partial, ok := parseRange(r.Header.Get("Range"), t.size)
	if !ok {
		w.Header().Set("Content-Range", "bytes */"+itoa64(t.size))
		writeErr(w, http.StatusRequestedRangeNotSatisfiable, errString("请求的字节范围无效"))
		return
	}

	// Clamp each response to a small block (default 2 MiB = 4 chunks), like
	// telegram-tt's Service Worker: the browser's open-ended "bytes=0-" gets a
	// short 206 it can canplay on immediately, then it issues the next Range
	// request. The session prefetch (independent 60s-idle lifecycle) keeps data
	// ahead.
	//
	// Block size trade-off: 512KiB (1 chunk) minimizes first-byte latency but
	// forces a new Range request every ~0.7s of playback at ~700KB/s bitrate —
	// if prefetch hasn't covered the next block, each request pays an RPC and
	// high-bitrate videos can stall. 2 MiB gives the player ~2.8s of buffered
	// playback per request, absorbing RPC jitter without delaying the first
	// byte (downloadAndServe streams the first ready chunk immediately).
	//
	// Exception: images don't benefit from clamping. <img> doesn't issue
	// follow-up Range requests like <video>/<audio>, so a clamped 512KiB
	// response truncates large images. And images are usually small enough
	// that returning the whole file in one shot is faster than the per-chunk
	// RPC round-trips (each ~1.5s to Telegram).
	isImage := strings.HasPrefix(t.mime, "image/")
	if !isImage && end-start+1 > streamResponseChunks*chunkSize {
		end = start + streamResponseChunks*chunkSize - 1
		if end > t.size-1 {
			end = t.size - 1
		}
	}

	ctype := t.mime
	if ctype == "" {
		ctype = mime.TypeByExtension(strings.ToLower(filepath.Ext(t.name)))
	}

	// Metadata-probe shortcut: the browser's "bytes=0-1" probe only wants
	// Content-Range to learn the total size. Answer it instantly.
	if start == 0 && end == 1 && partial {
		s.serveProbe(w, r, a, t, dataPath, ctype)
		return
	}

	ss, err := s.acquireSession(a, t, dataPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer s.releaseSession(dataPath)

	// Tail-moov prefetch for non-streaming videos (moov atom at file tail).
	// Audio files don't have a moov atom — skip the pointless tail fetch.
	// Images neither: photo targets never set supportsStreaming, so without
	// this check every image stream would trigger a full-file tail download.
	//
	// Async: blocking the first request on the tail (up to 8MiB of RPCs) delays
	// first-byte and makes the browser's own tail probe race this download.
	// The browser's tail request downloads the moov itself when it needs it;
	// prefetchTail warms it in the background so the probe usually hits cache.
	isAudio := strings.HasPrefix(t.mime, "audio/")
	if start == 0 && t.size > 0 && !t.supportsStreaming && !isAudio && !isImage {
		go s.prefetchTail(ss, t)
	}

	s.log().Info("stream req",
		zap.String("chat", chatID), zap.Int("msg", msgID),
		zap.String("range", r.Header.Get("Range")),
		zap.Int64("start", start), zap.Int64("end", end))

	length := end - start + 1
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", itoa64(length))
	if partial {
		w.Header().Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa64(t.size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	serveStart := time.Now()
	fetched, firstByte, serveErr := ss.ServeRange(r.Context(), w, start, end)
	s.log().Info("stream done",
		zap.String("chat", chatID), zap.Int("msg", msgID),
		zap.Int64("start", start), zap.Int64("end", end),
		zap.Int64("fetched", fetched),
		zap.Duration("firstByte", firstByte), zap.Duration("cost", time.Since(serveStart)))
	if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
		s.log().Warn("stream error", zap.Error(serveErr))
	}
}

// serveProbe answers a "bytes=0-1" metadata probe. The browser only needs the
// Content-Range total to size the progress bar; tt answers instantly from
// metadata without downloading. We do the same — no network fetch needed.
func (s *Server) serveProbe(w http.ResponseWriter, r *http.Request, a *account, t *mediaTarget, dataPath, ctype string) {
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Range", "bytes 0-1/"+itoa64(t.size))
	w.Header().Set("Content-Length", "2")
	w.WriteHeader(http.StatusPartialContent)
	// tt trick: answer with 2 empty bytes, no download needed.
	// The browser only reads Content-Range for the total size.
	_, _ = w.Write([]byte{0, 0})
	s.log().Info("stream probe", zap.String("name", t.name), zap.Int64("size", t.size))
}

// prefetchTail warms the last 8 MiB of a non-streaming video in the background
// so the browser's tail-moov probe usually hits cache. Non-fatal on failure.
func (s *Server) prefetchTail(ss *streamSession, t *mediaTarget) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tail := int64(8 * 1024 * 1024)
	if t.size < tail {
		tail = t.size
	}
	start := t.size - tail
	prefetchStart := time.Now()
	if err := ss.WaitForRange(ctx, start, t.size-1); err != nil {
		s.log().Info("prefetch tail failed", zap.String("file", t.name), zap.Error(err))
		return
	}
	s.log().Info("prefetch tail done",
		zap.String("file", t.name), zap.Int64("tailBytes", tail),
		zap.Duration("cost", time.Since(prefetchStart)))
}

// warmupThumbs pre-downloads thumbnails for the given messages in parallel.
// Called after listing messages so the browser's subsequent thumb requests
// hit the cache instead of waiting for a cold RPC+download. It uses the
// ThumbInfo already captured during FetchMessages — no extra RPC. Downloads
// go through fetchThumb so they share the single-flight map with the HTTP
// handler (no double-download / .part rename conflicts). ctx should be
// cancellable and tied to the chat session.
func (s *Server) warmupThumbs(ctx context.Context, a *account, chatID string, msgs []*chat.MessageInfo) {
	sem := make(chan struct{}, 16) // 16 concurrent thumb downloads
	var wg sync.WaitGroup
	for _, mi := range msgs {
		if ctx.Err() != nil {
			break
		}
		if !mi.HasMedia || mi.Media == nil || mi.Media.Thumb == nil {
			continue
		}
		msgID := mi.ID
		th := &thumbTarget{
			loc:  mi.Media.Thumb.Loc,
			size: mi.Media.Thumb.Size,
			dc:   mi.Media.Thumb.DC,
		}
		if _, ok := s.cache.thumbExists(peerIDFromChat(chatID), msgID); ok {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(msgID int, th *thumbTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := s.fetchThumb(ctx, a, chatID, msgID, th); err != nil && ctx.Err() == nil {
				s.log().Debug("warmup thumb failed", zap.String("chat", chatID), zap.Int("msg", msgID), zap.Error(err))
			}
		}(msgID, th)
	}
	wg.Wait()
}

// evictCache trims the media cache if it exceeds the configured cap. Rate-limited
// to at most once per minute.
func (s *Server) evictCache() {
	s.evictMu.Lock()
	if time.Since(s.lastEvict) < time.Minute {
		s.evictMu.Unlock()
		return
	}
	s.lastEvict = time.Now()
	s.evictMu.Unlock()
	s.cache.evictLRU(s.settings.cacheMaxBytes(), func(msg string, args ...any) {
		s.log().Sugar().Infof(msg, args...)
	})
}

// downloadToFile fully downloads a location to dst (thumbnails, full images).
// Uses fixed chunkSize for consistency with streaming state tracking.
func (s *Server) downloadToFile(ctx context.Context, a *account, loc tg.InputFileLocationClass, size int64, dc int, dst string, useTakeout bool) error {
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	var client *tg.Client
	if useTakeout {
		client = a.pool.Takeout(ctx, dc)
	} else {
		// Dial the file's own DC. The first dial to a DC pays a one-time
		// handshake cost (~seconds), but the pool caches the invoker and
		// subsequent files on the same DC are fast. Using Default (current DC)
		// would trigger FILE_MIGRATE, which the pool's retry middleware does
		// NOT auto-follow — so per-DC is both correct and, after warm-up, fast.
		client = a.pool.Client(ctx, dc)
	}

	// Fixed chunk size for consistency — adaptive sizes cause state tracking issues
	chunk := int64(chunkSize)

	var off int64
	for off < size {
		limit := chunk
		if rem := size - off; rem < limit {
			limit = rem
		}
		if limit%4096 != 0 {
			limit = (limit/4096 + 1) * 4096
		}
		res, err := fetchUploadFile(ctx, client, &tg.UploadGetFileRequest{
			Location: loc, Offset: off, Limit: int(limit),
		})
		if err != nil {
			if isLimitInvalid(err) {
				if next := smallerChunk(chunk); next > 0 {
					chunk = next
					continue
				}
			}
			f.Close()
			return errors.Wrap(err, "get file")
		}
		file, ok := res.(*tg.UploadFile)
		if !ok {
			f.Close()
			return errors.Errorf("unexpected response %T", res)
		}
		if len(file.Bytes) == 0 {
			break
		}
		if _, err := f.Write(file.Bytes); err != nil {
			f.Close()
			return err
		}
		off += int64(len(file.Bytes))
		if int64(len(file.Bytes)) < limit {
			break
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func isLimitInvalid(err error) bool {
	return err != nil && strings.Contains(err.Error(), "LIMIT_INVALID")
}

func smallerChunk(cur int64) int64 {
	switch {
	case cur > 512*1024:
		return 512 * 1024
	case cur > 128*1024:
		return 128 * 1024
	case cur > 32*1024:
		return 32 * 1024
	case cur > 4096:
		return 4096
	case cur > 1024:
		return 1024
	default:
		return 0
	}
}

// --- helpers ---

func parseMediaVars(r *http.Request) (chatID string, msgID int, err error) {
	vars := mux.Vars(r)
	chatID = vars["id"]
	msgID, err = strconv.Atoi(vars["msg"])
	if err != nil {
		return "", 0, errors.New("invalid message id")
	}
	return chatID, msgID, nil
}

func peerIDFromChat(chatID string) int64 {
	id, _ := strconv.ParseInt(chatID, 10, 64)
	return id
}

// parseRange parses an HTTP Range header for a resource of the given size.
func parseRange(header string, size int64) (start, end int64, partial, ok bool) {
	if size <= 0 {
		return 0, -1, false, false
	}
	if header == "" {
		return 0, size - 1, false, true
	}
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false, false
	}
	spec := strings.TrimPrefix(header, "bytes=")
	if strings.Contains(spec, ",") {
		return 0, 0, false, false // multi-range not supported
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, false
	}

	if parts[0] == "" {
		// suffix form: last N bytes
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, true
	}

	s, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || s < 0 {
		return 0, 0, false, false
	}
	e := size - 1
	if parts[1] != "" {
		e, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 0, false, false
		}
	}
	if e > size-1 {
		e = size - 1
	}
	if s > e || s >= size {
		return 0, 0, false, false
	}
	return s, e, true, true
}

func serveFileWithMime(w http.ResponseWriter, r *http.Request, path, name string) {
	ctype := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}

func itoa(v int64) string   { return strconv.FormatInt(v, 10) }
func itoa64(v int64) string { return strconv.FormatInt(v, 10) }
