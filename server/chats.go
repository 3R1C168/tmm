package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-faster/errors"
	"github.com/gorilla/mux"
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/chat"
)

// activeAccount resolves the current single active (authorized) account for
// browsing. Returns nil if none is logged in.
func (s *Server) activeAccount() *account {
	for _, a := range s.accounts.accountsSnapshot() {
		if a.ready() {
			return a
		}
	}
	return nil
}

// msgsCall is an in-flight FetchMessages RPC shared by concurrent HTTP
// requests for the same cacheKey.
type msgsCall struct {
	done chan struct{}
	msgs []*chat.MessageInfo
	next int
	err  error
}

// fetchMessagesFlight runs FetchMessages on a background context detached
// from the HTTP request, deduplicated per cacheKey. If the user navigates
// away mid-fetch (aborting the request), the RPC still completes and caches
// its result, so the next visit hits cache instead of re-fetching. Callers
// that arrive while a fetch is in flight wait for it and share the result.
func (s *Server) fetchMessagesFlight(ctx context.Context, a *account, chatID string, offsetID, limit int, mediaOnly bool, cacheKey string) ([]*chat.MessageInfo, int, error) {
	s.msgsMu.Lock()
	if c, ok := s.msgsInflight[cacheKey]; ok {
		s.msgsMu.Unlock()
		// 等待共享结果要响应请求取消 + 设上限（单个 flight 尝试上限 3×15s）：
		// 否则浏览器连接被无界阻塞（与 thumb 3s 超时返回 503 同一思路）。
		// RPC 本身跑在 detach 的 Background ctx 上，这里放弃等待不影响它完成并缓存。
		select {
		case <-c.done:
			return c.msgs, c.next, c.err
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(45 * time.Second):
			return nil, 0, errors.New("等待消息加载超时")
		}
	}
	c := &msgsCall{done: make(chan struct{})}
	s.msgsInflight[cacheKey] = c
	s.msgsMu.Unlock()

	// Detached context: NOT cancelled when the HTTP request aborts. Bounded
	// per attempt so a stuck main-client connection (seen in logs as
	// "waitSession: context deadline exceeded" / "write tcp ... i/o timeout")
	// fails fast instead of dead-waiting the full 60s. Each retry gets a fresh
	// deadline, which also gives the main client's recovery/reconnect logic a
	// chance to re-establish a healthy connection between attempts.
	const (
		perAttempt = 15 * time.Second
		maxAttempt = 3 // first attempt + 2 retries, total ≤ 45s (< old 60s)
	)
	for attempt := 0; attempt < maxAttempt; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), perAttempt)
		c.msgs, c.next, c.err = chat.FetchMessages(ctx, a.client, a.kvd, chatID, offsetID, limit, mediaOnly)
		cancel()
		if c.err == nil {
			break
		}
		s.log().Warn("fetch messages retry",
			zap.String("chat", chatID), zap.Int("attempt", attempt+1), zap.Error(c.err))
	}
	// Only cache non-empty successful pages. An empty page can mean a
	// genuinely empty chat, but it can also result from a transient RPC hiccup
	// — caching it for 2h would wrongly show "暂无消息" until a manual
	// refresh. Empty chats are cheap to re-fetch, so we skip caching them.
	if c.err == nil && len(c.msgs) > 0 {
		_ = s.cache.writeMeta(cacheKey, msgsPayloadT{Messages: c.msgs, NextCursor: c.next})
	}

	s.msgsMu.Lock()
	delete(s.msgsInflight, cacheKey)
	s.msgsMu.Unlock()
	close(c.done)
	return c.msgs, c.next, c.err
}

// msgsPayloadT is the cached form of a messages page.
type msgsPayloadT struct {
	Messages   []*chat.MessageInfo `json:"messages"`
	NextCursor int                 `json:"nextCursor"`
}

// handleListChats returns the account's dialogs, using the meta cache unless
// ?refresh=1 is passed.
func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}

	cacheKey := "chats_" + a.ns
	refresh := r.URL.Query().Get("refresh") == "1"

	if !refresh {
		var cached []*chat.DialogInfo
		if fresh, _ := s.cache.readMeta(cacheKey, chatsCacheTTL, &cached); fresh {
			writeJSON(w, http.StatusOK, map[string]any{"chats": cached, "cached": true})
			return
		}
	}

	dialogs, err := chat.FetchDialogs(r.Context(), a.client, a.kvd)
	if err != nil {
		s.log().Warn("fetch chats failed", zap.String("ns", a.ns), zap.Error(err))
		writeErr(w, http.StatusInternalServerError, errors.Wrap(err, "拉取会话列表失败"))
		return
	}
	_ = s.cache.writeMeta(cacheKey, dialogs)
	s.log().Info("chats fetched", zap.String("ns", a.ns), zap.Int("count", len(dialogs)), zap.Bool("refresh", refresh))

	writeJSON(w, http.StatusOK, map[string]any{"chats": dialogs, "cached": false})
}

// handleListMessages pages a chat's history (newest first).
// Query: offset_id (cursor), limit, media_only, refresh.
func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}

	chatID := mux.Vars(r)["id"]
	q := r.URL.Query()
	offsetID, _ := strconv.Atoi(q.Get("offset_id"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	mediaOnly := q.Get("media_only") == "1"
	refresh := q.Get("refresh") == "1"

	cacheKey := "msgs_" + a.ns + "_" + chatID + "_" + strconv.Itoa(offsetID) + "_" + strconv.Itoa(limit)
	if mediaOnly {
		cacheKey += "_m"
	}

	if !refresh {
		var cached msgsPayloadT
		if fresh, _ := s.cache.readMeta(cacheKey, msgsCacheTTL, &cached); fresh {
			// 缓存命中也预热缩略图：用户清了 media 缓存或首次访问时，消息列表虽
			// 有 meta 缓存但缩略图是冷的。不预热会导致 LazyThumb 逐个冷下载，体
			// 现为"流量跑不起来、缩略图加载慢"。warmupThumbs 内部跳过已缓存的。
			s.warmupChatThumbs(a, chatID, cached.Messages)
			s.log().Info("messages served (cache hit)",
				zap.String("chat", chatID), zap.Int("count", len(cached.Messages)),
				zap.Bool("mediaOnly", mediaOnly))
			writeJSON(w, http.StatusOK, map[string]any{
				"messages":   cached.Messages,
				"nextCursor": cached.NextCursor,
				"cached":     true,
			})
			return
		}
	}

	start := time.Now()
	msgs, next, err := s.fetchMessagesFlight(r.Context(), a, chatID, offsetID, limit, mediaOnly, cacheKey)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.log().Warn("fetch messages failed", zap.String("chat", chatID), zap.Error(err))
		}
		// 连接重连中/RPC 抖动：返回 503 让前端轮询重试，而非 500 显示"加载失败"。
		// 重试耗尽后仍失败，说明主 client 连接断开重连中（gotd 单连接架构特性），
		// 这种情况下用户手动刷新没用，需等连接恢复后前端自动重载。
		writeErr(w, http.StatusServiceUnavailable, errString("连接重连中，请稍候"))
		return
	}

	s.log().Info("messages fetched",
		zap.String("chat", chatID), zap.Int("offset", offsetID), zap.Int("limit", limit),
		zap.Bool("mediaOnly", mediaOnly), zap.Int("count", len(msgs)),
		zap.Duration("cost", time.Since(start)))

	s.warmupChatThumbs(a, chatID, msgs)

	writeJSON(w, http.StatusOK, map[string]any{
		"messages":   msgs,
		"nextCursor": next,
		"cached":     false,
	})
}

// warmupChatThumbs pre-downloads thumbnails for the given messages in the
// background so the browser's LazyThumb requests hit cache. Pages of the same
// chat share one warmup ctx; switching to another chat cancels the previous.
// Skips messages whose thumbs are already cached (warmupThumbs checks
// thumbExists), so calling it on cache-hit pages is cheap when thumbs exist.
func (s *Server) warmupChatThumbs(a *account, chatID string, msgs []*chat.MessageInfo) {
	if len(msgs) == 0 {
		return
	}
	s.warmupMu.Lock()
	if s.warmupSession.chatID != chatID || s.warmupSession.ctx == nil || s.warmupSession.ctx.Err() != nil {
		if s.warmupSession.cancel != nil {
			s.warmupSession.cancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.warmupSession.ctx = ctx
		s.warmupSession.cancel = cancel
		s.warmupSession.chatID = chatID
	}
	ctx := s.warmupSession.ctx
	s.warmupMu.Unlock()
	go s.warmupThumbs(ctx, a, chatID, msgs)
}
