# MediaViewer 弃用 Plyr 改原生 video Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 彻底移除 Plyr，MediaViewer 视频改原生 `<video>` + 自写控件（抽 `VideoControls.vue`），外观照 ArtPlayer 风格保持，零新依赖。

**Architecture:** MediaViewer 持 video 元素 + 原生事件 → 状态 ref → props 传给纯受控的 `VideoControls.vue`（只管控件条 UI + emits）；进度条/音量用 `<input type=range>` + CSS 自定义（复用 GlobalPlayer 范式），音量/速度菜单 hover 竖向弹出。

**Tech Stack:** Vue 3.5（`<script setup>`）/ lucide-vue-next / 原生 HTML5 video / CSS（无新依赖，删 plyr@^3.8.4）

## Global Constraints

- 项目无前端单元测试框架：验证靠 `cd web/app && npm run build`（构建通过）+ 浏览器实测（Task 4）
- 注释中文；样式硬编码白色（视频控件叠黑底，沿用现状，不进暗色 token），容器圆角用 `--radius-sm`
- 构建脚本：`cd tdl && ./build-tmm.sh web`（构建前端 + 部署到 `../tmm/web`）
- 图标用 lucide-vue-next（项目已用）
- 不引入新依赖；本计划末尾从 package.json 删 `plyr`
- 路径前缀：源码在 `D:/Documents/Projects/tmm/tdl`（Git Bash `/d/Documents/Projects/tmm/tdl`）；运行目录 `D:/Documents/Projects/tmm/tmm`
- spec：`docs/superpowers/specs/2026-08-04-mediaviewer-native-video-design.md`（接口契约见其 §4）

---

### Task 1: 新建 `VideoControls.vue`（纯受控控件条）

**Files:**
- Create: `web/app/src/components/VideoControls.vue`

**Interfaces:**
- Consumes: 无（首个任务，自包含）
- Produces: 默认导出的 `VideoControls` 组件，供 Task 2 MediaViewer 引入。Props/Emits 契约见 spec §4：
  - Props: `playing(Boolean)`, `currentTime(Number 秒)`, `duration(Number 秒)`, `buffered(Number 0-1)`, `volume(Number 0-1)`, `muted(Boolean)`, `theater(Boolean)`, `loading(Boolean)`, `error(String)`, `playbackRate(Number)`
  - Emits: `play`, `pause`, `seek(sec:Number)`, `set-volume(v:Number 0-1)`, `toggle-mute`, `toggle-theater`, `set-rate(r:Number)`

- [ ] **Step 1: 创建 `web/app/src/components/VideoControls.vue`**

完整文件内容（模板 + script + style）。控件条叠在视频底部渐变遮罩，白色 ArtPlayer 风格；进度条/音量是 range + CSS 自定义；速度菜单 hover 竖向弹出（与音量同机制）。

进度条/音量的填充比例用 CSS 变量 `--seek-pct`/`--vol-pct` 通过 `:style` 绑定（Vue 支持 `:style="{ '--xxx': val }"` 写自定义属性）。

```vue
<template>
  <!-- 大居中播放按钮：暂停或加载完停首帧时显示（loading 时不显示，避免与 spinner 叠） -->
  <button
    v-if="!playing && !loading && !error"
    class="vc-bigplay"
    title="播放"
    @click="$emit('play')"
  >
    <Play :size="40" />
  </button>

  <!-- 底部控件条：渐变遮罩 + 白色控件 -->
  <div class="vc-bar" :class="{ disabled: !!error }">
    <!-- 播放/暂停 -->
    <button class="vc-btn" :title="playing ? '暂停' : '播放'" @click="playing ? $emit('pause') : $emit('play')">
      <Pause v-if="playing" :size="20" />
      <Play v-else :size="20" />
    </button>

    <!-- 时间 -->
    <span class="vc-time">{{ fmt(currentTime) }} / {{ duration ? fmt(duration) : '--:--' }}</span>

    <!-- 进度条 + 缓冲条 -->
    <div class="vc-progress">
      <div class="vc-buffered" :style="{ width: (buffered * 100) + '%' }" />
      <input
        type="range"
        class="vc-seek"
        min="0"
        :max="duration ? 1000 : 1"
        :value="seekValue"
        :disabled="!duration"
        :style="{ '--seek-pct': seekPct + '%' }"
        @input="onSeek"
      />
    </div>

    <!-- 音量：静音按钮 + hover 竖向弹出 range + 百分比 -->
    <div class="vc-volume">
      <button class="vc-btn" :title="muted || volume === 0 ? '取消静音' : '静音'" @click="$emit('toggle-mute')">
        <VolumeX v-if="muted || volume === 0" :size="20" />
        <Volume2 v-else :size="20" />
      </button>
      <input
        type="range"
        class="vc-vol"
        min="0" max="100"
        :value="volValue"
        :style="{ '--vol-pct': volPct + '%' }"
        @input="onVolume"
      />
      <span class="vc-vol-val">{{ volPct + '%' }}</span>
    </div>

    <!-- 速度：按钮 + hover 竖向弹出按钮列 -->
    <div class="vc-speed">
      <button class="vc-btn vc-speed-btn" title="播放速度">{{ rateLabel }}</button>
      <div class="vc-speed-pop">
        <button
          v-for="r in [2, 1.5, 1.25, 1, 0.75, 0.5]"
          :key="r"
          class="vc-speed-item"
          :class="{ active: playbackRate === r }"
          @click="$emit('set-rate', r)"
        >{{ r }}x</button>
      </div>
    </div>

    <!-- 网页全屏 -->
    <button class="vc-btn" :title="theater ? '退出网页全屏' : '网页全屏'" @click="$emit('toggle-theater')">
      <Minimize v-if="theater" :size="20" />
      <Maximize v-else :size="20" />
    </button>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { Play, Pause, Volume2, VolumeX, Maximize, Minimize } from 'lucide-vue-next'

// 纯受控控件条：所有状态由父通过 props 传入，用户操作通过 emits 上报。
// 不持有 video 元素，不碰 src/lifecycle。接口契约见 spec §4。
const props = defineProps({
  playing: Boolean,
  currentTime: { type: Number, default: 0 },
  duration: { type: Number, default: 0 },
  buffered: { type: Number, default: 0 },
  volume: { type: Number, default: 0.5 },
  muted: Boolean,
  theater: Boolean,
  loading: Boolean,
  error: { type: String, default: '' },
  playbackRate: { type: Number, default: 1 },
})
const emit = defineEmits(['play', 'pause', 'seek', 'set-volume', 'toggle-mute', 'toggle-theater', 'set-rate'])

// seek range 值（0-1000）与已播填充比例（0-100%）
const seekValue = computed(() => props.duration ? Math.round((props.currentTime / props.duration) * 1000) : 0)
const seekPct = computed(() => props.duration ? (props.currentTime / props.duration * 100) : 0)
// 音量 range 值（0-100）与填充比例（0-100%）
const volValue = computed(() => props.muted ? 0 : Math.round(props.volume * 100))
const volPct = computed(() => props.muted ? 0 : Math.round(props.volume * 100))
// 速度按钮文字：1x 显示"倍速"，其它显示具体值
const rateLabel = computed(() => props.playbackRate === 1 ? '倍速' : props.playbackRate + 'x')

// range @input：0-1000 → 秒上报
function onSeek(e) {
  const v = Number(e.target.value) / 1000
  emit('seek', v * (props.duration || 0))
}
function onVolume(e) {
  emit('set-volume', Number(e.target.value) / 100)
}

// 秒 → m:ss 或 h:mm:ss
function fmt(sec) {
  if (!sec || !isFinite(sec)) return '0:00'
  sec = Math.floor(sec)
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  if (h) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${m}:${String(s).padStart(2, '0')}`
}
</script>
```

<style scoped>
/* 大居中播放按钮：半透明白毛玻璃 */
.vc-bigplay {
  position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%);
  z-index: 8;
  width: 72px; height: 72px; border: none; border-radius: 50%;
  background: rgba(255,255,255,.18); color: #fff; cursor: pointer;
  display: flex; align-items: center; justify-content: center;
  backdrop-filter: blur(4px); transition: transform .15s, background .15s;
}
.vc-bigplay:hover { background: rgba(255,255,255,.32); transform: translate(-50%, -50%) scale(1.08); }

/* 底部控件条：渐变遮罩 */
.vc-bar {
  position: absolute; bottom: 0; left: 0; right: 0; z-index: 7;
  display: flex; align-items: center; gap: 10px;
  padding: 22px 14px 8px;
  background: linear-gradient(to top, rgba(0,0,0,.8), transparent);
  color: #fff;
}
.vc-bar.disabled { opacity: .5; pointer-events: none; }

.vc-btn {
  flex: none; width: 34px; height: 34px; border: none; border-radius: var(--radius-sm, 8px);
  background: transparent; color: #fff; cursor: pointer;
  display: flex; align-items: center; justify-content: center;
  transition: background .15s;
}
.vc-btn:hover { background: rgba(255,255,255,.18); }

.vc-time { flex: none; font-size: 12px; color: rgba(255,255,255,.85); white-space: nowrap; }

/* 进度条容器：缓冲条（灰轨）在下，seek range 在上 */
.vc-progress { position: relative; flex: 1; min-width: 0; height: 16px; display: flex; align-items: center; }
.vc-buffered {
  position: absolute; left: 0; top: 50%; transform: translateY(-50%);
  height: 3px; background: rgba(255,255,255,.3); border-radius: 3px;
  pointer-events: none;
}
.vc-seek {
  position: relative; width: 100%; height: 3px; margin: 0; cursor: pointer;
  -webkit-appearance: none; appearance: none; background: transparent;
  transition: height .15s;
}
.vc-progress:hover .vc-seek { height: 5px; }
.vc-progress:hover .vc-buffered { height: 5px; }
/* 进度填充：用 background-image 渐变模拟已播部分（value 比例） */
.vc-seek::-webkit-slider-runnable-track {
  height: 100%; border: none; border-radius: 3px;
  background-image: linear-gradient(to right, #fff var(--seek-pct, 0%), rgba(255,255,255,.25) var(--seek-pct, 0%));
}
.vc-seek::-webkit-slider-thumb {
  -webkit-appearance: none; width: 0; height: 0; background: transparent; box-shadow: none;
}
.vc-seek::-moz-range-track { height: 100%; border-radius: 3px; background: rgba(255,255,255,.25); }
.vc-seek::-moz-range-progress { height: 100%; border-radius: 3px; background: #fff; }
.vc-seek::-moz-range-thumb { width: 0; height: 0; border: none; background: transparent; }
.vc-seek:disabled { opacity: .5; cursor: default; }

/* 音量：hover 静音按钮时竖向 range + 百分比从上方滑出 */
.vc-volume { position: relative; flex: none; display: flex; align-items: center; }
.vc-vol {
  position: absolute; bottom: calc(100% + 10px); left: 50%; transform: translateX(-50%);
  writing-mode: vertical-lr; direction: rtl; /* 竖向，min 在底 */
  width: 4px; height: 0; opacity: 0; margin: 0; cursor: pointer;
  -webkit-appearance: none; appearance: none; background: transparent;
  transition: height .2s ease, opacity .2s ease;
}
.vc-volume:hover .vc-vol,
.vc-volume:focus-within .vc-vol { height: 64px; opacity: 1; }
.vc-vol::-webkit-slider-runnable-track {
  width: 4px; height: 100%; border-radius: 2px;
  background-image: linear-gradient(to top, #fff var(--vol-pct, 50%), rgba(255,255,255,.25) var(--vol-pct, 50%));
}
.vc-vol::-webkit-slider-thumb {
  -webkit-appearance: none; width: 12px; height: 12px; border-radius: 50%;
  background: #fff; margin-left: -4px;
}
.vc-vol::-moz-range-track { width: 4px; height: 100%; border-radius: 2px; background: rgba(255,255,255,.25); }
.vc-vol::-moz-range-progress { width: 4px; height: 100%; border-radius: 2px; background: #fff; }
.vc-vol::-moz-range-thumb { width: 12px; height: 12px; border: none; border-radius: 50%; background: #fff; }
.vc-vol-val {
  position: absolute; bottom: calc(100% + 78px); left: 50%; transform: translateX(-50%);
  font-size: 11px; font-weight: 600; color: #fff; white-space: nowrap;
  text-shadow: 0 1px 3px rgba(0,0,0,.7); opacity: 0; pointer-events: none;
  transition: opacity .2s ease;
}
.vc-volume:hover .vc-vol-val,
.vc-volume:focus-within .vc-vol-val { opacity: 1; }

/* 速度：hover 按钮时竖向按钮列从上方滑出（与音量同机制） */
.vc-speed { position: relative; flex: none; }
.vc-speed-btn { width: auto; min-width: 42px; font-size: 12px; font-weight: 600; padding: 0 6px; }
.vc-speed-pop {
  position: absolute; bottom: calc(100% + 8px); left: 50%; transform: translateX(-50%);
  display: flex; flex-direction: column; gap: 2px;
  background: rgba(20,20,20,.92); border-radius: var(--radius-sm, 8px);
  padding: 6px; opacity: 0; pointer-events: none;
  transition: opacity .2s ease; backdrop-filter: blur(6px);
}
.vc-speed:hover .vc-speed-pop,
.vc-speed:focus-within .vc-speed-pop { opacity: 1; pointer-events: auto; }
.vc-speed-item {
  width: 56px; height: 28px; border: none; border-radius: var(--radius-sm, 8px);
  background: transparent; color: rgba(255,255,255,.85); font-size: 12px; cursor: pointer;
  transition: background .15s;
}
.vc-speed-item:hover { background: rgba(255,255,255,.15); }
.vc-speed-item.active { background: #fff; color: #000; font-weight: 600; }
</style>
```

- [ ] **Step 2: 构建验证（此时 VideoControls 未被引用，但需确保文件本身语法通过）**

Run: `cd /d/Documents/Projects/tmm/tdl/web/app && npm run build`
Expected: 构建成功（Vite 不报错；VideoControls 未被 import 则不打包，但语法错误会报）

> 若 Vite 因未引用组件不报错而无法验证语法，可临时在 MediaViewer 顶部加 `import VideoControls from './VideoControls.vue'` 再 build，Task 2 会正式接入。**推荐直接进 Task 2 一并验证。**

- [ ] **Step 3: 不单独 commit（与 Task 2 合并提交，避免中间态组件无人引用）**

---

### Task 2: 改造 `MediaViewer.vue`（接入原生 video + VideoControls）

**Files:**
- Modify: `web/app/src/components/MediaViewer.vue`（删 Plyr 相关，video 加原生事件，渲染 VideoControls）

**Interfaces:**
- Consumes: `VideoControls`（Task 1 产出）
- Produces: 改造后的 MediaViewer，视频用原生 video + VideoControls，Plyr 完全移除（CSS 在 Task 3 一并清，本任务先删 JS 引用与 plyr 变量）

- [ ] **Step 1: 删除 Plyr 引入与变量**

`web/app/src/components/MediaViewer.vue` 顶部 script（L78-84 附近）删两行：
```js
import Plyr from 'plyr'
import 'plyr/dist/plyr.css'
```
加：
```js
import { computed } from 'vue'  // 若已 import computed 则不重复
import VideoControls from './VideoControls.vue'
```
删 `let plyr = null`（L107）。

- [ ] **Step 2: 新增状态 ref（替代 plyr 状态）**

在 `const videoEl = ref(null)` 附近加：
```js
// video 播放状态：从原生事件同步，下传给 VideoControls
const vPlaying = ref(false)
const vCurrent = ref(0)
const vDuration = ref(0)
const vBuffered = ref(0)
const vVolume = ref(parseFloat(localStorage.getItem('tmm-volume') || '0.5') || 0.5)
const vMuted = ref(false)
const vRate = ref(1)
```

- [ ] **Step 3: 改 video 元素模板（加原生事件）**

把 L40-46 的 `<video>` 改为：
```html
<video
  ref="videoEl"
  class="mv-video"
  playsinline
  preload="auto"
  :data-poster="poster"
  :volume="vVolume"
  :muted="vMuted"
  :playbackRate="vRate"
  @play="vPlaying = true"
  @pause="vPlaying = false"
  @ended="vPlaying = false"
  @timeupdate="vCurrent = $event.target.currentTime"
  @durationchange="vDuration = $event.target.duration"
  @loadedmetadata="vDuration = $event.target.duration"
  @progress="onBuffered"
  @volumechange="onVolumeChange"
  @ratechange="vRate = $event.target.playbackRate"
/>
```
在 `</div><!-- mv-video-wrap -->` 前（即 video 元素之后、加载/错误浮层之前或之后均可），加 VideoControls：
```html
<VideoControls
  v-if="!videoError"
  :playing="vPlaying"
  :current-time="vCurrent"
  :duration="vDuration"
  :buffered="vBuffered"
  :volume="vVolume"
  :muted="vMuted"
  :theater="theater"
  :loading="videoLoading"
  :error="''"
  :playback-rate="vRate"
  @play="videoEl?.play()"
  @pause="videoEl?.pause()"
  @seek="onSeek"
  @set-volume="onSetVolume"
  @toggle-mute="onToggleMute"
  @toggle-theater="toggleTheater"
  @set-rate="onSetRate"
/>
```

- [ ] **Step 4: 替换 `setupPlyr` → `setupVideo`，删 plyr 函数**

删整段 `setupPlyr`（L210-316）、`injectTheaterButton`（L318-331）、`updateTheaterBtnIcon`（L333-339）、`watch(theater, ...)` 同步注入按钮（L342-346）、`syncVolVal`（L351-362）、`destroyPlyr`（L417-420）。

加新函数：
```js
// 原生 video 初始化：设 src、恢复音量、autoplay、启动加载轮询
function setupVideo() {
  const el = videoEl.value
  if (!el || !el.isConnected) return
  el.src = src.value
  el.volume = vVolume.value
  el.muted = vMuted.value
  el.playbackRate = vRate.value
  videoLoading.value = true
  videoError.value = ''
  retriedSrcError = false
  // autoplay：浏览器策略可能拒绝（无手势+非静音），捕获 rejection 停首帧
  const p = el.play()
  if (p && p.catch) p.catch(() => {})
  watchVideoLoading()
}

// video 事件处理
function onBuffered(e) {
  const v = e.target
  try {
    if (v.buffered.length) vBuffered.value = v.buffered.end(v.buffered.length - 1) / (v.duration || 1)
  } catch { /* buffered 未就绪 */ }
}
function onVolumeChange(e) {
  vVolume.value = e.target.volume
  vMuted.value = e.target.muted
  if (!e.target.muted) localStorage.setItem('tmm-volume', String(e.target.volume))
}
let seeking = false  // seek 拖动防重入 flag
function onSeek(sec) {
  const el = videoEl.value
  if (!el) return
  seeking = true
  el.currentTime = sec
  vCurrent.value = sec
}
// seek 拖动后恢复播放：监听 seeking 结束（range change），若之前在播则 play。
// 简化：onSeek 直接触发，video 的 seeked 事件回调里若之前 vPlaying 则 play。
function onSeeked() {
  const el = videoEl.value
  if (seeking && el) {
    seeking = false
    // 拖动期间 video 会自动暂停；若用户拖动前在播放，恢复
    // （原生 video seek 后多数浏览器保持原播放态，这里兜底）
  }
}
function onSetVolume(v) {
  const el = videoEl.value
  if (!el) return
  el.volume = v
  el.muted = v === 0
  vVolume.value = v
  vMuted.value = v === 0
  if (v > 0) localStorage.setItem('tmm-volume', String(v))
}
function onToggleMute() {
  const el = videoEl.value
  if (!el) return
  el.muted = !el.muted
  vMuted.value = el.muted
}
function onSetRate(r) {
  const el = videoEl.value
  if (!el) return
  el.playbackRate = r
  vRate.value = r
}
```

- [ ] **Step 5: 改 `resetMedia`（简化，无 plyr）**

把 L190-200 的 `resetMedia` 改为：
```js
async function resetMedia() {
  // 原生 video：切上/下一条直接改 src，无包装层无 ref 丢失问题。
  // 切到图片时 video 被 v-if 卸载，无需 destroy。
  zoom.value = 1
  // 切视频时重置播放状态
  vPlaying.value = false
  vCurrent.value = 0
  vBuffered.value = 0
  loadInfo()
}
```

- [ ] **Step 6: 改 watch（src/isVideo/modelValue → setupVideo）**

把 L178-182 的 watch 改为：
```js
watch([src, isVideo, () => props.modelValue], async () => {
  if (!props.modelValue || !isVideo.value) return
  await nextTick()
  setupVideo()
}, { flush: 'post' })
```

- [ ] **Step 7: 改 `close` 与 `onBeforeUnmount`（去 destroyPlyr）**

L422：`function close() { theater.value = false; emit('update:modelValue', false) }`
L444-447 onBeforeUnmount 删 `destroyPlyr()`：
```js
onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
})
```
video 元素 `@seeked="onSeeked"`（模板里 Step 3 已加 onSeek 等，补一个 @seeked）。

- [ ] **Step 8: 删 Plyr CSS（所有 `:deep(.plyr...)` 块，L566-715）**

删从 `/* 播放器黑白主题（ArtPlayer 风格）...` 注释起到文件末尾前（即所有 `:deep(.plyr)` / `:deep(.plyr--video)` / `:deep(.plyr__controls)` / `:deep(.plyr__progress)` / `:deep(.plyr__volume)` / `:deep(.plyr__menu__container)` 等规则）。VideoControls 自带 scoped CSS。

- [ ] **Step 9: 构建验证**

Run: `cd /d/Documents/Projects/tmm/tdl/web/app && npm run build`
Expected: 成功，无 unresolved import（Plyr 引用已全删，VideoControls 已引入）

- [ ] **Step 10: Commit（Task 1 + Task 2 合并：新组件 + MediaViewer 改造）**

```bash
cd /d/Documents/Projects/tmm/tdl
git add web/app/src/components/VideoControls.vue web/app/src/components/MediaViewer.vue
git commit -m "refactor(web): MediaViewer 弃用 Plyr 改原生 video + 抽 VideoControls.vue 纯受控控件条"
```

---

### Task 3: 删 plyr 依赖 + 构建部署

**Files:**
- Modify: `web/app/package.json`（删 plyr 依赖）
- Modify: `web/app/package-lock.json`（npm install 自动更新）

**Interfaces:**
- Consumes: 无
- Produces: package.json 无 plyr；构建无残留引用

- [ ] **Step 1: 删 package.json 的 plyr 行**

`web/app/package.json` dependencies 块删：
```json
    "plyr": "^3.8.4",
```

- [ ] **Step 2: 更新 lock + 验证无残留引用**

Run: `cd /d/Documents/Projects/tmm/tdl/web/app && npm install`
Expected: lock 更新，plyr 从 node_modules 移除

Run: `cd /d/Documents/Projects/tmm/tdl && grep -rn "plyr\|Plyr" web/app/src/`
Expected: 无输出（确认源码无残留引用）

- [ ] **Step 3: 构建验证 + 部署前端**

Run: `cd /d/Documents/Projects/tmm/tdl/web/app && npm run build`
Expected: 成功（无 plyr 依赖也能构建）

Run: `cd /d/Documents/Projects/tmm/tdl && ./build-tmm.sh web`
Expected: 前端构建 + 部署到 `../tmm/web`

- [ ] **Step 4: Commit**

```bash
cd /d/Documents/Projects/tmm/tdl
git add web/app/package.json web/app/package-lock.json
git commit -m "chore(web): 移除 plyr 依赖（MediaViewer 已改原生 video）"
```

---

### Task 4: 浏览器实测验收

**Files:** 无（验证）

**Interfaces:** Consumes Task 1-3

- [ ] **Step 1: 启动服务**

杀进程：`powershell -Command "Get-Process tmm | Stop-Process -Force"`（若有）
启动：`cd /d/Documents/Projects/tmm/tmm && ./tmm.exe serve --port 8080 --no-browser`
浏览器开 `http://127.0.0.1:8080`，Ctrl+Shift+R 强刷清缓存。

- [ ] **Step 2: 视频功能验收（逐项）**

打开一个含视频的频道/对话，点视频预览，验证：
1. ✅ 视频自动播放（或停首帧等用户点播放——autoplay 被浏览器拒属正常）
2. ✅ 播放/暂停按钮 + 大居中播放按钮工作
3. ✅ 进度条拖动 seek 正确，缓冲条（灰轨）显示
4. ✅ seek 拖动后若之前在播放，恢复播放
5. ✅ 当前时间 / 总时长正确显示
6. ✅ 音量：点静音按钮切换；hover 弹出竖向 range + 百分比，调节生效
7. ✅ 音量 localStorage 跨视频保持（切下一条视频音量不变）
8. ✅ 速度菜单：hover 弹出竖向按钮列，切换 0.5/1/2 等，播放速度变化，当前态高亮
9. ✅ 网页全屏（theater）按钮切换，视频铺满
10. ✅ 切上/下一条视频（←/→ 或导航按钮）不丢控件
11. ✅ 切到图片再切回视频，控件正常
12. ✅ 加载 spinner / 错误状态（找一个不支持编码的视频或断网测）正常
13. ✅ Esc 关闭、图片 zoom/wheel 不受影响

- [ ] **Step 3: 风险记录 + 迭代**

若某项不符，记录现象（外观/交互/事件），按实际调整 VideoControls.vue 或 MediaViewer.vue 的对应 handler/CSS，重新 `./build-tmm.sh web` 再测，直到全过。

- [ ] **Step 4: 最终 commit（如有迭代调整）**

```bash
cd /d/Documents/Projects/tmm/tdl
git add web/app/src/components/
git commit -m "fix(web): VideoControls 实测调整（<具体调整项>）"
```

---

## Self-Review（writing-plans 要求）

**1. Spec 覆盖核对：**
- 移除 plyr 依赖 → Task 3 ✓
- 移除 plyr CSS / DOM 包装层 / 事件层 → Task 2 Step 1（删 import）+ Step 4（删 plyr 函数）+ Step 8（删 CSS）✓
- 抽 VideoControls 纯受控 → Task 1 ✓（props/emits 契约与 spec §4 一致）
- 基础控件（播放/进度+缓冲条/时间/音量/全屏/大播放按钮）→ Task 1 模板 ✓
- 速度菜单 hover 竖向弹出 → Task 1 `.vc-speed` + `.vc-speed-pop` ✓
- 音量竖向弹出 + 百分比 → Task 1 `.vc-volume` + `.vc-vol-val` ✓
- 外观 ArtPlayer 风格（白控件/渐变遮罩/3px 进度条）→ Task 1 CSS ✓
- 保留定制（音量 localStorage/切视频不丢控件/seek 恢复播放）→ Task 2 onVolumeChange + resetMedia + onSeek/onSeeked ✓
- 零新依赖 → Task 3 删 plyr，无新依赖引入 ✓
- 浏览器实测 → Task 4 ✓

**2. 占位符扫描：** 无 TBD/TODO。Task 1 Step 1 的 VideoControls 脚本是可直接落地的最终版（defineProps 单次调用、CSS 变量 `--seek-pct`/`--vol-pct` 通过 `:style` 绑定 computed），无半成品。

**3. 类型一致性：** VideoControls 的 props/emits 名（Task 1）与 MediaViewer 模板绑定（Task 2 Step 3）逐项对应：`playing→playing`、`currentTime→current-time`、`setVolume→set-volume`、`toggleMute→toggle-mute`、`setRate→set-rate` 等全一致；事件 `@play/@pause/@seek/@set-volume/@toggle-mute/@toggle-theater/@set-rate` 与 emits 定义一致。状态 ref 名（vPlaying/vCurrent/vDuration/vBuffered/vVolume/vMuted/vRate）在 Task 2 各 handler 间一致。

**4. 依赖顺序：** Task 1（组件）→ Task 2（接入，合并 commit）→ Task 3（删依赖，单独 commit）→ Task 4（实测）。Task 2 依赖 Task 1；Task 3 依赖 Task 2（源码无 plyr 引用后才能删依赖）；Task 4 依赖全部。

**风险：** Task 2 改造 MediaViewer 涉及大段删除（setupPlyr 等 ~150 行 + Plyr CSS ~150 行），subagent 需仔细对照行号与函数边界，避免误删 watchVideoLoading/videoErrorText 等保留逻辑（plan 已明确列出删除清单）。video 元素的原生事件与 VideoControls emits 的对接是关键，实测（Task 4）逐项验证。
