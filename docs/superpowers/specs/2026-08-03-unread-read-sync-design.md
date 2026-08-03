# 未读定位 + 已读同步 设计规格（Spec）

> 日期：2026-08-03 ｜ 仓库：`D:\Documents\workspace\telegram manager\tdl` ｜ 分支：master
> 状态：待用户审阅（通过后进入 writing-plans）

## 1. 概述

仿 Telegram 桌面端：**进入频道时从未读开始展示**（未读区顶部「未读消息」分隔线，可上翻已读历史），并**同步已读状态**——自动已读 / 手动已读由**设置开关**控制。

## 2. 目标 / 非目标

**目标：**
- 后端 `DialogInfo` 暴露 `ReadInboxMaxID`（未读分界点）
- 进入频道定位到 `ReadInboxMaxID+1` 展示，未读区顶部「未读消息」分隔线，往上可翻已读历史
- 已读同步：调 `messages.readHistory(peer, maxID)` 标记已读；**设置开关**选自动已读 / 手动已读
- 手动模式：频道内提供「标记已读」按钮
- 已读后会话列表未读徽标消失（刷新 `/api/chats`）

**非目标（v1 明确不做）：**
- ❌ 实时推送已读回执（不处理对端已读状态）
- ❌ 未读区与已读区之间滚动动画/平滑过渡
- ❌ 群组/私聊的特殊已读语义（与频道统一处理）
- ❌ 已读状态持久化到 tmm 侧（已读状态在 Telegram 服务端，tmm 只读/写）

## 3. 已核实的技术事实（探索+源码验证）

| 项 | 结论 |
|---|---|
| 未读分界 | gotd `Dialog.ReadInboxMaxID int`（已读到的最大消息 ID，未读从它之后开始）+ `TopMessage`（最新）——`DialogInfo` 已有 `UnreadCount`，需补 `ReadInboxMaxID` |
| 定位未读起点 | `messages.getHistory(offset_id=ReadInboxMaxID+1)`（排他游标，同现有 FetchMessages 的 OffsetID 语义） |
| 标记已读 | `Client.MessagesReadHistory(ctx, &MessagesReadHistoryRequest{Peer, MaxID})` → `*MessagesAffectedMessages`——对频道通用 |
| 会话刷新 | `handleListChats` 支持 `?refresh=1`；前端 `api.listChats(refresh)` 已有（Messages.vue:675）；`chatsCacheTTL` 缓存 |
| settings | `server/settings.go`：`settings` struct + `settingsJSON`（指针区分未设置）+ `defaultSettings`；前端 `Settings.vue` 用 `el-switch` + `saveSettings` 模式 |

## 4. 数据模型与接口（后端）

### 4.1 `DialogInfo` 补字段（`app/chat/fetch.go`）

```go
type DialogInfo struct {
    // ...现有字段
    ReadInboxMaxID int `json:"readInboxMaxId,omitempty"` // 已读到的最大消息 ID（未读起点 = +1）
}
```
`FetchDialogs` 里从 `dlg.ReadInboxMaxID` 填充。

### 4.2 settings 加 `autoRead`（`server/settings.go`）

```go
// settings 结构加：
AutoRead bool `json:"autoRead"` // 进入频道是否自动标记已读（false = 手动）
```
- `settingsJSON` 加 `AutoRead *bool`（指针区分未设置）
- `defaultSettings`：`AutoRead: true`（默认自动已读，仿桌面端）
- `save`/`snapshot` 跟随现有模式（`json.Marshal(settings)`）

### 4.3 新接口（`server/chats.go`）

```
GET /api/chats/{id}/read?max_id={n}
→ 200 {"ok": true}
```
- 调 `MessagesReadHistory(ctx, {Peer, MaxID})` 标记 ≤maxID 已读
- 成功后清空该频道的 `chats_` 缓存（`cache.delete` 或下次 `?refresh=1`），前端刷新会话列表未读徽标
- 错误 503「连接重连中」

## 5. 前端设计

### 5.1 进入频道定位未读（`Messages.vue`）

- 切频道后：`current` 有 `readInboxMaxId` 时，首次加载用 `offset_id = readInboxMaxId + 1`（未读起点）而不是 0（最新）
- 加载结果在未读分界处插入「未读消息」分隔线（仿桌面端分隔样式，`.unread-divider`）
- **往上翻历史**：`nextCursor` 逻辑不变，从分隔线往下继续加载已读历史（当前 `loadMore` 方向是「更早」= 已读方向，需确认展示顺序：未读区在上（新），已读历史在下（旧）——桌面端是未读在上、已读在下，翻历史向下）
- **无未读**（`readInboxMaxId` 为 0 或无未读）：正常从最新开始，无分隔线

### 5.2 已读同步（`Messages.vue` + `Settings.vue`）

- **自动模式**（`settings.autoRead = true`）：进入频道（定位到未读区后）自动调 `POST /api/chats/{id}/read?max_id={当前最新已显示的消息ID}`，成功后 `api.listChats(true)` 刷新会话列表未读徽标
- **手动模式**（`autoRead = false`）：频道顶部/操作区加「标记已读」按钮，点击调同一接口
- 触发时机：自动模式在消息加载完成后；避免每次切频道都重复调（已读的频道跳过）

### 5.3 设置开关（`Settings.vue`）

- 加「已读同步」分组：`el-switch` 绑定 `settings.autoRead`，on 文案「自动标记已读」（仿桌面端）/ off「手动标记已读」
- `@change="saveSettings"`（现有模式）

## 6. 测试策略

**单元测试：**
- `app/chat/fetch_test.go`：`FetchDialogs` 填充 `ReadInboxMaxID`（mock dialog）
- `server/settings_test.go`：`AutoRead` 默认 true + JSON 序列化（指针区分）

**端到端实测（真会话）：**
1. 打开有未读的频道 → 定位到未读分界 + 「未读消息」分隔线
2. 自动模式：进入后会话列表未读徽标消失（服务端已读）
3. 手动模式：进入未读不消失，点「标记已读」后消失
4. 往上翻历史正常
5. 设置开关切换生效

## 7. 风险与预案

| 风险 | 预案 |
|---|---|
| `ReadInboxMaxID` 为 0（从未读过） | 未读起点 = 0+1 = 最新区，无分隔线（等价当前行为） |
| 频道与讨论组已读语义差异 | 频道用 `readHistory`（已验证通用）；若实测讨论组需 `readDiscussion`，接口加参数 |
| 自动已读误伤浏览 | 默认自动（仿桌面端），设置可关；手动模式完全控制 |
| 已读后 `chats_` 缓存 | `?refresh=1` 强制刷新（前端已支持），无需清缓存 |
