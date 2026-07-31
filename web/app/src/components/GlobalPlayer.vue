<template>
  <!-- 全局唯一 audio 元素：必须在 v-if 外面始终渲染，否则 current=null 时
       元素不存在、_el 为 null，player.play() 直接 return —— 点了没反应 -->
  <audio
    ref="audioEl"
    preload="auto"
    style="display:none"
    @play="player._onPlay()"
    @pause="player._onPause()"
    @timeupdate="onTimeUpdate"
    @loadedmetadata="onLoadedMetadata"
    @ended="player._onEnded()"
    @error="onError"
  />

  <transition name="gp">
    <div v-if="player.current" class="gp-bar" :class="{ loading: player.loading }">
      <!-- 第一行：文件名（独占一行，可换行显示完整长文件名） -->
      <div class="gp-title-row" @click="player.locate()" :title="'定位到消息：' + player.current.name">
        <span class="gp-cover"><Music :size="14" /></span>
        <span class="gp-name">{{ player.current.name }}</span>
        <span v-if="player.error" class="gp-err">{{ player.error }}</span>
        <span v-else class="gp-time">{{ fmtTime(player.currentTime) }} / {{ player.duration ? fmtTime(player.duration) : '--:--' }}</span>
      </div>

      <!-- 第二行：控件（进度条 + 播放 + 音量 + 关闭） -->
      <div class="gp-row">
        <input
          class="gp-progress"
          type="range"
          min="0" max="1000"
          :value="Math.round(player.progress * 1000)"
          @input="onSeek"
          :disabled="!player.duration"
        />

        <!-- 控制按钮 -->
        <div class="gp-controls">
          <button class="gp-btn" :disabled="!player.hasPrev" @click="player.prev()" title="上一首"><SkipBack :size="18" /></button>
          <button class="gp-btn gp-play" @click="player.toggle()" :title="player.playing ? '暂停' : '播放'">
            <Pause v-if="player.playing" :size="22" />
            <Play v-else :size="22" />
          </button>
          <button class="gp-btn" :disabled="!player.hasNext" @click="player.next()" title="下一首"><SkipForward :size="18" /></button>
        </div>

        <!-- 音量 -->
        <div class="gp-volume">
          <button class="gp-btn" @click="player.setMuted(!player.muted)" :title="player.muted ? '取消静音' : '静音'">
            <VolumeX v-if="player.muted || player.volume === 0" :size="18" />
            <Volume2 v-else :size="18" />
          </button>
          <input
            class="gp-vol-slider"
            type="range" min="0" max="100"
            :value="player.muted ? 0 : Math.round(player.volume * 100)"
            @input="onVolume"
          />
        </div>

        <!-- 关闭 -->
        <button class="gp-btn gp-close" @click="player.close()" title="关闭播放器"><X :size="18" /></button>
      </div>
    </div>
  </transition>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { usePlayerStore } from '../stores/player'
import { SkipBack, SkipForward, Play, Pause, X, VolumeX, Volume2, Music } from 'lucide-vue-next'

const player = usePlayerStore()
const audioEl = ref(null)

onMounted(() => {
  player.registerEl(audioEl.value)
})

function onSeek(e) {
  player.seek(Number(e.target.value) / 1000)
}
function onVolume(e) {
  player.setVolume(Number(e.target.value) / 100)
}
function onTimeUpdate(e) {
  player._onTimeUpdate(e.target.currentTime)
}
function onLoadedMetadata(e) {
  player._onLoadedMetadata(e.target.duration)
}
function onError(e) {
  const err = e.target?.error
  const map = { 1: '加载被中止', 2: '网络错误', 3: '解码失败（格式不支持）', 4: '不支持的音频格式' }
  player._onError(err ? (map[err.code] || `错误 ${err.code}`) : '音频加载失败')
}

function fmtTime(sec) {
  if (!sec || !isFinite(sec)) return '0:00'
  sec = Math.floor(sec)
  const m = Math.floor(sec / 60)
  const s = sec % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
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
  gap: 8px;
  width: min(880px, calc(100vw - 48px));
  padding: 10px 16px;
  background: var(--bg-panel);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.18);
  color: var(--text);
}

/* 第一行：文件名 + 时间，独占一行可换行，长文件名也能显示完整 */
.gp-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  cursor: pointer;
}
.gp-title-row:hover .gp-name { color: var(--accent, #4f7cff); }
.gp-name {
  font-size: 12.5px; font-weight: 500;
  /* 关键：允许换行，长文件名完整显示 */
  white-space: normal; word-break: break-all;
  line-height: 1.35;
  flex: 1; min-width: 0;
  transition: color .15s;
}
.gp-time { font-size: 11px; color: var(--text-3); flex: none; white-space: nowrap; }
.gp-err { color: var(--red); font-size: 11px; flex: none; }

/* 第二行：进度条 + 控件，单行 */
.gp-row { display: flex; align-items: center; gap: 12px; }

.gp-cover {
  width: 20px; height: 20px; flex: none;
  display: flex; align-items: center; justify-content: center;
  color: var(--text-2);
}
.gp-progress {
  flex: 1;
  height: 4px;
  accent-color: var(--accent);
  cursor: pointer;
}
.gp-progress:disabled { opacity: .4; cursor: default; }

.gp-controls { display: flex; align-items: center; gap: 4px; flex: none; }
.gp-btn {
  width: 32px; height: 32px; flex: none;
  border: none; border-radius: 50%; background: transparent;
  color: var(--text); cursor: pointer;
  display: flex; align-items: center; justify-content: center;
  transition: background .15s;
}
.gp-btn:hover:not(:disabled) { background: var(--bg-hover); }
.gp-btn:disabled { opacity: .35; cursor: default; }
.gp-play {
  width: 36px; height: 36px;
  background: var(--accent);
  color: #fff;
}
.gp-play:hover:not(:disabled) { background: var(--accent); filter: brightness(1.1); }

.gp-volume { display: flex; align-items: center; gap: 4px; flex: none; }
.gp-vol-slider { width: 64px; height: 4px; accent-color: var(--accent); cursor: pointer; }

.gp-close { color: var(--text-3); }

/* 进出场动画 */
.gp-enter-active, .gp-leave-active { transition: all .25s ease; }
.gp-enter-from, .gp-leave-to { opacity: 0; transform: translateX(-50%) translateY(20px); }
</style>
