# 频道内全文搜索实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 tmm 加频道内消息全文搜索：搜索框输入关键词（含 # 标签）→ 服务器 `messages.search` 索引秒回 → 下拉结果 → 点击任意位置跳转。

**Architecture:** 三层：(1) `app/chat` 加 `SearchMessages`（封装 `messages.search`，复用 `FetchMessages` 的 MessageInfo 转换）+ `FetchMessageAround`（按 msgid 取上下文定位）；(2) `server` 加 `GET /api/chats/{id}/search` + `GET /api/chats/{id}/around` 两个端点（复用 fetchMessagesFlight 单飞 + meta 缓存）；(3) 前端 Messages.vue 消息区头部加搜索框 + 下拉结果面板 + 点击跳转（复用现有 `scrollToMsg`）。

**Tech Stack:** Go（app/chat、server）、Vue 3 + Element Plus（web/app/src）、SSE 无关（搜索是普通 HTTP）。

## Global Constraints

- 行为约束（来自 spec）：
  - `q` 原样透传给 `messages.search`（`#` 是普通字符，无需转义/前缀处理）。
  - **空查询校验**：去首尾空白 + 去掉所有 `#` 后剩余仍有 ≥1 非空白字符才算有效。无效（`#`/`##`/空白）前端不发起请求 + 后端 400。
  - 除空查询外不做白名单过滤（emoji/符号都放行）。
  - 搜索结果返回 `results + nextOffset + count`（count = 服务器总匹配数）。
  - around 端点按 `msg` 取上下文，返回消息**含目标 msgID 本身**，供 `scrollToMsg` 定位。
  - 搜索缓存短 TTL 5 分钟；around 不缓存。
  - 搜索状态前端独立，不污染现有 messages/session；跳转后替换 messages 列表（进入目标上下文视图），关闭只关下拉不改列表。
- 复用现有：`fetchMessagesFlight` 单飞结构（`server/chats.go:41`）、`meta` 缓存（`server/cache.go`）、`scrollToMsg`（`Messages.vue:695`）、LazyThumb 缩略图。
- 回归：现有 `go test ./app/... ./core/... ./server/...` 全绿。
- 提交规范：每任务独立 commit，`feat:`（功能）前缀。
- 命令均在 `D:\Documents\workspace\telegram manager\tdl`（Git Bash）下执行。

---

### Task 1: app/chat 加 SearchMessages + FetchMessageAround

**Files:**
- Create: `app/chat/search.go`
- Modify: `app/chat/messages.go`（提取 MessageInfo 转换复用，若需要）
- Create: `app/chat/search_test.go`

**Interfaces:**
- Produces:
  ```go
  // SearchResult 是一次搜索的完整结果。
  type SearchResult struct {
      Results    []*MessageInfo `json:"results"`
      NextOffset int            `json:"nextOffset"`
      Count      int            `json:"count"`
  }
  // ValidSearchQuery 判定搜索查询是否有效（去 # 后仍有非空白字符）。
  func ValidSearchQuery(q string) bool
  // SearchMessages 在单个频道内搜索消息文本（含 # 标签）。服务器端索引（messages.search）。
  func SearchMessages(ctx context.Context, c *telegram.Client, kvd storage.Storage,
      chatID, q string, offsetID, limit int) (*SearchResult, error)
  // FetchMessageAround 按 msgid 取该消息及其往前 limit 条历史，用于跳转定位。
  func FetchMessageAround(ctx context.Context, c *telegram.Client, kvd storage.Storage,
      chatID string, msgID, limit int) ([]*MessageInfo, error)
  ```
- Consumes: `MessageInfo`（`app/chat/messages.go:54`）、`MessageMedia`（同文件）、`tmedia.GetMedia`、`mediaDimensions/mediaMime/extractThumb`、`manager.Apply(ctx, users, chats)`（gotd peers.Manager，`messages.go:248` 同款）。

- [ ] **Step 1: 写失败测试**

`app/chat/search_test.go`：

```go
package chat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 搜索是真实 Telegram RPC，无法单测 mock；这些测试验证纯逻辑边界。
// 真正调通由 Task 4 端到端（临时程序或真实账号）验证。

func TestValidSearchQuery(t *testing.T) {
	// 空查询校验规则（与 spec §3.0 一致）：去 # 后仍有非空白字符才有效
	cases := []struct {
		q    string
		want bool
	}{
		{"#", false},
		{"##", false},
		{"# ", false},
		{"# #", false},
		{"", false},
		{"   ", false},
		{"#分", true},
		{"#分享", true},
		{"# 分", true},
		{"#💩", true},
		{"分享", true},
		{"分", true},
	}
	for _, c := range cases {
		got := ValidSearchQuery(c.q)
		require.Equal(t, c.want, got, "q=%q", c.q)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./app/chat/ -run TestValidSearchQuery -v`
Expected: 编译失败（`validSearchQuery` 未定义）。

- [ ] **Step 3: 实现 validSearchQuery + SearchMessages + FetchMessageAround**

`app/chat/search.go`：

```go
package chat

import (
	"context"
	"strings"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/util/tutil"
)

// ValidSearchQuery 判定搜索查询是否有效：去首尾空白 + 去掉所有 # 后
// 剩余仍有至少 1 个非空白字符。纯 #/##/空白 服务器返回 SEARCH_QUERY_EMPTY。
func ValidSearchQuery(q string) bool {
	s := strings.TrimSpace(strings.ReplaceAll(q, "#", ""))
	return s != ""
}

// SearchResult 是一次搜索的完整结果。
type SearchResult struct {
	Results    []*MessageInfo `json:"results"`
	NextOffset int            `json:"nextOffset"`
	Count      int            `json:"count"`
}

// SearchMessages 在单个频道内搜索消息文本（含 # 标签）。
// 服务器端索引（messages.search），频道多大都是亚秒级。
func SearchMessages(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID, q string, offsetID, limit int) (*SearchResult, error) {

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return nil, errors.Wrap(err, "resolve chat")
	}

	if limit <= 0 || limit > 100 {
		limit = 50 // 默认/上限
	}

	res, err := c.API().MessagesSearch(ctx, &tg.MessagesSearchRequest{
		Peer:     peer.InputPeer(),
		Q:        q,
		OffsetID: offsetID,
		Limit:    limit,
		Filter:   &tg.InputMessagesFilterEmpty{},
	})
	if err != nil {
		return nil, errors.Wrap(err, "search messages")
	}

	ch, ok := res.(*tg.MessagesChannelMessages)
	if !ok {
		return nil, errors.Errorf("unexpected search result type %T", res)
	}

	// 预热 peer 缓存：search 返回 Users/Chats 两个 slice，直接用 gotd peers.Manager.Apply
	// （与 applyEntities 内部同款，messages.go:248）。
	if len(ch.Users)+len(ch.Chats) > 0 {
		_ = manager.Apply(ctx, ch.Users, ch.Chats)
	}

	out := make([]*MessageInfo, 0, len(ch.Messages))
	nextOffset := 0
	for _, mc := range ch.Messages {
		m, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		nextOffset = m.ID // search 的 offset 用 msgid
		out = append(out, messageInfoFrom(m))
	}
	return &SearchResult{Results: out, NextOffset: nextOffset, Count: ch.Count}, nil
}

// FetchMessageAround 按 msgid 取该消息及其往前 limit 条历史，用于跳转定位。
func FetchMessageAround(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID string, msgID, limit int) ([]*MessageInfo, error) {

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return nil, errors.Wrap(err, "resolve chat")
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// messages.getHistory offset=msgID 往回取，含 msgID 本身
	q := query.NewQuery(c.API()).Messages().GetHistory(peer.InputPeer()).OffsetID(msgID)
	iter := messages.NewIterator(q, 100)

	out := make([]*MessageInfo, 0, limit)
	for iter.Next(ctx) && len(out) < limit {
		elem := iter.Value()
		applyEntities(ctx, manager, elem.Entities)
		if m, ok := elem.Msg.(*tg.Message); ok {
			out = append(out, messageInfoFrom(m))
		}
	}
	if err := iter.Err(); err != nil {
		return nil, errors.Wrap(err, "fetch around")
	}
	return out, nil
}
```

`messageInfoFrom` 从 `FetchMessages` 的循环体提取（`app/chat/messages.go` 现有逻辑 → 抽成独立函数，`FetchMessages` 改调它，避免重复）：

```go
// messageInfoFrom 把 tg.Message 转成 MessageInfo（含 media 提取）。
// 从 FetchMessages 循环体提取，供 FetchMessages / SearchMessages / FetchMessageAround 复用。
func messageInfoFrom(m *tg.Message) *MessageInfo {
	media, hasMedia := tmedia.GetMedia(m)
	info := &MessageInfo{
		ID:       m.ID,
		Date:     m.Date,
		Text:     m.Message,
		Views:    m.Views,
		Forwards: m.Forwards,
		Out:      m.Out,
		HasMedia: hasMedia,
	}
	if gid, ok := m.GetGroupedID(); ok {
		info.GroupedID = gid
	}
	if hasMedia {
		mm := &MessageMedia{Name: media.Name, Size: media.Size, DC: media.DC}
		mm.Width, mm.Height = mediaDimensions(m)
		mm.Mime = mediaMime(m)
		mm.Thumb = extractThumb(m)
		if st := tmedia.GetStrippedThumb(m); st != nil {
			mm.Stripped = &StrippedThumb{DataURI: st.DataURI, W: st.W, H: st.H}
		}
		info.Media = mm
	}
	return info
}
```

**`FetchMessages` 改造注意**：提取后 `FetchMessages` 循环体内 `mediaOnly` 过滤（`if mediaOnly && !hasMedia { continue }`）**必须保留在循环里**（在 `messageInfoFrom` 调用前判断），`messageInfoFrom` 本身不含 mediaOnly 逻辑（search/around 不需要 mediaOnly）。改造后 `FetchMessages` 行为必须逐字节不变（现有测试回归）。

**applyEntities 签名确认**：`FetchMessages` 用 `applyEntities(ctx, manager, elem.Entities)`（单参数实体），而 search 返回 `ch.Users + ch.Chats`（两个 slice）。查 `applyEntities` 定义，若它接受 `*tg.Message` 的 entities 集合，需要适配——可能改为 `applyEntities(ctx, manager, ch.Users...)` 分开调，或加一个接受 Users/Chats 的重载。**实现时以 `app/chat/messages.go` 里 applyEntities 的实际签名为准**，若它只接受一种类型，search 里用 `manager.Apply(ctx, ch.Users, ch.Chats)`（gotd peers.Manager 自带，`app/chat/messages.go:248` 已见 `_ = manager.Apply(ctx, users, chats)`）。

**query/messages import**：`FetchMessageAround` 用 `query.NewQuery(...).Messages().GetHistory(...)` + `messages.NewIterator`，import `"github.com/gotd/td/telegram/query"` 和 `"github.com/gotd/td/telegram/messages"`（与 `app/chat/messages.go` 一致）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./app/chat/ -run TestValidSearchQuery -v`
Expected: PASS。

- [ ] **Step 5: 编译 + 回归**

Run: `go build ./... && go test ./app/chat/... ./app/dl/... ./server/...`
Expected: 全绿（`FetchMessages` 提取 messageInfoFrom 后行为不变）。

- [ ] **Step 6: Commit**

```bash
git add app/chat/search.go app/chat/search_test.go app/chat/messages.go
git commit -m "feat(chat): SearchMessages（messages.search 服务器索引）+ FetchMessageAround（按 msgid 取上下文）+ validSearchQuery 空查询校验"
```

---

### Task 2: server 搜索/around 端点 + 单飞 + 缓存

**Files:**
- Modify: `server/chats.go`（handleChatSearch + handleChatAround + search 单飞/缓存）
- Modify: `server/server.go:253-292`（注册路由）
- Create: `server/chats_search_test.go`

**Interfaces:**
- Consumes: `chat.SearchMessages`、`chat.FetchMessageAround`（Task 1）、`fetchMessagesFlight` 模式、`msgsInflight`/`msgsMu`（server.go:99-100）、`s.cache` meta 缓存（cache.go）。
- Produces:
  - Route `GET /api/chats/{id}/search`（q, offset, limit query）
  - Route `GET /api/chats/{id}/around`（msg, limit query）

- [ ] **Step 1: 写失败测试**

`server/chats_search_test.go`：

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// 搜索 handler 的纯逻辑测试（不连 telegram——activeAccount 返回 nil 走 401 分支）
func TestHandleChatSearchNoAccount(t *testing.T) {
	s := newTestServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/chats/1/search?q=%23分享", nil)
	w := httptest.NewRecorder()
	s.handleChatSearch(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code) // 未登录
}
```

（真实搜索需 telegram，401/400 校验分支可测，RPC 路径 Task 4 端到端验证。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./server/ -run TestHandleChatSearchNoAccount -v`
Expected: 编译失败（`handleChatSearch` 未定义）。

- [ ] **Step 3: 实现 handler + 单飞 + 缓存**

`server/chats.go` 加：

```go
// searchCacheTTL 搜索缓存短 TTL——搜索结果秒回但避免高频重复 RPC。
const searchCacheTTL = 5 * time.Minute
```

**搜索单飞 + 缓存的完整实现**：参照 `fetchMessagesFlight`（`server/chats.go:41-94`）——search 需要独立 `searchInflight map[string]*searchCall` + `searchMu`。缓存读写参照 `handleListMessages` 的 `readMeta/writeMeta`（`cache.go`），`count` 来自 `SearchResult.Count`（Task 1 已返回）。

`server/chats.go` 顶部加：

```go
// searchCall 是并发搜索请求共享结果的 flight。
type searchCall struct {
	done chan struct{}
	res  *chat.SearchResult
	err  error
}
```

`handleChatSearch` 完整实现：

```go
func (s *Server) handleChatSearch(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}
	chatID := mux.Vars(r)["id"]
	q := r.URL.Query().Get("q")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	if !chat.ValidSearchQuery(q) {
		writeErr(w, http.StatusBadRequest, errString("搜索关键词为空"))
		return
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	cacheKey := "search_" + a.ns + "_" + chatID + "_" + q + "_" + strconv.Itoa(offset)
	cached := false

	// 缓存命中
	var cachedRes chat.SearchResult
	if fresh, _ := s.cache.readMeta(cacheKey, searchCacheTTL, &cachedRes); fresh {
		writeJSON(w, http.StatusOK, map[string]any{
			"results":    cachedRes.Results,
			"nextOffset": cachedRes.NextOffset,
			"count":      cachedRes.Count,
			"cached":     true,
		})
		return
	}

	// 单飞：同 (chat, q, offset) 并发共享一个 RPC
	s.searchMu.Lock()
	if c, ok := s.searchInflight[cacheKey]; ok {
		s.searchMu.Unlock()
		select {
		case <-c.done:
			if c.err != nil {
				writeErr(w, http.StatusServiceUnavailable, errString("连接重连中，请稍候"))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"results":    c.res.Results,
				"nextOffset": c.res.NextOffset,
				"count":      c.res.Count,
				"cached":     false,
			})
			return
		case <-r.Context().Done():
			writeErr(w, http.StatusServiceUnavailable, errString("连接重连中，请稍候"))
			return
		case <-time.After(45 * time.Second):
			writeErr(w, http.StatusServiceUnavailable, errString("等待搜索超时"))
			return
		}
	}
	c := &searchCall{done: make(chan struct{})}
	s.searchInflight[cacheKey] = c
	s.searchMu.Unlock()

	// 后台执行（detached ctx，复用 fetchMessagesFlight 模式）
	c.res, c.err = chat.SearchMessages(context.Background(), a.client, a.kvd, chatID, q, offset, limit)
	if c.err == nil && c.res != nil && len(c.res.Results) > 0 {
		_ = s.cache.writeMeta(cacheKey, *c.res)
	}

	s.searchMu.Lock()
	delete(s.searchInflight, cacheKey)
	s.searchMu.Unlock()
	close(c.done)

	if c.err != nil {
		writeErr(w, http.StatusServiceUnavailable, errString("连接重连中，请稍候"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":    c.res.Results,
		"nextOffset": c.res.NextOffset,
		"count":      c.res.Count,
		"cached":     false,
	})
}
```

`Server` 结构加字段（`server/server.go`）：

```go
	searchMu       sync.Mutex
	searchInflight map[string]*searchCall
```

`newServer`/`New` 里初始化 `searchInflight: make(map[string]*searchCall)`。

`handleChatAround` 完整实现：

```go
func (s *Server) handleChatAround(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}
	chatID := mux.Vars(r)["id"]
	msgID, _ := strconv.Atoi(r.URL.Query().Get("msg"))
	if msgID <= 0 {
		writeErr(w, http.StatusBadRequest, errString("消息ID无效"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	msgs, err := chat.FetchMessageAround(r.Context(), a.client, a.kvd, chatID, msgID, limit)
	if err != nil {
		s.log().Warn("fetch around failed", zap.String("chat", chatID), zap.Int("msg", msgID), zap.Error(err))
		writeErr(w, http.StatusServiceUnavailable, errString("连接重连中，请稍候"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}
```

需要 `context`、`sync`、`time` import（chats.go 已有部分）。

- [ ] **Step 4: server.go 注册路由**

`server/server.go`（`/chats/{id}/messages` 旁）加：

```go
	api.HandleFunc("/chats/{id}/search", s.handleChatSearch).Methods(http.MethodGet)
	api.HandleFunc("/chats/{id}/around", s.handleChatAround).Methods(http.MethodGet)
```

- [ ] **Step 5: 全量测试**

Run: `go build ./... && go test ./server/... ./app/chat/...`
Expected: 全绿。

- [ ] **Step 6: Commit**

```bash
git add server/chats.go server/server.go server/chats_search_test.go app/chat/search.go app/chat/search_test.go
git commit -m "feat(server): 频道搜索/around 端点——search 单飞+短缓存，q 空查询 400，msg 无效 400"
```

---

### Task 3: 前端搜索框 + 下拉结果 + 点击跳转

**Files:**
- Modify: `web/app/src/api/index.js`（searchMessages + chatAround）
- Modify: `web/app/src/views/Messages.vue`（搜索框 + 下拉面板 + 跳转逻辑）

**Interfaces:**
- Consumes: `api.req`（api/index.js:3-27）、`scrollToMsg`（Messages.vue:695）、`current.id`（当前频道）、`messages`/`nextCursor`（现有消息状态）、LazyThumb 组件、`displayItems`。
- Produces: 搜索状态（searchOpen/searchQuery/searchResults/searchOffset/searchCount/searchLoading/searchHasMore）+ 搜索框 UI + 下拉结果 + 点击跳转。

- [ ] **Step 1: api.js 加方法**

`web/app/src/api/index.js`（`listMessages` 旁）加：

```js
  searchMessages: (chatId, { q, offset = 0, limit = 50 } = {}) =>
    req('GET', `/api/chats/${chatId}/search?q=${encodeURIComponent(q)}&offset=${offset}&limit=${limit}`),
  chatAround: (chatId, msgId, limit = 50) =>
    req('GET', `/api/chats/${chatId}/around?msg=${msgId}&limit=${limit}`),
```

- [ ] **Step 2: 空查询校验工具**

`Messages.vue` script 加：

```js
// 空查询校验（spec §3.0）：去 # 后仍有非空白字符才有效
function validSearchQuery(q) {
  return q.replace(/#/g, '').trim().length > 0
}
```

- [ ] **Step 3: 搜索状态 + 搜索框模板**

`Messages.vue` 头部（`chat-header` 的 `spacer` 后、`只看媒体` 前）加搜索框：

```html
<div class="chat-search-inline" :class="{ open: searchOpen }">
  <input
    v-model="searchQuery"
    placeholder="搜索本频道…"
    @input="onSearchInput"
    @keydown.esc="closeSearch"
    @focus="searchOpen = true"
  />
  <button v-if="searchQuery" class="search-clear" @click="clearSearch">✕</button>
</div>
```

script 加状态与逻辑：

```js
// --- 频道内搜索状态（独立于 messages/session，不污染现有视图） ---
const searchOpen = ref(false)
const searchQuery = ref('')
const searchResults = ref([])
const searchOffset = ref(0)
const searchCount = ref(0)
const searchLoading = ref(false)
const searchHasMore = ref(false)
let searchTimer = null

function onSearchInput() {
  clearTimeout(searchTimer)
  if (!validSearchQuery(searchQuery.value)) {
    searchResults.value = []
    searchHasMore.value = false
    return // 空查询不发起请求（spec §3.0）
  }
  searchTimer = setTimeout(() => doSearch(0), 300) // 防抖
}

async function doSearch(offset) {
  if (!current.value || !validSearchQuery(searchQuery.value)) return
  searchLoading.value = true
  try {
    const d = await api.searchMessages(current.value.id, { q: searchQuery.value, offset })
    if (offset === 0) {
      searchResults.value = d.results || []
      searchOffset.value = d.nextOffset || 0
      searchCount.value = d.count || 0
    } else {
      searchResults.value.push(...(d.results || []))
      searchOffset.value = d.nextOffset || 0
    }
    searchHasMore.value = searchOffset.value > 0
  } catch (e) {
    ElMessage.error(e.message || '搜索失败')
  } finally {
    searchLoading.value = false
  }
}

function closeSearch() {
  searchOpen.value = false
  searchQuery.value = ''
  searchResults.value = []
  searchHasMore.value = false
}
function clearSearch() {
  searchQuery.value = ''
  searchResults.value = []
  searchHasMore.value = false
}
// 滚动结果到底部续拉
function onSearchScroll(e) {
  const el = e.target
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 100 && searchHasMore.value && !searchLoading.value) {
    doSearch(searchOffset.value)
  }
}
// 点击结果：取上下文 → 替换列表 → 定位
async function jumpToResult(res) {
  if (!current.value) return
  closeSearch()
  try {
    const d = await api.chatAround(current.value.id, res.id, 50)
    const msgs = d.messages || []
    if (!msgs.length) {
      ElMessage.warning('消息不存在或已删除')
      return
    }
    messages.value = msgs // 替换列表（进入目标上下文视图，spec §3.4）
    nextCursor.value = msgs[msgs.length - 1].id // 对齐：往前可继续加载
    if (currentSession.value) currentSession.value.messages = msgs
    await nextTick()
    scrollToMsg(res.id)
  } catch (e) {
    ElMessage.error(e.message || '跳转失败')
  }
}
```

**注意**：`messages`/`nextCursor`/`currentSession` 是现有状态变量名（实现时以 Messages.vue 实际变量为准，见 `reloadMessages`/`loadMore` 的用法）。`currentSession` 若不存在，只更新 `messages` + `nextCursor` + session 内存。

- [ ] **Step 4: 下拉结果面板模板**

在 `msg-scroll` 上方（或作为覆盖层）加：

```html
<div v-if="searchOpen && validSearchQuery(searchQuery)" class="search-dropdown" @scroll="onSearchScroll">
  <div class="search-drop-head">
    <span>{{ searchCount }} 条匹配</span>
    <span v-if="searchLoading" class="muted">搜索中…</span>
  </div>
  <div v-for="res in searchResults" :key="res.id" class="search-item" @click="jumpToResult(res)">
    <LazyThumb v-if="res.hasMedia" :msg="res" :size="36" />
    <div class="search-item-body">
      <div class="search-item-text">{{ firstLine(res.text) || '(无文本)' }}</div>
      <div class="search-item-date muted">{{ dateStrFromTs(res.date) }}</div>
    </div>
  </div>
  <div v-if="!searchResults.length && !searchLoading" class="muted search-empty">无匹配消息</div>
  <div v-if="searchHasMore" class="muted search-more">加载更多…</div>
</div>
```

**注意**：`LazyThumb`/`firstLine`/`dateStrFromTs` 是现有组件/工具（实现时以 Messages.vue 实际用法为准）。

- [ ] **Step 5: 样式**

`Messages.vue` `<style>` 加 `.chat-search-inline` / `.search-dropdown` / `.search-item` 等样式，延续现有 token（背景、边框、hover）。

- [ ] **Step 6: 前端构建**

Run: `cd web/app && npm run build`
Expected: 构建成功。

- [ ] **Step 7: Commit**

```bash
git add web/app/src/api/index.js web/app/src/views/Messages.vue
git commit -m "feat(web): 频道内搜索框+下拉结果+点击跳转——空查询不发起，防抖 300ms，around 定位"
```

---

### Task 4: 端到端验证 + 部署 + 文档

**Files:**
- Run: 后端构建 + 全量测试 + 前端构建 + 部署 + 真实账号验证
- Modify: `docs/交接-2026-08-03.md`（或新交接文档记录）

**Interfaces:**
- Consumes: Task 1-3 全部产物。

- [ ] **Step 1: 后端全量测试**

Run: `go build ./... && go test ./app/... ./core/... ./server/...`
Expected: 全绿。

- [ ] **Step 2: 重建 + 部署**

Run: `./build-tmm.sh web`
Expected: `tmm.v11/tmm.exe` 干净标签 + web 部署。

- [ ] **Step 3: 重启服务器 + 真实账号端到端验证**

Run: 杀旧 tmm → 重启 → 浏览器或 curl 验证：
1. `GET /api/chats/2070986972/search?q=分享` → 返回 results + count（>0）
2. `GET /api/chats/2070986972/search?q=#` → 400「搜索关键词为空」（空查询校验）
3. `GET /api/chats/2070986972/around?msg=4594&limit=50` → 返回含 msg 4594 的消息
4. 浏览器：搜 `#分享` → 下拉结果 → 点击 → 跳转定位
5. 浏览器：搜 `#`（纯标签）→ 不发起请求（无结果面板）

- [ ] **Step 4: 文档**

交接文档记录本次功能（搜索端点、前端交互、验证结果），遗留事项标记。

- [ ] **Step 5: Commit**

```bash
git add docs/
git commit -m "docs: 频道内搜索实现完成——更新交接文档"
```
