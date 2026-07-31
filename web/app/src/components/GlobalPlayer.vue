<template>
  <!-- 全局唯一 audio 元素：必须在 v-if 外面始终渲染，否则 current=null 时
       元素不存在、Plyr 无从绑定 -->
  <audio ref="audioEl" preload="auto" />

  <transition name="gp">
    <div v-if="player.current" class="gp-bar" :class="{ loading: player.loading }">
      <!-- 文件名行 + 关闭 -->
      <div class="gp-title-row">
        <span class="gp-cover"><Music :size="14" /></span>
        <span class="gp-name" @click="player.locate()" :title="'定位到消息：' + player.current.name">{{ player.current.name }}</span>
        <span v-if="player.error" class="gp-err">{{ player.error }}</span>
        <button class="gp-btn gp-close" @click="player.close()" title="关闭播放器"><X :size="18" /></button>
      </div>

      <!-- Plyr 接管 audio：自带进度条/播放/时间/音量，注入上一首/下一首 -->
      <div class="gp-plyr-wrap" ref="plyrWrap" />
    </div>
  </transition>
</template>

<script setup>
import { ref, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import Plyr from 'plyr'
import 'plyr/dist/plyr.css'
import { usePlayerStore } from '../stores/player'
import { SkipBack, SkipForward, Play, Pause, X, Music } from 'lucide-vue-next'

const player = usePlayerStore()
const audioEl = ref(null)
const plyrWrap = ref(null)
let plyr = null

onMounted(() => {
  // Plyr 绑定 audio 元素，控件配置对齐 MediaViewer 风格
  plyr = new Plyr(audioEl.value, {
    controls: ['play', 'progress', 'current-time', 'duration', 'mute', 'volume'],
    settings: ['speed'],
    speed: { selected: 1, options: [0.5, 0.75, 1, 1.25, 1.5, 2] },
    i18n: {
      speed: '播放速度', normal: '正常', settings: '设置', menuBack: '返回上一菜单',
      play: '播放', pause: '暂停', mute: '静音', unmute: '取消静音',
      volume: '音量', currentTime: '当前时间', duration: '时长', seek: '跳转',
    },
  })
  // 把 Plyr 生成的 DOM 移到 gp-plyr-wrap 内（audio 是 display:none 定位的，
  // Plyr 的 UI 需要放到可见的 wrap 里）
  if (plyr.elements?.container && plyrWrap.value) {
    plyrWrap.value.appendChild(plyr.elements.container)
  }
  // 注册 audio 元素给 store（store 直接操作 audio API，Plyr 只是 UI 层）
  player.registerEl(audioEl.value)

  // Plyr 事件同步到 store
  plyr.on('play', () => { player._onPlay() })
  plyr.on('pause', () => { player._onPause() })
  plyr.on('timeupdate', (e) => { player._onTimeUpdate(e.detail.plyr.currentTime) })
  plyr.on('loadedmetadata', (e) => { player._onLoadedMetadata(e.detail.plyr.duration) })
  plyr.on('ended', () => { player._onEnded() })
  plyr.on('error', (e) => {
    const err = e?.detail?.error
    const map = { 1: '加载被中止', 2: '网络错误', 3: '解码失败（格式不支持）', 4: '不支持的音频格式' }
    player._onError(err ? (map[err.code] || `错误 ${err.code}`) : '音频加载失败')
  })

  // 从 localStorage 恢复音量
  const savedVol = parseFloat(localStorage.getItem('tmm-volume') || '0.5')
  const vol = isNaN(savedVol) ? 0.5 : Math.min(1, Math.max(0, savedVol))
  plyr.volume = vol
  // 注入上一首/下一首按钮到控件条
  injectPrevNextButtons()
})

function injectPrevNextButtons() {
  if (!plyr) return
  const controls = plyr.elements?.container?.querySelector('.plyr__controls')
  if (!controls) return
  if (controls.querySelector('.plyr__prev-btn')) return

  const prevBtn = document.createElement('button')
  prevBtn.className = 'plyr__control plyr__prev-btn'
  prevBtn.type = 'button'
  prevBtn.title = '上一首'
  prevBtn.innerHTML = iconSvg(SkipBack)
  prevBtn.addEventListener('click', (e) => { e.preventDefault(); player.prev() })
  prevBtn.disabled = !player.hasPrev

  const nextBtn = document.createElement('button')
  nextBtn.className = 'plyr__control plyr__next-btn'
  nextBtn.type = 'button'
  nextBtn.title = '下一首'
  nextBtn.innerHTML = iconSvg(SkipForward)
  nextBtn.addEventListener('click', (e) => { e.preventDefault(); player.next() })
  nextBtn.disabled = !player.hasNext

  // 插到 play 按钮前面
  const playBtn = controls.querySelector('.plyr__control[data-plyr="play"]')
  if (playBtn) controls.insertBefore(prevBtn, playBtn)
  else controls.appendChild(prevBtn)
  // next 插到 play 后面
  if (playBtn && playBtn.nextSibling) controls.insertBefore(nextBtn, playBtn.nextSibling)
  else controls.appendChild(nextBtn)
}

function iconSvg(Component) {
  // Lucide 组件渲染成 SVG 字符串（用于注入 DOM）
  const size = 18
  const map = {
    [SkipBack]: '<path d="m19 20-7-4.5v9L19 20Z"/><path d="M19 20v0M5 19V5M19 5 12 9.5v-9L19 5Z"/>',
    [SkipForward]: '<path d="m5 4 7 4.5v-9L5 4Z"/><path d="M5 4v0M19 5v14M5 19l7-4.5v9L5 19Z"/>',
  }
  return '<svg viewBox="0 0 24 24" width="' + size + '" height="' + size + '" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' + (map[Component] || '') + '</svg>'
}

// 监听 hasPrev/hasNext 更新按钮 disabled
watch(() => [player.hasPrev, player.hasNext], () => {
  if (!plyr) return
  const prev = plyr.elements?.container?.querySelector('.plyr__prev-btn')
  const next = plyr.elements?.container?.querySelector('.plyr__next-btn')
  if (prev) prev.disabled = !player.hasPrev
  if (next) next.disabled = !player.hasNext
})

// current 变化时确保注入按钮存在（Plyr 可能重新渲染）
watch(() => player.current, async () => {
  await nextTick()
  injectPrevNextButtons()
})

onBeforeUnmount(() => {
  if (plyr) { try { plyr.destroy() } catch {} plyr = null }
})
</script>

<style scoped>
.gp-bar {
  position: fixed;
  left: 50%;
  bottom: 18px;
  transform: translateX(-50%);
  z-index: 1000;
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: min(880px, calc(100vw - 48px));
  padding: 10px 16px 6px;
  background: var(--bg-panel);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.18);
  color: var(--text);
}

.gp-title-row {
  display: flex; align-items: center; gap: 8px; min-width: 0;
}
.gp-cover { color: var(--text-2); flex: none; display: flex; }
.gp-name {
  font-size: 12.5px; font-weight: 500; flex: 1; min-width: 0;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  cursor: pointer; transition: color .15s;
}
.gp-name:hover { color: var(--accent); }
.gp-err { color: var(--red); font-size: 11px; flex: none; }
.gp-close { color: var(--text-3); }

.gp-btn {
  width: 32px; height: 32px; flex: none; border: none; border-radius: 50%;
  background: transparent; color: var(--text); cursor: pointer;
  display: flex; align-items: center; justify-content: center; transition: background .15s;
}
.gp-btn:hover:not(:disabled) { background: var(--bg-hover); }

/* Plyr 适应浮动条主题 */
.gp-plyr-wrap :deep(.plyr) {
  --plyr-color-main: var(--accent);
  --plyr-audio-control-color: var(--text);
  --plyr-audio-control-color-hover: var(--text);
  --plyr-audio-progress-color: var(--accent);
  --plyr-audio-progress-buffered-color: var(--border);
  width: 100%;
}
.gp-plyr-wrap :deep(.plyr--audio) { background: transparent; padding: 0; }
.gp-plyr-wrap :deep(.plyr__controls) {
  background: transparent; padding: 0; border-radius: 0;
}
.gp-plyr-wrap :deep(.plyr__controls__item) { margin-right: 6px; }

/* 进出场动画 */
.gp-enter-active, .gp-leave-active { transition: all .25s ease; }
.gp-enter-from, .gp-leave-to { opacity: 0; transform: translateX(-50%) translateY(20px); }
</style>
