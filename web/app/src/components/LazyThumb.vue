<template>
  <div ref="root" class="lazy-thumb">
    <!-- blurred inline placeholder: renders instantly with zero network -->
    <img
      v-if="placeholder"
      class="thumb-ph"
      :class="{ faded: loaded && !failed }"
      :src="placeholder"
      :alt="alt"
      aria-hidden="true"
    />
    <!-- real thumbnail: fades in over the placeholder once loaded -->
    <img
      v-if="shown && !failed"
      class="thumb-real"
      :class="{ loaded }"
      :src="realSrc"
      :alt="alt"
      @load="onLoad"
      @error="onError"
    />
    <!-- dark fallback when there's nothing to show yet (no placeholder, or
         the real thumb 404'd with no placeholder either). Dark, not the
         near-white --bg-hover, so a missing video thumb doesn't read as a
         blank white card. -->
    <div v-if="!placeholder && (!shown || failed)" class="thumb-placeholder" />
  </div>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount, watch } from 'vue'

// LazyThumb assigns img src when the element scrolls near the viewport.
// If `placeholder` (a data: URI of the message's inline stripped thumbnail)
// is provided, it renders immediately — blurred — so the user sees a preview
// with zero network, then the real thumbnail cross-fades in on top.
// No artificial concurrency limit — the browser's own connection pool
// (6 per origin) is sufficient for a local server.
const props = defineProps({
  src: { type: String, required: true },
  placeholder: { type: String, default: '' },
  alt: { type: String, default: '' },
})
// DynamicScroller 复用 slot 时 LazyThumb 组件实例被复用——src prop 变了但内部
// 状态（shown/loaded/failed/realSrc）仍是旧消息的，导致新消息显示旧缩略图。
// 监听 src 变化，重置全部状态并重新走观察→加载流程。
watch(() => props.src, (v) => {
  if (!v) return
  loaded.value = false
  failed.value = false
  realSrc.value = ''
  retryCount = 0
  if (retryTimer) { clearTimeout(retryTimer); retryTimer = null }
  // 若已进入视口（shown 仍 true），直接加载新 src；否则等 IO 触发
  if (shown.value) {
    realSrc.value = v
  } else if (root.value && observer) {
    observer.observe(root.value)
  }
})

const emit = defineEmits(['load', 'error'])

const root = ref(null)
const shown = ref(false)
const loaded = ref(false)
const failed = ref(false)
const realSrc = ref('') // actual img src; may be retried with cache-buster on 503
let observer = null
let retryCount = 0
let retryTimer = null
let disposed = false

// 503 重试窗口对齐服务端 warmup 时长：缩略图由 warmup（16 并发，每张 RPC
// ~1.5-2s）后台下载，thumb single-flight 3s 超时返回 503。重试太短会在
// warmup 完成前放弃，卡片永远停在 placeholder。6 次指数退避 ≈ 19s 总窗口。
const MAX_RETRY = 6
const backoff = (n) => Math.min(4000, 600 * 2 ** (n - 1))

function setRealSrc() {
  // force reload by appending a cache-busting query
  const src = props.src
  const sep = src.includes('?') ? '&' : '?'
  realSrc.value = src + sep + 'r=' + retryCount
}

function onLoad(e) {
  loaded.value = true
  emit('load', e)
}

// <img> 的 error 事件拿不到 HTTP 状态码，但必须区分：
// - 404：视频无服务端缩略图，重试多少次都不会成功，且每次重试都是一次
//   resolveMedia RPC（~1.5-2s）→ 直接 failed，保留清晰 placeholder；
// - 503：warmup 进行中，稍后就有 → 按退避重试（窗口对齐 warmup 时长）。
// 用同源 fetch 探测状态（本地服务器开销可忽略；探测成功还顺带填了 HTTP 缓存，
// 后面 img 直接命中缓存）。
async function onError(e) {
  if (!shown.value || loaded.value || disposed) return
  let status = 0
  try {
    const res = await fetch(props.src, { cache: 'no-cache' })
    status = res.status
  } catch { /* 网络错误：status 保持 0，按临时错误重试 */ }
  if (disposed) return
  if (status >= 200 && status < 300) {
    // 探测时已成功（如 warmup 刚完成）：换真实地址立即加载
    setRealSrc()
    return
  }
  if (status === 404) {
    failed.value = true
    emit('error', e)
    return
  }
  // 503 / 网络错误：延迟重试
  if (retryCount < MAX_RETRY) {
    retryCount++
    retryTimer = setTimeout(setRealSrc, backoff(retryCount))
    return
  }
  failed.value = true
  emit('error', e)
}

onMounted(() => {
  // 找最近的滚动祖先作为 IntersectionObserver 的 root。
  // 滚动发生在 DynamicScroller（.vue-recycle-scroller, overflow-y:auto）内部，
  // 不是 window。若用默认视口 root，DynamicScroller 内部滚动时元素的视口位置
  // 不更新，导致视口内媒体的 IO 不触发 shown → 缩略图永远不加载。
  let ioRoot = null
  let p = root.value?.parentElement
  while (p) {
    const style = getComputedStyle(p)
    if (/auto|scroll/.test(style.overflowY)) { ioRoot = p; break }
    p = p.parentElement
  }
  observer = new IntersectionObserver((entries) => {
    for (const e of entries) {
      if (e.isIntersecting && !shown.value) {
        shown.value = true
        realSrc.value = props.src
        observer.unobserve(root.value)
      }
    }
  }, { root: ioRoot, rootMargin: '1200px' })
  if (root.value) observer.observe(root.value)
})
onBeforeUnmount(() => {
  disposed = true
  if (observer) observer.disconnect()
  if (retryTimer) clearTimeout(retryTimer)
})
</script>

<style scoped>
.lazy-thumb { position: relative; width: 100%; height: 100%; overflow: hidden; }
.lazy-thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
/* placeholder sits under the real image, blurred like telegram-tt */
.thumb-ph {
  position: absolute; inset: 0;
  filter: blur(6px); transform: scale(1.1); /* hide blur edge artifacts */
  transition: opacity .3s ease;
}
.thumb-ph.faded { opacity: 0; }
/* real thumbnail fades in on top */
.thumb-real { position: relative; opacity: 0; transition: opacity .3s ease; }
.thumb-real.loaded { opacity: 1; }
/* dark fallback when there's no thumbnail at all (avoids a blank white card) */
.thumb-placeholder { width: 100%; height: 100%; background: #3a3d42; }
</style>
