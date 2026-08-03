<template>
  <teleport to="body">
    <div class="cm-overlay" tabindex="-1" ref="overlayEl" @keydown="onKey" @click.self="close">
      <div class="cm-card">
        <div class="cm-head">
          <div class="cm-title"><MessageCircle :size="16" /> 评论区</div>
          <button class="cm-x" @click="close" title="关闭 (Esc)"><X :size="20" /></button>
        </div>
        <div v-if="loading" class="cm-status">加载评论…</div>
        <div v-else-if="error" class="cm-status error">{{ error }}</div>
        <div v-else class="cm-body" @scroll="onScroll">
          <div v-if="!comments.length" class="cm-status">暂无评论</div>
          <div v-for="c in comments" :key="c.id" class="cm-item">
            <div class="cm-avatar">{{ avatarChar(c.name) }}</div>
            <div class="cm-main">
              <div class="cm-meta">
                <span class="cm-name">{{ c.name || '未知' }}</span>
                <span v-if="c.username" class="cm-username">@{{ c.username }}</span>
                <span class="cm-time">{{ fmtDate(c.date) }}</span>
              </div>
              <div v-if="c.text" class="cm-text">{{ c.text }}</div>
              <div v-if="c.hasMedia" class="cm-media">
                <LazyThumb :src="api.mediaThumbUrl(chatId, c.id)" :placeholder="c.media?.stripped?.dataUri || ''" />
              </div>
              <div v-if="c.reactions && c.reactions.length" class="cm-reactions">
                <span v-for="(rc, i) in c.reactions" :key="i" class="cm-reaction">{{ rc.emoji }} {{ rc.count }}</span>
              </div>
            </div>
          </div>
          <div v-if="hasMore" class="cm-loadmore">
            <el-button size="small" text :loading="loadingMore" @click="loadMore">
              {{ loadingMore ? '加载中…' : '加载更多评论' }}
            </el-button>
          </div>
          <div v-else-if="comments.length" class="cm-end">已到底</div>
        </div>
      </div>
    </div>
  </teleport>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { MessageCircle, X } from 'lucide-vue-next'
import { api } from '../api'
import LazyThumb from './LazyThumb.vue'

const props = defineProps({
  chatId: { type: String, required: true }, // 讨论组 ID
  msgId: { type: Number, required: true },  // 频道帖子 ID
})
const emit = defineEmits(['close'])

const overlayEl = ref(null)
const loading = ref(true)
const error = ref('')
const comments = ref([])
const nextCursor = ref(0)
const hasMore = ref(true)
const loadingMore = ref(false)

async function loadPage(offsetId) {
  try {
    const res = await api.listReplies(props.chatId, props.msgId, { offsetId, limit: 50 })
    return res
  } catch (e) {
    throw new Error(e.message || '加载评论失败')
  }
}

async function loadMore() {
  if (!hasMore.value || loadingMore.value) return
  loadingMore.value = true
  try {
    const res = await loadPage(nextCursor.value)
    comments.value.push(...res.comments)
    nextCursor.value = res.nextCursor
    hasMore.value = res.nextCursor > 0
  } catch (e) {
    error.value = e.message
  } finally {
    loadingMore.value = false
  }
}

function onScroll() {
  const el = document.querySelector('.cm-body')
  if (!el || !hasMore.value || loadingMore.value) return
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 150) loadMore()
}

function avatarChar(name) {
  return (name || '?').trim().charAt(0).toUpperCase() || '?'
}

function fmtDate(ts) {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function close() { emit('close') }
function onKey(e) { if (e.key === 'Escape') close() }

onMounted(async () => {
  overlayEl.value?.focus()
  try {
    const res = await loadPage(0)
    comments.value = res.comments
    nextCursor.value = res.nextCursor
    hasMore.value = res.nextCursor > 0
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
})

onBeforeUnmount(() => {})
</script>

<style scoped>
/* 评论区弹层：沿用现有 token（--bg-panel/--border/--radius-lg/--accent/--text-3），
   结构与 TelegraphReader 全屏弹层一致，Esc/遮罩关闭抄 MediaViewer。 */
.cm-overlay {
  position: fixed; inset: 0; z-index: 1200;
  background: rgba(0,0,0,.5);
  display: flex; align-items: center; justify-content: center;
  padding: 40px 60px;
  outline: none; /* tabindex=-1 聚焦时不显示焦点框 */
}
.cm-card {
  width: min(640px, 100%); height: 100%;
  display: flex; flex-direction: column;
  background: var(--bg-panel);
  border-radius: var(--radius-lg);
  box-shadow: 0 16px 48px rgba(0,0,0,.35);
  overflow: hidden;
}
.cm-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 14px 18px; border-bottom: 1px solid var(--border);
}
.cm-title { display: flex; align-items: center; gap: 6px; font-size: 14px; font-weight: 600; color: var(--text); }
.cm-x {
  display: inline-flex; align-items: center; justify-content: center;
  width: 30px; height: 30px; border-radius: 50%;
  background: transparent; border: none; color: var(--text-3); cursor: pointer;
  transition: .15s;
}
.cm-x:hover { background: var(--bg-hover); color: var(--text); }
.cm-status { padding: 28px; text-align: center; font-size: 13px; color: var(--text-3); }
.cm-status.error { color: var(--red); }
.cm-body { flex: 1; min-height: 0; overflow-y: auto; padding: 8px 18px 12px; }
.cm-item { display: flex; gap: 10px; padding: 12px 0; border-bottom: 1px solid var(--border); }
.cm-item:last-child { border-bottom: none; }
.cm-avatar {
  flex: none; width: 34px; height: 34px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  background: var(--accent-soft); color: var(--accent);
  font-size: 14px; font-weight: 600;
}
.cm-main { flex: 1; min-width: 0; }
.cm-meta { display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; }
.cm-name { font-size: 13px; font-weight: 600; color: var(--text); }
.cm-username { font-size: 12px; color: var(--text-3); }
.cm-time { font-size: 11px; color: var(--text-3); margin-left: auto; }
.cm-text { margin-top: 4px; font-size: 13px; color: var(--text-2); word-break: break-word; white-space: pre-wrap; }
.cm-media { margin-top: 6px; }
.cm-media .lazy-thumb { width: 200px; height: 130px; border-radius: var(--radius-sm); }
.cm-reactions { margin-top: 6px; display: flex; gap: 6px; flex-wrap: wrap; }
.cm-reaction { font-size: 12px; color: var(--text-2); background: var(--bg-hover); border-radius: 999px; padding: 2px 8px; }
.cm-loadmore, .cm-end { text-align: center; padding: 10px 0 4px; font-size: 12px; color: var(--text-3); }
</style>
