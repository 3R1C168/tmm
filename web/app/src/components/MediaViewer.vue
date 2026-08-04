<template>
  <teleport to="body">
    <div v-if="modelValue" class="mv-overlay" :class="{ theater }" @keydown="onKey" tabindex="-1" ref="overlayEl" @click.self="close">
      <!-- 居中卡片：按视频尺寸自适应，黑色只是衬底不是全屏遮罩；影院模式铺满视口 -->
      <div class="mv-card" :style="cardStyle" @click.stop>
        <!-- 顶部信息栏：普通模式在视频外（实色黑头占布局），影院模式叠在视频内顶部（渐变） -->
        <div class="mv-top" :class="{ 'mv-top--theater': theater }">
          <div class="mv-title">
            <div class="mv-name" :title="info?.name || item?.media?.name">{{ info?.name || item?.media?.name || '媒体' }}</div>
            <div class="mv-sub">
              <span v-if="dims">{{ dims }}</span>
              <span v-if="info?.size"> · {{ fmtBytes(info.size) }}</span>
              <span class="mv-count"> · {{ index + 1 }} / {{ list.length }}</span>
            </div>
          </div>
        </div>

        <!-- 影院模式浮层按钮：右上角悬浮，hover 显现，不占布局。 -->
        <div v-if="theater" class="mv-float-actions">
          <button class="mv-icon-btn" @click="download" title="下载此媒体"><Download :size="16" /></button>
          <button class="mv-icon-btn" @click="close" title="关闭 (Esc)"><X :size="20" /></button>
        </div>

        <!-- 媒体区 -->
        <div class="mv-stage" ref="stageEl" @wheel.prevent="onWheel">
          <img
            v-if="!isVideo"
            :key="item.id"
            :src="src"
            class="mv-img"
            :style="{ transform: `scale(${zoom})` }"
            draggable="false"
            @load="onImgLoad"
          />
          <div v-else class="mv-video-wrap" :style="videoWrapStyle">
            <video
              ref="videoEl"
              class="mv-video"
              playsinline
              preload="auto"
              :poster="poster"
              @play="onVideoPlay"
              @pause="onVideoPause"
              @ended="onVideoPause"
              @timeupdate="vCurrent = $event.target.currentTime"
              @durationchange="vDuration = $event.target.duration"
              @loadedmetadata="vDuration = $event.target.duration"
              @progress="onBuffered"
              @volumechange="onVolumeChange"
            />
            <!-- 加载 / 错误提示：流式起播需要拉取首块 + 尾部 moov，给可读反馈 -->
            <div v-if="videoLoading" class="mv-vstatus">
              <div class="mv-spinner" />
              <span>正在加载视频…</span>
            </div>
            <div v-else-if="videoError" class="mv-vstatus error">
              <span><TriangleAlert :size="16" /> 视频无法播放</span>
              <span class="mv-vstatus-sub">{{ videoError }}</span>
              <button class="mv-dl" @click="download"><Download :size="16" /> 下载后本地播放</button>
            </div>
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
              @play="videoEl?.play()"
              @pause="videoEl?.pause()"
              @seek="onSeek"
              @set-volume="onSetVolume"
              @toggle-mute="onToggleMute"
              @toggle-theater="toggleTheater"
            />
          </div>

          <!-- 图片控制条：浮在图片底部，hover 显隐 -->
          <div v-if="!isVideo" class="mv-controls">
            <button class="mv-cbtn" @click="zoomStep(-0.2)" title="缩小"><ZoomOut :size="18" /></button>
            <span class="mv-zoom">{{ Math.round(zoom * 100) }}%</span>
            <button class="mv-cbtn" @click="zoomStep(0.2)" title="放大"><ZoomIn :size="18" /></button>
            <button class="mv-cbtn" @click="zoom = 1" title="重置"><RotateCcw :size="16" /></button>
            <div class="mv-flex" />
          </div>
        </div>

        <!-- 左侧导航：贴卡片外侧，上一条 -->
        <button v-if="index > 0" class="mv-nav left" @click="prev" title="上一个 (←)"><ChevronLeft :size="26" stroke-width="3" /></button>
        <!-- 右上角：关闭 + 下载，贴卡片外侧右上 -->
        <div class="mv-nav-col top-right">
          <button class="mv-nav" title="关闭 (Esc)" @click="close"><X :size="22" /></button>
          <button class="mv-nav" title="下载此媒体" @click="download"><Download :size="20" /></button>
        </div>
        <!-- 右侧导航：下一条，贴卡片外侧垂直居中 -->
        <button v-if="index < list.length - 1" class="mv-nav right" @click="next" title="下一个 (→)"><ChevronRight :size="26" stroke-width="3" /></button>
      </div>
    </div>
  </teleport>
</template>

<script setup>
import { ref, computed, watch, nextTick, onBeforeUnmount, onMounted } from 'vue'
import { api } from '../api'
import VideoControls from './VideoControls.vue'
import { fmtBytes } from '../utils/format'
import { isVideo as isVideoMedia } from '../utils/media'
import { reportPlayback } from '../utils/playback'
import { Download, X, TriangleAlert, ChevronLeft, ChevronRight, ZoomOut, ZoomIn, RotateCcw } from 'lucide-vue-next'

const props = defineProps({
  modelValue: Boolean,
  list: { type: Array, default: () => [] },
  startIndex: { type: Number, default: 0 },
  chatId: { type: [String, Number], required: true },
})
const emit = defineEmits(['update:modelValue', 'download'])

const index = ref(props.startIndex)
const zoom = ref(1)
const info = ref(null)
const naturalW = ref(0)
const naturalH = ref(0)
const overlayEl = ref(null)
const stageEl = ref(null)
const videoEl = ref(null)
const videoLoading = ref(false)
const videoError = ref('')
const theater = ref(false)
const viewportW = ref(window.innerWidth)
const viewportH = ref(window.innerHeight)

// video 播放状态：从原生事件同步，下传给 VideoControls
const vPlaying = ref(false)
const vCurrent = ref(0)
const vDuration = ref(0)
const vBuffered = ref(0)
const vVolume = ref(parseFloat(localStorage.getItem('tmm-volume') || '0.5') || 0.5)
const vMuted = ref(false)

const item = computed(() => props.list[index.value] || null)
const isVideo = computed(() => isVideoMedia(item.value?.media))
const src = computed(() => {
  if (!item.value) return ''
  // 图片用 stream：边下边显示（ServeRange 首块即返回），比 file（等完整下载到磁盘
  // 再 serve）快 8 倍。后端对 image/* 不做 512KB 钳制，大图也能一次返回完整数据。
  return api.mediaStreamUrl(props.chatId, item.value.id)
})
const poster = computed(() => item.value ? api.mediaThumbUrl(props.chatId, item.value.id) : '')

const dims = computed(() => {
  const w = info.value?.width || naturalW.value
  const h = info.value?.height || naturalH.value
  return w && h ? `${w}×${h}` : ''
})

// 卡片尺寸：打开即最大化适应网页（保持媒体比例）。宽高 96%×94% 占满
// 视口、按宽高比等比放大；媒体元信息未到位时也先按视口最大化撑开，
// 不落到固定 1200px 小卡，避免"打开瞬间没最大化"的视觉。
const MIN_CONTROLS_W = 460
const cardStyle = computed(() => {
  if (theater.value) return { width: '100%', height: '100%', borderRadius: '0' }
  const w = info.value?.width || naturalW.value
  const h = info.value?.height || naturalH.value
  // 最大化：卡片占视口宽 96%、高 94%。顶栏悬浮不占布局，高度只扣控件条。
  const maxW = viewportW.value * 0.96
  const maxH = viewportH.value * 0.94
  if (!w || !h) {
    return { width: maxW + 'px', height: maxH + 'px' }
  }
  const ar = w / h
  const controlsBar = 50 // 控件条约估
  const topBar = 46      // 顶栏悬浮在卡片上方遮罩区，视频高度上限要预留
  // 按高度优先：视频高度撑满 maxH - 控件条 - 顶栏，宽度按比例算
  let vh = maxH - controlsBar - topBar
  let cw = vh * ar
  // 宽度超 maxW 则按宽度收，高度等比降
  if (cw > maxW) {
    cw = maxW
    vh = cw / ar
  }
  // 竖屏视频宽度窄，撑到控件条最小宽度
  cw = Math.max(cw, Math.min(MIN_CONTROLS_W, maxW))
  // 视频按宽度等比算高度，不强制撑满（避免 contain 留上下黑边）；
  // 顶栏悬浮在卡片上方遮罩区（不占布局），控件条在视频下方 flex 自然排列。
  return { width: Math.round(cw) + 'px' }
})

const videoWrapStyle = computed(() => {
  const w = info.value?.width || naturalW.value
  const h = info.value?.height || naturalH.value
  if (!w || !h) return { width: '100%' }
  // 按视频比例自适应：width 撑满卡片，高度由 aspectRatio 自动算，
  // 不强制 height:100% 避免容器高于视频比例时上下留黑边。
  return { width: '100%', aspectRatio: `${w} / ${h}`, display: 'flex' }
})

watch(() => props.startIndex, (v) => { index.value = v })
watch(() => props.modelValue, async (v) => {
  if (v) {
    index.value = props.startIndex
    await resetMedia()
    await nextTick()
    overlayEl.value?.focus()
  } else {
    theater.value = false
  }
})
watch(index, () => resetMedia())

watch([src, isVideo, () => props.modelValue], async () => {
  if (!props.modelValue || !isVideo.value) return
  await nextTick()
  setupVideo()
}, { flush: 'post' })

function onResize() {
  viewportW.value = window.innerWidth
  viewportH.value = window.innerHeight
}
onMounted(() => window.addEventListener('resize', onResize))

async function resetMedia() {
  // 原生 video：切上/下一条直接改 src，无包装层无 ref 丢失问题。
  // 切到图片时 video 被 v-if 卸载，无需 destroy。
  zoom.value = 1
  // 切视频时重置播放状态（含 duration，防新视频 loadedmetadata 前用旧 duration）
  vPlaying.value = false
  // 旧视频停止播放：上报停止（切换后若 autoplay 成功会再触发 @play 上报 true，幂等无害）
  report(false)
  vCurrent.value = 0
  vBuffered.value = 0
  vDuration.value = 0
  loadInfo()
}

async function loadInfo() {
  if (!item.value) return
  try {
    info.value = await api.mediaInfo(props.chatId, item.value.id)
    if (info.value?.width) { naturalW.value = info.value.width; naturalH.value = info.value.height }
  } catch { /* optional */ }
}

// 原生 video 初始化：设 src、恢复音量、autoplay、启动加载轮询
function setupVideo() {
  const el = videoEl.value
  if (!el || !el.isConnected) return
  el.src = src.value
  el.volume = vVolume.value
  el.muted = vMuted.value
  videoLoading.value = true
  videoError.value = ''
  retriedSrcError = false
  // autoplay：浏览器策略可能拒绝（无手势+非静音），捕获 rejection 停首帧
  const p = el.play()
  if (p && p.catch) p.catch(() => {})
  watchVideoLoading()
}

// video 事件处理
// 播放状态上报（幂等配对）：播放器可能重复触发 play/pause/ended（seek、关闭双路径），
// 用组件级标志保证上报严格配对，避免误停其他播放源（引用计数）
let mvReported = false
function report(playing) {
  if (playing && !mvReported) { mvReported = true; reportPlayback(true) }
  else if (!playing && mvReported) { mvReported = false; reportPlayback(false) }
}
// 视频播放状态上报：播放→后端限速后台下载；暂停/结束→恢复
function onVideoPlay() { vPlaying.value = true; report(true) }
function onVideoPause() { vPlaying.value = false; report(false) }

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
function onSeek(sec) {
  const el = videoEl.value
  if (!el) return
  el.currentTime = sec
  vCurrent.value = sec
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

function watchVideoLoading() {
  // Loading / error feedback. 轮询播放状态直到视频就绪。
  // 就绪 = 有数据可播（readyState>=2），不要求"正在播放且时间推进"：若要求
  // 播放中，停在首帧暂停的视频会让 spinner 永远盖住画面、连控件都点不到。
  stopLoadingWatch()
  loadingTimer = setInterval(() => {
    const v = videoEl.value
    if (!v) { stopLoadingWatch(); return }
    if (v.error) {
      // MEDIA_ERR_SRC_NOT_SUPPORTED(4) 不一定是"编码不支持"：流式起播时
      // 若 Range 响应没拿到元数据（首块未就绪/连接排队），video 也会报 4。
      // 先给一次重载机会（重设 src 强制重新拉取），仍失败才显示错误。
      if (v.error.code === 4 && !retriedSrcError) {
        retriedSrcError = true
        const s = v.currentSrc || videoEl.value?.src
        videoError.value = ''
        v.src = s
        v.load()
        return
      }
      videoLoading.value = false
      videoError.value = videoErrorText(v.error)
      stopLoadingWatch()
      return
    }
    if (v.readyState >= 2) {
      videoLoading.value = false
      videoError.value = ''
      stopLoadingWatch()
      return
    }
  }, 200)
}

let loadingTimer = null
let retriedSrcError = false
function stopLoadingWatch() {
  if (loadingTimer) { clearInterval(loadingTimer); loadingTimer = null }
}

function videoErrorText(err) {
  if (!err) return '未知错误'
  switch (err.code) {
    case 1: return '加载被取消'
    case 2: return '网络错误，无法获取视频数据'
    case 3: return '视频解码失败（可能是编码不受支持）'
    case 4: return '视频加载失败：可能为编码不受浏览器支持（如 HEVC/AV1），或流式数据未就绪，可重试或下载后本地播放'
    default: return '播放出错'
  }
}

function close() {
  theater.value = false
  stopLoadingWatch()
  // video 卸载前上报停止，解除后台下载限速
  report(false)
  emit('update:modelValue', false)
}
function prev() { if (index.value > 0) index.value-- }
function next() { if (index.value < props.list.length - 1) index.value++ }
function download() { if (item.value) emit('download', item.value) }
function toggleTheater() { theater.value = !theater.value }

function onWheel(e) {
  if (isVideo.value) return
  zoomStep(e.deltaY < 0 ? 0.1 : -0.1)
}
function zoomStep(d) { zoom.value = Math.min(5, Math.max(0.2, +(zoom.value + d).toFixed(2))) }
function onImgLoad(e) {
  if (!naturalW.value) { naturalW.value = e.target.naturalWidth; naturalH.value = e.target.naturalHeight }
}

function onKey(e) {
  if (!props.modelValue) return
  if (e.key === 'Escape') { if (theater.value) theater.value = false; else close() }
  else if (e.key === 'ArrowLeft') prev()
  else if (e.key === 'ArrowRight') next()
}

onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  stopLoadingWatch()
  // 组件卸载兜底：浏览器返回/路由切换时可能不触发 pause 事件，主动上报停止防心跳泄漏
  if (mvReported) { mvReported = false; reportPlayback(false) }
})
</script>

<style scoped>
.mv-overlay {
  position: fixed; inset: 0; z-index: 3000;
  background: rgba(10, 12, 16, 0.72); /* 半透明衬底，不是全黑 */
  display: flex; align-items: center; justify-content: center;
  outline: none; user-select: none;
  backdrop-filter: blur(2px);
}
.mv-overlay.theater { background: rgba(0, 0, 0, 0.94); backdrop-filter: none; }
/* 卡片：按视频尺寸自适应，深色面板 */
.mv-card {
  position: relative;
  display: flex; flex-direction: column;
  background: #14161b;
  border-radius: var(--radius-lg);
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.5);
  overflow: visible;
  max-width: 100vw; max-height: 100vh;
}
.mv-overlay.theater .mv-card { border-radius: 0; box-shadow: none; width: 100%; height: 100%; }
/* 影院模式：右上角浮层按钮 hover 显现 */
.mv-float-actions { position: absolute; top: 12px; right: 12px; z-index: 20; display: flex; gap: 6px; opacity: 0; transition: opacity .2s; }
.mv-overlay.theater:hover .mv-float-actions,
.mv-float-actions:hover { opacity: 1; }
.mv-float-actions .mv-icon-btn { background: rgba(0,0,0,.5); color: #fff; backdrop-filter: blur(8px); }
.mv-float-actions .mv-icon-btn:hover { background: rgba(0,0,0,.75); }
.mv-top {
  position: absolute; top: -46px; left: 0; right: 0; z-index: 15;
  flex: none; display: flex; align-items: center; justify-content: space-between;
  padding: 6px 4px 14px; color: #fff; gap: 12px;
  /* 透明：让 overlay 透黑遮罩透出来，不加黑条 */
  background: transparent;
}
/* 影院模式：顶栏叠在视频内顶部，渐变遮罩，不占布局 */
.mv-top--theater {
  top: 0;
  background: linear-gradient(to bottom, rgba(0,0,0,.55) 0%, rgba(0,0,0,.15) 75%, transparent 100%);
  border-radius: 0;
  pointer-events: none;
}
.mv-top--theater > * { pointer-events: auto; }
.mv-title { min-width: 0; }
.mv-name { font-weight: 600; font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.mv-sub { font-size: 12px; color: rgba(255,255,255,.6); margin-top: 2px; }
.mv-icon-btn {
  height: 30px; min-width: 34px; padding: 0 8px; border: none; border-radius: var(--radius-sm);
  background: rgba(255,255,255,.08); color: #fff; font-size: 15px; cursor: pointer; transition: .15s;
  display: inline-flex; align-items: center; justify-content: center;
}
.mv-icon-btn:hover { background: rgba(255,255,255,.2); }

.mv-stage { flex: 1; min-height: 0; display: flex; align-items: center; justify-content: center; position: relative; background: #000; overflow: hidden; border-radius: var(--radius-lg); }
.mv-img { max-width: 100%; max-height: 100%; object-fit: contain; transition: transform .1s ease-out; display: block; }
/* 视频容器：撑满 stage 高度，按比例 contain 不裁切；圆角匹配整体风格（video + 控件条统一裁切） */
.mv-video-wrap { max-width: 100%; max-height: 100%; display: flex; align-items: center; justify-content: center; overflow: hidden; position: relative; border-radius: var(--radius); }
.mv-video { width: 100%; height: 100%; max-width: 100%; max-height: 100%; object-fit: contain; display: block; border-radius: var(--radius); }
/* 影院模式：视频铺满整个区域，按高度 contain 等比放大（不裁切，
   比例不匹配时留黑边是 contain 的正常行为，优于裁切丢失画面内容）；全屏无圆角 */
.mv-overlay.theater .mv-video-wrap { width: 100%; height: 100%; max-width: none; max-height: none; border-radius: 0; }
.mv-overlay.theater .mv-video { width: 100%; height: 100%; object-fit: contain; border-radius: 0; }
.mv-overlay.theater .mv-stage { border-radius: 0; }

/* 视频加载 / 错误状态浮层 */
.mv-vstatus {
  position: absolute; inset: 0; z-index: 6;
  display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px;
  background: rgba(0,0,0,.45); color: #fff; font-size: 14px; text-align: center; padding: 20px;
}
.mv-vstatus.error { background: rgba(0,0,0,.72); }
.mv-vstatus-sub { font-size: 12.5px; color: rgba(255,255,255,.7); max-width: 420px; line-height: 1.5; }
.mv-spinner {
  width: 34px; height: 34px; border-radius: 50%;
  border: 3px solid rgba(255,255,255,.25); border-top-color: #4f7cff;
  animation: mv-spin .8s linear infinite;
}
@keyframes mv-spin { to { transform: rotate(360deg); } }

.mv-nav {
  width: 36px; height: 36px; border: none; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  background: var(--glass-bg); color: var(--glass-color); cursor: pointer; transition: .15s;
  backdrop-filter: blur(6px);
}
.mv-nav:hover { background: var(--accent); color: #fff; }
/* 左侧上一条 / 右侧下一条：贴卡片外侧，垂直居中 */
.mv-nav.left { position: absolute; top: 50%; transform: translateY(-50%); z-index: 10; left: -44px; }
.mv-nav.right { position: absolute; top: 50%; transform: translateY(-50%); z-index: 10; right: -44px; }
/* 右上角关闭+下载列：贴卡片外侧右上 */
.mv-nav-col.top-right {
  position: absolute; top: 12px; right: -44px; z-index: 10;
  display: flex; flex-direction: column; align-items: center; gap: 10px;
}
/* 影院模式：卡片铺满视口，按钮回到内侧边缘 */
.mv-overlay.theater .mv-nav.left { left: 12px; }
.mv-overlay.theater .mv-nav.right { right: 12px; }
.mv-overlay.theater .mv-nav-col.top-right { right: 12px; }

.mv-controls { position: absolute; bottom: 0; left: 0; right: 0; z-index: 15; display: flex; align-items: center; gap: 10px; padding: 16px 16px 8px; color: #fff; background: linear-gradient(to top, rgba(0,0,0,.7), transparent); opacity: 0; transition: opacity .2s; }
.mv-stage:hover .mv-controls { opacity: 1; }
.mv-cbtn {
  width: 34px; height: 34px; flex: none; border: none; border-radius: 50%;
  background: rgba(255,255,255,.1); color: #fff; font-size: 15px; cursor: pointer; transition: .15s;
  display: flex; align-items: center; justify-content: center;
}
.mv-cbtn:hover { background: rgba(255,255,255,.22); }
.mv-zoom { font-size: 12.5px; min-width: 48px; text-align: center; color: rgba(255,255,255,.8); }
.mv-flex { flex: 1; }
.mv-dl {
  height: 34px; padding: 0 16px; flex: none; border: none; border-radius: var(--radius-lg);
  background: #4f7cff; color: #fff; font-size: 13px; font-weight: 600; cursor: pointer; transition: .15s;
}
.mv-dl:hover { background: #6b93ff; }
</style>
