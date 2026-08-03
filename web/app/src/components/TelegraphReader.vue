<template>
  <teleport to="body">
    <div class="tr-overlay" @click.self="close">
      <div class="tr-card">
        <header class="tr-head">
          <div class="tr-title-wrap">
            <div class="tr-title">{{ page.title || 'Telegraph 文章' }}</div>
            <div class="tr-sub">
              <span v-if="page.author">{{ page.author }}</span>
              <span v-if="page.views"> · {{ page.views }} 次浏览</span>
              <span v-if="page.url" class="tr-site">{{ hostname(page.url) }}</span>
            </div>
          </div>
          <div class="tr-actions">
            <button class="tr-dl" :disabled="downloading || loading || !!error" @click="download" :title="'下载文章全部图片到 ' + downloadHint">
              <Download :size="15" /> {{ downloading ? '下载中…' : '下载图片' }}
            </button>
            <button class="tr-x" @click="close" title="关闭 (Esc)"><X :size="20" /></button>
          </div>
        </header>

        <div class="tr-body">
          <div v-if="loading" class="tr-status">加载中…</div>
          <div v-else-if="error" class="tr-status error">{{ error }}</div>
          <article v-else class="tr-article">
            <template v-for="(n, i) in page.content" :key="i">
              <TelegraphNode :node="n" />
            </template>
          </article>
        </div>
      </div>
    </div>
  </teleport>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { Download, X } from 'lucide-vue-next'
import { api } from '../api'
import TelegraphNode from './TelegraphNode.vue'

// 站内 Telegraph 阅读弹层：拉 /api/telegraph/{chat}/{msg}/read 渲染正文节点树，
// 并提供「下载图片」按钮（后端直接出网抓取落盘）。
const props = defineProps({
  chatId: { type: String, required: true },
  msgId: { type: Number, required: true },
})
const emit = defineEmits(['close'])

const page = ref({ title: '', author: '', views: 0, url: '', content: [] })
const loading = ref(true)
const error = ref('')
const downloading = ref(false)
const lastDir = ref('') // 最近一次下载返回的目录，用于按钮 title 提示

function hostname(u) {
  try { return new URL(u).hostname } catch { return '' }
}

const downloadHint = computed(() => lastDir.value || '')

onMounted(async () => {
  try {
    const d = await api.telegraphRead(props.chatId, props.msgId)
    page.value = d
  } catch (e) {
    error.value = e.message || '加载失败'
  } finally {
    loading.value = false
  }
})

function close() { emit('close') }

async function download() {
  if (downloading.value) return
  downloading.value = true
  try {
    const r = await api.telegraphDownload(props.chatId, props.msgId)
    lastDir.value = r.dir || ''
    if (r.failed > 0) {
      ElMessage.warning(`已下载 ${r.success}/${r.total} 张，失败 ${r.failed} 张（共 ${r.dir}）`)
    } else {
      ElMessage.success(`已下载 ${r.success} 张图片 → ${r.dir}`)
    }
  } catch (e) {
    ElMessage.error(e.message || '下载失败')
  } finally {
    downloading.value = false
  }
}
</script>

<style scoped>
.tr-overlay {
  position: fixed; inset: 0; z-index: 1200;
  background: rgba(0,0,0,.5);
  display: flex; align-items: center; justify-content: center;
  padding: 40px 60px;
}
.tr-card {
  width: min(760px, 100%); height: 100%;
  display: flex; flex-direction: column;
  background: var(--bg-panel);
  border-radius: var(--radius-lg);
  box-shadow: 0 16px 48px rgba(0,0,0,.35);
  overflow: hidden;
}
.tr-head {
  flex: none; display: flex; align-items: center; gap: 12px;
  padding: 14px 18px;
  background: var(--bg-panel);
  border-bottom: 1px solid var(--border);
}
.tr-title-wrap { flex: 1; min-width: 0; }
.tr-title { font-weight: 700; font-size: 16px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.tr-sub { font-size: 12px; color: var(--text-2); margin-top: 2px; display: flex; align-items: center; gap: 8px; }
.tr-site { color: var(--text-3); }
.tr-actions { flex: none; display: flex; align-items: center; gap: 8px; }
.tr-dl {
  height: 32px; padding: 0 12px; border: none; border-radius: var(--radius-sm);
  background: var(--accent); color: #fff; font-size: 12.5px; cursor: pointer;
  display: flex; align-items: center; gap: 6px; transition: .15s;
}
.tr-dl:hover:not(:disabled) { filter: brightness(1.1); }
.tr-dl:disabled { opacity: .55; cursor: default; }
.tr-x {
  width: 32px; height: 32px; border: none; border-radius: var(--radius-sm);
  background: transparent; color: var(--text-3); cursor: pointer;
  display: flex; align-items: center; justify-content: center; transition: .15s;
}
.tr-x:hover { background: var(--bg-hover); color: var(--text); }

.tr-body { flex: 1; min-height: 0; overflow-y: auto; }
.tr-status { padding: 48px 0; text-align: center; color: var(--text-2); font-size: 13px; }
.tr-status.error { color: var(--red); }

.tr-article {
  padding: 28px 40px 48px;
  font-size: 15px; line-height: 1.75; color: var(--text);
  max-width: 680px; margin: 0 auto;
}
/* Telegraph 正文节点样式（延续现有 token，干净阅读排版） */
.tr-article :deep(h1), .tr-article :deep(h2), .tr-article :deep(h3),
.tr-article :deep(h4), .tr-article :deep(h5), .tr-article :deep(h6) {
  font-weight: 700; line-height: 1.35; margin: 1.4em 0 .5em;
}
.tr-article :deep(h1) { font-size: 1.6em; }
.tr-article :deep(h2) { font-size: 1.35em; }
.tr-article :deep(h3) { font-size: 1.18em; }
.tr-article :deep(p) { margin: 0 0 1em; }
.tr-article :deep(figure) { margin: 1.2em 0; }
.tr-article :deep(img) {
  display: block; max-width: 100%; height: auto;
  border-radius: var(--radius); cursor: zoom-in; margin: 0 auto;
}
.tr-article :deep(blockquote) {
  margin: 1em 0; padding: .6em 1em;
  border-left: 3px solid var(--accent);
  background: var(--bg-hover); border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  color: var(--text-2);
}
.tr-article :deep(a) { color: var(--accent); text-decoration: none; }
.tr-article :deep(a:hover) { text-decoration: underline; }
.tr-article :deep(pre) {
  padding: 12px 14px; background: var(--bg); border: 1px solid var(--border);
  border-radius: var(--radius); overflow-x: auto; font-size: 13px;
}
.tr-article :deep(code) { font-family: "Cascadia Code", Consolas, monospace; font-size: .92em; }
.tr-article :deep(ul), .tr-article :deep(ol) { padding-left: 1.6em; margin: 0 0 1em; }
.tr-article :deep(table) { border-collapse: collapse; width: 100%; margin: 1em 0; }
.tr-article :deep(td), .tr-article :deep(th) { border: 1px solid var(--border); padding: 6px 10px; font-size: 13px; }
.tr-article :deep(hr) { border: none; border-top: 1px solid var(--border); margin: 1.4em 0; }
</style>
