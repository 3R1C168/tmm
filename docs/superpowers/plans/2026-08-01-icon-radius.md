# emoji→矢量图标 + 圆角 token 规整 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 web 前端所有 emoji 图标换成 Lucide 矢量图标 + 状态色，圆角散值规整为 sm/radius/lg 三档 token。

**Architecture:** 纯视觉层重构。新增 `lucide-vue-next` 依赖，按需引入图标组件；圆角 token 集中在 `main.css`，各文件引用 var()。Tasks 状态用图标组件 + CSS class 控制颜色。

**Tech Stack:** Vue 3 + Vite + lucide-vue-next（新增）

## Global Constraints
- 工作目录：`D:\Documents\workspace\telegram manager\tdl\web\app`
- 后端不动；业务逻辑/API/Plyr 配置不动
- 图标统一描边 2px（Lucide 默认）、颜色用 `currentColor` 继承
- 圆角三档：`--radius-sm: 8px`（按钮/输入框/小标签）/ `--radius: 12px`（卡片/气泡/芯片）/ `--radius-lg: 16px`（大面板/模态/播放器条）；头像/圆形按钮 50% 保留；药丸状 20px 保留；scrollbar 8px 保留；进度条 2-3px 功能性保留
- 每个任务结束运行 `npm run build` 验证编译通过
- 全部完成后部署 dist 到 tmm.v11/web 和 tmm.v10/web

---

### Task 1: 安装 lucide-vue-next + 设置圆角 token

**Files:**
- Modify: `package.json`（加依赖）
- Modify: `src/styles/main.css:3-4`（圆角 token）

**Interfaces:**
- Produces: `--radius-sm: 8px` / `--radius: 12px` / `--radius-lg: 16px` 三个 CSS 变量，供后续任务引用

- [ ] **Step 1: 安装依赖**

Run:
```bash
cd "D:\Documents\workspace\telegram manager\tdl\web\app"
npm install lucide-vue-next
```
Expected: package.json 出现 `"lucide-vue-next": "^..."`，node_modules 装上

- [ ] **Step 2: 修改圆角 token**

Edit `src/styles/main.css`，找到 `:root` 里的：
```css
  --radius: 14px;
  --radius-sm: 10px;
```
改为：
```css
  --radius: 12px;
  --radius-sm: 8px;
  --radius-lg: 16px;
```

- [ ] **Step 3: 构建验证**

Run: `npm run build`
Expected: 构建成功（圆角值变小不会报错，仅视觉变化）

- [ ] **Step 4: Commit**

```bash
cd "D:\Documents\workspace\telegram manager\tdl"
git add web/app/package.json web/app/package-lock.json web/app/src/styles/main.css
git commit -m "ui: 加 lucide-vue-next + 圆角 token 三档(8/12/16px)"
```

---

### Task 2: App.vue 导航 + 主题图标 + 圆角

**Files:**
- Modify: `src/App.vue:13-14`（主题切换）、`:27,52-54`（导航 ico）、`:86`（iconbtn 圆角）

**Interfaces:**
- Consumes: `--radius-sm` token
- Produces: 无（顶层组件）

- [ ] **Step 1: 导入 Lucide 图标**

在 `<script setup>` 的 import 区加（`import GlobalPlayer` 那行之后）：
```js
import { Moon, Sun, MessageSquare, Inbox, UserRound } from 'lucide-vue-next'
```

- [ ] **Step 2: 改 nav 数据结构为组件引用**

把：
```js
const nav = [
  { path: '/messages', label: '消息', ico: '💬' },
  { path: '/tasks', label: '任务', ico: '📥' },
  { path: '/settings', label: '账号', ico: '👤' },
]
```
改为：
```js
const nav = [
  { path: '/messages', label: '消息', ico: MessageSquare },
  { path: '/tasks', label: '任务', ico: Inbox },
  { path: '/settings', label: '账号', ico: UserRound },
]
```

- [ ] **Step 3: 模板用动态组件**

把模板里的：
```html
<span class="ico">{{ item.ico }}</span>
```
改为：
```html
<component :is="item.ico" class="ico" :size="20" />
```

- [ ] **Step 4: 主题切换 emoji 换图标**

把：
```html
{{ store.theme === 'light' ? '🌙' : '☀️' }}
```
改为：
```html
<Moon v-if="store.theme === 'light'" :size="18" />
<Sun v-else :size="18" />
```

- [ ] **Step 5: iconbtn 圆角归并**

CSS 里 `.iconbtn` 的 `border-radius: 10px` 改为 `border-radius: var(--radius-sm)`

- [ ] **Step 6: 构建验证**

Run: `npm run build`
Expected: 成功

- [ ] **Step 7: Commit**

```bash
git add web/app/src/App.vue
git commit -m "ui: App 导航/主题图标换 Lucide + 圆角 token"
```

---

### Task 3: GlobalPlayer.vue 播放控制图标 + 圆角

**Files:**
- Modify: `src/components/GlobalPlayer.vue:39,41,43,60`（图标）、圆角 `:16px→var(--radius-lg)`

- [ ] **Step 1: 导入图标**

`<script setup>` 加：
```js
import { SkipBack, SkipForward, Play, Pause, X } from 'lucide-vue-next'
```

- [ ] **Step 2: 替换 5 个 emoji**

```html
<button class="gp-btn" :disabled="!player.hasPrev" @click="player.prev()" title="上一首">⏮</button>
```
→
```html
<button class="gp-btn" :disabled="!player.hasPrev" @click="player.prev()" title="上一首"><SkipBack :size="18" /></button>
```

```html
{{ player.playing ? '⏸' : '▶' }}
```
→
```html
<Pause v-if="player.playing" :size="22" />
<Play v-else :size="22" />
```

```html
<button class="gp-btn" :disabled="!player.hasNext" @click="player.next()" title="下一首">⏭</button>
```
→
```html
<button class="gp-btn" :disabled="!player.hasNext" @click="player.next()" title="下一首"><SkipForward :size="18" /></button>
```

```html
<button class="gp-btn gp-close" @click="player.close()" title="关闭播放器">✕</button>
```
→
```html
<button class="gp-btn gp-close" @click="player.close()" title="关闭播放器"><X :size="18" /></button>
```

- [ ] **Step 3: 圆角归并**

CSS 里 GlobalPlayer 容器的 `border-radius: 16px` → `border-radius: var(--radius-lg)`

- [ ] **Step 4: 构建验证 + Commit**

Run: `npm run build`
```bash
git add web/app/src/components/GlobalPlayer.vue
git commit -m "ui: GlobalPlayer 控制图标换 Lucide + 圆角 token"
```

---

### Task 4: MediaViewer.vue 图标 + 圆角归并

**Files:**
- Modify: `src/components/MediaViewer.vue:18,19,48,50,66`（图标）、多处圆角

- [ ] **Step 1: 导入图标**

`<script setup>` 加：
```js
import { Download, X, TriangleAlert } from 'lucide-vue-next'
```

- [ ] **Step 2: 替换 emoji**

`:18` `<button class="mv-dl-top" ...>⬇ 下载</button>` →
```html
<button class="mv-dl-top" @click="download" title="下载此媒体"><Download :size="16" /> 下载</button>
```

`:19` `<button class="mv-x" ...>✕</button>` →
```html
<button class="mv-x" title="关闭 (Esc)" @click="close"><X :size="20" /></button>
```

`:48` `<span>⚠ 视频无法播放</span>` →
```html
<span><TriangleAlert :size="16" /> 视频无法播放</span>
```

`:50` `<button class="mv-dl" @click="download">⬇ 下载后本地播放</button>` →
```html
<button class="mv-dl" @click="download"><Download :size="16" /> 下载后本地播放</button>
```

`:66` `<button class="mv-dl" ...>⬇ 下载</button>` →
```html
<button class="mv-dl" @click="download" title="下载此媒体"><Download :size="16" /> 下载</button>
```

- [ ] **Step 3: 圆角归并**

CSS 里按值归类（用 Edit 逐个改，注意同值多处用 replace_all 谨慎）：
- `border-radius: 14px`（容器）→ `var(--radius-lg)`
- `border-radius: 0 0 14px 14px` → `0 0 var(--radius-lg) var(--radius-lg)`
- `border-radius: 15px`（顶部按钮）→ `var(--radius-lg)`
- `border-radius: 17px`（播放按钮）→ `var(--radius-lg)`
- `border-radius: 10px`（输入/按钮）→ `var(--radius-sm)`
- `border-radius: 6px` → `var(--radius-sm)`
- `border-radius: 8px` → `var(--radius-sm)`
- `border-radius: 3px` / `2px`（进度条/handle）→ **保留不动**
- `border-radius: 50%` → 保留
- `border-radius: 0` → 保留（全屏模式）

- [ ] **Step 4: 构建验证 + Commit**

Run: `npm run build`
```bash
git add web/app/src/components/MediaViewer.vue
git commit -m "ui: MediaViewer 图标换 Lucide + 圆角归并"
```

---

### Task 5: Messages.vue 图标 + 圆角归并

**Files:**
- Modify: `src/views/Messages.vue:98,104,120,128,146`（图标）、多处圆角

- [ ] **Step 1: 导入图标**

`<script setup>` 加：
```js
import { Play, Pause, Download } from 'lucide-vue-next'
```

- [ ] **Step 2: 替换 emoji**

`:98` `<span v-if="isVideo(m.media)" class="cell-play">▶</span>` →
```html
<span v-if="isVideo(m.media)" class="cell-play"><Play :size="14" /></span>
```

`:104` `<button class="dl-group" ...>⬇ 下载整组(...)</button>` →
```html
<button class="dl-group" @click.stop="downloadAlbum(item)"><Download :size="14" /> 下载整组({{ item.msgs.length }})</button>
```

`:120` `<span class="play-ico">▶</span>` →
```html
<span class="play-ico"><Play :size="20" /></span>
```

`:128` `<span class="fico">{{ ... ? '⏸' : '▶' }}</span>` →
```html
<span class="fico"><Pause v-if="player.current?.msgId === item.msg.id && player.playing" :size="14" /><Play v-else :size="14" /></span>
```

`:146` `<button class="dl-msg" ...>⬇ 下载</button>` →
```html
<button class="dl-msg" :disabled="!item.msg.hasMedia" @click.stop="downloadSingle(item.msg)" :title="item.msg.hasMedia ? '下载媒体' : '无媒体'"><Download :size="14" /> 下载</button>
```

- [ ] **Step 3: 圆角归并**

- `border-radius: 10px`（搜索框/标签）→ `var(--radius-sm)`
- `border-radius: 9px`（视频单元）→ `var(--radius-sm)`
- `border-radius: 11px`（文件芯片，多处）→ `var(--radius)`
- `border-radius: 12px`（相册/气泡）→ `var(--radius)`
- `border-radius: 16px`（气泡 1rem）→ `var(--radius-lg)`
- `border-radius: 20px`（下载按钮药丸）→ **保留**
- `border-radius: 50%` → 保留
- 已用 `var(--radius-sm)` 的保持

- [ ] **Step 4: 构建验证 + Commit**

Run: `npm run build`
```bash
git add web/app/src/views/Messages.vue
git commit -m "ui: Messages 图标换 Lucide + 圆角归并"
```

---

### Task 6: Tasks.vue 状态图标 + 状态色 + 圆角

**Files:**
- Modify: `src/views/Tasks.vue:5,18,39,43,101-115`（状态图标逻辑）、圆角

**Interfaces:**
- 状态色 CSS class 命名：`.st-pending`/`.st-running`/`.st-paused`/`.st-done`/`.st-failed`/`.st-interrupted`

- [ ] **Step 1: 导入图标**

`<script setup>` 加：
```js
import { Clock, Download, Pause, Check, X, TriangleAlert, Plus } from 'lucide-vue-next'
```

- [ ] **Step 2: 新建任务按钮 ➕ 换图标**

`:5` `<el-button type="primary" @click="$router.push('/new')">➕ 新建任务</el-button>` →
```html
<el-button type="primary" @click="$router.push('/new')"><el-icon><Plus :size="16" /></el-icon> 新建任务</el-button>
```
注：el-button 内用 el-icon 包裹保证对齐；若 el-icon 未全局注册则直接 `<Plus :size="16" />`

- [ ] **Step 3: 改 statusIcon / fileIcon 返回组件而非字符串**

把：
```js
function statusIcon(s) {
  return { pending: '⏳', running: '⬇', paused: '⏸', done: '✓', failed: '✕', interrupted: '⚠' }[s] || '•'
}
function fileIcon(s) {
  return { pending: '⏳', running: '⬇', done: '✓', failed: '✕' }[s] || '•'
}
```
改为：
```js
const statusIconMap = { pending: Clock, running: Download, paused: Pause, done: Check, failed: X, interrupted: TriangleAlert }
const fileIconMap = { pending: Clock, running: Download, done: Check, failed: X }
function statusIcon(s) { return statusIconMap[s] || null }
function fileIcon(s) { return fileIconMap[s] || null }
```

- [ ] **Step 4: 模板用动态组件渲染状态图标**

`:18` `<div class="tico" :class="statusClass(t.status)">{{ statusIcon(t.status) }}</div>` →
```html
<div class="tico" :class="statusClass(t.status)"><component :is="statusIcon(t.status)" :size="18" /></div>
```

`:43` `<span class="fstatus" :class="f.status">{{ fileIcon(f.status) }}</span>` →
```html
<span class="fstatus" :class="f.status"><component :is="fileIcon(f.status)" :size="14" /></span>
```

`:39` `<div v-if="t.error" class="terror">⚠ {{ t.error }}</div>` →
```html
<div v-if="t.error" class="terror"><TriangleAlert :size="14" /> {{ t.error }}</div>
```

- [ ] **Step 5: 加状态色 CSS**

`<style scoped>` 里加（`.tico` 已有 statusClass 生成 `.s-pending` 等）：
```css
.tico.st-pending { color: var(--text-3); }
.tico.st-running { color: #3b82f6; }
.tico.st-paused { color: #f59e0b; }
.tico.st-done { color: #10b981; }
.tico.st-failed { color: #ef4444; }
.tico.st-interrupted { color: #eab308; }
.fstatus { display: inline-flex; }
.fstatus.pending { color: var(--text-3); }
.fstatus.running { color: #3b82f6; }
.fstatus.done { color: #10b981; }
.fstatus.failed { color: #ef4444; }
```
（`.tico` 需确保 `color` 生效——图标用 currentColor。若现有 CSS 有 `.tico` 的 color 覆盖需移除或调整）

- [ ] **Step 6: 圆角归并**

`border-radius: 11px`（task-card）→ `var(--radius)`

- [ ] **Step 7: 构建验证 + Commit**

Run: `npm run build`
```bash
git add web/app/src/views/Tasks.vue
git commit -m "ui: Tasks 状态图标+状态色换 Lucide + 圆角 token"
```

---

### Task 7: Login/Settings/NewTask 散落圆角归并 + 构建 + 部署

**Files:**
- Modify: `src/views/Login.vue`、`src/views/Settings.vue`、`src/views/NewTask.vue`（若有散落圆角）
- Deploy: dist → `tmm.v11/web`、`tmm.v10/web`

- [ ] **Step 1: 检查并归并散落圆角**

Run:
```bash
cd "D:\Documents\workspace\telegram manager\tdl\web\app"
grep -rn "border-radius:" src/views/Login.vue src/views/Settings.vue src/views/NewTask.vue
```
规则同前：10px→var(--radius-sm)、12px→var(--radius)、14/16px→var(--radius-lg)、50%保留。逐个 Edit。

- [ ] **Step 2: 最终构建**

Run: `npm run build`
Expected: 成功

- [ ] **Step 3: 部署到 tmm.v11 和 tmm.v10**

Run:
```bash
cp -r "D:/Documents/workspace/telegram manager/tdl/web/app/dist/." "D:/Documents/workspace/telegram manager/tmm.v11/web/"
cp -r "D:/Documents/workspace/telegram manager/tdl/web/app/dist/." "D:/Documents/workspace/telegram manager/tmm.v10/web/"
```

- [ ] **Step 4: Commit**

```bash
git add web/app/src/views/Login.vue web/app/src/views/Settings.vue web/app/src/views/NewTask.vue
git commit -m "ui: Login/Settings/NewTask 圆角归并 + 部署"
```

- [ ] **Step 5: 浏览器实测**

启动 tmm.v11，硬刷新（Ctrl+Shift+R），逐页验证：
- 导航/主题：Lucide 线性图标，hover 变色正常
- 播放器：上一首/播放/暂停/下一首/关闭图标正常
- 媒体查看器：下载/关闭/警告图标正常
- 消息页：播放/下载图标正常
- 任务页：6 种状态图标 + 对应状态色（蓝/橙/绿/红/黄/灰）
- 圆角层级统一，无突兀
- 暗色模式无 emoji 发亮
