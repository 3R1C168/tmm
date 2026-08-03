# 群组分组/归档（只读显示）设计规格（Spec）

> 日期：2026-08-03 ｜ 仓库：`D:\Documents\workspace\telegram manager\tdl` ｜ 分支：master
> 状态：待用户审阅（通过后进入 writing-plans）

## 1. 概述

仿 Telegram 桌面端，**只读显示**群组分组/归档：会话列表按文件夹分组展示 + 归档会话折叠区。数据存在 Telegram 服务端（账号级、跨设备同步），tmm 只读不写——分组/归档在手机/桌面端管理，tmm 展示。

## 2. 目标 / 非目标

**目标：**
- 后端 `DialogInfo` 暴露 `FolderID`（归档标记，免费随对话附带）
- 后端拉文件夹定义（`messages.getDialogFilters`）→ 按文件夹分组会话
- 前端会话列表：按文件夹分组展示（标题 + 图标 + 色标），归档会话进「已归档」折叠区
- 无文件夹的会话留在「全部」默认区

**非目标（v1 明确不做）：**
- ❌ 在 tmm 里建/改/删分组、归档/取消归档（写 API，手机/桌面端管理）
- ❌ 文件夹的复杂规则匹配（`Contacts/Groups/Bots` 等类型规则字段 v1 不精确匹配，只按 `IncludePeers` 枚举的会话分组；规则型文件夹回退到「全部」区）
- ❌ 分组拖拽/重排/多选管理 UI
- ❌ 未读徽标与分组的联动统计

## 3. 已核实的技术事实（探索+源码验证）

| 项 | 结论 |
|---|---|
| 归档标记 | gotd `Dialog.FolderID int`（`GetFolderID() (int, bool)`），**随对话列表免费附带**，零额外 RPC；归档 = FolderID 特殊值 |
| 文件夹定义 | `Client.MessagesGetDialogFilters(ctx)` → `*MessagesDialogFilters`，含 `Filters []DialogFilterClass`（一次 RPC） |
| `DialogFilter` 结构 | `ID int`、`Title TextWithEntities`（标题）、`Emoticon`（图标）、`Color`（色标）+ 类型规则字段（`Contacts/Groups/Broadcasts/Bots/ExcludeMuted` 等）+ `IncludePeers []InputPeerClass`/`ExcludePeers` |
| 会话匹配 | `IncludePeers` 元素是 `InputPeerClass`（`InputPeerChannel/User/Chat`），用 `GetChannelID()/GetUserID()/GetChatID()` 转成裸 ID 与 `DialogInfo.ID` 匹配 |
| `DialogInfo` 现状 | 有 `ID/Type/VisibleName/Username/UnreadCount/Pinned`，**无 `FolderID`** |
| 会话列表缓存 | `handleListChats` 用 `chatsCacheTTL` 缓存；`?refresh=1` 强制刷新 |

## 4. 数据模型与接口（后端）

### 4.1 `DialogInfo` 补 `FolderID`（`app/chat/fetch.go`）

```go
type DialogInfo struct {
    // ...现有字段
    FolderID int `json:"folderId,omitempty"` // 所属文件夹 ID；归档/无文件夹时语义见下
}
```
`FetchDialogs` 里从 `dlg.GetFolderID()` 填充。

**归档语义确认**：Telegram 官方约定 `FolderID=1` 是「归档」。但为稳妥，前端判定归档用「`FolderID` 存在且为 1」；其他 FolderID 是用户自定义文件夹。**v1 简化**：`FolderID != 0` 即视为「已归档/已分组」，前端统一折叠处理（不区分归档 vs 自定义文件夹，避免 FolderID 特殊值歧义）——若实测发现自定义文件夹 ID 需精确区分，再加逻辑。

### 4.2 文件夹定义接口（`app/chat/fetch.go` + `server/chats.go`）

**新函数**（`app/chat`）：
```go
// FolderInfo 一个文件夹定义（只读展示用）
type FolderInfo struct {
    ID    int    `json:"id"`
    Title string `json:"title"`
    Emoticon string `json:"emoticon,omitempty"`
    Color int    `json:"color,omitempty"`
    PeerIDs []int64 `json:"peerIds"` // IncludePeers 转成的裸 ID（channel/user/chat）
}

// FetchFolders 拉取账号的文件夹定义（messages.getDialogFilters，一次 RPC）
func FetchFolders(ctx context.Context, c *telegram.Client, kvd storage.Storage) ([]FolderInfo, error)
```
- 遍历 `MessagesDialogFilters.Filters`，断言 `*tg.DialogFilter`（跳过默认/空）
- `Title` 是 `TextWithEntities`，取 `.Text`
- `IncludePeers` 遍历：`InputPeerChannel → GetChannelID()`、`InputPeerUser → GetUserID()`、`InputPeerChat → GetChatID()`
- 返回 `[]FolderInfo`

**新接口**（`server/chats.go`）：
```
GET /api/folders
→ {"folders": [FolderInfo, ...]}
```
- 无缓存无单飞（文件夹变化不频繁，但简单起见每次拉；或加短 TTL——v1 无缓存，前端切页时拉）
- 错误 503「连接重连中」

### 4.3 前端数据流

- `api.listFolders()` 拉文件夹定义
- 会话列表加载后（`/api/chats`），前端按 `folderId` 分组：
  - 匹配 `folders[].peerIds` 的会话 → 对应文件夹
  - `folderId != 0` 但不在任何文件夹定义（或归档 FolderID=1）→ 「已归档」折叠区
  - 其余 → 「全部」区

## 5. 前端设计（`Messages.vue` 左侧会话列表）

### 5.1 分组展示

- 会话列表顶部加**分组切换/滚动导航**：文件夹标题（`Emoticon` 图标 + Title + 色标 `Color`）
- 每个文件夹下是它的会话；「全部」区 = 无分组会话；「已归档」折叠区（默认收起，点开展开）
- 用现有 `chat-item` 结构，文件夹用分组头分隔（类似现有 pinned 分组）

### 5.2 归档折叠

- `FolderID=1`（归档）或不在文件夹定义的会话 → 「已归档」区
- 默认**折叠**（显示「已归档 (N)」，点击展开），避免 481 频道里归档的占满列表

### 5.3 交互

- 文件夹标题可点击：定位到该分组（滚动或过滤显示）
- 点击会话进入消息浏览（现有逻辑不变）
- 未读徽标在分组内照常显示（现有 `unreadCount`）

## 6. 测试策略

**单元测试：**
- `app/chat/fetch_test.go`：`FetchFolders` 解析（mock `MessagesDialogFilters`：IncludePeers 各 peer 类型 → 裸 ID）
- `app/chat/fetch_test.go`：`FetchDialogs` 填充 `FolderID`

**端到端实测（真会话）：**
1. 拉 `/api/folders` 返回账号的真实文件夹
2. 会话列表按文件夹分组显示（标题/图标/色标正确）
3. 归档会话进「已归档」折叠区，展开可见
4. 无分组会话在「全部」区
5. 未读徽标在分组内正常

## 7. 风险与预案

| 风险 | 预案 |
|---|---|
| 归档 FolderID 特殊值歧义（1 vs 自定义） | v1 简化 `FolderID!=0` 视为归档/已分组统一折叠；实测发现需区分再加逻辑 |
| DialogFilter 类型规则字段（Groups/Bots 等） | v1 不精确匹配规则型文件夹（回退「全部」区），只按 IncludePeers 枚举分组 |
| 文件夹很多/空文件夹 | 空文件夹隐藏；文件夹多时滚动导航 |
| `getDialogFilters` 一次性 RPC 慢 | 前端切页时拉 + 可加短 TTL（v1 无缓存，性能可接受则保持） |
