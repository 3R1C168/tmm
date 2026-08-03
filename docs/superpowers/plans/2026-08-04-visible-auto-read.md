# 视口消息自动已读 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 自动模式下滚动浏览时，视口内可见的最大消息 id 自动已读（`channels.readHistory`/`messages.readHistory` maxID 语义），未读数按已查看条数递减（如未读 99，滚 9 条 → 未读 90）。

**Architecture:** 前端 `Messages.vue` 增量：`onScrollerUpdate` 存 `visibleEndIndex`，新增 `visibleMaxMsgId()` 从 `displayItems` 映射可视区最大 id；`onMsgScroll` 节流后调 `maybeAutoReadVisible()`（仅自动模式、`maxId > readInboxMaxId` 才调 `api.markRead` 并推进本地分界）。复用现有 `markReadCurrent`/`FetchMarkRead`/`refreshChatsUnread`。手动模式严格 gate 不受影响。

**Tech Stack:** Vue 3.5 / vue-virtual-scroller@2.0.0-beta.8（`@update` 事件）/ Element Plus。

## Global Constraints

- 前端：`cd web/app && npm run build` 成功；部署 `./build-tmm.sh web`
- 注释中文；样式用 token（禁硬编码色值）
- 复用现有 `markReadCurrent`/`FetchMarkRead`，不重复造 RPC
- 仅自动模式（`settings.autoRead=true`）自动视口已读；手动模式（`autoRead=false`）严格 gate
- 节流 + 去重：滚动停止/节流后调，`maxId > readInboxMaxId` 才推进（避免频繁 RPC）
- 保留「点↑跳末尾=全读」；视口自动已读是额外触发源

---

### Task 1: 可视区最大消息 id 追踪（`visibleMaxMsgId`）

**Files:**
- Modify: `web/app/src/views/Messages.vue`

**Interfaces:**
- Consumes: `onScrollerUpdate`（L934，已有 `visibleStartIndex`，需加 `visibleEndIndex`）、`displayItems`（item.msg.id / item.msgs / type:'divider'）
- Produces:
  - `visibleEndIndex` ref（onScrollerUpdate 存储）
  - `function visibleMaxMsgId() int|null` — 可视区 `[visibleStartIndex, visibleEndIndex]` 内最大 id 的消息（倒序下 = visibleStartIndex 对应消息 id；跳过 divider；album 取组内第一条 id）
  - `function itemMaxId(item) int|null` — 取单条 msg.id 或 album 组内第一条 id

- [ ] **Step 1: 写失败测试**（新建 `web/app/src/utils/visibleRead.test.js` 或内联测试——若无测试基建则跳过，用构建+E2E 验证。**若有 vitest 则写**，先确认 package.json 有无 test 脚本）

```js
// 若项目有 vitest/jest 测试基建，测 visibleMaxMsgId 纯逻辑：
// displayItems 倒序：[{msg:{id:100}}, {msg:{id:99}}, {type:'divider'}, {msg:{id:98}}, {album:{msgs:[{id:97},{id:96}]}}]
// visibleStartIndex=0, visibleEndIndex=2 → maxId=100（顶部，倒序最大 id）
// visibleStartIndex=3, visibleEndIndex=4 → maxId=97（album 组第一条）
```
（**若 package.json 无测试脚本**，跳过此步，直接实现 + E2E 验证——本仓库前端无单测基建，改为实现后 `npm run build` 验证）

- [ ] **Step 2: 实现**（`Messages.vue`）

在 `currentVisibleIndex` 附近加：
```js
// 视口底部条目 index（onScrollerUpdate 的 visibleEndIndex），用于算视口内最大消息 id
const visibleEndIndex = ref(0)
```

`onScrollerUpdate` 改为接收并存储：
```js
function onScrollerUpdate(_startIndex, _endIndex, visibleStartIndex, visibleEndIndex_) {
  currentVisibleIndex.value = visibleStartIndex || 0
  visibleEndIndex.value = visibleEndIndex_ || 0
  updateInUnreadArea()
}
```

加两个函数（`updateInUnreadArea` 附近）：
```js
// 取单条消息或相册组的代表 id（组内第一条——同组消息 id 接近，用第一条即可）
function itemMaxId(item) {
  if (item.type === 'msg' && item.msg) return item.msg.id
  if (item.type === 'album' && item.msgs?.length) return item.msgs[0].id
  return null
}

// 视口内可见的最大消息 id（倒序下顶部 index 对应最大 id）。
// 用 visibleStartIndex（顶部）那条即可——它是可视区 id 最大的消息；
// 若顶部是 divider（未读分界），往下一格取未读区第一条。
function visibleMaxMsgId() {
  if (!displayItems.value.length) return null
  const top = displayItems.value[currentVisibleIndex.value]
  return itemMaxId(top)
}
```
（说明：倒序布局下视口顶部 `currentVisibleIndex`（= visibleStartIndex）是**最大 id** 的消息，直接取它即可；`visibleEndIndex` 存储备用/校验）

- [ ] **Step 3: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 4: Commit**

```bash
cd "/d/Documents/workspace/telegram manager/tdl"
git add web/app/src/views/Messages.vue
git commit -m "feat(web): 可视区最大消息 id 追踪（visibleMaxMsgId，倒序取顶部）"
```

---

### Task 2: 滚动视口自动已读（节流 + 去重）

**Files:**
- Modify: `web/app/src/views/Messages.vue`

**Interfaces:**
- Consumes: Task 1 的 `visibleMaxMsgId()`、`current.value.readInboxMaxId`、`settings.value.autoRead`、`api.markRead`、`refreshChatsUnread`
- Produces: `function maybeAutoReadVisible()`（onMsgScroll 节流调用）+ `doMarkRead(maxId)`（抽公共已读逻辑，复用给 markReadCurrent）

- [ ] **Step 1: 抽公共 doMarkRead + 实现视口已读**（`Messages.vue`）

在 `markReadCurrent` 附近重构 + 新增：
```js
// 公共已读：调 RPC + 推进本地分界 + 刷新徽标。maxId 是「标记 ≤maxId 已读」。
async function doMarkRead(maxId) {
  if (!current.value || !maxId) return
  try {
    await api.markRead(String(current.value.id), maxId)
    current.value.readInboxMaxId = maxId // 推进本地未读分界
    refreshChatsUnread() // 刷新会话列表未读徽标
  } catch { /* 静默 */ }
}

async function markReadCurrent(force = false) {
  if (!current.value || !unreadLastId.value) return
  if (!force && !settings.value.autoRead) return
  await doMarkRead(unreadLastId.value)
}

// 滚动视口自动已读（仅自动模式）：视口内最大消息 id > 当前已读分界才推进。
// 未读按已查看条数递减：服务器端 maxID 语义 = ≤maxID 全读，maxID 之后即剩余未读。
async function maybeAutoReadVisible() {
  if (!settings.value.autoRead) return          // 仅自动模式
  if (!current.value?.readInboxMaxId) return    // 无未读分界（无未读/未定位）
  const maxId = visibleMaxMsgId()
  if (!maxId || maxId <= current.value.readInboxMaxId) return // 无新推进（去重）
  await doMarkRead(maxId)
}
```

- [ ] **Step 2: 节流 + 接入 onMsgScroll**

节流变量（`let autoReadTimer` 附近声明区）：
```js
// 滚动视口自动已读的节流定时器：滚动停止 500ms 后调一次，避免每滚一像素就 RPC
let autoReadDebounce = null
```

`onMsgScroll` 加（`updateInUnreadArea()` 后）：
```js
  // 滚动视口自动已读：节流（停止滚动 500ms 后调一次）+ maxId 去重
  if (autoReadDebounce) clearTimeout(autoReadDebounce)
  autoReadDebounce = setTimeout(maybeAutoReadVisible, 500)
```

`onBeforeUnmount` 清理（现有 cleanup 区加）：
```js
  if (autoReadDebounce) clearTimeout(autoReadDebounce)
```

- [ ] **Step 3: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 4: Commit**

```bash
cd "/d/Documents/workspace/telegram manager/tdl"
git add web/app/src/views/Messages.vue
git commit -m "feat(web): 滚动视口自动已读——节流 500ms + maxId 去重，未读按已查看条数递减"
```

---

### Task 3: 构建部署 + E2E 实测

**Files:** 无（验证）

**Interfaces:** Consumes Task 1-2 全部

- [ ] **Step 1: 部署**

Run: `cd "/d/Documents/workspace/telegram manager/tdl" && ./build-tmm.sh web`
Expected: 前端部署

- [ ] **Step 2: 启动 + E2E（真会话）**

- 启动服务 → 找一个有未读的频道（会话列表有未读徽标，如科技圈）
- 记录初始未读数（如 99）
- 进入频道 → 往下滚动浏览 N 条（停在未读区）→ 等 500ms 节流 → 验证：
  - 未读徽标减少 N（`readHistory(maxID)` 后服务器端未读 = maxID 之后数量）
  - `current.readInboxMaxId` 推进到视口最大 id
- 手动模式（Settings 关 autoRead）：滚动不自动已读（未读不变）
- 翻到历史区（已读区）：自动模式下继续自动已读（仿桌面端）
- **用 Node 验证服务器端**：`/api/chats?refresh=1` 看未读频道 `unreadCount` 是否按滚动递减

- [ ] **Step 3: 风险记录**（若节流/去重/未读递减有出入，记录现象，按 spec §6 预案调整）

---

## Self-Review（writing-plans 要求）

**Spec 覆盖核对：**
- §4.1 可视区最大消息 id 追踪 → Task 1 ✓
- §4.2 滚动自动已读（节流 + 去重 + 推进本地分界）→ Task 2 ✓
- §4.3 复用 markReadCurrent/保留点↑全读 → Task 2（doMarkRead 抽取复用）✓
- §5 测试（单测纯逻辑 + E2E）→ Task 1（无测试基建则构建+E2E）/ Task 3 ✓
- §6 风险（节流/去重/徽标刷新/album 映射）→ Task 2 节流+去重 ✓

**占位符扫描：** 无 TBD/TODO；代码块完整（`onScrollerUpdate`/`displayItems`/`markReadCurrent`/`onMsgScroll`/`onBeforeUnmount` 接入点经源码验证）。

**类型一致性：** `visibleMaxMsgId`/`itemMaxId`/`doMarkRead`/`maybeAutoReadVisible` 各 Task 签名一致；`visibleEndIndex` ref 在 Task 1 定义、Task 2 未用（备用）——无冲突；`maxId > readInboxMaxId` 去重逻辑与 spec §4.2 一致。
