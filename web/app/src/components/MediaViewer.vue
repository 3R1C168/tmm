<template>
  <teleport to="body">
    <div v-if="modelValue" class="mv-overlay" :class="{ theater }" @keydown="onKey" tabindex="-1" ref="overlayEl" @click.self="close">
      <!-- 居中卡片：按视频尺寸自适应，黑色只是衬底不是全屏遮罩；影院模式铺满视口 -->
      <div class="mv-card" :style="cardStyle" @click.stop>
        <!-- 顶部信息栏（影院模式隐藏，改用浮层按钮） -->
        <div class="mv-top" :class="{ 'theater-hide': theater }">
          <div class="mv-title">
            <div class="mv-name" :title="info?.name || item?.media?.name">{{ info?.name || item?.media?.name || '媒体' }}</div>
            <div class="mv-sub">
              <span v-if="dims">{{ dims }}</span>
              <span v-if="info?.size"> · {{ fmtBytes(info.size) }}</span>
              <span class="mv-count"> · {{ index + 1 }} / {{ list.length }}</span>
            </div>
          </div>
          <div class="mv-topactions">
            <button class="mv-dl-top" @click="download" title="下载此媒体"><Download :size="16" /> 下载</button>
            <button class="mv-x" title="关闭 (Esc)" @click="close"><X :size="20" /></button>
          </div>
        </div>

        <!-- 影院模式浮层按钮：右上角悬浮，hover 显现，不占布局。网页全屏按钮已在控件条，此处只留下载/关闭 -->
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
              @seeked="onSeeked"
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
              :playback-rate="vRate"
              @play="videoEl?.play()"
              @pause="videoEl?.pause()"
              @seek="onSeek"
              @set-volume="onSetVolume"
              @toggle-mute="onToggleMute"
              @toggle-theater="toggleTheater"
              @set-rate="onSetRate"
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

        <!-- 上一个/下一个导航：贴卡片外侧（mv-card overflow:visible 可显示） -->
        <button v-if="index > 0" class="mv-nav left" @click="prev" title="上一个 (←)"><ChevronLeft :size="26" stroke-width="3" /></button>
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
const vRate = ref(1)
let seeking = false  // seek 拖动防重入 flag

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
  const controlsBar = 50 // 控件条约估，顶栏已悬浮不占
  // 按高度优先：视频高度撑满 maxH - 控件条，宽度按比例算
  let vh = maxH - controlsBar
  let cw = vh * ar
  // 宽度超 maxW 则按宽度收，高度等比降
  if (cw > maxW) {
    cw = maxW
    vh = cw / ar
  }
  // 竖屏视频宽度窄，撑到控件条最小宽度
  cw = Math.max(cw, Math.min(MIN_CONTROLS_W, maxW))
  // 视频按宽度等比算高度，不强制撑满（避免 contain 留上下黑边）；
  // 顶栏悬浮不占布局，控件条在视频下方 flex 自然排列。
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
  // 切视频时重置播放状态
  vPlaying.value = false
  vCurrent.value = 0
  vBuffered.value = 0
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
function onSeek(sec) {
  const el = videoEl.value
  if (!el) return
  seeking = true
  el.currentTime = sec
  vCurrent.value = sec
}
// seek 拖动后恢复播放：原生 video seek 后多数浏览器保持原播放态，这里兜底
function onSeeked() {
  if (seeking) seeking = false
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

function close() { theater.value = false; stopLoadingWatch(); emit('update:modelValue', false) }
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
  position: absolute; top: 0; left: 0; right: 0; z-index: 15;
  flex: none; display: flex; align-items: center; justify-content: space-between;
  padding: 10px 16px 8px; color: #fff; gap: 12px;
  background: linear-gradient(to bottom, rgba(0,0,0,.55) 0%, rgba(0,0,0,.15) 75%, transparent 100%);
  pointer-events: none;
}
.mv-top > * { pointer-events: auto; }
.mv-top.theater-hide { display: none; }
.mv-title { min-width: 0; }
.mv-name { font-weight: 600; font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.mv-sub { font-size: 12px; color: rgba(255,255,255,.6); margin-top: 2px; }
.mv-topactions { display: flex; align-items: center; gap: 8px; flex: none; }
.mv-icon-btn {
  height: 30px; min-width: 34px; padding: 0 8px; border: none; border-radius: var(--radius-sm);
  background: rgba(255,255,255,.08); color: #fff; font-size: 15px; cursor: pointer; transition: .15s;
  display: inline-flex; align-items: center; justify-content: center;
}
.mv-icon-btn:hover { background: rgba(255,255,255,.2); }
.mv-dl-top {
  height: 36px; padding: 0 14px; border: none; border-radius: var(--radius-lg);
  display: inline-flex; align-items: center; gap: 4px;
  background: var(--glass-bg); color: var(--glass-color); font-size: 13px; font-weight: 600; cursor: pointer; transition: .15s;
  backdrop-filter: blur(6px);
}
.mv-dl-top:hover { background: var(--accent); color: #fff; }
.mv-x {
  width: 36px; height: 36px; border: none; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  background: var(--glass-bg); color: var(--glass-color); cursor: pointer; transition: .15s;
  backdrop-filter: blur(6px);
}
.mv-x:hover { background: var(--red, #ef4444); color: #fff; }

.mv-stage { flex: 1; min-height: 0; display: flex; align-items: center; justify-content: center; position: relative; background: #000; overflow: hidden; }
.mv-img { max-width: 100%; max-height: 100%; object-fit: contain; transition: transform .1s ease-out; display: block; }
/* 视频容器：撑满 stage 高度，按比例 contain 不裁切 */
.mv-video-wrap { max-width: 100%; max-height: 100%; display: flex; align-items: center; justify-content: center; overflow: hidden; position: relative; }
.mv-video { max-width: 100%; max-height: 100%; object-fit: contain; display: block; }
/* 影院模式：视频铺满整个区域，按高度 contain 等比放大（不裁切，
   比例不匹配时留黑边是 contain 的正常行为，优于裁切丢失画面内容） */
.mv-overlay.theater .mv-video-wrap { width: 100%; height: 100%; max-width: none; max-height: none; }
.mv-overlay.theater .mv-video { width: 100%; height: 100%; object-fit: contain; }

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
  position: absolute; top: 50%; transform: translateY(-50%); z-index: 10;
  width: 36px; height: 36px; border: none; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  background: var(--glass-bg); color: var(--glass-color); cursor: pointer; transition: .15s;
  backdrop-filter: blur(6px);
}
.mv-nav:hover { background: var(--accent); color: #fff; }
/* 普通模式：按钮贴卡片外侧，留 12px 间隙不贴边 */
.mv-nav.left { left: -48px; }
.mv-nav.right { right: -48px; }
/* 影院模式：卡片铺满视口，按钮回到内侧边缘 */
.mv-overlay.theater .mv-nav.left { left: 12px; }
.mv-overlay.theater .mv-nav.right { right: 12px; }

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
