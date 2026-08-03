# 手动全读按钮 + 移除视口自动已读 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 移除视口自动已读（未读线不再变动、左侧列表不频繁刷新），标题栏新增「全部已读」按钮（弹确认框→全读当前频道），顺手做「只看媒体」按钮化、加载更早悬浮、分隔线文案、卡片阴影加深、设置页卡片居中。

**Architecture:** 前端 `Messages.vue` 为主：删自动已读触发链（`maybeAutoReadVisible`/`autoReadDebounce`/`updateInUnreadArea`/`inUnreadArea`/`jumpUnread`），`markReadCurrent` 改造为弹确认框的「全部已读」；标题栏重排（刷新、全部已读、按范围下载、下载所选、只看媒体、搜索框）；`loadMore` 悬浮化；分隔线文案改。后端 `settings.go`/`handlers.go` 删 `AutoRead` 字段；设置页删开关；`main.css` 加深 `--shadow`；`Settings.vue` 卡片居中。

**Tech Stack:** Vue 3.5 / Element Plus / Go 1.26.5 / gotd v0.140.0

## Global Constraints

- 前端构建：`cd web/app && npm run build` 成功；部署 `./build-tmm.sh web`
- 后端改动必须重建 exe：`./build-tmm.sh`
- 注释中文；样式用 token（禁硬编码色值）
- 复用现有 `doMarkRead`/`api.markRead`/`refreshChatsUnread`，不重复造 RPC
- 「全部已读」必须弹确认框（防误触）；确认后 `doMarkRead(最新消息 id)` 全读当前频道
- 右下角浮动按钮点击直接跳最后一条消息（最新），不再有「跳到未读末尾=全读」/「回第一条未读」双态
- 未读分隔线保留（`hasUnread`/`readInboxMaxId` 逻辑不动），文案「未读消息」→「以上是未读消息」
- 移除 autoRead：设置页开关、后端 `AutoRead` 字段、Messages.vue 的 `settings.autoRead` 引用全删
- 卡片阴影只改 `--shadow` token（亮/暗两处），适度加深

---

### Task 1: 后端移除 AutoRead 设置字段

**Files:**
- Modify: `server/settings.go:33,65,80`（settingsJSON.AutoRead、defaultSettings.AutoRead）
- Modify: `server/handlers.go:417`（updateSettingsRequest.AutoRead）
- Modify: `server/settings_test.go:58`（autoRead 断言）
- Test: 无新增测试——后端 `go test ./server/...` 通过

**Interfaces:**
- Consumes: 现有 settings 结构
- Produces: `settings` 无 `AutoRead` 字段；`updateSettingsRequest` 无 `AutoRead`；`settingsJSON` 无 `AutoRead`

- [ ] **Step 1: 移除 `server/settings.go` 的 AutoRead**

`server/settings.go` L33-34（settingsJSON）删：
```go
	AutoRead           *bool   `json:"autoRead"`
```
L65-66（settings struct）删：
```go
	// AutoRead 进入频道定位未读后是否自动标记已读（false = 手动模式）。
	AutoRead bool `json:"autoRead"`
```
L80（defaultSettings）删：
```go
		AutoRead:           true,
```

- [ ] **Step 2: 移除 `server/handlers.go` 的 AutoRead**

`server/handlers.go` L417（updateSettingsRequest）删：
```go
	AutoRead           *bool   `json:"autoRead"`
```

- [ ] **Step 3: 更新 `server/settings_test.go`**

L58 `require.Equal(t, false, snap["autoRead"])` 删除（该断言测的字段已移除）。

- [ ] **Step 4: 后端测试 + 构建**

Run: `cd "D:/Documents/workspace/telegram manager/tdl" && go build ./... && go test ./server/...`
Expected: 全部通过（autoRead 相关测试已删）

- [ ] **Step 5: Commit**

```bash
cd "D:/Documents/workspace/telegram manager/tdl"
git add server/settings.go server/handlers.go server/settings_test.go
git commit -m "feat(server): 移除 AutoRead 设置字段（视口自动已读已移除）"
```

---

### Task 2: Messages.vue 移除自动已读触发链

**Files:**
- Modify: `web/app/src/views/Messages.vue`（多处，见下）

**Interfaces:**
- Consumes: 无（纯删除）
- Produces: `onMsgScroll` 不再含自动已读节流；`onScrollerUpdate` 简化；`jumpUnread`/`maybeAutoReadVisible`/`autoReadDebounce`/`updateInUnreadArea`/`inUnreadArea` 删除

- [ ] **Step 1: 删 `autoReadDebounce` 声明**（L621）

```js
let autoReadDebounce = null
```
整行删除。

- [ ] **Step 2: 删 `onBeforeUnmount` 中的清理**（L719）

```js
  if (autoReadDebounce) clearTimeout(autoReadDebounce)
```
整行删除。

- [ ] **Step 3: 删 `onMsgScroll` 中的节流接入**（L936-937）

```js
  // 滚动视口自动已读：节流（停止滚动 500ms 后调一次）+ maxId 去重
  if (autoReadDebounce) clearTimeout(autoReadDebounce)
  autoReadDebounce = setTimeout(maybeAutoReadVisible, 500)
```
整块删除（`onMsgScroll` 保留 loadMore/backTop/updateInUnreadArea）。

- [ ] **Step 4: 删 `updateInUnreadArea` 及 `inUnreadArea` 状态**（L456、L948-952）

L456 `const inUnreadArea = ref(true)` 删除。
L948-952 `updateInUnreadArea()` 函数整体删除。
`onMsgScroll` 内 `updateInUnreadArea()` 调用删除（L934）。
`onScrollerUpdate` 内 `updateInUnreadArea()` 调用删除（L945）。

- [ ] **Step 5: 删 `onScrollerUpdate` 的可见区追踪（保留简化版）**

`onScrollerUpdate`（L940-946）改为：
```js
// vue-virtual-scroller 的 update 事件：参数为 [startIndex, endIndex, visibleStartIndex, visibleEndIndex]
// 无自动已读后仅保留占位（DynamicScroller emit-update 需要），不追踪视口。
function onScrollerUpdate() {}
```

同时删除（不再被引用）：
- L458 `const currentVisibleIndex = ref(0)`
- L459-460 `const visibleEndIndex = ref(0)`
- L956-963 `function itemMaxId(item)`
- L965-969 `function visibleMaxMsgId()`

- [ ] **Step 6: 删 `jumpUnread` + `jump-btn` 模板**

L974-982 `function jumpUnread()` 整体删除。
模板 L284-292（`<!-- 未读跳转按钮 -->` 整个 `<button class="jump-btn">...</button>`）删除。

- [ ] **Step 7: 删 autoRead 相关**

L396-398：
```js
// 已读设置（autoRead）：无 settings store，与 Settings.vue 同模式从 api 读取；
// 默认 true（与后端 defaultSettings 一致），onMounted 拉取真实值覆盖
const settings = ref({ autoRead: true })
```
删除 `settings` 定义。

L699（onMounted 拉取）：
```js
    if (s && typeof s.autoRead === 'boolean') settings.value.autoRead = s.autoRead
```
删除（连同拉取 settings 的整个逻辑，若只服务 autoRead）。

模板 L63-68（手动模式「标记已读」按钮）：
```html
          <!-- 手动模式「标记已读」按钮：autoRead 关闭时显示（手动标记全部已读），有未读分界才需要 -->
          <button
            v-if="current && current.readInboxMaxId > 0 && !settings.autoRead"
            class="chat-read-btn"
            @click="markReadCurrent(true)"
            title="标记全部已读"
          >
            标记已读
          </button>
```
删除（Task 3 由新「全部已读」按钮替代）。

**同时**：`markReadCurrent`（L994-998）去掉 autoRead 依赖（`settings` 已删，参数 force 也无意义）：
```js
async function markReadCurrent() {
  if (!current.value || !unreadLastId.value) return
  await doMarkRead(unreadLastId.value)
}
```
（本任务先保证 build 通过；确认框化在 Task 3。）

- [ ] **Step 8: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功（无 `settings.autoRead` 残留引用）

- [ ] **Step 9: Commit**

```bash
cd "D:/Documents/workspace/telegram manager/tdl"
git add web/app/src/views/Messages.vue
git commit -m "feat(web): 移除视口自动已读触发链（未读线不再自动变动，左侧列表不频繁刷新）"
```

---

### Task 3: 标题栏「全部已读」按钮 + 布局调整 + 只看媒体按钮化

**Files:**
- Modify: `web/app/src/views/Messages.vue`

**Interfaces:**
- Consumes: `doMarkRead(maxId)`（保留，L985-992）、`unreadLastId`（= 最新消息 id）、`hasUnread`（全读判断）、`refreshChatsUnread`
- Produces: `markReadAll()`（弹确认框 → doMarkRead）；标题栏新布局；`mediaOnly` 改按钮

- [ ] **Step 1: 改造 `markReadCurrent` 为「全部已读」（弹确认框）**

先确认 import：L376 目前 `import { ElMessage } from 'element-plus'`，改为：
```js
import { ElMessage, ElMessageBox } from 'element-plus'
```

L994-998 现有（Task 2 已改为无参版）：
```js
async function markReadCurrent() {
  if (!current.value || !unreadLastId.value) return
  await doMarkRead(unreadLastId.value)
}
```
改为弹确认框：
```js
// 标题栏「全部已读」：弹确认框防误触，确认后全读当前频道（≤最新消息 id）。
async function markReadCurrent() {
  if (!current.value || !unreadLastId.value) return
  try {
    await ElMessageBox.confirm(
      `确认将「${current.value.visibleName || current.value.username || ''}」全部标记为已读？`,
      '全部已读',
      { confirmButtonText: '确认', cancelButtonText: '取消', type: 'warning' }
    )
  } catch {
    return // 用户取消
  }
  await doMarkRead(unreadLastId.value)
}
```

- [ ] **Step 2: 标题栏模板调整**

标题栏（L47-78）重新排列，从右到左：
```html
        <header class="chat-header">
          <div class="hinfo">
            <div class="title">{{ current.visibleName || current.username }}</div>
            <div class="sub muted">{{ typeLabel(current.type) }}<span v-if="selected.size"> · 已选 {{ selected.size }} 条</span></div>
          </div>
          <div class="spacer" />
          <div class="chat-search-inline" :class="{ open: searchOpen }">
            <input
              v-model="searchQuery"
              placeholder="搜索本频道…"
              @input="onSearchInput"
              @compositionstart="composing = true"
              @compositionend="composing = false; nextTick(onSearchInput)"
              @keydown.esc="closeSearch"
              @focus="searchOpen = true"
            />
            <button v-if="searchQuery" class="search-clear" @click="clearSearch">✕</button>
          </div>
          <!-- 只看媒体：按钮点击高亮（mediaOnly 激活态），切换后重载消息 -->
          <button
            class="chat-media-btn"
            :class="{ active: mediaOnly }"
            @click="mediaOnly = !mediaOnly; reloadMessages()"
            title="只看媒体"
          >只看媒体</button>
          <el-button size="small" :disabled="!selected.size" @click="downloadSelected">下载所选 ({{ selected.size }})</el-button>
          <el-button size="small" @click="rangeDialog = true">按范围下载</el-button>
          <el-button
            size="small"
            :disabled="!hasUnread"
            @click="markReadCurrent"
            title="将该频道全部标记为已读"
          >全部已读</el-button>
          <button class="chat-refresh-btn" :disabled="msgsLoading" @click="reloadMessages(true)" title="刷新消息">
            <RefreshCw v-if="!msgsLoading" :size="14" />
            <Loader2 v-else :size="14" class="spin" />
          </button>
        </header>
```
（从右到左：刷新、全部已读、按范围下载、下载所选、只看媒体、搜索框——`chat-refresh-btn` 是最后（最右），`chat-search-inline` 最左。原「只看媒体 checkbox」L62 删除，换按钮。）

- [ ] **Step 3: 删「只看媒体」checkbox**

模板 L62：
```html
          <el-checkbox v-model="mediaOnly" size="small" @change="reloadMessages">只看媒体</el-checkbox>
```
删除（Step 2 已用按钮替代）。

- [ ] **Step 4: 添加「只看媒体」按钮 CSS**

在 `.chat-refresh-btn` 附近加：
```css
/* 只看媒体：按钮点击高亮激活态，与其他标题栏按钮视觉统一 */
.chat-media-btn {
  flex: none; height: 32px; padding: 0 12px; border: 1px solid var(--border);
  border-radius: var(--radius-sm); background: transparent; color: var(--text-3);
  cursor: pointer; font-size: 13px; transition: .15s;
}
.chat-media-btn:hover { border-color: var(--accent); color: var(--accent); background: var(--accent-soft); }
.chat-media-btn.active { border-color: var(--accent); color: var(--accent); background: var(--accent-soft); }
```

- [ ] **Step 5: 删 `.chat-read-btn` CSS（旧手动按钮）**

L1375-1381 `.chat-read-btn` 相关 CSS 删除（按钮已移除）。

- [ ] **Step 6: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 7: Commit**

```bash
cd "D:/Documents/workspace/telegram manager/tdl"
git add web/app/src/views/Messages.vue
git commit -m "feat(web): 标题栏「全部已读」按钮（确认框）+ 布局调整 + 只看媒体按钮化"
```

---

### Task 4: 加载更早消息悬浮 + 分隔线文案 + 卡片阴影 + 设置页居中

**Files:**
- Modify: `web/app/src/views/Messages.vue`（loadMore 悬浮、分隔线文案）
- Modify: `web/app/src/styles/main.css`（--shadow 加深）
- Modify: `web/app/src/views/Settings.vue`（卡片居中）

**Interfaces:**
- Consumes: 无（纯 UI）
- Produces: `loadMore` 悬浮按钮、分隔线「以上是未读消息」、--shadow 加深、设置卡片居中

- [ ] **Step 1: 「加载更早的消息」悬浮化**

模板 L272-276 现状：
```html
          <div v-if="hasMore" class="loading-tip">
            <el-button size="small" text :loading="loadingMore" @click="loadMore">{{ loadingMore ? '加载中…' : '加载更早的消息' }}</el-button>
          </div>
          <div v-else-if="displayItems.length" class="loading-tip muted">已到最早的消息</div>
```
改为（悬浮按钮移到 msg-scroll 容器内绝对定位，不占布局流）：
```html
          <!-- 加载更早的消息：底部中央悬浮（不占布局流，滚动时浮动在消息区底部） -->
          <div v-if="hasMore" class="load-more-float">
            <el-button size="small" :loading="loadingMore" @click="loadMore">{{ loadingMore ? '加载中…' : '加载更早的消息' }}</el-button>
          </div>
          <div v-else-if="displayItems.length" class="loading-tip muted">已到最早的消息</div>
```

CSS 加（`.loading-tip` 附近）：
```css
/* 加载更早的消息：底部中央悬浮，叠在消息列表上方不占布局流 */
.load-more-float {
  position: absolute; bottom: 12px; left: 50%; transform: translateX(-50%);
  z-index: 20; pointer-events: auto;
}
.load-more-float .el-button { box-shadow: var(--shadow); }
```

悬浮定位说明：`.msg-scroll` 是 flex 列容器，按钮绝对定位的参考系是最近的 `position: relative` 祖先——即 `.chat-view`（L1393 已有 `position: relative`）。`.msg-scroll` 自身无需改 position。

- [ ] **Step 2: 分隔线文案**

L134：
```html
                  <span>未读消息</span>
```
改为：
```html
                  <span>以上是未读消息</span>
```

- [ ] **Step 3: 卡片阴影加深**

`web/app/src/styles/main.css`：
- L24 亮色：
```css
  --shadow: 0 1px 3px rgba(16, 24, 40, 0.06), 0 4px 16px rgba(16, 24, 40, 0.05);
```
改为（适度加深）：
```css
  --shadow: 0 1px 3px rgba(16, 24, 40, 0.10), 0 6px 20px rgba(16, 24, 40, 0.09);
```
- L40 暗色：
```css
  --shadow: 0 1px 3px rgba(0, 0, 0, 0.3), 0 4px 16px rgba(0, 0, 0, 0.25);
```
改为（适度加深）：
```css
  --shadow: 0 1px 3px rgba(0, 0, 0, 0.35), 0 6px 20px rgba(0, 0, 0, 0.32);
```

- [ ] **Step 4: 设置页卡片居中**

`web/app/src/views/Settings.vue` L197：
```css
.card { max-width: 640px; margin-bottom: 18px; }
```
改为：
```css
.card { max-width: 640px; margin: 0 auto 18px; }
```

- [ ] **Step 5: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 6: Commit**

```bash
cd "D:/Documents/workspace/telegram manager/tdl"
git add web/app/src/views/Messages.vue web/app/src/styles/main.css web/app/src/views/Settings.vue
git commit -m "feat(web): 加载更早消息底部悬浮 + 分隔线文案「以上是未读消息」+ 卡片阴影加深 + 设置页卡片居中"
```

---

### Task 5: 设置页移除「自动已读」开关 + 构建部署 + E2E 实测

**Files:**
- Modify: `web/app/src/views/Settings.vue`（移除 autoRead 开关 + settings 字段）
- 无其他（验证）

**Interfaces:**
- Consumes: Task 1（后端无 AutoRead）、Task 3（前端无 autoRead）
- Produces: 设置页无「已读同步」卡片

- [ ] **Step 1: 移除设置页 autoRead 开关**

`Settings.vue` L58-68（「已读同步」卡片整块）：
```html
    <div class="card">
      <h3 class="sec">已读同步</h3>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">自动已读</div>
          <div class="setdesc muted">进入频道定位未读后，跳到未读末尾自动标记已读（仿桌面端）。关闭后进入频道只定位不标记，可手动点「标记已读」。</div>
        </div>
        <el-switch v-model="settings.autoRead" @change="saveSettings" />
      </div>
    </div>
```
删除整块。

- [ ] **Step 2: 移除 settings 初始值的 autoRead**

`Settings.vue` L135：
```js
const settings = ref({ previewTakeout: false, cacheMaxMB: 5120, clearPreviewOnExit: true, autoRead: true, msgIdPrefix: false, channelIdPrefix: false, channelDir: false, downloadDir: '', filenameMaxLen: 200 })
```
删 `autoRead: true,`。

L144：
```js
      autoRead: s.autoRead !== false,
```
删除。

L160：
```js
      autoRead: settings.value.autoRead,
```
删除。

- [ ] **Step 3: 前端构建**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 4: 部署（前端 + 后端 exe）**

Run: `cd "D:/Documents/workspace/telegram manager/tdl" && ./build-tmm.sh web && ./build-tmm.sh`
Expected: 前端部署 + 后端 exe 重建（Task 1 的后端改动在此生效）

- [ ] **Step 5: 重启服务 + E2E 实测（真实浏览器）**

1. 重启 tmm 服务（`start.bat` 或 kill + `./tmm.exe serve --port 8080 --no-browser`）
2. 打开 http://127.0.0.1:8080 进入有未读的频道
3. 验证点：
   - 标题栏从右到左：刷新、全部已读、按范围下载、下载所选、只看媒体、搜索框 ✓
   - 点「全部已读」→ 弹确认框 → 取消 → 未读不变；确认 → 未读归零、左侧徽标清除 ✓
   - 滚动浏览 → **未读线不自动变动**、左侧列表**不刷新** ✓
   - 点「只看媒体」→ 按钮高亮 + 只显示媒体消息；再点取消 ✓
   - 右下角按钮点击 → 跳到最后一条消息（最新）✓
   - 「加载更早的消息」悬浮在底部中央，点击加载更早消息 ✓
   - 分隔线显示「以上是未读消息」✓
   - 卡片阴影比之前深（暗色模式同样验证）✓
   - 设置页无「已读同步」卡片；卡片水平居中 ✓

- [ ] **Step 6: Commit（若测试有微调）**

```bash
cd "D:/Documents/workspace/telegram manager/tdl"
git add web/app/src/views/Settings.vue
git commit -m "feat(web): 设置页移除自动已读开关（autoRead 已彻底移除）"
```

---

## Self-Review（writing-plans 要求）

**Spec 覆盖核对：**
- 移除视口自动已读（未读线变动/列表刷新根源）→ Task 2 ✓
- 标题栏「全部已读」按钮 + 确认框 → Task 3 ✓
- 标题栏布局（刷新、全部已读、按范围下载、下载所选、只看媒体、搜索框）→ Task 3 ✓
- 「只看媒体」按钮化 + 点击高亮 → Task 3 ✓
- 移除「跳到未读末尾」；右下角跳最新 → Task 2（jump-btn 删）+ 保留 back-top ✓
- 「加载更早的消息」底部悬浮 → Task 4 ✓
- 分隔线文案「以上是未读消息」→ Task 4 ✓
- 移除 autoRead（设置页 + 后端 + Messages）→ Task 1 + Task 2 + Task 5 ✓
- 卡片阴影加深（--shadow token）→ Task 4 ✓
- 设置页卡片居中 → Task 4 ✓

**占位符扫描：** 无 TBD/TODO；所有代码块完整（源码接入点已核实 L621/L719/L936/L948/L456/L940/L974/L284/L396/L699/L63/L62/L994/L272/L134/L197/L135/L144/L160）。

**类型一致性：** `markReadCurrent`（Task 2 无参化、Task 3 确认框化）模板调用一致；`doMarkRead(maxId)`（保留）签名一致；`unreadLastId`/`hasUnread` 保留供 Task 3 用；`settings` 删除顺序（Task 2 删 Messages 内、Task 5 删 Settings 内）无冲突。

**依赖顺序：** Task 2 删除 `settings.autoRead` 引用前需确认无残留（Task 3 改造 markReadCurrent 时同步清理）；`markReadCurrent` 的 `settings.value.autoRead` 在 Task 2 步骤 7 保留定义、Task 3 步骤 1 移除——两任务衔接需注意 build 通过。

**风险：** `el-button` 的 `:disabled="!hasUnread"` 全读按钮在无未读时禁用；确认框 `ElMessageBox` 已 import（现有代码用）；`.msg-scroll` 需 `position:relative`（Task 4 步骤 1 检查）。
