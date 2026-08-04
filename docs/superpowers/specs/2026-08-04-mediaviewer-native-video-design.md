# MediaViewer 弃用 Plyr 改原生 video 设计

> **状态**：设计已定（brainstorming 完成），待 writing-plans 出实施计划。
> **范围**：只换视频播放器。前台播放限速是后续独立任务（见 `docs/superpowers/plans/2026-08-04-playback-download-throttle.md`）。

---

## 1. 背景与目的

用户对 Plyr 长期积怨——非单点问题，是整体厌倦：DOM 包装层不可控（每次定制要查 `.plyr__volume`/`.plyr__controls` 内部结构，升级就断）、默认样式重需大量覆盖、第三方依赖与体积、外观不满意。本次**彻底移除 Plyr**，MediaViewer 视频改原生 `<video>` + 自写控件。

## 2. 成功标准

- 移除 `plyr` 依赖（`package.json` + `package-lock.json`）、所有 plyr CSS、DOM 包装层、事件层
- 视频功能不退化：基础控件 + 速度菜单 + 现有定制行为全部保留
- 外观照 ArtPlayer 风格保持（白色控件 / 竖向音量弹出 / 3px 进度条 / 底部渐变遮罩），用户无感
- 零新依赖

## 3. 架构

两个文件，单一职责边界：

### `MediaViewer.vue`（改造）— 持 video 元素，管容器与生命周期

- **保留不动**：图片分支（zoom/wheel/控件条）、顶栏/浮层按钮、导航(prev/next)、`cardStyle`/`videoWrapStyle`、`watchVideoLoading` 轮询、错误重试逻辑
- **video 元素**：原生事件监听 `@play @pause @ended @timeupdate @progress @volumechange @loadedmetadata @error @waiting @canplay`，src 由 computed 驱动（`api.mediaStreamUrl`）
- **状态 ref**：从 video 事件同步到 `vPlaying, vCurrent, vDuration, vBuffered, vVolume, vMuted, vRate`，props 下传给 VideoControls
- **`setupVideo()` 替代 `setupPlyr()`**：设 el.src、从 localStorage 恢复音量、`el.play().catch(()=>{})`（autoplay 捕获拒绝）、启动 `watchVideoLoading`
- **`resetMedia()` 简化**：切上/下一条直接 `el.src = src.value; el.play()`（原生 video 无包装层，无 ref 丢失问题）；切到图片时 video 被 v-if 卸载，无需 destroy

### `VideoControls.vue`（新建）— 纯受控控件条

- 不持有 video 元素，不碰 src/lifecycle
- 通过 props 接状态、emits 报动作
- 无 video ref、无生命周期——纯受控组件

### 数据流

```
video 原生事件 → MediaViewer 状态 ref → props 下传 → VideoControls 渲染
                                                          ↓ 用户操作
                MediaViewer 操作 video 元素 ← emits 上报 ←
```

video 元素是唯一真源，单向流。VideoControls 不直接读写 video。

**为什么 video 留 MediaViewer**：src 依赖 `item/chatId`（computed），切上/下一条的 resetMedia、加载轮询、错误重试都跟 video 生命周期紧耦合，留在 MediaViewer 最自然。VideoControls 抽出后是纯展示+输入，可独立理解与测试。

## 4. VideoControls 接口（实施契约）

**Props**：

| Prop | 类型 | 说明 |
|---|---|---|
| `playing` | Boolean | 是否播放中（控制大播放按钮/暂停图标） |
| `currentTime` | Number | 秒 |
| `duration` | Number | 秒（0 表示未就绪） |
| `buffered` | Number | 0-1 已缓冲比例 |
| `volume` | Number | 0-1 |
| `muted` | Boolean | |
| `theater` | Boolean | 网页全屏态 |
| `loading` | Boolean | 加载中（控件条仍可点，但可显示 spinner） |
| `error` | String | 错误文案（非空时控件条禁用或提示） |
| `playbackRate` | Number | 当前速度 |

**Emits**：

| Emit | 参数 | 说明 |
|---|---|---|
| `play` | — | 点播放按钮 |
| `pause` | — | 点暂停按钮 |
| `seek` | sec:Number | 拖动/点击进度条 |
| `set-volume` | v:Number(0-1) | |
| `toggle-mute` | — | |
| `toggle-theater` | — | 网页全屏 |
| `set-rate` | r:Number | 0.5/0.75/1/1.25/1.5/2 |

## 5. 功能清单

- **基础控件**：播放/暂停（含大居中按钮）、进度条（range + 缓冲条灰轨）、当前时间/总时长、音量+静音、网页全屏(theater)
- **速度菜单**：0.5/0.75/1/1.25/1.5/2，**hover 竖向弹出**（与音量风格/操作方式统一），当前态高亮
- **音量**：竖向 range hover 弹出 + 百分比数字（移植现有 `.plyr__volume` 机制）
- **保留定制行为**：
  - 音量 localStorage 跨视频持久化（`tmm-volume` key，沿用现有）
  - 切上/下一条视频不丢控件（原生 video 无包装层，比 Plyr 版更稳）
  - seek 拖动后自动恢复播放

## 6. 技术路线

- `<input type="range">` + CSS 自定义（复用 GlobalPlayer `.gp-progress` 范式：`::-webkit-slider-runnable-track` / `::-webkit-slider-thumb` / `::-moz-range-*`）
- 速度菜单：`<button>` 列表 + `:hover/:focus-within` CSS transition
- 图标：lucide-vue-next（项目已用：Play/Pause/Volume2/VolumeX/Maximize/Minimize）
- 零新依赖

**进度条/音量**：移植现有 `:deep(.plyr__progress)` / `:deep(.plyr__volume)` 规则，去掉 `:deep(.plyr...)` 前缀，DOM class 改成自己的（`.vc-progress` / `.vc-volume`）。

## 7. 外观

- 白色控件叠黑底视频（硬编码白，沿用现状，不进暗色 token 系统——视频控件叠在黑色视频上，与 GlobalPlayer 浅色页面底部条场景不同）
- 底部渐变遮罩控件条、3px 进度条 hover 5px、白色填充无圆点
- 圆角用 token `--radius-sm`
- 大居中播放按钮：半透明白毛玻璃（移植 `.plyr__control--overlaid`）

## 8. 范围外（YAGNI）

- CC 字幕（现状就关着）
- 键盘播放快捷键（MediaViewer 现有 Esc/←/→ 只管导航，不扩展到播放控制）
- 缩略图预览进度条、多段标记
- headless UI 库 / 任何新依赖
- 限速功能（后续独立任务）

## 9. 风险

- **buffered TimeRanges**：取 `end(length-1)` 算比例，多区间取最后一个端点
- **range 拖动 seek 抖动**：`@input` 实时设 currentTime 可能触发 seeking 事件循环，用 flag 防重入
- **autoplay 被拒**：`el.play()` 捕获 rejection，停首帧，用户手动点播放（与现状一致）
- **构建后需浏览器实测**：交接文档 §7.6 提到 IAB 浏览器交互通道偶有不稳，关键验收点需用户配合

## 10. 验收点

- 视频：播放/暂停/seek/调音量/静音/网页全屏/速度切换，外观与原 Plyr 皮肤一致
- 音量/速度竖向弹出风格统一
- 切上/下一条视频不丢控件、音量 localStorage 保持
- 加载 spinner / 错误状态正常
- 图片分支不受影响
- `plyr` 从 package.json 移除，构建无残留引用
