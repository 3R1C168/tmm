# 视口消息自动已读（未读逐条递减）设计规格（Spec）

> 日期：2026-08-04 ｜ 仓库：`D:\Documents\workspace\telegram manager\tdl` ｜ 分支：master
> 状态：待用户审阅（通过后进入 writing-plans）

## 1. 概述

在已实现的「未读定位 + 已读同步」基础上，**自动模式下滚动浏览时，视口内可见的消息自动标记已读**——未读数按「已查看条数」递减（如未读 99，滚动了 9 条 → 未读 90），仿桌面端滚动即已读。

## 2. 目标 / 非目标

**目标：**
- 自动模式（`autoRead=true`）：滚动时视口内可见的**最大消息 id** 自动已读（`channels.readHistory`/`messages.readHistory` maxID 语义 = 该 id 及之前全读）
- 未读计数按已查看条数递减（服务器端天然支持：maxID 之后的未读数）
- 节流：滚动停止/节流后调 RPC，避免每滚一像素就请求
- 去重：只有**新的更大 id** 进入视口才调（已读过的 maxID 不重复）

**非目标（v1 明确不做）：**
- ❌ 手动模式（`autoRead=false`）不自动视口已读（保持现状：点↑或按钮才已读）
- ❌ 逐条调 RPC（服务器端 maxID 语义天然递减，无需逐条）
- ❌ 往上翻历史自动已读的精确控制（自动模式下翻历史也标已读，仿桌面端；若需「仅未读区自动已读」作 v2 开关）

## 3. 已核实的技术事实（探索+源码验证）

| 项 | 结论 |
|---|---|
| 已读 RPC | `FetchMarkRead`（`app/chat/fetch.go`）已实现：频道 `channels.readHistory`、用户/群 `messages.readHistory`，maxID 语义 = ≤maxID 全读 |
| 前端已读入口 | `markReadCurrent(force)`（`Messages.vue` L959）已存在：调 `api.markRead` + 推进本地 `readInboxMaxId` + 刷新未读徽标；`force=true` 跳过 autoRead 检查 |
| 可视区追踪 | `onScrollerUpdate(_startIndex, _endIndex, visibleStartIndex, visibleEndIndex)`（L934）已有，`currentVisibleIndex = visibleStartIndex`；**但只用了 visibleStartIndex，没存 visibleEndIndex** |
| 倒序布局 | 消息流最新在上（index 0）；可视区 `[visibleStartIndex, visibleEndIndex]`，顶部（visibleStartIndex）对应更大 id |
| 未读数递减 | 服务器端 `readHistory(maxID)` 后未读 = maxID 之后的数量——「看 N 条 → 未读减 N」天然成立，无需逐条 RPC |
| 滚动监听 | `onMsgScroll`（L920）已有滚动监听（loadMore + showBackTop + updateInUnreadArea） |
| settings | `settings = ref({ autoRead: true })`（L398），无 settings store |

## 4. 设计（前端，`Messages.vue`）

### 4.1 可视区最大消息 id 追踪

- `onScrollerUpdate` 增加存 `visibleEndIndex`（或直接算可视区消息）
- 新增 `visibleMaxMsgId()`：取可视区 `[visibleStartIndex, visibleEndIndex]` 内**最大 id 的消息**（倒序下 = visibleStartIndex 对应的消息 id，因顶部 id 最大）——用 `displayItems` 映射 index → 消息（跳过 divider）

### 4.2 滚动自动已读

- `onMsgScroll` 里（或节流后）调 `maybeAutoReadVisible()`：
  ```js
  async function maybeAutoReadVisible() {
    if (!settings.value.autoRead) return        // 仅自动模式
    if (!current.value?.readInboxMaxId) return  // 无未读分界
    const maxId = visibleMaxMsgId()
    if (!maxId || maxId <= current.value.readInboxMaxId) return // 无新的已读推进
    // 节流：滚动停止后再调（简单 debounce 或节流）
    await api.markRead(String(current.value.id), maxId)
    current.value.readInboxMaxId = maxId  // 推进本地分界
    // 未读徽标刷新（可选：节流合并，避免频繁 loadChats）
  }
  ```
- **节流**：`onMsgScroll` 高频触发，用 debounce（如 500ms 停止滚动后调一次）或节流（如 1s 内最多一次）；去重靠 `maxId > readInboxMaxId` 判断
- **未读徽标刷新**：每次已读后 `loadChats(true)` 太重——**节流合并**：本地推进 `readInboxMaxId`（徽标可本地算），间隔性刷新或离开频道时刷新

### 4.3 与现有功能关系

- 复用 `markReadCurrent` 的 RPC 逻辑（或抽公共 `doMarkRead(maxId)`）
- 保留「点↑跳末尾=全读」；视口自动已读是**额外触发源**（自动模式下滚动时）
- 手动模式不受影响

## 5. 测试策略

**单元测试：**
- `visibleMaxMsgId()` 纯逻辑：倒序 displayItems 取可视区最大 id（跳过 divider）
- 节流/去重逻辑（maxId 推进判断）

**端到端实测（真会话）：**
1. 有未读频道（如 99 未读），进入 → 滚动浏览 9 条 → 未读徽标 90（服务器端确认）
2. 滚动停止后自动已读（节流生效，非滚动即发）
3. 手动模式滚动不自动已读
4. 翻历史（已读区）自动已读（仿桌面端）

## 6. 风险与预案

| 风险 | 预案 |
|---|---|
| 滚动触发频繁 RPC | debounce/节流 + maxId 去重（只推进更大的） |
| 未读徽标频繁刷新 | 本地推进 readInboxMaxId + 节流合并刷新（离开频道/间隔刷） |
| 可视区 index→id 映射（album 聚合） | displayItems 里 album 取组内消息 id，跳过 divider |
| 手动模式误已读 | `settings.autoRead` 严格 gate |
