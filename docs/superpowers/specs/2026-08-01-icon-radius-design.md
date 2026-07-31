# 设计：emoji→矢量图标 + 圆角 token 规整

日期：2026-08-01
范围：`tdl/web/app` 前端纯视觉层，不动业务逻辑/API/Plyr 配置

## 动机
当前 UI 大量用 emoji 当图标（导航/主题/播放/关闭/下载/任务状态），渲染不一致、无法控制粗细颜色、暗色下发亮突兀，拉低现代感。圆角值散落 15+ 种硬编码（2~20px），缺乏层级统一。

## 方案

### 1. 图标库：lucide-vue-next
- 新增依赖 `lucide-vue-next`，Tree-shakeable 按需引入，只打包用到的图标
- 统一描边 2px（Lucide 默认）、颜色用 `currentColor` 继承父元素、size 按场景 16/18/20

### 2. 图标映射（共 ~15 个图标覆盖全部 ~20 处 emoji）

| 文件 | 当前 emoji | Lucide 图标 | 位置说明 |
|---|---|---|---|
| App.vue | 🌙/☀️ | `Moon`/`Sun` | 主题切换按钮 |
| GlobalPlayer.vue | ⏮/⏸/▶/⏭/✕ | `SkipBack`/`Pause`/`Play`/`SkipForward`/`X` | 播放器控制 |
| MediaViewer.vue | ⬇/✕/⚠ | `Download`/`X`/`TriangleAlert` | 下载/关闭/警告 |
| Messages.vue | ▶/⏸/⬇ | `Play`/`Pause`/`Download` | 单元格播放标、整组下载、消息下载、播放控件 |
| Tasks.vue | ⏳/⬇/⏸/✓/✕/⚠ | `Clock`/`Download`/`Pause`/`Check`/`X`/`TriangleAlert` | 任务状态（含子任务）+ 错误提示 |

### 3. 任务状态图标 + 状态色（Tasks.vue）
状态映射（替换 `statusIcon`/`subStatusIcon` 的 emoji 字符为图标组件 + 颜色 class）：

| 状态 | 图标 | 颜色 class | 颜色值 |
|---|---|---|---|
| pending | `Clock` | `.st-pending` | `var(--text-3)` 灰 |
| running | `Download` | `.st-running` | `#3b82f6` 蓝 |
| paused | `Pause` | `.st-paused` | `#f59e0b` 橙 |
| done | `Check` | `.st-done` | `#10b981` 绿 |
| failed | `X` | `.st-failed` | `#ef4444` 红 |
| interrupted | `TriangleAlert` | `.st-interrupted` | `#eab308` 黄 |

状态色作为 CSS class 加在图标容器上，图标用 `currentColor` 取色。

### 4. 圆角 token 三档（main.css）
```css
--radius-sm: 8px;   /* 按钮、输入框、小标签 */
--radius:    12px;  /* 卡片、气泡、文件芯片、任务卡 */
--radius-lg: 16px;  /* 大面板、模态、播放器条 */
```
现有 `--radius: 14px` / `--radius-sm: 10px` 调整为上述值，新增 `--radius-lg`。

### 5. 圆角归并映射（散值 → token）

| 现值 | 出现处 | 归并到 |
|---|---|---|
| 2px | MediaViewer 进度条/缓冲条 | 保留（功能性极小圆角） |
| 3px | MediaViewer 进度条 handle | 保留（功能性） |
| 6px | MediaViewer 下载按钮 hover | `var(--radius-sm)` 8px |
| 8px | main.css scrollbar | 保留（scrollbar 独立） |
| 9px | Messages 视频单元 | `var(--radius-sm)` |
| 10px | App/Messages/MediaViewer 输入框/标签/按钮 | `var(--radius-sm)` |
| 11px | Messages 文件芯片/Tasks 卡片 | `var(--radius)` 12px |
| 12px | Messages 相册/气泡 | `var(--radius)` |
| 14px | MediaViewer 容器 + `0 0 14px 14px` | `var(--radius-lg)` |
| 15px | MediaViewer 顶部按钮 | `var(--radius-lg)` |
| 16px | GlobalPlayer/Messages 气泡 | `var(--radius-lg)` |
| 17px | MediaViewer 播放按钮 | `var(--radius-lg)` |
| 20px | App 头像容器/Messages 下载按钮（药丸状） | 保留（药丸状特例） |
| 50% | 头像/圆形按钮 | 保留 |

药丸状按钮（20px pill）属特殊形态，不强归并。

## 改动文件清单
1. `package.json` — 加 `lucide-vue-next`
2. `src/styles/main.css` — 圆角 token 调整 + scrollbar 保留
3. `src/App.vue` — Moon/Sun + 圆角
4. `src/components/GlobalPlayer.vue` — SkipBack/Pause/Play/SkipForward/X + 圆角
5. `src/components/MediaViewer.vue` — Download/X/TriangleAlert + 圆角归并
6. `src/views/Messages.vue` — Play/Pause/Download + 圆角归并
7. `src/views/Tasks.vue` — 6 状态图标+色 + 圆角归并
8. `src/views/Login.vue` / `Settings.vue` / `NewTask.vue` — 散落圆角归并（若有）

## 验证
- `npm run build` 通过
- 浏览器硬刷新（Ctrl+Shift+R）逐页检查：
  - 导航/主题图标线性、hover 变色
  - 播放器控制图标正常
  - 任务状态图标 + 颜色区分
  - 圆角层级统一，无明显突兀
  - 暗色模式无 emoji 发亮
- 不影响功能：播放/下载/任务状态语义不变
