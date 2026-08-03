# 评论区功能 设计规格（Spec）

> 日期：2026-08-03 ｜ 仓库：`D:\Documents\workspace\telegram manager\tdl` ｜ 分支：master
> 状态：待用户审阅（用户审阅通过后进入 writing-plans）

## 1. 概述

Telegram 频道消息卡片显示 **emoji 反应 + 评论数**，有评论的消息可点击打开**只读评论区弹层**（拉取讨论组回复、分页浏览、显示评论作者/文本/时间/媒体）。基于 gotd v0.140.0 原生封装，不引新依赖。

## 2. 目标 / 非目标

**目标：**
- 消息列表单条消息显示反应数（🔥 5）与评论数（💬 12），评论数可点击
- 评论区弹层：作者名（名字首字徽章）+ @用户名 + 文本 + 时间 + 该评论自己的反应 + 该评论媒体缩略图
- 滚动续拉分页，新→旧

**非目标（v1 明确不做）：**
- ❌ 作者头像（真实头像需 photos.getUserPhotos 额外 RPC + 头像专用下载管线，复杂度高一个量级；StrippedThumb 模糊图也暂不做，徽章足够）
- ❌ 发评论（只读，无 flood 风险）
- ❌ 相册组（grouped）消息的反应/评论数（组内多条各自有，聚合复杂）
- ❌ 自定义 emoji 反应（`ReactionCustomEmoji`，premium 功能，跳过不显示）
- ❌ `MessageInfo` 加作者字段（频道广播作者无信息量，且会牵动 2h 缓存 schema）

## 3. 已核实的技术事实（探索+源码验证）

| 项 | 结论 |
|---|---|
| `MessageReplies` | `tg/tl_message_replies_gen.go`：`Replies int`、`Comments bool`、`ChannelID int64`（讨论组 ID），随消息自带，零 RPC |
| `MessageReactions` | `tg/tl_message_reactions_gen.go`：`Results []ReactionCount{Reaction ReactionClass, Count int}`，随消息自带 |
| `ReactionClass` 实现 | `ReactionEmoji{Emoticon string}`（显示）、`ReactionCustomEmoji{DocumentID int64}`（跳过） |
| `MessagesGetReplies` | 请求 `{Peer, MsgID, OffsetID, OffsetDate, AddOffset, Limit, MaxID, MinID}` → 返回 `MessagesMessagesClass`（断言 `*MessagesChannelMessages`，带 `Users []UserClass`/`Chats []ChatClass` 实体，作者解析零额外 RPC） |
| 评论媒体 thumb | `server/media.go` `resolveThumb` 是纯函数（只读消息 media）；`handleMediaThumb` 走 `resolveMedia` 每次**重新 resolve peer**（media.go:62-63），**不依赖** `mediaTargets` 缓存 → 前端用讨论组 ID 调 `/media/{讨论组ID}/{评论msgID}/thumb` 可行 |
| 拉评论同构模板 | `app/chat/search.go:162` `FetchMessageAround`：手写 `MessagesGetHistory` + 断言 + `messageInfoFrom` 转换 |
| peer 先例 | `app/chat/export.go:105`：`GetReplies(peer.InputPeer()).MsgID(thread)` 传原频道 + 帖子 msgID，服务器自动映射讨论组 |
| 取消息封装 | `tutil.GetSingleMessage`（`core/util/tutil/tutil.go:177`） |
| 作者名拼接 | `app/chat/ls.go:350` `visibleName(first, last)`，同包复用 |
| 缓存 schema | `msgsCacheTTL=2h`（server/cache.go:27），旧缓存 payload 无新字段 → 需缓存 key 加 `_v2` |

## 4. 数据模型（后端）

### 4.1 `app/chat/messages.go` — MessageInfo 扩展

```go
type MessageRepliesInfo struct {
    Count       int   `json:"count"`                    // 评论总数
    ChannelID   int64 `json:"channelId,omitempty"`      // 讨论组 ID（评论区弹层用）
    HasComments bool  `json:"hasComments,omitempty"`    // 是否讨论组评论区
}

type MessageReaction struct {
    Emoji string `json:"emoji"`
    Count int    `json:"count"`
}

// MessageInfo 新增字段：
Replies   *MessageRepliesInfo `json:"replies,omitempty"`
Reactions []MessageReaction   `json:"reactions,omitempty"`
```

`messageInfoFrom`（L150）提取逻辑：
- `m.GetReplies()`（注意：gotd 需 `SetReplies` 设 flag，单测里直接赋字段拿不到）→ 若 `Comments==true` 或 `Replies>0` → `MessageRepliesInfo`
- `m.GetReactions()` → `Results` 遍历：`*ReactionEmoji` → 取 `Emoticon`；`*ReactionCustomEmoji` → 跳过

### 4.2 `app/chat/replies.go`（新文件）

```go
// CommentInfo 一条评论区回复（作者信息跟随 getReplies 响应自带）
type CommentInfo struct {
    ID        int              `json:"id"`
    Date      int              `json:"date"`
    Text      string           `json:"text"`
    AuthorID  int64            `json:"authorId"`
    Name      string           `json:"name"`      // visibleName(first, last) 或频道/群 title
    Username  string           `json:"username,omitempty"`
    HasMedia  bool             `json:"hasMedia"`
    Media     *MessageMedia    `json:"media,omitempty"` // 复用，含 StrippedThumb
    Reactions []MessageReaction `json:"reactions,omitempty"`
}

// FetchReplies 拉取帖子的评论区（新→旧）。offsetID=0 取最新页；
// 续拉传上一页最旧评论的 ID（排他游标，add_offset=0）。
func FetchReplies(ctx context.Context, c *telegram.Client, kvd storage.Storage,
    chatID string, msgID, offsetID, limit int) ([]*CommentInfo, int, error)
```

实现要点：
- `GetInputPeer(chatID)` 传**原频道** peer + 帖子 `msgID`（对齐 export.go:105 先例）
- `MessagesGetReplies{Peer, MsgID, OffsetID, OffsetDate:0, AddOffset:0, Limit}` → 断言 `*tg.MessagesChannelMessages`
- 作者解析：响应 `Users` → `map[int64]*tg.User`；每条评论 `m.FromID` → `tutil.GetPeerID` → 查 map → `visibleName`；`FromID` 为 `PeerChannel/PeerChat` 时查响应 `Chats` 用 title
- 评论媒体：内部辅助函数 `commentInfoFrom(m *tg.Message, users map[int64]*tg.User, chats map[int64]*tg.Chat) *CommentInfo`，复用 `messageInfoFrom` 的媒体提取（`extractThumb`/`mediaDimensions`/`mediaMime` 是纯函数）
- 分页返回 `nextCursor`（0 = 到底）

### 4.3 服务端

`server/chats.go` 新增 `handleChatReplies`（仿 `handleChatAround` chats.go:354：无缓存无单飞）：
- 路由参数复用 `parseMediaVars`（media.go:758）
- `limit` 校验 `<=0 || >100 → 50`
- 错误返回 503「连接重连中」

`server/server.go` 注册路由：
```go
api.HandleFunc("/chats/{id}/messages/{msg}/replies", s.handleChatReplies).Methods(http.MethodGet)
```

**缓存 schema 版本**：`handleListMessages` 的 `cacheKey`（chats.go:162）加 `_v2` 后缀——旧 2h 缓存 payload 不含新字段，加版本立即失效重拉，避免升级后 2 小时内列表不显示反应/评论数。

## 5. API 设计

```
GET /api/chats/{chatId}/messages/{msgId}/replies?offset_id=0&limit=50
→ {
    "comments": [CommentInfo, ...],   // 新→旧
    "nextCursor": 0                    // 0 = 到底
}
```

## 6. 前端设计

### 6.1 `web/app/src/api/index.js`
```js
listReplies: (chatId, msgId, { offsetId = 0, limit = 50 } = {}) =>
  req('GET', `/api/chats/${chatId}/messages/${msgId}/replies?offset_id=${offsetId}&limit=${limit}`),
```

### 6.2 `web/app/src/components/CommentsPanel.vue`（新）
- 仿 `TelegraphReader.vue` 弹层骨架（teleport + overlay + card + 关闭按钮 + 加载/错误三态）；**Esc 关闭**抄 `MediaViewer.vue`（overlay `tabindex="-1"` + focus + `@keydown`）
- props：`chatId`（String，**讨论组 ID**）、`msgId`（Number，帖子 ID）
- 评论项：名字首字圆形徽章（`--accent-soft` 底 + `--accent` 字）+ 名字 + `@用户名` + 时间 + 文本 + 该评论反应 + 媒体 `LazyThumb`（`api.mediaThumbUrl(chatId, msg.id)`，placeholder 用 StrippedThumb）
- 滚动续拉：`.cm-body` 绑 `@scroll`，复刻 `onSearchScroll` 判断（Messages.vue:449，距底 150px 触发），底部 `el-button` 兜底
- 样式全用 token：`--bg-panel`/`--border`/`--radius-lg`/`--accent`/`--text-3`，禁硬编码色值

### 6.3 `web/app/src/views/Messages.vue`
- 单条消息 meta 行（L205-209）：views 后加反应（`v-for` `item.msg.reactions`）+ 评论数（`v-if="item.msg.replies?.count > 0 && item.msg.replies.channelId"`，`MessageCircle` 图标，`@click.stop="openComments(item.msg)"`）；相册组 meta（L145）v1 不动
- 新增 `commentsPanel` ref + `openComments(m)`（`chatId = String(m.replies.channelId)`）+ 模板 `v-if` 渲染 + `@close` 置 false（照抄 telegraphReader 模式）

## 7. 测试策略

**单元测试：**
- `app/chat/replies_test.go`：`commentInfoFrom` 转换（构造带 Users/Chats 的响应验证作者名解析）、反应提取（ReactionEmoji 正常 / ReactionCustomEmoji 跳过）
- `app/chat/messages_test.go` 增补：`messageInfoFrom` 的 Replies/Reactions 提取（用 `SetReplies/SetReactions` 设 flag）

**端到端实测（真会话）：**
1. 启动服务，找有评论区的频道（滚动找 💬 消息）→ 打开弹层验证评论渲染
2. 滚动续拉分页、评论媒体缩略图（讨论组 ID thumb 管线）
3. Esc/遮罩关闭
4. 反应/评论数显示

## 8. 风险与预案

| 风险 | 预案 |
|---|---|
| getReplies peer 语义：传频道 vs 传讨论组 | 先例是传频道 + 帖子 ID（export.go:105）；若实测失败，接口入参改「讨论组 ID + 帖子 ID」（前端从 `replies.channelId` 拿），改动集中在 `FetchReplies` 一处 |
| 讨论组 ID 空间（裸正数 vs -100 前缀） | `tutil.GetInputPeer` 的 `ResolveChannelID` 应能解析；若需归一化加一行 |
| 无评论区的频道验证数据 | 需实测找有评论区的频道（Exhentai 无评论区，已确认） |
