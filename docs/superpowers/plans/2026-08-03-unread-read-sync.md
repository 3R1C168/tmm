# 未读定位 + 已读同步 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 仿桌面端：进入频道停在第一条未读 + 未读分隔线，跳转按钮（未读区↑跳末尾=已读、历史区↓回第一条），自动/手动已读设置开关。

**Architecture:** 后端 `DialogInfo` 补 `ReadInboxMaxID`（免费随对话附带）+ 新 `read` 接口（`messages.readHistory`）+ settings 加 `AutoRead`；前端进入频道用 `offset_id=ReadInboxMaxID+1` 定位未读起点，插入分隔线，右下角跳转按钮方向随位置动态，自动/手动已读开关控制。

**Tech Stack:** Go 1.26.5 / gotd v0.140.0（`Dialog.ReadInboxMaxID`/`MessagesReadHistory`）/ Vue 3.5 / Element Plus / lucide-vue-next。

## Global Constraints

- Go 构建：`go build ./...`、`go vet ./app/chat ./server`、`go test ./app/chat ./server -count=1` 全绿
- 前端：`cd web/app && npm run build` 成功；部署 `./build-tmm.sh web`
- gotd conditional 字段用 `GetXxx` helper（`ReadInboxMaxID` 有 flag）
- 单测用 `github.com/stretchr/testify/require`
- 注释中文（延续代码库风格）
- 保持 tmm 倒序（最新在上）；跳转按钮**图标方向 = 实际滚动方向**，随位置动态
- 已读只在「未读区点↑跳末尾」时触发，翻历史不误触
- settings 默认 `AutoRead: true`（自动已读，仿桌面端）

---

### Task 1: 后端 DialogInfo 补 ReadInboxMaxID + FetchDialogs 填充

**Files:**
- Modify: `app/chat/fetch.go`
- Test: `app/chat/fetch_test.go`（若不存在则新建）

**Interfaces:**
- Consumes: 无（自包含）
- Produces: `DialogInfo.ReadInboxMaxID int json:"readInboxMaxId,omitempty"`（后续 Task 2 前端用、Task 3 接口用）

- [ ] **Step 1: 写失败测试**（`app/chat/fetch_test.go` 追加）

```go
func TestDialogInfoReadInboxMaxID(t *testing.T) {
	// DialogInfo 结构有 ReadInboxMaxID 字段（编译期验证）
	var d DialogInfo
	d.ReadInboxMaxID = 12345
	require.Equal(t, 12345, d.ReadInboxMaxID)
	require.Contains(t, "readInboxMaxId", `readInboxMaxId,omitempty`)
}
```
（真实 RPC 填充由端到端验证；此处验证字段存在 + JSON tag）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd "/d/Documents/workspace/telegram manager/tdl" && go test ./app/chat -run TestDialogInfoReadInboxMaxID -count=1`
Expected: FAIL（`d.ReadInboxMaxID undefined`）

- [ ] **Step 3: 实现**（`app/chat/fetch.go`）

在 `DialogInfo` 结构加字段：
```go
	UnreadCount int    `json:"unreadCount"`
	Pinned      bool   `json:"pinned,omitempty"`
	// ReadInboxMaxID 是该会话已读到的最大消息 ID（未读起点 = +1）。
	// 来自 gotd Dialog.ReadInboxMaxID，随对话列表免费附带。
	ReadInboxMaxID int `json:"readInboxMaxId,omitempty"`
```

在 `FetchDialogs` 填充（找 `UnreadCount: dlg.UnreadCount` 附近，或 `info := &DialogInfo{...}` 构造处）：
```go
	info.ReadInboxMaxID = dlg.ReadInboxMaxID
```
（若 DialogInfo 是 `info := &DialogInfo{...}` 结构体字面量构造，直接加 `ReadInboxMaxID: dlg.ReadInboxMaxID` 字段；`dlg` 是 gotd `*tg.Dialog`。若构造处取 `dlg` 的方式不同，以实际代码为准，逻辑不变）

- [ ] **Step 4: 跑测试确认通过**

Run: `cd "/d/Documents/workspace/telegram manager/tdl" && go build ./... && go vet ./app/chat && go test ./app/chat -count=1`
Expected: 全绿

- [ ] **Step 5: Commit**

```bash
cd "/d/Documents/workspace/telegram manager/tdl"
git add app/chat/fetch.go app/chat/fetch_test.go
git commit -m "feat(chat): DialogInfo 补 ReadInboxMaxID（未读起点，随对话免费附带）"
```

---

### Task 2: 后端 read 接口 + settings AutoRead

**Files:**
- Modify: `app/chat/fetch.go`（新 `FetchMarkRead`）
- Modify: `server/chats.go`（`handleMarkRead`）
- Modify: `server/server.go`（路由）
- Modify: `server/settings.go`（`AutoRead` 字段 + default + snapshot）
- Modify: `server/handlers.go`（`updateSettingsRequest` 加 `AutoRead`）
- Test: `server/settings_test.go`

**Interfaces:**
- Consumes: Task 1 的 `DialogInfo.ReadInboxMaxID`（不直接依赖）；现有 `tutil.GetInputPeer`、`a.client`、`a.kvd`、`writeJSON`/`writeErr`、`errString`
- Produces:
  - `func FetchMarkRead(ctx context.Context, c *telegram.Client, kvd storage.Storage, chatID string, maxID int) error`（app/chat）
  - `POST /api/chats/{id}/read?max_id=N` → 200（server）
  - `settings.AutoRead bool` + `updateSettingsRequest.AutoRead *bool`

- [ ] **Step 1: 写失败测试**（`server/settings_test.go` 追加）

```go
func TestSettingsAutoReadDefaultAndUpdate(t *testing.T) {
	st := defaultSettings("")
	require.True(t, st.AutoRead, "默认自动已读")
	// 手动模式
	err := st.update(&updateSettingsRequest{AutoRead: boolPtr(false)})
	require.NoError(t, err)
	require.False(t, st.AutoRead)
	// snapshot 包含
	snap := st.snapshot()
	require.Equal(t, false, snap["autoRead"])
}
```
（`boolPtr` 若不存在加个 helper：`func boolPtr(b bool) *bool { return &b }`）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd "/d/Documents/workspace/telegram manager/tdl" && go test ./server -run TestSettingsAutoRead -count=1`
Expected: FAIL（`st.AutoRead undefined` / `snap["autoRead"]` 缺）

- [ ] **Step 3: 实现 settings**（`server/settings.go`）

`settings` struct 加字段（在 `ClearPreviewOnExit` 附近）：
```go
	// AutoRead 进入频道定位未读后是否自动标记已读（false = 手动模式）。
	AutoRead bool `json:"autoRead"`
```
`defaultSettings` 加：
```go
		AutoRead:           true,
```
`settingsJSON` 加（指针区分未设置）：
```go
	AutoRead *bool `json:"autoRead"`
```
`load()` 里读取（找其他指针字段的 load 模式，如 `ClearPreviewOnExit`）：
```go
	if v := aux.AutoRead; v != nil {
		st.AutoRead = *v
	}
```
`snapshot()` 加：
```go
	"autoRead":  st.AutoRead,
```
（`settingsJSON` 的 load 辅助结构：load 时把 settingsJSON 填进 settings，参考现有字段）

- [ ] **Step 4: 实现 updateSettingsRequest**（`server/handlers.go`）

```go
type updateSettingsRequest struct {
	// ...现有字段
	AutoRead *bool `json:"autoRead"`
}
```
`update` 方法加：
```go
	if a.AutoRead != nil {
		st.AutoRead = *a.AutoRead
	}
```

- [ ] **Step 5: 实现 FetchMarkRead + 接口**

`app/chat/fetch.go` 加：
```go
// FetchMarkRead 标记会话 ≤maxID 的消息已读（messages.readHistory）。
func FetchMarkRead(ctx context.Context, c *telegram.Client, kvd storage.Storage, chatID string, maxID int) error {
	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return errors.Wrap(err, "resolve chat")
	}
	if _, err := c.API().MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{
		Peer:  peer.InputPeer(),
		MaxID: maxID,
	}); err != nil {
		return errors.Wrap(err, "mark read")
	}
	return nil
}
```
（import 补 `github.com/gotd/td/telegram/peers`、`github.com/iyear/tdl/core/util/tutil`、`tg`、`errors`——按 fetch.go 现有 import 风格）

`server/chats.go` 加 handler（`handleChatAround` 后）：
```go
// handleMarkRead 标记频道消息已读（≤maxID）。仿桌面端「跳到未读末尾=已读」。
func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}
	chatID := mux.Vars(r)["id"]
	maxID, _ := strconv.Atoi(r.URL.Query().Get("max_id"))
	if maxID <= 0 {
		writeErr(w, http.StatusBadRequest, errString("max_id 无效"))
		return
	}
	if err := chat.FetchMarkRead(r.Context(), a.client, a.kvd, chatID, maxID); err != nil {
		s.log().Warn("mark read failed", zap.String("chat", chatID), zap.Int("maxId", maxID), zap.Error(err))
		writeErr(w, http.StatusServiceUnavailable, errString("连接重连中，请稍候"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
```

`server/server.go` 注册路由（`/chats/{id}/around` 后）：
```go
	api.HandleFunc("/chats/{id}/read", s.handleMarkRead).Methods(http.MethodPost)
```

- [ ] **Step 6: 构建 + 测试 + commit**

Run: `cd "/d/Documents/workspace/telegram manager/tdl" && go build ./... && go vet ./app/chat ./server && go test ./app/chat ./server -count=1`
Expected: 全绿（handler 无法单测，E2E 覆盖）

```bash
cd "/d/Documents/workspace/telegram manager/tdl"
git add app/chat/fetch.go server/chats.go server/server.go server/settings.go server/handlers.go server/settings_test.go
git commit -m "feat: read 接口（messages.readHistory）+ settings AutoRead（自动/手动已读开关）"
```

---

### Task 3: 前端 api + 进入频道定位未读 + 分隔线

**Files:**
- Modify: `web/app/src/api/index.js`
- Modify: `web/app/src/views/Messages.vue`
- Create: `web/app/src/components/UnreadDivider.vue`（可选，或直接模板内联）

**Interfaces:**
- Consumes: Task 1 `DialogInfo.readInboxMaxId`、Task 2 `POST /api/chats/{id}/read`
- Produces:
  - `api.markRead(chatId, maxId)` —— `req('POST', ...)`
  - 进入频道首次加载 `offsetId = readInboxMaxId + 1`（未读起点）
  - 「未读消息」分隔线（未读区顶部）

- [ ] **Step 1: api 加 markRead**（`web/app/src/api/index.js`）

```js
  // 标记频道消息已读（≤maxID），仿桌面端「跳到未读末尾=已读」
  markRead: (chatId, maxId) => req('POST', `/api/chats/${chatId}/read?max_id=${maxId}`),
```

- [ ] **Step 2: 进入频道定位未读起点**

`Messages.vue` 的 `loadMessagesWithRetry`（首次加载，`attempt===0` 且无会话记忆时）：
- 当前逻辑：`api.listMessages(chatId, { limit: 50, ... })`（offsetId 默认 0 = 最新）
- 改为：**首次进入且 `current.value.readInboxMaxId > 0`** 时，`offsetId: current.value.readInboxMaxId + 1`（未读起点），否则 0（最新）

```js
// 首次加载定位：有未读时从第一条未读（readInboxMaxId+1）开始，仿桌面端
const firstUnreadOffset = current.value?.readInboxMaxId > 0 ? current.value.readInboxMaxId + 1 : 0
const r = await api.listMessages(chatId, { offsetId: firstUnreadOffset, limit: 50, mediaOnly: mediaOnly.value, refresh, signal })
```
（注意：只在**无会话记忆**的首次进入用此 offset；有 `chatState` 记忆时保持 `saved.scrollTop` 恢复逻辑不变——判断 `!saved` 分支）

- [ ] **Step 3: 未读分隔线**

在 `displayItems` 里，未读分界（`current.readInboxMaxId` 处）插入分隔线 item：
```js
// displayItems 聚合时：若 current.readInboxMaxId > 0，在对应位置插入 unread-divider item
```
具体：`displayItems` computed 里，遍历消息时遇到 `m.id === current.value.readInboxMaxId`（或第一条 `id <= readInboxMaxId` 的消息）时，在其前插入：
```js
items.push({ type: 'divider', key: chatKey + '-divider-' + m.id })
```
模板渲染（在 album/msg 的 v-if/v-else 链前加）：
```html
<div v-else-if="item.type === 'divider'" class="unread-divider">
  <span>未读消息</span>
</div>
```
CSS：
```css
.unread-divider {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 0; font-size: 11px; color: var(--accent);
}
.unread-divider::before, .unread-divider::after {
  content: ''; flex: 1; height: 1px; background: var(--accent);
}
```

- [ ] **Step 4: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 5: Commit**

```bash
cd "/d/Documents/workspace/telegram manager/tdl"
git add web/app/src/api/index.js web/app/src/views/Messages.vue
git commit -m "feat(web): 进入频道定位未读起点（readInboxMaxId+1）+ 未读消息分隔线"
```

---

### Task 4: 跳转按钮（方向随位置动态）+ 已读触发

**Files:**
- Modify: `web/app/src/views/Messages.vue`

**Interfaces:**
- Consumes: Task 3 的 `api.markRead`、`current.readInboxMaxId`、`displayItems` 分隔线
- Produces: 右下角跳转按钮（未读区↑跳末尾=已读、历史区↓回第一条）

- [ ] **Step 1: 状态与计算**

`Messages.vue` script 加：
```js
// 跳转按钮状态：在未读区（↑跳未读末尾=已读）vs 在已读历史（↓回第一条未读）
const unreadFirstId = computed(() => current.value?.readInboxMaxId > 0 ? current.value.readInboxMaxId + 1 : 0)
const unreadLastId = computed(() => {
  // 未读末尾 = 频道最新消息 ID（TopMessage）；v1 用已加载消息里的最大 id
  if (!messages.value.length) return 0
  return messages.value[0].id // 倒序：messages[0] 是最新
})
// 当前是否在未读区（滚动位置 < 未读起点消息的 index）
const inUnreadArea = ref(true) // 默认在未读区（进入定位到第一条未读）
```

- [ ] **Step 2: 跳转逻辑**

```js
// 右下角跳转按钮：方向随位置动态
// - 在未读区（默认）：↑ 跳到未读末尾（最后一条未读=最新）→ 触发已读
// - 在已读历史（往下滚过分隔线）：↓ 回第一条未读（不触发已读）
function jumpUnread() {
  if (inUnreadArea.value) {
    // ↑ 跳未读末尾 = 最新 = 全频道已读
    scrollToMsg(unreadLastId.value)
    markReadCurrent()
  } else {
    // ↓ 回第一条未读
    scrollToMsg(unreadFirstId.value)
  }
}
async function markReadCurrent() {
  if (!current.value || !unreadLastId.value) return
  if (!settingsStore.settings.autoRead) return // 手动模式：只跳转，不自动已读
  try {
    await api.markRead(String(current.value.id), unreadLastId.value)
    // 更新本地未读状态：ReadInboxMaxID 推进到最新 → 无未读
    current.value.readInboxMaxId = unreadLastId.value
    refreshChatsUnread() // 刷新会话列表未读徽标
  } catch { /* 静默 */ }
}
function refreshChatsUnread() {
  // 刷新会话列表未读徽标（api.listChats(true) 或本地更新）
  loadChats(true)
}
```

- [ ] **Step 3: 滚动监听更新 inUnreadArea**

`onMsgScroll` 里更新 `inUnreadArea`：滚动位置在未读区（分隔线之上）为 true，滚过分隔线（进入历史）为 false：
```js
// 简化判断：滚动位置 <= 分隔线位置 = 在未读区；否则在历史
// 用 displayItems 找分隔线 index，比较 scroller 当前 index
const dividerIdx = displayItems.value.findIndex(it => it.type === 'divider')
inUnreadArea.value = dividerIdx < 0 || scrollerRef.value?.getCurrentIndex?.() < dividerIdx
```

- [ ] **Step 4: 按钮模板 + 图标**

右下角按钮（现有 `back-top` 区域）：
```html
<button
  v-if="current && unreadFirstId"
  class="jump-btn"
  :class="{ 'up': inUnreadArea, 'down': !inUnreadArea }"
  @click="jumpUnread"
  :title="inUnreadArea ? '跳到未读末尾（全部已读）' : '回到第一条未读'"
>
  <ArrowUp v-if="inUnreadArea" :size="20" />
  <ArrowDown v-else :size="20" />
</button>
```
（import 加 `ArrowDown`；现有 `ArrowUp` 已有。`.jump-btn` 样式复用 `back-top` 定位，右下角）

- [ ] **Step 5: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 6: Commit**

```bash
cd "/d/Documents/workspace/telegram manager/tdl"
git add web/app/src/views/Messages.vue
git commit -m "feat(web): 跳转按钮方向随位置动态——未读区↑跳末尾=已读，历史区↓回第一条未读"
```

---

### Task 5: 设置开关（自动/手动已读）+ 手动模式工具栏按钮

**Files:**
- Modify: `web/app/src/views/Settings.vue`
- Modify: `web/app/src/views/Messages.vue`（手动模式工具栏「标记已读」按钮）

**Interfaces:**
- Consumes: Task 2 `settings.autoRead`、Task 4 `api.markRead`
- Produces: Settings 加「已读同步」开关；手动模式工具栏「标记已读」按钮

- [ ] **Step 1: Settings.vue 加开关**

「已读同步」分组（现有设置项附近）：
```html
<div class="setting-row">
  <div class="setting-label">
    <div>自动已读</div>
    <div class="muted">进入频道定位未读后，跳到未读末尾自动标记已读（仿桌面端）</div>
  </div>
  <el-switch v-model="settings.autoRead" @change="saveSettings" />
</div>
```
（`settings` 前端对象需含 `autoRead`——检查 Settings.vue 的 settings 加载/默认值，若缺补默认 `autoRead: true`）

- [ ] **Step 2: 手动模式工具栏「标记已读」按钮**

`Messages.vue` 频道头部工具栏（`只看媒体` 附近）加：
```html
<button
  v-if="current && current.readInboxMaxId > 0 && !settingsStore.settings.autoRead"
  class="chat-read-btn"
  @click="markReadCurrent(true)"
  title="标记全部已读"
>
  标记已读
</button>
```
`markReadCurrent(true)` 传 `force=true` 跳过 autoRead 检查（手动模式也调）：
```js
async function markReadCurrent(force = false) {
  if (!current.value || !unreadLastId.value) return
  if (!force && !settingsStore.settings.autoRead) return
  // ...（Task 4 同款实现）
}
```
（注意：手动模式「标记已读」用最新消息 maxID=unreadLastId 或 TopMessage）

- [ ] **Step 3: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 4: Commit**

```bash
cd "/d/Documents/workspace/telegram manager/tdl"
git add web/app/src/views/Settings.vue web/app/src/views/Messages.vue
git commit -m "feat(web): 设置加自动/手动已读开关 + 手动模式工具栏标记已读按钮"
```

---

### Task 6: 构建部署 + E2E 实测

**Files:** 无（验证）

**Interfaces:** Consumes Task 1-5 全部

- [ ] **Step 1: 后端全量测试**

Run: `cd "/d/Documents/workspace/telegram manager/tdl" && go build ./... && go vet ./app/chat ./server && go test ./app/chat ./server -count=1`
Expected: 全绿

- [ ] **Step 2: 部署**

Run: `cd "/d/Documents/workspace/telegram manager/tdl" && ./build-tmm.sh && ./build-tmm.sh web`
Expected: exe 重建 + 前端部署

- [ ] **Step 3: 启动 + E2E**

- 启动服务 → 找一个**有未读的频道**（会话列表有未读徽标）
- 进入频道 → 验证定位到第一条未读 + 「未读消息」分隔线
- 自动模式：跳到未读末尾（点↑）→ 已读 → 会话列表未读徽标消失
- 往上翻历史（滚过分隔线）→ 点↓ → 回第一条未读（未读徽标未变）
- 手动模式（设置关 autoRead）：进入未读不变，点工具栏「标记已读」→ 已读
- 设置开关切换生效
- **若无未读频道**：用 Node 调 `/api/chats` 找 `unreadCount>0` 的频道

- [ ] **Step 4: 风险记录**（若 `readInboxMaxId` 语义/已读触发有出入，记录现象，按 spec §7 预案调整）

---

## Self-Review（writing-plans 要求）

**Spec 覆盖核对：**
- §4.1 `DialogInfo.ReadInboxMaxID` → Task 1 ✓
- §4.2 `read` 接口 + `FetchMarkRead` → Task 2 ✓
- §4.3 settings `AutoRead` → Task 2 ✓
- §5.1 进入定位未读起点 + 分隔线 → Task 3 ✓
- §5.2 跳转按钮方向动态 + 已读触发 → Task 4 ✓
- §5.3 设置开关 + 手动按钮 → Task 5 ✓
- §6 E2E → Task 6 ✓
- §7 风险预案 → Task 6 Step 4 ✓

**占位符扫描：** 无 TBD/TODO；所有代码块完整（签名经源码验证：`MessagesReadHistoryRequest{Peer,MaxID}`、`GetInputPeer`、`updateSettingsRequest`、`defaultSettings`、`snapshot`、`selectChat`/`loadMessagesWithRetry`/`onMsgScroll` 接入点）。

**类型一致性：** `readInboxMaxId`（后端 JSON）↔ 前端 `current.readInboxMaxId` ↔ `offsetId: readInboxMaxId+1` 一致；`api.markRead(chatId, maxId)` ↔ 后端 `POST /api/chats/{id}/read?max_id` 一致；`settings.autoRead`（前后端）一致；`unreadFirstId`/`unreadLastId` 各 Task 引用一致。
