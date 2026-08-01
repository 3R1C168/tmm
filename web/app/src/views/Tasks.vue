<template>
  <div class="page tasks-page">
    <!-- 行 1：状态筛选标签 + 快捷按钮 -->
    <div class="filter-bar">
      <div class="status-tabs">
        <button
          v-for="g in statusGroups"
          :key="g.label"
          class="stab"
          :class="{ active: isGroupActive(g) }"
          @click="toggleGroup(g)"
        >{{ g.label }} <span class="count">{{ countByGroup(g) }}</span></button>
      </div>
      <div class="spacer" />
      <div class="quick-actions">
        <button class="qbtn" :disabled="countDone === 0" @click="clearCompleted">清空已完成</button>
        <button class="qbtn" :disabled="countFailedInterrupted === 0" @click="retryFailed">重试失败</button>
      </div>
    </div>

    <!-- 行 2：批量操作工具栏 -->
    <div class="batch-bar">
      <el-checkbox
        :model-value="allInViewSelected"
        :indeterminate="someInViewSelected"
        @change="toggleSelectAllInView"
      >全选</el-checkbox>
      <span v-if="selected.size > 0" class="sel-count">已选 {{ selectedCountInView }} 条</span>
      <div class="spacer" />
      <div class="batch-actions">
        <button class="qbtn" :disabled="selected.size === 0" @click="batchPause">暂停</button>
        <button class="qbtn" :disabled="selected.size === 0" @click="batchStart">开始</button>
        <button class="qbtn danger" :disabled="selected.size === 0" @click="batchDelete">删除</button>
      </div>
    </div>

    <el-empty v-if="!filtered.length" description="暂无任务，在消息页选择媒体下载" />

    <div v-for="t in filtered" :key="t.id" class="task-card card">
      <div class="trow">
        <el-checkbox
          class="pick"
          :model-value="selected.has(t.id)"
          @change="toggleSelect(t.id)"
        />
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
import { ref, computed, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api'
import { useAppStore } from '../stores/app'
import { Clock, Download, Pause, Check, X, TriangleAlert } from 'lucide-vue-next'

const store = useAppStore()
const expanded = ref('')

// --- 选中态：按 id 跟踪，切过滤/SSE 都不丢 ---
const selected = reactive(new Set())

// --- 过滤：底层是 6 状态集合，默认全选 = 显示全部 ---
const filterStatuses = reactive(new Set(['pending', 'running', 'paused', 'done', 'failed', 'interrupted']))

// 状态归并 4 组（UI 展示用）+ 全部
const statusGroups = [
  { label: '全部', statuses: ['pending', 'running', 'paused', 'done', 'failed', 'interrupted'] },
  { label: '进行中', statuses: ['pending', 'running'] },
  { label: '已完成', statuses: ['done'] },
  { label: '已暂停', statuses: ['paused'] },
  { label: '失败', statuses: ['failed', 'interrupted'] },
]

function countByGroup(g) {
  if (g.label === '全部') return store.tasks.length
  return store.tasks.filter((t) => g.statuses.includes(t.status)).length
}
function isGroupActive(g) {
  return g.statuses.every((s) => filterStatuses.has(s))
}
function toggleGroup(g) {
  if (isGroupActive(g)) g.statuses.forEach((s) => filterStatuses.delete(s))
  else g.statuses.forEach((s) => filterStatuses.add(s))
}

// --- 过滤后的任务列表（保持 createdAt 倒序）---
const filtered = computed(() =>
  store.tasks
    .filter((t) => filterStatuses.has(t.status))
    .sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt))
)

// --- 全选 / 已选计数 ---
const allInViewSelected = computed(
  () => filtered.value.length > 0 && filtered.value.every((t) => selected.has(t.id))
)
const someInViewSelected = computed(
  () => filtered.value.some((t) => selected.has(t.id)) && !allInViewSelected.value
)
const selectedCountInView = computed(
  () => filtered.value.filter((t) => selected.has(t.id)).length
)
function toggleSelect(id) {
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
}
function toggleSelectAllInView(checked) {
  const ids = filtered.value.map((t) => t.id)
  if (checked) ids.forEach((id) => selected.add(id))
  else ids.forEach((id) => selected.delete(id))
}

// --- 快捷数量（按钮 disabled 用）---
const countDone = computed(() => store.tasks.filter((t) => t.status === 'done').length)
const countFailedInterrupted = computed(
  () => store.tasks.filter((t) => t.status === 'failed' || t.status === 'interrupted').length
)

// --- 批量动作 ---
async function batchPause() {
  const ids = [...selected]
  if (!ids.length) return
  try {
    await store.batchAction(ids, 'pause')
    selected.clear()
  } catch (e) { ElMessage.error(e.message) }
}
async function batchStart() {
  const ids = [...selected]
  if (!ids.length) return
  try {
    const r = await store.batchAction(ids, 'resume')
    reportFailures(r, ids.length)
    selected.clear()
  } catch (e) { ElMessage.error(e.message) }
}
async function batchDelete() {
  const ids = [...selected]
  if (!ids.length) return
  try {
    await ElMessageBox.confirm(`确定删除选中的 ${ids.length} 条任务吗？（磁盘文件保留）`, '批量删除', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch { return }
  try {
    await store.batchAction(ids, 'delete')
    selected.clear()
  } catch (e) { ElMessage.error(e.message) }
}

// --- 快捷动作（按状态批量，不经 selected）---
async function clearCompleted() {
  const ids = store.tasks.filter((t) => t.status === 'done').map((t) => t.id)
  if (!ids.length) return
  try {
    await ElMessageBox.confirm(`确定清空 ${ids.length} 条已完成任务吗？（磁盘文件保留）`, '清空已完成', {
      type: 'warning', confirmButtonText: '清空', cancelButtonText: '取消',
    })
  } catch { return }
  try {
    await store.batchAction(ids, 'delete')
    ElMessage.success('已清空')
  } catch (e) { ElMessage.error(e.message) }
}
async function retryFailed() {
  const ids = store.tasks.filter((t) => t.status === 'failed' || t.status === 'interrupted').map((t) => t.id)
  if (!ids.length) return
  try {
    const r = await store.batchAction(ids, 'resume')
    reportFailures(r, ids.length)
  } catch (e) { ElMessage.error(e.message) }
}

// resume 可能部分失败（account 未登录等），统一提示失败项
function reportFailures(r, total) {
  const fails = (r?.results || []).filter((x) => !x.ok)
  if (fails.length === 0) ElMessage.success(`已开始 ${total} 个任务`)
  else ElMessage.warning(`${total - fails.length}/${total} 成功，${fails.length} 个失败`)
}

// --- 单条操作（保留现有，删除时同步清 selected）---
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
    selected.delete(t.id)
    ElMessage.success('已删除')
  } catch (e) { ElMessage.error(e.message) }
}

// --- 格式化 helper（保留现有）---
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
</script>

<style scoped>
/* --- 行 1：状态标签栏 + 快捷按钮 --- */
.filter-bar { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; flex-wrap: wrap; }
.status-tabs { display: flex; gap: 6px; flex-wrap: wrap; }
.stab {
  padding: 4px 10px; border-radius: var(--radius-sm); border: 1px solid var(--border);
  background: var(--bg-panel); color: var(--text-2); font-size: 13px; cursor: pointer;
  display: inline-flex; align-items: center; gap: 4px; transition: all .15s;
}
.stab:hover { border-color: var(--accent); }
.stab.active { background: var(--accent-soft); color: var(--accent); border-color: var(--accent); }
.stab .count { font-size: 12px; opacity: .8; }
.quick-actions { display: flex; gap: 6px; }
.qbtn {
  padding: 4px 10px; border-radius: var(--radius-sm); border: 1px solid var(--border);
  background: var(--bg-panel); color: var(--text-2); font-size: 13px; cursor: pointer;
  transition: all .15s;
}
.qbtn:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
.qbtn.danger:hover:not(:disabled) { border-color: var(--red); color: var(--red); }
.qbtn:disabled { opacity: .45; cursor: not-allowed; }

/* --- 行 2：批量工具栏 --- */
.batch-bar {
  display: flex; align-items: center; gap: 12px;
  padding: 8px 0; margin-bottom: 12px;
  border-bottom: 1px solid var(--border);
}
.sel-count { font-size: 13px; color: var(--text-2); }
.batch-actions { display: flex; gap: 6px; }

.spacer { flex: 1; }

/* --- 任务卡片（保留现有，加 pick 复选框）--- */
.task-card { margin-bottom: 12px; }
.trow { display: flex; align-items: flex-start; gap: 12px; }
.pick { flex: none; margin-top: 14px; }
.tico {
  width: 42px; height: 42px; flex: none; border-radius: var(--radius); display: flex;
  align-items: center; justify-content: center;
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
