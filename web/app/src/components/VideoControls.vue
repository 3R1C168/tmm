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
