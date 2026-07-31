<template>
  <div class="page tasks-page">
    <div class="task-head">
      <h2 class="page-title">下载任务</h2>
      <div class="spacer" />
      <el-radio-group v-model="filter" size="small">
        <el-radio-button value="all">全部</el-radio-button>
        <el-radio-button value="active">进行中</el-radio-button>
        <el-radio-button value="done">已完成</el-radio-button>
      </el-radio-group>
    </div>

    <el-empty v-if="!filtered.length" description="暂无任务，在消息页选择媒体下载" />

    <div v-for="t in filtered" :key="t.id" class="task-card card">
      <div class="trow">
        <div class="tico" :class="statusClass(t.status)"><component :is="statusIcon(t.status)" :size="18" /></div>
        <div class="tmain">
          <div class="tline1">
            <span class="tname">{{ taskName(t) }}</span>
            <el-tag size="small" :type="tagType(t.status)" effect="light">{{ statusText(t.status) }}</el-tag>
          </div>
          <div class="tmeta muted">
            <span>{{ t.files?.length || 0 }} 个文件</span>
            <span v-if="t.totalSize">· {{ fmtBytes(t.downloaded) }} / {{ fmtBytes(t.totalSize) }}</span>
            <span>· {{ fmtTime(t.createdAt) }}</span>
            <span class="mono">· #{{ t.id.slice(0, 8) }}</span>
          </div>

          <el-progress
            v-if="t.totalSize > 0"
            :percentage="percent(t)"
            :status="progressStatus(t.status)"
            :stroke-width="8"
            class="tprog"
          />

          <div v-if="t.error" class="terror"><TriangleAlert :size="14" /> {{ t.error }}</div>

          <div v-if="expanded === t.id && t.files?.length" class="files">
            <div v-for="f in t.files" :key="f.key" class="file-row">
              <span class="fstatus" :class="f.status"><component :is="fileIcon(f.status)" :size="14" /></span>
              <span class="fname" :title="f.path">{{ f.name }}</span>
              <span class="fsize muted">{{ fmtBytes(f.downloaded) }} / {{ fmtBytes(f.size) }}</span>
              <span v-if="f.error" class="ferr" :title="f.error">失败</span>
            </div>
          </div>
        </div>

        <div class="tactions">
          <el-button
            v-if="t.status === 'running'"
            size="small"
            @click="pause(t)"
          >暂停</el-button>
          <el-button
            v-else-if="['pending', 'paused', 'failed', 'interrupted'].includes(t.status)"
            size="small"
            type="primary"
            @click="resume(t)"
          >开始</el-button>
          <el-button
            v-if="t.files?.length"
            size="small"
            text
            @click="expanded = expanded === t.id ? '' : t.id"
          >{{ expanded === t.id ? '收起' : '详情' }}</el-button>
          <el-button
            size="small"
            text
            type="danger"
            @click="remove(t)"
          >删除</el-button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api'
import { useAppStore } from '../stores/app'
import { Clock, Download, Pause, Check, X, TriangleAlert } from 'lucide-vue-next'

const store = useAppStore()
const filter = ref('all')
const expanded = ref('')

const filtered = computed(() => {
  const list = [...store.tasks].sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt))
  if (filter.value === 'active') return list.filter((t) => ['pending', 'running'].includes(t.status))
  if (filter.value === 'done') return list.filter((t) => t.status === 'done')
  return list
})

const percent = (t) => (t.totalSize > 0 ? Math.min(100, Math.round((t.downloaded / t.totalSize) * 100)) : 0)
const taskName = (t) => {
  if (t.files?.length) return t.files[0].fromName || t.urls[0]
  return t.urls?.[0] || '下载任务'
}

function statusText(s) {
  return { pending: '排队中', running: '下载中', paused: '已暂停', done: '已完成', failed: '失败', interrupted: '已中断' }[s] || s
}
const statusIconMap = { pending: Clock, running: Download, paused: Pause, done: Check, failed: X, interrupted: TriangleAlert }
const fileIconMap = { pending: Clock, running: Download, done: Check, failed: X }
function statusIcon(s) { return statusIconMap[s] || null }
function fileIcon(s) { return fileIconMap[s] || null }
function statusClass(s) { return `s-${s}` }
function tagType(s) {
  return { running: 'primary', done: 'success', failed: 'danger', paused: 'warning', interrupted: 'warning', pending: 'info' }[s] || 'info'
}
function progressStatus(s) {
  if (s === 'done') return 'success'
  if (s === 'failed') return 'exception'
  return ''
}

function fmtBytes(n) {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(n >= 100 || i === 0 ? 0 : 1)} ${u[i]}`
}
function fmtTime(t) {
  if (!t) return ''
  const d = new Date(t)
  return d.toLocaleString('zh-CN', { hour12: false })
}

async function pause(t) {
  try { await api.pauseTask(t.id) } catch (e) { ElMessage.error(e.message) }
}
async function resume(t) {
  try { await api.resumeTask(t.id); ElMessage.success('已开始') } catch (e) { ElMessage.error(e.message) }
}
async function remove(t) {
  try {
    await ElMessageBox.confirm('确定删除此任务？下载记录将清除（已下载文件保留）。', '删除任务', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch { return }
  try {
    await api.deleteTask(t.id)
    store.tasks = store.tasks.filter((x) => x.id !== t.id)
    ElMessage.success('已删除')
  } catch (e) { ElMessage.error(e.message) }
}
</script>

<style scoped>
.task-head { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; }
.task-head .page-title { margin: 0; }
.task-head .spacer { flex: 1; }

.task-card { margin-bottom: 12px; }
.trow { display: flex; align-items: flex-start; gap: 12px; }
.tico {
  width: 42px; height: 42px; flex: none; border-radius: var(--radius); display: flex;
  align-items: center; justify-content: center;
  /* 用浅色背景 + 状态色前景，替代原来实色块背景，线性图标质感更轻 */
  background: var(--bg-hover);
}
.tico.s-pending { color: var(--text-3); }
.tico.s-running { color: #3b82f6; }
.tico.s-paused { color: #f59e0b; }
.tico.s-done { color: #10b981; }
.tico.s-failed { color: #ef4444; }
.tico.s-interrupted { color: #eab308; }

.tmain { flex: 1; min-width: 0; }
.tline1 { display: flex; align-items: center; gap: 10px; }
.tname { font-weight: 600; font-size: 14.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.tmeta { font-size: 12px; margin-top: 3px; display: flex; flex-wrap: wrap; gap: 6px; }
.tprog { margin-top: 10px; max-width: 560px; }
.terror { margin-top: 8px; color: var(--red); font-size: 12.5px; display: flex; align-items: center; gap: 4px; }

.files { margin-top: 12px; border-top: 1px solid var(--border); padding-top: 8px; display: flex; flex-direction: column; gap: 4px; max-height: 260px; overflow-y: auto; }
.file-row { display: flex; align-items: center; gap: 8px; font-size: 12.5px; }
.fstatus { width: 18px; display: inline-flex; align-items: center; justify-content: center; }
.fstatus.pending { color: var(--text-3); }
.fstatus.running { color: #3b82f6; }
.fstatus.done { color: var(--green); }
.fstatus.failed { color: var(--red); }
.fname { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.fsize { flex: none; }
.ferr { color: var(--red); flex: none; }

.tactions { display: flex; flex-direction: column; gap: 6px; flex: none; }
</style>
