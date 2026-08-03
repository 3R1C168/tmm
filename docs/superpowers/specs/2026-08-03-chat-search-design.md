# 频道内全文搜索设计文档

> 日期：2026-08-03
> 状态：已批准
> 关联：用户需求「搜索频道内带 #标签 的消息，找出对应标签的所有消息」

---

## 1. 背景与目标

用户需要搜索单个频道内的消息文本（主要是 #标签，如 `#分享`），找出所有匹配的消息并跳转查看。核心洞察来自实测：

- **`messages.search` API（Telegram Desktop 的做法）完全可用**——服务器端维护全量消息索引，频道多大都是亚秒级返回。实测频道 2070986972 搜 `#分享` 返回 count=9，一次 RPC 出结果。
- **不需要本地建索引 / 不需要「加载所有消息」开关**——服务器索引已解决，拉全量 2.1 万条要 262 秒（实测），但搜索秒回。
- **`messages.search` 的多标签组合是文本匹配不是标签 AND**——实测 `#分享 #娇妻` 返回 count=0（找「同时含两串的文本」），`#分享` 9 条。**单标签/词搜索够用**，多标签不做。

## 2. 需求（已与用户确认）

1. **范围**：频道内消息搜索（单频道），**架构预留全局搜索**（搜索服务抽象，本次只实现频道内模式）。
2. **交互**：频道内搜索框（消息区顶部）→ 输入关键词防抖搜索 → 下拉结果面板（缩略图+摘要+日期+总匹配数）→ 点击结果**任意位置跳转**（含未加载区域）。
3. **搜索语义**：单标签/词，服务器 `messages.search` 全文匹配（# 是普通字符）。
4. **结果分页**：下拉滚动续拉（nextOffset）。
5. **跳转**：点击结果 → 后端取目标消息上下文（around）→ 加载进列表 → 滚动定位。

## 3. 设计

### 3.0 搜索语义：`#` 只是普通字符（无差异处理）

实测确认（频道 2070986972）：`#分享` 返回 9 条、`分享`（不带 #）也匹配。`messages.search` 的 `q` 是**全文文本匹配**，`#` 是普通字符，**不是特殊语法**。

**实现上零差异**：`SearchMessages` 的 `q` 参数原样透传给 `messages.search`，前端搜索框自由输入（`#标签` 或 `普通词` 都行），**无需转义、无需前缀处理、无单独分支**。用户搜 `#分享` 得到「文本含 #分享 的消息」，搜 `分享` 得到「文本含 分享 的消息」——两者是不同字符串匹配，服务器天然支持。

**空查询校验（关键，实测）**：`q="#"` / `q="##"` / 纯空白返回 `SEARCH_QUERY_EMPTY`（400）——服务器把纯 `#` 当空查询。因此：

- **有效查询判定**：去除首尾空白后，**去掉所有 `#` 字符，剩余仍有至少 1 个非空白字符**才算有效。
  - 无效（不触发搜索）：`#`、`##`、`# `、`# #`、``、`   `（全空白）
  - 有效（触发搜索）：`#分`、`#分享`、`# 分`、`#💩`、`分享`、`分`
- **前端**：搜索框输入防抖后先做此校验，无效则**不发起搜索请求**（静默等待，或显示占位提示「输入标签或关键词」），不调 API。
- **后端兜底**：`handleChatSearch` 同样校验，`q` 无效返回 400「搜索关键词为空」（防御前端绕过）。
- **奇怪字符**：除空查询外不做白名单过滤——中英文、数字、符号、emoji 都放行给服务器搜（服务器语义正确，且用户可能真想搜 emoji）。

### 3.1 架构：搜索服务抽象（预留全局）

`app/chat` 新增搜索能力，设计为可扩展模式——本次只实现「频道内搜索」，但函数签名/结构为「全局搜索」预留（全局搜索是同一模式，无 chatID 参数 + 返回带频道归属，后续加时增量小）。

### 3.2 后端：`app/chat/search.go`

**`SearchMessages`**（频道内搜索）：

```go
// SearchMessages 在单个频道内搜索消息文本（含 #标签）。
// 服务器端索引（messages.search），频道多大都是亚秒级。
// 返回匹配消息（含 media 信息，供前端渲染缩略图）+ 下一个分页 offset。
func SearchMessages(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID, q string, offsetID, limit int) ([]*MessageInfo, int, error)
```

- 内部：peers manager 解析 → `client.API().MessagesSearch`（`MessagesSearchRequest{Peer, Q, OffsetID, Limit, Filter: InputMessagesFilterEmpty}`）→ 解包 `MessagesChannelMessages` → 复用 `FetchMessages` 的 MessageInfo 转换（含 media/thumb/grouped 提取）+ peers 预热。
- 返回：匹配的 `[]*MessageInfo` + `NextOffset`（0 = 无更多）。

**`FetchMessageAround`**（跳转定位）：

```go
// FetchMessageAround 按 msgid 取该消息及其往前 limit 条历史，
// 用于「跳转到搜索结果」时加载目标附近上下文（搜索目标可能未在已加载列表里）。
func FetchMessageAround(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID string, msgID, limit int) ([]*MessageInfo, error)
```

- 内部：`messages.getHistory` offset=msgID 往回取（含 msgID 本身）→ 复用 MessageInfo 转换。
- 注意：getHistory 以 offset_id 游标，offset=msgID 返回从 msgID 往前的消息，**含 msgID 本身**。

### 3.3 server API

```
GET /api/chats/{id}/search?q=分享&offset=0&limit=50
  → 200 { results: [MessageInfo...], nextOffset: int, count: int }
  → 400 q 为空 / 401 未登录 / 503 RPC 失败（连接重连中，与消息加载一致）

GET /api/chats/{id}/around?msg=3210&limit=50
  → 200 { messages: [MessageInfo...] }
  → 400 msg 无效 / 401 / 503
```

- **单飞去重**：复用 `fetchMessagesFlight` 模式（`msgsInflight` map），同 `(chatID, q, offset)` 并发请求共享结果；around 同理。
- **缓存**：
  - search：`cache/meta/search_<ns>_<chat>_<q>_<offset>` 短 TTL（5 分钟）——搜索秒回但避免高频重复 RPC。
  - around：不缓存（一次性定位，无重复价值）。
- **handler**：`server/chats.go` 加 `handleChatSearch` + `handleChatAround`，路由注册 `server/server.go`。
- `count`：`MessagesChannelMessages.Count` 直接返回（总匹配数，前端显示）。

### 3.4 前端（web/app/src/views/Messages.vue + api/index.js）

**api.js**：

```js
searchMessages: (chatId, { q, offset = 0, limit = 50 } = {}) =>
  req('GET', `/api/chats/${chatId}/search?q=${encodeURIComponent(q)}&offset=${offset}&limit=${limit}`),
chatAround: (chatId, msgId, limit = 50) =>
  req('GET', `/api/chats/${chatId}/around?msg=${msgId}&limit=${limit}`),
```

**Messages.vue**（搜索状态独立于现有 messages/session，不污染分页/筛选）：

- 状态：`searchOpen`（bool）、`searchQuery`（string）、`searchResults`（数组）、`searchOffset`、`searchCount`、`searchLoading`、`searchHasMore`。
- 搜索框：消息区顶部右侧（Desktop 样式），与现有「搜频道列表」的搜索框区分。
- 输入防抖 300ms → `searchMessages` → 填充 `searchResults`（每次新词清空重查）。
- 下拉结果面板：覆盖消息区上方；每条 = 缩略图（复用 LazyThumb 组件）+ 首行文本摘要 + 日期 + 来源（全局预留显示频道名）；头部显示「共 N 条」；滚动底部续拉（nextOffset）。
- 点击结果：
  1. `chatAround(chatId, msgId)` 取上下文；
  2. 把返回消息替换当前 `messages`（+ `nextCursor` 对齐），置 `searchOpen=false` 关闭下拉；
  3. `nextTick` 后 `scrollToMsg(msgId)`（复用现有 `scrollToItem` 定位，Messages.vue:695）。
- **跳转后的视图语义**：点击跳转 = 用户想看目标消息，替换后的列表（目标上下文）成为**当前视图**。搜索下拉关闭后，列表停留在跳转位置（不自动跳回原位置）；用户可自行滚动或重新打开搜索。跳转前的列表不保留（与「切频道」行为一致——进入新上下文）。
- 关闭：Esc / 点击面板外 → 仅关闭下拉面板，**不改动当前 messages 列表**（未跳转时列表保持原样；已跳转时保持跳转后的视图）。
- 空结果：显示「无匹配消息」。
- 错误：503 → 提示「连接重连中，请稍候」（与消息加载一致）。

### 3.5 错误处理

- q 无效（去 # 后无非空白字符，如 `#`/`##`/空白）→ 前端不发起请求；后端防御返回 400「搜索关键词为空」。
- msg 非数字 / 0 → 400「消息ID无效」。
- 搜索 RPC 失败 → 单飞 + 重试（复用 fetchMessagesFlight 的 15s×3 模式）→ 仍失败 503。
- around 目标消息已删除 → 返回空或 404，前端提示「消息不存在或已删除」。

## 4. 测试

### 4.1 后端单测
| 层 | 用例 |
|---|---|
| app/chat | `SearchMessages` 解析 `MessagesChannelMessages`（含 count/nextOffset）、空结果、媒体消息含 media 字段 |
| server | search 端点：q 无效（`#`/`##`/空白）400、正常返回 results/nextOffset/count、缓存命中；around 端点：msg 定位返回含目标、无效 msgID 400 |

（搜索是真实 Telegram RPC，单测用 mock 或依赖已有测试模式——参照 `media_stream_test.go` / `task_batch_test.go` 的做法，若不可 mock 则标注手动验证。）

### 4.2 手动浏览器验证清单
1. 频道内搜 `#分享` → 下拉显示结果（缩略图+摘要+日期）+ 总匹配数
2. 点击结果 → 跳转到该消息（任意位置，含未加载区域），列表滚动定位
3. 滚动下拉续拉更多结果
4. 关闭搜索（Esc/外部点击）→ 恢复原列表
5. 搜不存在词 → 空结果提示
6. 断网/连接重连 → 503 提示，恢复后重试

## 5. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 跳转目标极老，around limit=50 不够定位 | limit 可调；目标消息在返回列表内即定位成功 |
| 搜索 RPC 抖动 | 复用 fetchMessagesFlight 单飞 + 15s×3 重试 + 503 |
| 搜索结果缓存膨胀 | 5 分钟 TTL + 按 q 哈希，容量有限，随 TTL 过期清理 |
| 搜索状态与现有视图冲突 | 独立状态 + 关闭恢复，不污染现有 messages/session |
| 中文分词 | messages.search 是服务器全文匹配，实测中文标签可用（`#分享` 命中），无需本地分词 |

## 6. 非目标（明确不做）

- 多标签组合筛选（实测服务器不支持 AND，用户确认单标签够用）。
- 全局搜索（`messages.searchGlobal`）——架构预留（搜索服务抽象），本次不实现。
- 本地索引 / 「加载所有消息」开关——服务器索引已够快。
- 搜索结果的批量下载——先跳转定位，后续可加。
- 搜索历史 / 热门标签。

## 7. 架构预留（全局搜索，后续增量小）

后续加全局搜索时：
- `app/chat` 加 `SearchMessagesGlobal(ctx, c, kvd, q, offset) ([]*GlobalSearchResult, error)`——`messages.searchGlobal`，返回带 `chatID`/`chatName` 归属。
- server 加 `GET /api/search/global?q=...`。
- 前端结果条目显示来源频道名（本设计下拉条目已预留频道名位）。
- around 端点复用（按 (channelID, msgID) 取上下文跳转）。
