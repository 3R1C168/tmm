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
          </div>
        </div>

        <!-- 图片控制条：仅缩放操作，下载/影院已在顶部统一操作 -->
        <div v-if="!isVideo" class="mv-controls">
          <button class="mv-cbtn" @click="zoomStep(-0.2)" title="缩小"><ZoomOut :size="18" /></button>
          <span class="mv-zoom">{{ Math.round(zoom * 100) }}%</span>
          <button class="mv-cbtn" @click="zoomStep(0.2)" title="放大"><ZoomIn :size="18" /></button>
          <button class="mv-cbtn" @click="zoom = 1" title="重置"><RotateCcw :size="16" /></button>
          <div class="mv-flex" />
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
import Plyr from 'plyr'
import 'plyr/dist/plyr.css'
import { api } from '../api'
import { Download, X, TriangleAlert, Maximize, Minimize, ChevronLeft, ChevronRight, ZoomOut, ZoomIn, RotateCcw } from 'lucide-vue-next'

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
let plyr = null

const item = computed(() => props.list[index.value] || null)
const isVideo = computed(() => item.value && /\.(mp4|mov|mkv|webm|avi)$/i.test(item.value.media?.name || ''))
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
  // 最大化：卡片占视口宽 96%、高 94%。顶栏悬浮不占布局，高度只扣 Plyr 控件条。
  const maxW = viewportW.value * 0.96
  const maxH = viewportH.value * 0.94
  if (!w || !h) {
    return { width: maxW + 'px', height: maxH + 'px' }
  }
  const ar = w / h
  const controlsBar = 50 // Plyr 控件条约估，顶栏已悬浮不占
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
  setupPlyr()
}, { flush: 'post' })

function onResize() {
  viewportW.value = window.innerWidth
  viewportH.value = window.innerHeight
}
onMounted(() => window.addEventListener('resize', onResize))

async function resetMedia() {
  // 切换上/下一条时不销毁 Plyr（销毁+重建会丢失 video ref，Plyr 包装/解包
  // 过程导致 video DOM 元素移位）。改由 setupPlyr 复用已包装的 video 直接更新
  // src，Plyr 会自动重载新视频。
  // 例外：切到图片（isVideo 变 false）时 <video> 被 v-if 卸载重建，Plyr 实例
  // 绑定的还是旧节点——必须销毁，否则切回视频走"复用"分支时控件（进度条/音量/
  // 全屏）全挂在已卸载节点上，新视频控制失灵。旧节点已卸载，destroy 安全。
  if (plyr && !isVideo.value) destroyPlyr()
  zoom.value = 1
  loadInfo()
}

async function loadInfo() {
  if (!item.value) return
  try {
    info.value = await api.mediaInfo(props.chatId, item.value.id)
    if (info.value?.width) { naturalW.value = info.value.width; naturalH.value = info.value.height }
  } catch { /* optional */ }
}

function setupPlyr() {
  const el = videoEl.value
  if (!el || !el.isConnected) return

  // If Plyr already exists (switching to next/prev video), reuse it: just
  // update the src and reset state. Destroying+recreating Plyr loses the
  // video ref because Plyr replaces the original <video> with its wrapper
  // DOM, and destroy's restoration is unreliable across rapid switches.
  if (plyr) {
    el.src = src.value
    videoLoading.value = true
    videoError.value = ''
    retriedSrcError = false
    // 直接改 el.src 不会触发 Plyr 的 autoplay（autoplay 只在初始化/设置 source
    // 时生效），切换后视频会停在首帧暂停、spinner 卡住 → 显式 play 一次对齐
    // autoplay 意图；被浏览器自动播放策略拒绝则忽略（用户手动点播放即可，
    // readyState>=2 后 spinner 会消失）。
    const p = plyr.play()
    if (p && p.catch) p.catch(() => {})
    watchVideoLoading()
    return
  }

  // First-time setup: create Plyr
  el.src = src.value
  videoLoading.value = true
  videoError.value = ''
  retriedSrcError = false
  // 全局音量：从 localStorage 恢复，默认 50%。切换视频时保持同一音量。
  const savedVol = parseFloat(localStorage.getItem('tmm-volume') || '0.5')
  const vol = isNaN(savedVol) ? 0.5 : Math.min(1, Math.max(0, savedVol))
  plyr = new Plyr(el, {
    controls: ['play', 'progress', 'current-time', 'duration', 'mute', 'volume', 'settings'],
    settings: ['speed'],
    speed: { selected: 1, options: [0.5, 0.75, 1, 1.25, 1.5, 2] },
    i18n: {
      speed: '播放速度',
      normal: '正常',
      settings: '设置',
      menuBack: '返回上一菜单',
      play: '播放',
      pause: '暂停',
      mute: '静音',
      unmute: '取消静音',
      volume: '音量',
      currentTime: '当前时间',
      duration: '时长',
      seek: '跳转',
    },
    // CC/字幕功能整体关掉：控件列表不含 captions，设置菜单限 speed，
    // 这里再显式禁用字幕避免视频带轨道时自动启用。
    captions: { active: false, language: 'auto', update: false },
    autoplay: true,
    muted: false,
    volume: vol,
    clickToPlay: true,
    hideControls: false,
    resetOnEnd: true,
  })
  // autoplay:true 已在新实例内部触发 play，但浏览器自动播放策略可能拒绝
  // （无手势 + 非静音）。显式再 play 一次捕获 rejection，避免 unhandled
  // rejection 告警；被拒则停在首帧，readyState>=2 后 spinner 仍会消失。
  const pp = plyr.play()
  if (pp && pp.catch) pp.catch(() => {
    // 浏览器仍拦截（极少见）：停在首帧，用户点播放键即可，spinner 会消失
  })
  // 音量变化时存到 localStorage（全局生效，下次打开/切换视频恢复）+ 同步弹出条数字
  plyr.on('volumechange', () => {
    if (plyr && !plyr.muted) localStorage.setItem('tmm-volume', String(plyr.volume))
    syncVolVal()
  })
  syncVolVal()

  // 拖动进度后自动恢复播放：Plyr 拖动 seek 时会暂停视频，seek 完成后停在
  // 暂停状态，看起来像"拖动卡住"。这里在 seek 开始时记住是否在播放，
  // seeked（拖动结束）后若之前在播放则自动 play。跳过"即将暂停"的 seek
  // 事件（如切视频时 Plyr 内部 seek），避免误恢复。
  let playingBeforeSeek = false
  let seekOngoing = false
  plyr.on('seeking', () => {
    if (plyr.playing) {
      playingBeforeSeek = true
      seekOngoing = true
    }
  })
  plyr.on('seeked', () => {
    if (seekOngoing && playingBeforeSeek) {
      const p = plyr.play()
      if (p && p.catch) p.catch(() => {})
    }
    seekOngoing = false
    playingBeforeSeek = false
  })

  // 拖动进度后自动恢复播放（seeked 恢复在下方已实现）。
  // 注意：不在这里加 waiting/stalled 自动 play 轮询——HTML5 video 缓冲充足时
  // 会自动继续播放，手动 play() 反而可能打断/触发非手势拒绝；且复杂状态机
  // 在切换视频时易残留，曾导致播放异常。

  // ArtPlayer 风格：控件叠加在视频底部（渐变遮罩），不再移到独立黑框
  watchVideoLoading()

  // 注入"网页全屏"按钮到 Plyr 控件条（音量右边）：Plyr 不自带此控件，
  // 用 DOM 插入一个与 Plyr 原生按钮同风格的按钮，点击触发 theater 模式
  // （网页全屏，非浏览器全屏 API）。
  injectTheaterButton()
  hijackSpeedMenu()
}

function injectTheaterButton() {
  if (!plyr) return
  const controls = plyr.elements?.container?.querySelector('.plyr__controls')
  if (!controls) return
  // 避免重复注入
  if (controls.querySelector('.plyr__theater-btn')) return
  const btn = document.createElement('button')
  btn.className = 'plyr__control plyr__theater-btn'
  btn.type = 'button'
  btn.title = '网页全屏'
  updateTheaterBtnIcon(btn, theater.value)
  btn.addEventListener('click', (e) => { e.preventDefault(); toggleTheater() })
  controls.appendChild(btn)
}

function updateTheaterBtnIcon(btn, isTheater) {
  // 进入网页全屏用 Minimize 图标，否则 Maximize 图标（Lucide 同款 path）
  const path = isTheater
    ? '<path d="M8 3v3a2 2 0 0 1-2 2H3M21 8h-3a2 2 0 0 1-2-2V3M3 16h3a2 2 0 0 1 2 2v3M16 21v-3a2 2 0 0 1 2-2h3"/>'
    : '<path d="M3 8V5a2 2 0 0 1 2-2h3M21 8V5a2 2 0 0 0-2-2h-3M3 16v3a2 2 0 0 0 2 2h3M21 16v3a2 2 0 0 1-2 2h-3"/>'
  btn.innerHTML = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' + path + '</svg>'
}

// 劫持 Plyr settings 菜单：保留原生齿轮按钮和菜单弹出，但当菜单显示
// "播放速度"时，把 Plyr 默认的选项列表替换成横向滑块（仿音量调节体验）。
const SPEED_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2]
function hijackSpeedMenu() {
  if (!plyr) return
  // 监听 Plyr 的 menu 事件——菜单显示时检查是否是 speed 子菜单
  plyr.on('loadedmetadata', () => {
    // 主动注入：在 Plyr 创建菜单 DOM 后 hook
    hijackOnce()
  })
  // 也监听 Plyr 控件条点击（菜单打开时机）
  const container = plyr.elements?.container
  if (!container) return
  container.addEventListener('click', () => setTimeout(hijackOnce, 0), true)
  plyr.on('canplay', hijackOnce)
}

function hijackOnce() {
  if (!plyr) return
  const menu = plyr.elements?.container?.querySelector('.plyr__menu__container')
  if (!menu) return
  // 找到速度选项的按钮（Plyr 用 data-plyr="speed" 标记）
  const speedBtn = menu.querySelector('[data-plyr="speed"]')
  if (!speedBtn) return
  // 已替换过
  if (menu.querySelector('.plyr__speed-slider')) return

  // 找到选项列表容器（.plyr__menu__value 后面跟着 radio 列表）
  const list = menu.querySelector('.plyr__menu__value')?.parentElement
  if (!list) return

  // 隐藏原始选项列表，插入滑块
  const radios = list.querySelectorAll('.plyr__menu__value, [data-plyr="speed-option"], .plyr__menu__container label, .plyr__menu__container button')
  // Plyr 速度选项是 input[type=radio] 在 form 里，直接隐藏整个 form
  const form = list.querySelector('form') || list
  if (form) form.style.display = 'none'

  const min = SPEED_OPTIONS[0], max = SPEED_OPTIONS[SPEED_OPTIONS.length - 1]
  const speedToValue = (s) => (s - min) / (max - min) * 100
  const valueToSpeed = (v) => {
    const exact = min + (v / 100) * (max - min)
    return SPEED_OPTIONS.reduce((best, opt) => Math.abs(opt - exact) < Math.abs(best - exact) ? opt : best, SPEED_OPTIONS[0])
  }
  const cur = plyr.speed || 1
  const slider = document.createElement('div')
  slider.className = 'plyr__speed-slider'
  slider.innerHTML = `<div class="plyr__speed-rate">${cur}x</div>
    <input class="plyr__speed-range" type="range" min="0" max="100" step="1" value="${speedToValue(cur)}">`
  list.appendChild(slider)

  const range = slider.querySelector('input')
  const rate = slider.querySelector('.plyr__speed-rate')
  range.addEventListener('input', (e) => {
    const s = valueToSpeed(Number(e.target.value))
    if (plyr) plyr.speed = s
    rate.textContent = s + 'x'
  })
  range.addEventListener('change', (e) => {
    const s = valueToSpeed(Number(e.target.value))
    if (plyr) plyr.speed = s
    rate.textContent = s + 'x'
    range.value = speedToValue(s)
  })
}

// theater 状态变化时同步注入按钮的图标
watch(theater, (v) => {
  if (!plyr) return
  const btn = plyr.elements?.container?.querySelector('.plyr__theater-btn')
  if (btn) updateTheaterBtnIcon(btn, v)
})

// 音量百分比数字：在 .plyr__volume 容器里放一个 <span>，hover 音量条时显示。
// 用 Plyr 的容器元素查找（Plyr 包装后原始 <video> 被替换，videoEl 上
// closest 不可靠），volumechange 时同步数字（hover 期间实时更新）。
function syncVolVal() {
  if (!plyr) return
  const host = plyr.elements?.container?.querySelector('.plyr__volume')
  if (!host) return
  let v = host.querySelector('.vol-val')
  if (!v) {
    v = document.createElement('span')
    v.className = 'vol-val'
    host.appendChild(v)
  }
  v.textContent = (plyr.muted ? 0 : Math.round(plyr.volume * 100)) + '%'
}

function watchVideoLoading() {
  // Loading / error feedback. 轮询播放状态直到视频就绪（单一 playing/canplay
  // 监听在 Plyr 包装后不可靠）。
  // 就绪 = 有数据可播（readyState>=2），不要求"正在播放且时间推进"：切换视频时
  // Plyr autoplay 可能不生效（见 setupPlyr 复用分支），若要求播放中，停在首帧
  // 暂停的视频会让 spinner 永远盖住画面、连控件都点不到。
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

function destroyPlyr() {
  stopLoadingWatch()
  if (plyr) { try { plyr.destroy() } catch {} plyr = null }
}

function close() { theater.value = false; destroyPlyr(); emit('update:modelValue', false) }
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
  destroyPlyr()
})

function fmtBytes(n) {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(n >= 100 || i === 0 ? 0 : 1)} ${u[i]}`
}
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
/* 视频容器：撑满 stage 高度，按比例 contain 不裁切；Plyr 内部 video 也 contain */
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

.mv-controls { flex: none; display: flex; align-items: center; gap: 10px; padding: 10px 16px; color: #fff; }
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

/* 播放器黑白主题（ArtPlayer 风格）：控件叠加在视频底部（渐变遮罩），
   白色主题、半透明白控件、3px 白色进度条无圆点。
   音量条参照 ArtPlayer：hover 静音按钮时竖向弹出，但去掉默认的黑色圆角
   底色——只留轨道线 + 白色填充 + 圆点，避免黑色块突兀地压在视频上。
   absolute 定位在按钮正上方，横向不侵入其他按钮。 */
:deep(.plyr) {
  --plyr-color-main: #fff;
  --plyr-video-control-color: #fff;
  --plyr-video-control-color-hover: #fff;
  --plyr-video-control-background: rgba(255,255,255,.15);
  --plyr-video-control-background-hover: rgba(255,255,255,.3);
  width: 100%; height: 100%; max-width: 100%;
}
:deep(.plyr--video) { background: #000; height: 100%; }
:deep(.plyr__video-wrapper) { max-width: 100%; height: 100%; }
:deep(.plyr video) { width: 100%; height: 100%; object-fit: contain; }

/* 控件叠加在视频底部，带渐变遮罩 */
:deep(.plyr__controls) {
  position: absolute !important;
  bottom: 0; left: 0; right: 0;
  display: flex !important; align-items: center !important;
  background: linear-gradient(to top, rgba(0,0,0,.8), transparent) !important;
  padding: 24px 16px 8px !important;
}
.mv-overlay.theater :deep(.plyr__controls) { border-radius: 0; }
:deep(.plyr__controls__item) { margin-right: 8px; }
:deep(.plyr__theater-btn) { margin-left: 0; }
/* 速度按钮 + 竖向滑块弹窗 */
/* 原生 settings 菜单里的速度滑块（替换 Plyr 默认选项列表） */
:deep(.plyr__speed-slider) { padding: 8px 4px; display: flex; flex-direction: column; align-items: center; gap: 6px; }
:deep(.plyr__speed-rate) { font-size: 12px; font-weight: 600; }
:deep(.plyr__speed-range) { width: 140px; margin: 0; -webkit-appearance: none; appearance: none; height: 4px; background: rgba(255,255,255,.3); border-radius: 2px; cursor: pointer; accent-color: var(--accent); }

/* 居中大播放按钮：半透明白毛玻璃 */
:deep(.plyr__control--overlaid) {
  background: rgba(255,255,255,.18); border-radius: 50%;
  width: 64px; height: 64px; transition: transform .15s, background .15s;
  backdrop-filter: blur(4px);
}
:deep(.plyr__control--overlaid:hover) { background: rgba(255,255,255,.3); transform: scale(1.08); }

/* 进度条：3px，hover 5px，白色填充无圆点。
   关键：隐藏 Plyr 默认的缓冲条 __buffer（它和 seek input 各画一条，叠着就
   是"双进度条"），seek input 独占一条：白色=已播，透明=未播（露出渐变遮罩）。 */
:deep(.plyr__progress__container) { flex: 1; min-width: 0; }
:deep(.plyr__progress) {
  position: relative; height: 3px; cursor: pointer;
  transition: height .15s; overflow: hidden; border-radius: 3px;
}
:deep(.plyr__progress:hover) { height: 5px; }
:deep(.plyr__progress__buffer) { display: none; }
:deep(.plyr__progress input[type=range]) {
  position: absolute; inset: 0; width: 100%; height: 100%;
  z-index: 2; margin: 0; cursor: pointer; padding: 0;
  -webkit-appearance: none; appearance: none;
  color: #fff; background: transparent;
}
:deep(.plyr__progress input[type=range])::-webkit-slider-runnable-track {
  height: 100% !important; /* 覆盖 Plyr 默认 height: var(--plyr-range-track-height,5px) */
  border: none; border-radius: 3px;
  background-image: linear-gradient(to right, rgba(255,255,255,.55) var(--value, 0%), transparent var(--value, 0%));
}
:deep(.plyr__progress input[type=range])::-webkit-slider-thumb {
  -webkit-appearance: none; width: 0 !important; height: 0 !important; /* 覆盖默认 13px 圆点 */
  background: transparent; box-shadow: none;
}
:deep(.plyr__progress input[type=range])::-moz-range-track {
  height: 100% !important; border-radius: 3px; background: transparent;
}
:deep(.plyr__progress input[type=range])::-moz-range-thumb {
  width: 0 !important; height: 0 !important;
  border: none; background: transparent; box-shadow: none;
}

/* 时间文字 */
:deep(.plyr__time) { font-size: 12px; color: rgba(255,255,255,.8); }

/* 音量：ArtPlayer 式竖向弹出。hover 静音按钮时从按钮正上方滑出（absolute），
   无黑色圆角底——轨道 + 白色填充 + 圆点即可。writing-mode + direction:rtl
   让原生 range 竖起来（min 在底部，值从下往上）。 */
:deep(.plyr__volume) { min-width: 0; position: relative; }
:deep(.plyr__volume input[type=range]) {
  position: absolute;
  bottom: calc(100% + 10px);
  left: 50%;
  transform: translateX(-50%);
  writing-mode: vertical-lr;
  direction: rtl;
  width: 4px;
  height: 0;
  opacity: 0;
  transition: height .2s ease, opacity .2s ease;
  margin: 0;
  cursor: pointer;
  -webkit-appearance: none; appearance: none;
  background: transparent;
}
:deep(.plyr__volume:hover input[type=range]),
:deep(.plyr__volume:focus-within input[type=range]) {
  height: 64px;
  opacity: 1;
}
:deep(.plyr__volume input[type=range])::-webkit-slider-runnable-track {
  width: 4px;
  height: 100%;
  border-radius: 2px;
  background-image: linear-gradient(to top, #fff var(--value, 0%), rgba(255,255,255,.25) var(--value, 0%));
}
:deep(.plyr__volume input[type=range])::-webkit-slider-thumb {
  -webkit-appearance: none;
  width: 12px; height: 12px;
  border-radius: 50%;
  background: #fff;
  margin-left: -4px; /* 12px 圆点相对 4px 轨道居中 */
}
:deep(.plyr__volume input[type=range])::-moz-range-track {
  width: 4px; height: 100%;
  border-radius: 2px;
  background: rgba(255,255,255,.25);
}
:deep(.plyr__volume input[type=range])::-moz-range-progress {
  width: 4px; height: 100%;
  border-radius: 2px;
  background: #fff;
}
:deep(.plyr__volume input[type=range])::-moz-range-thumb {
  width: 12px; height: 12px;
  border: none; border-radius: 50%;
  background: #fff;
}

/* 音量百分比数字（参照 ArtPlayer）：hover 音量条时显示在滑块上方。
   白色无底色，靠 text-shadow 保证在视频/渐变遮罩上可读（不引入黑块）。 */
:deep(.plyr__volume .vol-val) {
  position: absolute;
  bottom: calc(100% + 78px); /* 滑块(64px) + 10px 间距之上 */
  left: 50%;
  transform: translateX(-50%);
  font-size: 11px;
  font-weight: 600;
  color: #fff;
  text-shadow: 0 1px 3px rgba(0, 0, 0, .7);
  opacity: 0;
  transition: opacity .2s ease;
  pointer-events: none;
  white-space: nowrap;
}
:deep(.plyr__volume:hover .vol-val),
:deep(.plyr__volume:focus-within .vol-val) { opacity: 1; }

/* 设置菜单：黑白 */
:deep(.plyr__menu__container) { background: rgba(20,20,20,.95); border-radius: var(--radius-sm); }
:deep(.plyr__control) { border-radius: var(--radius-sm); }
</style>
