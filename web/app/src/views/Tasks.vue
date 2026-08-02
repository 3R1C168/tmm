<template>
  <div class="dl-page">
    <div class="dl-body">
      <!-- 左侧边栏：状态 / 频道·群组 / 活动，双维度叠加筛选 -->
      <aside class="dl-sidebar" :style="{ width: sidebarWidth + 'px' }">
        <div class="cat-section">
          <div class="cat-title">状态</div>
          <button v-for="c in statusCats" :key="c.key" class="cat-item"
            :class="{ active: statusFilter === c.key }" @click="statusFilter = statusFilter === c.key ? 'all' : c.key">
            <component v-if="c.icon" :is="c.icon" :size="14" />
            <span class="cat-label">{{ c.label }}</span>
            <span class="cat-count">{{ c.count }}</span>
          </button>
        </div>

        <div v-if="sourceCats.length" class="cat-section">
          <div class="cat-title">频道 / 群组</div>
          <button class="cat-item" :class="{ active: !sourceFilter }" @click="sourceFilter = ''">
            <span class="cat-label">全部</span>
            <span class="cat-count">{{ rows.length }}</span>
          </button>
          <button v-for="s in sourceCats" :key="s.name" class="cat-item"
            :class="{ active: sourceFilter === s.name }" @click="sourceFilter = sourceFilter === s.name ? '' : s.name">
            <span class="cat-dot" />
            <span class="cat-label" :title="s.name">{{ s.name || '未知来源' }}</span>
            <span class="cat-count">{{ s.count }}</span>
          </button>
        </div>

        <div class="cat-section">
          <div class="cat-title">活动</div>
          <button class="cat-item" :class="{ active: activityFilter === 'active' }"
            @click="activityFilter = activityFilter === 'active' ? '' : 'active'">
            <Zap :size="14" />
            <span class="cat-label">活跃</span>
            <span class="cat-count">{{ activeCount }}</span>
          </button>
          <button class="cat-item" :class="{ active: activityFilter === 'inactive' }"
            @click="activityFilter = activityFilter === 'inactive' ? '' : 'inactive'">
            <Moon :size="14" />
            <span class="cat-label">非活跃</span>
            <span class="cat-count">{{ inactiveCount }}</span>
          </button>
        </div>
      </aside>

      <!-- 侧边栏宽度拖拽手柄 -->
      <div class="sidebar-resize" @mousedown="startSidebarResize" />

      <!-- 主区：工具栏 + 表头 + 列表 -->
      <main class="dl-main">
        <!-- 工具栏 -->
        <div class="dl-toolbar">
          <div class="toolbar-group">
            <button class="tool-btn" :disabled="!selectedTaskIds.size" @click="batchStart" title="开始">
              <Play :size="16" />
            </button>
            <button class="tool-btn" :disabled="!selectedTaskIds.size" @click="batchPause" title="暂停">
              <Pause :size="16" />
            </button>
            <button class="tool-btn danger" :disabled="!selectedTaskIds.size" @click="batchDelete" title="删除">
              <Trash2 :size="16" />
            </button>
            <button class="tool-btn" :disabled="!selected.size" @click="openBatchRename" title="批量重命名">
              <Pencil :size="16" />
            </button>
            <button class="tool-btn" :class="{ active: multiSelectMode }" @click="toggleMultiSelectMode"
              :title="multiSelectMode ? '多选模式已开启（点首行再点末行选区间，点空白清空）' : '多选模式'">
              <CheckSquare :size="16" />
            </button>
          </div>
          <div class="spacer" />
          <div class="toolbar-group">
            <el-popover :visible="colSettingsOpen" placement="bottom-end" :width="220" trigger="manual">
              <template #reference>
                <button class="tool-btn" :class="{ active: colSettingsOpen }" @click="colSettingsOpen = !colSettingsOpen" title="列设置">
                  <Settings2 :size="16" />
                </button>
              </template>
              <div class="col-settings">
                <div class="col-settings-title">列设置（右键表头输入宽度，勾选显隐，↑↓调顺序）</div>
                <div v-for="(c, i) in configurableColumns" :key="c.key" class="col-row">
                  <el-checkbox v-model="c.visible" @change="saveColumns">{{ c.label }}</el-checkbox>
                  <div class="col-arrows">
                    <button class="col-arrow" :disabled="i === 0" @click="moveColumn(c.key, -1)" title="上移">↑</button>
                    <button class="col-arrow" :disabled="i === configurableColumns.length - 1" @click="moveColumn(c.key, 1)" title="下移">↓</button>
                  </div>
                </div>
                <div class="col-settings-actions">
                  <button class="col-action-btn" @click="saveAsDefault">存为默认</button>
                  <button class="col-action-btn" @click="resetToDefault">恢复默认</button>
                </div>
              </div>
            </el-popover>
          </div>
        </div>

        <!-- 表格区 -->
        <div class="dl-table">
          <!-- 表头（sticky，高度与行一致 34px）-->
          <div class="thead">
            <div class="th th-check">
              <el-checkbox :model-value="allSelected" :indeterminate="someSelected" @change="toggleSelectAll" />
            </div>
            <div v-for="c in visibleColumns" :key="c.key"
              class="th"
              :class="{
                'th-num': ['size','downloaded'].includes(c.key),
                'th-flex': c.width === 0,
                'th-dragging': dragCol === c.key,
                'th-order-target': dragOrderKey && dragOrderKey !== c.key,
              }"
              :style="c.width > 0 ? { width: c.width + 'px', flex: 'none' } : {}"
              draggable="true"
              @dragstart="onColDragStart(c.key)" @dragover.prevent @drop="onColDrop(c.key)"
              @dragend="dragOrderKey = null"
              @contextmenu.prevent="promptColWidth(c.key, c.width)"
            >
              {{ c.label }}
              <span class="col-resize" @mousedown="startColResize(c.key, $event)" />
            </div>
            <!-- 末尾 drop zone：允许把列拖到最后位置 -->
            <div v-if="dragOrderKey" class="th th-end-drop"
              @dragover.prevent @drop="onColDropEnd" @dragend="dragOrderKey = null">
              <span class="drop-hint">⟲</span>
            </div>
          </div>
          <!-- 拖拽列宽时的指示竖线 -->
          <div v-if="dragCol" class="resize-indicator" :style="{ left: dragX + 'px' }" />

          <!-- 列表区 -->
          <div class="tbody">
            <template v-for="g in groupedRows" :key="g.source">
              <!-- 来源分组头：流式聚合信息，不按列对齐（避免空占位） -->
              <div class="group-head" @click="toggleGroup(g.source)">
                <div class="gh-fold">
                  <component :is="collapsedGroups.has(g.source) ? ChevronRight : ChevronDown" :size="14" />
                </div>
                <span class="gh-name">{{ g.source || '未知来源' }}</span>
                <span class="gh-tag">{{ g.rows.length }} 文件</span>
                <span class="gh-tag">{{ fmtBytes(g.downloaded) }} / {{ fmtBytes(g.size) }}</span>
                <span class="gh-tag gh-pct-tag">{{ groupPct(g) }}%</span>
                <span v-if="g.speed > 0" class="gh-tag gh-speed">↓ {{ fmtBytes(g.speed) }}/s</span>
              </div>
              <!-- 文件行：点击行即选择，checkbox 仅显示选中状态 -->
              <div v-show="!collapsedGroups.has(g.source)" v-for="r in g.rows" :key="r.rowId"
                class="trow" :class="{ selected: selected.has(r.rowId), 'multi-mode': multiSelectMode }"
                @click="onRowClick(r, $event)"
                @contextmenu.prevent="onRowContextMenu(r, $event)">
                <div class="td td-check">
                  <el-checkbox :model-value="selected.has(r.rowId)" class="row-cb" />
                </div>
                <template v-for="c in visibleColumns" :key="c.key">
                  <!--文件名列：图标+文件名-->
                  <div v-if="c.key === 'name'" class="td td-name"
                    :style="c.width > 0 ? { width: c.width + 'px', flex: 'none' } : {}">
                    <component :is="fileIcon(r.file.status)" :size="14" class="ficon" :class="r.file.status" />
                    <span class="fname" :title="r.file.name || fileLabel(r.file)"
                      @dblclick.stop.prevent="openRenameSingle(r)">{{ r.file.name || fileLabel(r.file) }}</span>
                  </div>
                  <!--消息ID列-->
                  <div v-else-if="c.key === 'msgid'" class="td td-mono"
                    :style="{ width: c.width + 'px', flex: 'none' }">
                    {{ r.file.msgId ? '#' + r.file.msgId : '—' }}
                  </div>
                  <!--来源列-->
                  <div v-else-if="c.key === 'source'" class="td td-source"
                    :style="{ width: c.width + 'px', flex: 'none' }">
                    <span class="src-name" :title="r.file.fromName">{{ r.file.fromName || '—' }}</span>
                  </div>
                  <!--备注列：消息文本 caption-->
                  <div v-else-if="c.key === 'note'" class="td td-note"
                    :style="{ width: c.width + 'px', flex: 'none' }">
                    <span class="note-text" :title="r.file.caption">{{ firstLine(r.file.caption) || '—' }}</span>
                  </div>
                  <!--大小列-->
                  <div v-else-if="c.key === 'size'" class="td td-num"
                    :style="{ width: c.width + 'px', flex: 'none' }">
                    {{ r.file.size > 0 ? fmtBytes(r.file.size) : '—' }}
                  </div>
                  <!--进度列-->
                  <div v-else-if="c.key === 'progress'" class="td td-progress"
                    :style="{ width: c.width + 'px', flex: 'none' }">
                    <el-progress v-if="r.file.size > 0" :percentage="filePct(r.file)" :status="progressStatus(r.file.status)" :stroke-width="4" :show-text="false" />
                    <span class="pct-text">{{ r.file.size > 0 ? filePct(r.file) + '%' : '—' }}</span>
                  </div>
                  <!--已下载列-->
                  <div v-else-if="c.key === 'downloaded'" class="td td-num"
                    :style="{ width: c.width + 'px', flex: 'none' }">
                    {{ r.file.size > 0 ? fmtBytes(r.file.downloaded) + ' / ' + fmtBytes(r.file.size) : '—' }}
                  </div>
                  <!--状态列-->
                  <div v-else-if="c.key === 'status'" class="td td-status"
                    :style="{ width: c.width + 'px', flex: 'none' }">
                    <span class="fstat" :class="r.file.status">{{ fileStatusText(r.file.status) }}</span>
                  </div>
                </template>
              </div>
            </template>

            <el-empty v-if="!groupedRows.length" description="暂无下载，在消息页选择媒体下载" />
          </div>
        </div>
      </main>
    </div>

    <!-- 底部状态栏 -->
    <footer class="dl-statusbar">
      <span>{{ totalFiles }} 文件</span>
      <span class="sb-sep">·</span>
      <span>{{ runningCount }} 下载中</span>
      <span class="sb-sep">·</span>
      <span class="sb-speed">↓ {{ fmtBytes(totalSpeed) }}/s</span>
      <span class="sb-sep">·</span>
      <span>已下载 {{ fmtBytes(totalDownloaded) }} / {{ fmtBytes(totalSize) }}</span>
    </footer>

    <!-- 删除确认对话框（复用现有） -->
    <el-dialog v-model="delDialog.show" class="del-dialog" width="420px" :show-close="true" align-center>
      <template #header>
        <div class="del-header">
          <TriangleAlert :size="18" class="del-warn-icon" />
          <span>{{ delDialog.title }}</span>
        </div>
      </template>
      <div class="del-body">
        <p class="del-msg">{{ delDialog.message }}</p>
        <div class="del-file-opt">
          <span class="del-opt-label">同时删除已下载文件</span>
          <el-radio-group v-model="delDialog.deleteFiles">
            <el-radio :value="true">是</el-radio>
            <el-radio :value="false">否</el-radio>
          </el-radio-group>
        </div>
      </div>
      <template #footer>
        <el-button @click="delDialog.show = false">取消</el-button>
        <el-button type="danger" :loading="delDialog.loading" @click="confirmDelete">删除</el-button>
      </template>
    </el-dialog>

    <!-- 重命名对话框：双击文件名触发，默认填 caption 首行 -->
    <el-dialog v-model="renameDialog.show" class="del-dialog" width="460px" :show-close="true" align-center
      @open="focusRenameInput">
      <template #header>
        <div class="del-header">
          <Pencil :size="18" class="del-warn-icon" style="color: var(--accent)" />
          <span>重命名文件</span>
        </div>
      </template>
      <div class="del-body">
        <p class="del-msg">消息 #{{ renameDialog.msgId }} — 输入新文件名（不含消息ID前缀，后端会自动添加）：</p>
        <el-input v-model="renameDialog.name" type="textarea" :autosize="{ minRows: 1, maxRows: 8 }"
          placeholder="输入文件名" :disabled="renameDialog.loading"
          @keydown.enter.prevent="confirmRename"
          @keydown="onRenameKeydown" ref="renameInput" />
      </div>
      <template #footer>
        <el-button @click="renameDialog.show = false">取消</el-button>
        <el-button type="primary" :loading="renameDialog.loading" @click="confirmRename">确定</el-button>
      </template>
    </el-dialog>

    <!-- 批量重命名对话框：勾选多个文件后点工具栏铅笔触发
         强制加 #msgId 前缀防冲突；可用 {n} 占位符按选中顺序编号 -->
    <el-dialog v-model="batchRenameDialog.show" class="del-dialog batch-rename-dialog" width="520px" :show-close="true" align-center
      @open="focusBatchRenameInput">
      <template #header>
        <div class="del-header">
          <Pencil :size="18" class="del-warn-icon" style="color: var(--accent)" />
          <span>批量重命名（{{ batchRenameDialog.count }} 个文件）</span>
        </div>
      </template>
      <div class="del-body">
        <p class="del-msg">所有选中文件套用同一个新名称，自动加 <code>#消息ID</code> 前缀防止冲突。</p>
        <p class="del-msg del-tip">可用 <code>&#123;n&#125;</code> 作为序号占位符（按选中顺序 1,2,3…），不用则所有文件同名。</p>
        <el-input v-model="batchRenameDialog.name" type="textarea" :autosize="{ minRows: 1, maxRows: 4 }"
          placeholder="输入文件名（不含扩展名，后端按原文件补全）" :disabled="batchRenameDialog.loading"
          @keydown.enter.prevent="confirmBatchRename"
          @keydown="onRenameKeydown" ref="batchRenameInput" />
        <p class="del-msg del-preview" v-if="batchRenamePreview">预览：{{ batchRenamePreview }}</p>
      </div>
      <template #footer>
        <el-button @click="batchRenameDialog.show = false">取消</el-button>
        <el-button type="primary" :loading="batchRenameDialog.loading" @click="confirmBatchRename">重命名</el-button>
      </template>
    </el-dialog>

    <!-- 右键菜单：右键文件行弹出，根据选中态显示单文件/批量操作 -->
    <div v-if="ctxMenu.show" class="ctx-menu" :style="{ left: ctxMenu.x + 'px', top: ctxMenu.y + 'px' }"
      @click.stop @contextmenu.prevent>
      <button class="ctx-item" @click="ctxRename">
        {{ ctxMenu.batch ? `批量重命名（${ctxMenu.count} 个文件）` : '重命名' }}
      </button>
      <button class="ctx-item" @click="ctxDelete">删除</button>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, reactive, watch, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api'
import { useAppStore } from '../stores/app'
import {
  Clock, Download, Pause, Check, X, TriangleAlert, MessageSquare,
  Play, Trash2, Settings2, Zap, Moon, ChevronDown, ChevronRight, Pencil,
  CheckSquare,
} from 'lucide-vue-next'

const store = useAppStore()

// --- 侧边栏宽度（可拖拽，持久化）---
const SIDEBAR_KEY = 'tmm.sidebarWidth'
const sidebarWidth = ref(parseInt(localStorage.getItem(SIDEBAR_KEY) || '160'))
function startSidebarResize(e) {
  e.preventDefault()
  const startX = e.clientX
  const startW = sidebarWidth.value
  const onMove = (ev) => {
    const nw = Math.max(120, Math.min(300, startW + ev.clientX - startX))
    sidebarWidth.value = nw
  }
  const onUp = () => {
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    localStorage.setItem(SIDEBAR_KEY, String(sidebarWidth.value))
  }
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
}

// --- 拍平文件行：每个 file 一行，携带所属 task 引用 ---
// pending 文件（排队中，Limit 并发未轮到）预填时 fromName/name 为空，
// 从同 task 其他已解析文件继承 fromName，保证归入正确来源分组。
const rows = computed(() => {
  const out = []
  for (const t of store.tasks) {
    // 找同 task 第一个有 fromName/caption 的文件作为继承源
    const fallbackFrom = (t.files || []).find(f => f.fromName)
    const fallbackCaption = (t.files || []).find(f => f.caption)
    for (const f of (t.files || [])) {
      out.push({
        rowId: t.id + '/' + f.key,
        taskId: t.id,
        task: t,
        file: {
          ...f,
          fromName: f.fromName || (fallbackFrom ? fallbackFrom.fromName : ''),
          caption: f.caption || (fallbackCaption ? fallbackCaption.caption : ''),
        },
      })
    }
  }
  return out
})

// --- 侧边栏筛选：三个独立维度，叠加生效 ---
const statusFilter = ref('all')   // all | downloading | done | paused | failed
const sourceFilter = ref('')      // fromName 字符串，空=全部
const activityFilter = ref('')    // active | inactive | ''(全部)

const fileStatusMap = {
  pending: 'paused', running: 'downloading', done: 'done', failed: 'failed',
}
function rowStatus(file) {
  return fileStatusMap[file.status] || file.status
}

const filteredRows = computed(() => {
  return rows.value.filter((r) => {
    // 状态维度
    if (statusFilter.value !== 'all') {
      const s = rowStatus(r.file)
      if (statusFilter.value === 'downloading' && s !== 'downloading') return false
      if (statusFilter.value === 'done' && s !== 'done') return false
      if (statusFilter.value === 'paused' && s !== 'paused') return false
      if (statusFilter.value === 'failed' && s !== 'failed') return false
    }
    // 来源维度
    if (sourceFilter.value && (r.file.fromName || '') !== sourceFilter.value) return false
    // 活动维度
    if (activityFilter.value === 'active' && !(r.task.status === 'running' && r.task.speed > 0)) return false
    if (activityFilter.value === 'inactive' && (r.task.status === 'running' && r.task.speed > 0)) return false
    return true
  })
})

// --- 分组：按 fromName 归组，保留首次出现顺序 ---
const groupedRows = computed(() => {
  const map = new Map()
  for (const r of filteredRows.value) {
    const src = r.file.fromName || ''
    if (!map.has(src)) {
      map.set(src, { source: src, rows: [], downloaded: 0, size: 0, speed: 0 })
    }
    const g = map.get(src)
    g.rows.push(r)
    g.downloaded += r.file.downloaded || 0
    g.size += r.file.size || 0
    // 聚合速率：同来源下不同 task 的 speed 之和（去重 taskId）
  }
  // 聚合 speed：按 taskId 去重后求和
  for (const g of map.values()) {
    const taskSpeeds = new Map()
    for (const r of g.rows) {
      if (r.task.status === 'running' && !taskSpeeds.has(r.taskId)) {
        taskSpeeds.set(r.taskId, r.task.speed || 0)
      }
    }
    g.speed = [...taskSpeeds.values()].reduce((a, b) => a + b, 0)
  }
  return [...map.values()]
})

// --- 侧边栏分类统计 ---
const statusCats = computed(() => [
  { key: 'all', label: '全部', count: rows.value.length, icon: null },
  { key: 'downloading', label: '下载中', count: rows.value.filter(r => rowStatus(r.file) === 'downloading').length, icon: Download },
  { key: 'done', label: '已完成', count: rows.value.filter(r => rowStatus(r.file) === 'done').length, icon: Check },
  { key: 'paused', label: '已暂停', count: rows.value.filter(r => rowStatus(r.file) === 'paused').length, icon: Pause },
  { key: 'failed', label: '失败', count: rows.value.filter(r => rowStatus(r.file) === 'failed').length, icon: X },
])

const sourceCats = computed(() => {
  const map = new Map()
  for (const r of rows.value) {
    const src = r.file.fromName || ''
    if (!src) continue // 空来源不作为独立分类（避免和「全部」的 sourceFilter='' 冲突）
    map.set(src, (map.get(src) || 0) + 1)
  }
  return [...map.entries()].map(([name, count]) => ({ name, count })).sort((a, b) => b.count - a.count)
})

const activeCount = computed(() => rows.value.filter(r => r.task.status === 'running' && r.task.speed > 0).length)
const inactiveCount = computed(() => rows.value.length - activeCount.value)

// --- 折叠分组 ---
const collapsedGroups = reactive(new Set(JSON.parse(localStorage.getItem('tmm.collapsedGroups') || '[]')))
function toggleGroup(src) {
  if (collapsedGroups.has(src)) collapsedGroups.delete(src)
  else collapsedGroups.add(src)
  localStorage.setItem('tmm.collapsedGroups', JSON.stringify([...collapsedGroups]))
}

// --- 列配置（宽度 + 顺序 + 显隐，全部持久化）---
// 优先级：用户默认(DEFAULT_KEY) > 普通配置(COLS_KEY) > 内置默认
const COLS_KEY = 'tmm.taskColumns'
const DEFAULT_KEY = 'tmm.taskColumnsDefault'
const builtinDefaults = [
  { key: 'name', label: '文件名', width: 0, visible: true },
  { key: 'msgid', label: '消息ID', width: 80, visible: true },
  { key: 'source', label: '来源', width: 130, visible: false },  // 分组名已是来源，默认隐藏
  { key: 'note', label: '备注', width: 180, visible: true },
  { key: 'size', label: '大小', width: 80, visible: true },
  { key: 'progress', label: '进度', width: 180, visible: true },
  { key: 'downloaded', label: '已下载', width: 130, visible: true },
  { key: 'status', label: '状态', width: 70, visible: true },
]
function loadColumns(saved) {
  if (!saved || !saved.order) return [...builtinDefaults]
  const byKey = Object.fromEntries(builtinDefaults.map(d => [d.key, d]))
  return saved.order
    .filter(k => byKey[k])
    .map(k => ({ ...byKey[k], width: saved.widths?.[k] ?? byKey[k].width, visible: saved.visible?.[k] ?? byKey[k].visible }))
    .concat(builtinDefaults.filter(d => !saved.order.includes(d.key)))
}
const columns = reactive(loadColumns(
  JSON.parse(localStorage.getItem(DEFAULT_KEY) || 'null') ||
  JSON.parse(localStorage.getItem(COLS_KEY) || 'null')
))
const visibleColumns = computed(() => columns.filter(c => c.visible))
const configurableColumns = computed(() => columns.filter(c => c.key !== 'name')) // 文件名始终显示
function col(key) { return columns.find(c => c.key === key) || { visible: true, width: 0 } }
const colSettingsOpen = ref(false)
function saveColumns() {
  localStorage.setItem(COLS_KEY, JSON.stringify({
    order: columns.map(c => c.key),
    widths: Object.fromEntries(columns.map(c => [c.key, c.width])),
    visible: Object.fromEntries(columns.map(c => [c.key, c.visible])),
  }))
}

// --- 列宽拖拽 ---
const dragCol = ref(null) // 正在拖拽宽度的列 key
const dragX = ref(0)       // 拖拽中鼠标 x 坐标（用于显示指示线）
function startColResize(key, e) {
  e.preventDefault()
  e.stopPropagation()
  const startX = e.clientX
  const c = col(key)
  const startWidth = c.width || 100
  dragCol.value = key
  dragX.value = e.clientX
  const onMove = (ev) => {
    const delta = ev.clientX - startX
    const nw = Math.max(50, startWidth + delta)
    c.width = nw
    dragX.value = ev.clientX
  }
  const onUp = () => {
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    dragCol.value = null
    dragX.value = 0
    saveColumns()
  }
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
}

// --- 列顺序拖拽（HTML5 drag-drop）---
const dragOrderKey = ref(null)
function onColDragStart(key) { dragOrderKey.value = key }
function onColDrop(targetKey) {
  if (!dragOrderKey.value || dragOrderKey.value === targetKey) return
  const from = columns.findIndex(c => c.key === dragOrderKey.value)
  const to = columns.findIndex(c => c.key === targetKey)
  if (from < 0 || to < 0) return
  const [moved] = columns.splice(from, 1)
  columns.splice(to, 0, moved)
  dragOrderKey.value = null
  saveColumns()
}
// 拖到末尾：把拖拽列移到 columns 数组最后
function onColDropEnd() {
  if (!dragOrderKey.value) return
  const from = columns.findIndex(c => c.key === dragOrderKey.value)
  if (from < 0) return
  const [moved] = columns.splice(from, 1)
  columns.push(moved)
  dragOrderKey.value = null
  saveColumns()
}
// 列设置 popover 里的上下移动按钮（不依赖 HTML5 drag）
function moveColumn(key, dir) {
  const i = columns.findIndex(c => c.key === key)
  const j = i + dir
  if (i < 0 || j < 0 || j >= columns.length) return
  const [moved] = columns.splice(i, 1)
  columns.splice(j, 0, moved)
  saveColumns()
}
// 右键表头：弹输入框设列宽（px）。输入 0 = 自适应弹性。
async function promptColWidth(key, currentWidth) {
  try {
    const { value } = await ElMessageBox.prompt(
      `输入列宽（px），0 = 自适应弹性。当前：${currentWidth || '自适应'}`,
      '调整列宽',
      { confirmButtonText: '设置', cancelButtonText: '取消', inputValue: String(currentWidth || 0) },
    )
    const w = parseInt(value)
    if (isNaN(w) || w < 0) { ElMessage.error('请输入非负整数'); return }
    col(key).width = w
    saveColumns()
    ElMessage.success(w === 0 ? '已设为自适应' : `已设为 ${w}px`)
  } catch { /* 用户取消 */ }
}
// 把当前列配置（顺序+宽度+显隐）存为用户默认
function saveAsDefault() {
  localStorage.setItem(DEFAULT_KEY, localStorage.getItem(COLS_KEY) || JSON.stringify({
    order: columns.map(c => c.key),
    widths: Object.fromEntries(columns.map(c => [c.key, c.width])),
    visible: Object.fromEntries(columns.map(c => [c.key, c.visible])),
  }))
  ElMessage.success('已存为默认')
}
// 恢复到内置默认（清空用户默认 + 重置当前）
function resetToDefault() {
  localStorage.removeItem(COLS_KEY)
  localStorage.removeItem(DEFAULT_KEY)
  // 重置 columns 到 defaults
  const defaults = [
    { key: 'name', label: '文件名', width: 0, visible: true },
    { key: 'msgid', label: '消息ID', width: 80, visible: true },
    { key: 'source', label: '来源', width: 130, visible: false },
    { key: 'note', label: '备注', width: 180, visible: true },
    { key: 'size', label: '大小', width: 80, visible: true },
    { key: 'progress', label: '进度', width: 180, visible: true },
    { key: 'downloaded', label: '已下载', width: 130, visible: true },
    { key: 'status', label: '状态', width: 70, visible: true },
  ]
  columns.splice(0, columns.length, ...defaults)
  saveColumns()
  ElMessage.success('已恢复默认')
}

// 单元格内容渲染：按列 key 返回文件行的文本/组件
function cellContent(file, key) {
  switch (key) {
    case 'name': return null // 特殊渲染（图标+文件名）
    case 'msgid': return file.msgId ? `#${file.msgId}` : '—'
    case 'source': return file.fromName || '—'
    case 'note': return '' // 特殊渲染（caption）
    case 'size': return file.size > 0 ? fmtBytes(file.size) : '—'
    case 'progress': return null // 特殊渲染（进度条）
    case 'downloaded': return file.size > 0 ? `${fmtBytes(file.downloaded)} / ${fmtBytes(file.size)}` : '—'
    case 'status': return null // 特殊渲染（状态文字）
    default: return ''
  }
}

// --- 勾选（按 rowId 跟踪，切筛选不丢） ---
const selected = reactive(new Set())
const selectedTaskIds = computed(() => {
  const ids = new Set()
  for (const id of selected) {
    const parts = id.split('/')
    ids.add(parts[0])
  }
  return ids
})
const allSelected = computed(() => filteredRows.value.length > 0 && filteredRows.value.every(r => selected.has(r.rowId)))
const someSelected = computed(() => filteredRows.value.some(r => selected.has(r.rowId)) && !allSelected.value)
function toggleSelect(rowId) {
  if (selected.has(rowId)) selected.delete(rowId)
  else selected.add(rowId)
}
function toggleSelectAll(checked) {
  const ids = filteredRows.value.map(r => r.rowId)
  if (checked) ids.forEach(id => selected.add(id))
  else ids.forEach(id => selected.delete(id))
}

// --- 多选模式开关 + 两击范围选择 ---
// 默认关闭：点击 checkbox 单选切换（原行为）。
// 开启后（无需 Shift）：
//   第一次点击 → 清空选中，选中当前行作「首行」锚点
//   第二次点击 → 选中首行到当前行之间所有行（含首尾），重置锚点准备下一轮
//   再次点击 → 视为新首行，重新开始
// 这样首行点击即时有选中指示，末行点击后整段高亮。
const MULTI_KEY = 'tmm.multiSelectMode'
const multiSelectMode = ref(localStorage.getItem(MULTI_KEY) === '1')
function toggleMultiSelectMode() {
  multiSelectMode.value = !multiSelectMode.value
  localStorage.setItem(MULTI_KEY, multiSelectMode.value ? '1' : '0')
  // 关闭多选时清空选中与锚点，避免残留
  if (!multiSelectMode.value) {
    selected.clear()
    anchorRowId = null
  }
}
let anchorRowId = null  // 多选模式首行锚点；null 表示下一次点击是新首行
// 多选模式核心：两击范围选择
function multiSelectPick(rowId) {
  const ids = filteredRows.value.map(x => x.rowId)
  if (anchorRowId == null) {
    selected.clear()
    selected.add(rowId)
    anchorRowId = rowId
    return
  }
  const a = ids.indexOf(anchorRowId)
  const b = ids.indexOf(rowId)
  if (a >= 0 && b >= 0) {
    const [from, to] = a < b ? [a, b] : [b, a]
    selected.clear()
    for (let i = from; i <= to; i++) selected.add(ids[i])
  } else {
    selected.clear()
    selected.add(rowId)
  }
  anchorRowId = null
}
// 统一行点击：单选模式 toggle 当前行；多选模式两击范围选择
// e.detail > 1 表示双击的第二次 click，跳过避免干扰双击重命名
function onRowClick(r, e) {
  e.preventDefault()
  if (e.detail > 1) return // 双击的后续 click，交给 dblclick 处理
  if (multiSelectMode.value) {
    multiSelectPick(r.rowId)
  } else {
    anchorRowId = r.rowId
    toggleSelect(r.rowId)
  }
}

// --- 右键菜单：右键文件行弹出，根据选中态显示单文件/批量重命名 ---
const ctxMenu = reactive({ show: false, x: 0, y: 0, rowId: '', batch: false, count: 0 })
function onRowContextMenu(r, e) {
  // 右键的文件若不在选中集合里，先选中它（清空其他）——符合资源管理器习惯
  if (!selected.has(r.rowId)) {
    selected.clear()
    selected.add(r.rowId)
    anchorRowId = r.rowId
  }
  const isBatch = selected.size > 1
  ctxMenu.rowId = r.rowId
  ctxMenu.batch = isBatch
  ctxMenu.count = selected.size
  ctxMenu.x = e.clientX
  ctxMenu.y = e.clientY
  ctxMenu.show = true
}
function closeCtxMenu() { ctxMenu.show = false }
function ctxRename() {
  ctxMenu.show = false
  if (ctxMenu.batch) {
    openBatchRename()
  } else {
    // 单文件重命名：找到该行数据调 openRename
    const row = rows.value.find(r => r.rowId === ctxMenu.rowId)
    if (row) openRenameSingle(row)
  }
}
function ctxDelete() {
  ctxMenu.show = false
  batchDelete()
}

// --- document 级空白点击清空（仅多选模式）---
// 用户真实点击的 target 可靠（CUA 合成事件不可靠，但手动操作没问题）。
// 点文件行、工具栏、对话框、侧边栏、表头、分组头、footer 等区域不清空。
function onDocClick(e) {
  // 关闭右键菜单（点菜单外任意处）
  if (ctxMenu.show) {
    if (!e.target || !e.target.closest || !e.target.closest('.ctx-menu')) {
      ctxMenu.show = false
    }
  }
  if (!multiSelectMode.value) return
  const t = e.target
  if (!t || !t.closest) return
  const protectors = ['.trow', '.dl-toolbar', '.el-dialog', '.dl-sidebar', '.thead', '.group-head', '.dl-statusbar', '.ctx-menu']
  for (const sel of protectors) {
    if (t.closest(sel)) return
  }
  selected.clear()
  anchorRowId = null
}
if (typeof window !== 'undefined') {
  window.addEventListener('click', onDocClick, true)
}

// --- 删除对话框（复用现有逻辑） ---
const delDialog = reactive({
  show: false, title: '删除任务', message: '', deleteFiles: false, loading: false, pending: null,
})
function openDeleteDialog(title, message, action) {
  delDialog.title = title
  delDialog.message = message
  delDialog.deleteFiles = false
  delDialog.loading = false
  delDialog.pending = action
  delDialog.show = true
}
async function confirmDelete() {
  if (!delDialog.pending) return
  delDialog.loading = true
  try {
    await delDialog.pending(delDialog.deleteFiles)
    delDialog.show = false
    ElMessage.success('已删除')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    delDialog.loading = false
  }
}

// --- 重命名：双击文件名触发，默认填 caption 首行 ---
// Ctrl+A 在重命名输入框里只选中框内文本（阻止全选整个网页）
const renameInput = ref(null)
function onRenameKeydown(e) {
  if ((e.ctrlKey || e.metaKey) && e.key === 'a') {
    e.preventDefault()
    e.stopPropagation()
    // 找到 el-input 内部的原生 textarea
    const ta = e.target
    if (ta && typeof ta.select === 'function') {
      ta.select()
    }
  }
}
// 对话框打开后焦点设到输入框并全选，让 Ctrl+A 作用于 textarea 而非 document
async function focusRenameInput() {
  await nextTick()
  await nextTick() // el-input 渲染需要两帧
  const ta = document.querySelector('.del-dialog textarea')
  if (ta) { ta.focus(); ta.select() }
}

const renameDialog = reactive({
  show: false, name: '', msgId: 0, taskId: '', key: '', loading: false,
})
function openRenameSingle(r) {
  // 单文件重命名：判断是否已被重命名过（原始媒体文件名主体通常是纯数字）
  const currentName = (r.file.name || '').replace(/^#\d+\s+/, '')
  const baseName = currentName.replace(/\.[^.]+$/, '') // 去扩展名
  const isRenamed = baseName && !/^\d+$/.test(baseName)

  let defaultName
  if (isRenamed) {
    defaultName = currentName
  } else {
    defaultName = (r.file.caption || '').trim() || currentName
  }
  renameDialog.name = defaultName
  renameDialog.msgId = r.file.msgId || 0
  renameDialog.taskId = r.taskId
  renameDialog.key = r.file.key
  renameDialog.loading = false
  renameDialog.show = true
}
async function confirmRename() {
  // 文件名不能含换行符，多行文本的换行替换成空格
  const cleanName = renameDialog.name.trim().replace(/[\r\n]+/g, ' ')
  if (!cleanName) { ElMessage.error('文件名不能为空'); return }
  renameDialog.loading = true
  try {
    await api.renameFile(renameDialog.taskId, renameDialog.key, cleanName)
    renameDialog.show = false
    ElMessage.success('已重命名')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    renameDialog.loading = false
  }
}

// --- 批量重命名：勾选多个文件 → 工具栏铅笔 → 一个名字套到所有文件 ---
// 强制加 #msgId 前缀（不依赖设置开关），{n} 占位符按选中顺序编号。
const batchRenameInput = ref(null)
const batchRenameDialog = reactive({
  show: false, name: '', count: 0, entries: [], loading: false,
})
// 预览：用前两个选中文件的实际 msgId 演示最终名
const batchRenamePreview = computed(() => {
  if (!batchRenameDialog.name.trim() || !batchRenameDialog.entries.length) return ''
  const tpl = batchRenameDialog.name.trim().replace(/[\r\n]+/g, ' ')
  const hasN = tpl.includes('{n}')
  const sample = batchRenameDialog.entries.slice(0, 2)
  return sample.map((e, i) => {
    const body = hasN ? tpl.replace(/\{n\}/g, String(i + 1)) : tpl
    return `#${e.msgId} ${body}.<扩展名>`
  }).join('，') + (batchRenameDialog.entries.length > 2 ? '，…' : '')
})
async function focusBatchRenameInput() {
  await nextTick()
  await nextTick()
  const ta = document.querySelector('.batch-rename-dialog textarea')
  if (ta) { ta.focus(); ta.select() }
}
function openBatchRename() {
  // 把 selected（rowId = taskId/fileKey）解析回 (taskId, key, msgId, caption) 列表
  const entries = []
  for (const rowId of selected) {
    const sep = rowId.indexOf('/')
    if (sep < 0) continue
    const taskId = rowId.slice(0, sep)
    const key = rowId.slice(sep + 1)
    const row = rows.value.find(r => r.rowId === rowId)
    entries.push({ taskId, key, msgId: row?.file.msgId || 0, caption: row?.file.caption || '' })
  }
  if (!entries.length) { ElMessage.warning('请先勾选文件'); return }
  batchRenameDialog.entries = entries
  batchRenameDialog.count = entries.length
  // 默认填充：跟单文件重命名一致用完整 caption；
  // 同一 task（同消息来源）内文件填该 task 的完整 caption，跨 task 留空避免误导
  const taskIds = new Set(entries.map(e => e.taskId))
  let defaultName = ''
  if (taskIds.size === 1) {
    const caps = entries.map(e => (e.caption || '').trim()).filter(Boolean)
    if (caps.length) defaultName = caps[0]
  }
  batchRenameDialog.name = defaultName
  batchRenameDialog.loading = false
  batchRenameDialog.show = true
}
async function confirmBatchRename() {
  const tpl = batchRenameDialog.name.trim().replace(/[\r\n]+/g, ' ')
  if (!tpl) { ElMessage.error('文件名不能为空'); return }
  const hasN = tpl.includes('{n}')
  batchRenameDialog.loading = true
  let ok = 0, fail = 0
  const fails = []
  // 顺序执行：并发 rename 同一 task 可能争锁，且按序号顺序更直观
  for (let i = 0; i < batchRenameDialog.entries.length; i++) {
    const e = batchRenameDialog.entries[i]
    const body = hasN ? tpl.replace(/\{n\}/g, String(i + 1)) : tpl
    try {
      await api.renameFile(e.taskId, e.key, body, /*forceMsgIdPrefix*/ true)
      ok++
    } catch (err) {
      fail++
      fails.push(`#${e.msgId}: ${err.message}`)
    }
  }
  batchRenameDialog.loading = false
  batchRenameDialog.show = false
  selected.clear()
  if (fail === 0) {
    ElMessage.success(`已重命名 ${ok} 个文件`)
  } else {
    ElMessageBox.alert(
      `${ok} 成功，${fail} 失败：\n${fails.join('\n')}`,
      '批量重命名完成',
      { confirmButtonText: '知道了' },
    )
  }
}
function removeDeletedFromStore(r) {
  const deleted = new Set((r?.results || []).filter(x => x.ok).map(x => x.id))
  if (!deleted.size) return
  store.tasks = store.tasks.filter(t => !deleted.has(t.id))
}
function reportFailures(r, total) {
  const fails = (r?.results || []).filter(x => !x.ok)
  if (!fails.length) ElMessage.success(`已开始 ${total} 个任务`)
  else ElMessage.warning(`${total - fails.length}/${total} 成功，${fails.length} 个失败`)
}

// --- 批量操作（勾选行→taskId 去重→batchAction） ---
async function batchPause() {
  const ids = [...selectedTaskIds.value]
  if (!ids.length) return
  try { await store.batchAction(ids, 'pause'); selected.clear() } catch (e) { ElMessage.error(e.message) }
}
async function batchStart() {
  const ids = [...selectedTaskIds.value]
  if (!ids.length) return
  try { const r = await store.batchAction(ids, 'resume'); reportFailures(r, ids.length); selected.clear() } catch (e) { ElMessage.error(e.message) }
}
function batchDelete() {
  const ids = [...selectedTaskIds.value]
  if (!ids.length) return
  openDeleteDialog('批量删除', `将删除选中的 ${ids.length} 个任务，下载记录将清除。`, async (deleteFiles) => {
    const r = await store.batchAction(ids, 'delete', deleteFiles)
    removeDeletedFromStore(r)
    selected.clear()
    return r
  })
}

// --- 底部状态栏聚合 ---
const totalFiles = computed(() => rows.value.length)
const runningCount = computed(() => rows.value.filter(r => rowStatus(r.file) === 'downloading').length)
const totalSpeed = computed(() => {
  // 按 task 去重求和
  const seen = new Set()
  let sum = 0
  for (const t of store.tasks) {
    if (t.status === 'running' && !seen.has(t.id)) { seen.add(t.id); sum += t.speed || 0 }
  }
  return sum
})
const totalDownloaded = computed(() => store.tasks.reduce((s, t) => s + (t.downloaded || 0), 0))
const totalSize = computed(() => store.tasks.reduce((s, t) => s + (t.totalSize || 0), 0))

// --- 格式化 helper ---
function fmtBytes(n) {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(n >= 100 || i === 0 ? 0 : 1)} ${u[i]}`
}
const filePct = (f) => (f.size > 0 ? Math.min(100, Math.round((f.downloaded / f.size) * 100)) : 0)
function groupPct(g) { return g.size > 0 ? Math.min(100, Math.round((g.downloaded / g.size) * 100)) : 0 }
function fileStatusText(s) {
  return { pending: '排队中', running: '下载中', done: '已完成', failed: '失败' }[s] || s
}
function progressStatus(s) {
  if (s === 'done') return 'success'
  if (s === 'failed') return 'exception'
  return ''
}
const fileIconMap = { pending: Clock, running: Download, done: Check, failed: X }
function fileIcon(s) { return fileIconMap[s] || null }
function firstLine(text, max = 30) {
  if (!text) return ''
  const line = String(text).split(/\r?\n/)[0].trim()
  if (!line) return ''
  return line.length > max ? line.slice(0, max) + '…' : line
}
function fileLabel(f) { return f.msgId ? `消息 #${f.msgId}` : '排队中' }
</script>

<style scoped>
/* === 整体布局：三栏 + 底部状态栏 === */
.dl-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.dl-body {
  display: flex;
  flex: 1;
  min-height: 0;
}

/* === 左侧边栏 === */
.dl-sidebar {
  flex: none;
  background: var(--bg-panel);
  border-right: 1px solid var(--border);
  padding: 12px 8px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
/* 侧边栏宽度拖拽手柄 */
.sidebar-resize {
  flex: none;
  width: 5px;
  cursor: col-resize;
  background: var(--border);
  transition: background .15s;
}
.sidebar-resize:hover { background: var(--accent); }
.cat-section { display: flex; flex-direction: column; gap: 2px; }
.cat-section + .cat-section { padding-top: 12px; border-top: 1px solid var(--border); }
.cat-title {
  font-size: 11px; font-weight: 600; color: var(--text-3);
  text-transform: uppercase; letter-spacing: 0.5px;
  padding: 0 8px 6px;
}
.cat-item {
  display: flex; align-items: center; gap: 8px;
  padding: 6px 8px; border-radius: var(--radius-sm);
  border: 1px solid transparent; background: transparent;
  color: var(--text-2); font-size: 13px; cursor: pointer;
  transition: all .15s; text-align: left;
}
.cat-item:hover { background: var(--bg-hover); color: var(--text); }
.cat-item.active { background: var(--accent-soft); color: var(--accent); border-color: var(--accent); font-weight: 500; }
.cat-label { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.cat-count { font-size: 11px; opacity: 0.7; font-variant-numeric: tabular-nums; }
.cat-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--text-3); flex: none; }
.cat-item.active .cat-dot { background: var(--accent); }

/* === 主区 === */
.dl-main {
  flex: 1; min-width: 0;
  display: flex; flex-direction: column;
}

/* === 工具栏 === */
.dl-toolbar {
  flex: none;
  display: flex; align-items: center; gap: 12px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-panel);
}
.toolbar-group { display: flex; align-items: center; gap: 4px; }
.spacer { flex: 1; }
.tool-btn {
  display: inline-flex; align-items: center; justify-content: center;
  width: 32px; height: 32px; border-radius: var(--radius-sm);
  border: 1px solid var(--border); background: var(--bg-panel);
  color: var(--text-2); cursor: pointer; transition: all .15s;
}
.tool-btn:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); background: var(--accent-soft); }
.tool-btn.active { border-color: var(--accent); color: var(--accent); background: var(--accent-soft); }
.tool-btn.danger:hover:not(:disabled) { border-color: var(--red); color: var(--red); background: transparent; }
.tool-btn:disabled { opacity: 0.4; cursor: not-allowed; }

/* 列设置 popover */
.col-settings { display: flex; flex-direction: column; gap: 8px; }
.col-settings-title { font-size: 12px; font-weight: 600; color: var(--text-3); margin-bottom: 8px; }
.col-settings :deep(.el-checkbox) { margin-right: 0; }
.col-settings :deep(.el-checkbox__label) { font-size: 13px; }
.col-row { display: flex; align-items: center; justify-content: space-between; padding: 2px 0; }
.col-arrows { display: flex; gap: 2px; }
.col-arrow {
  width: 22px; height: 22px; border: 1px solid var(--border); border-radius: 4px;
  background: var(--bg-panel); color: var(--text-2); font-size: 12px; cursor: pointer;
  display: inline-flex; align-items: center; justify-content: center; line-height: 1;
}
.col-arrow:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
.col-arrow:disabled { opacity: 0.3; cursor: not-allowed; }
.col-settings-actions { display: flex; gap: 6px; margin-top: 8px; padding-top: 8px; border-top: 1px solid var(--border); }
.col-action-btn {
  flex: 1; padding: 4px 8px; border: 1px solid var(--border); border-radius: var(--radius-sm);
  background: var(--bg-panel); color: var(--text-2); font-size: 12px; cursor: pointer;
}
.col-action-btn:hover { border-color: var(--accent); color: var(--accent); }

/* === 表格区 === */
.dl-table { flex: 1; min-height: 0; overflow: hidden; display: flex; flex-direction: column; }

/* 表头（sticky，高度与文件行一致 34px）*/
.thead {
  flex: none;
  display: flex; align-items: center;
  background: var(--bg-panel);
  border-bottom: 1px solid var(--border);
  position: sticky; top: 0; z-index: 2;
  height: 34px;
}
.th {
  font-size: 11.5px; font-weight: 600; color: var(--text-3);
  padding: 0 8px; white-space: nowrap;
  position: relative;
  display: flex; align-items: center;
  height: 34px;
}
.th-check { width: 40px; display: flex; justify-content: center; flex: none; }
.th-flex { flex: 1; min-width: 0; }
.th-num { justify-content: flex-start; text-align: left; }
/* 列宽拖拽手柄：表头列之间的分割线，默认可见（浅色），hover 变蓝提示可拖拽。
   命中区 8px（左右各 4px 跨列边界），中间 1px 实线视觉提示。 */
.col-resize {
  position: absolute; right: -4px; top: 4px; bottom: 4px;
  width: 8px; cursor: col-resize; z-index: 3;
  display: flex; justify-content: center;
}
.col-resize::before {
  content: ''; width: 1px; height: 100%; background: var(--border);
  transition: background .15s, width .15s;
}
.col-resize:hover::before, .col-resize:active::before {
  background: var(--accent); width: 2px;
}
/* 拖拽列宽时：当前列高亮 + 全局指示竖线 */
.th-dragging { background: var(--accent-soft) !important; }
.resize-indicator {
  position: fixed; top: 0; bottom: 0; width: 2px;
  background: var(--accent); z-index: 9999; pointer-events: none;
  box-shadow: 0 0 4px rgba(79,124,255,.4);
}
/* 列顺序拖拽时：其他列显示可放置提示 */
.th-order-target { background: var(--bg-hover); }
.th-order-target:hover { background: var(--accent-soft); }
/* 末尾 drop zone：拖列时出现，放下列即移到最后 */
.th-end-drop {
  width: 36px; flex: none; display: flex; align-items: center; justify-content: center;
  border: 1px dashed var(--border); border-radius: var(--radius-sm);
  margin-left: 4px; cursor: pointer;
}
.th-end-drop:hover { border-color: var(--accent); background: var(--accent-soft); }
.drop-hint { font-size: 14px; color: var(--text-3); }

/* 列表区 */
.tbody { flex: 1; overflow-y: auto; }

/* 来源分组头：高度 ≥ 行高，聚合信息对齐表头列位置 */
.group-head {
  display: flex; align-items: center;
  min-height: 34px;
  background: var(--bg-panel);
  border-bottom: 2px solid var(--border);
  font-size: 12px; color: var(--text-2); cursor: pointer;
  position: sticky; top: 0; z-index: 1;
}
.group-head:hover { background: var(--bg-hover); }
.gh-fold { width: 40px; flex: none; display: flex; align-items: center; justify-content: center; }
.gh-name { font-weight: 600; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 300px; }
/* 分组头聚合标签：流式紧凑排列，不按列对齐 */
.gh-tag {
  flex: none; font-size: 11.5px; color: var(--text-3); font-variant-numeric: tabular-nums;
  padding: 2px 6px; border-radius: var(--radius-sm);
  white-space: nowrap;
}
.gh-tag + .gh-tag { margin-left: 2px; }
.gh-pct-tag { color: var(--text-2); font-weight: 500; }
.gh-speed { color: var(--accent); font-weight: 500; }

/* 文件行 */
.trow {
  display: flex; align-items: center;
  height: 34px;
  border-bottom: 1px solid var(--border);
  transition: background .1s;
  cursor: default;
  user-select: none; /* shift 范围选择时避免选中正文文字 */
}
.trow:hover { background: var(--bg-hover); }
.trow.selected { background: var(--bg-active); }
/* 多选模式：行体可点击，指针变 pointer 提示可点 */
.trow.multi-mode { cursor: pointer; }
.td {
  padding: 0 8px; font-size: 12.5px; color: var(--text-2);
  white-space: nowrap; overflow: hidden;
  display: flex; align-items: center;
  height: 34px;
}
.td-check { width: 40px; display: flex; justify-content: center; flex: none; cursor: default; }
/* el-checkbox 仅做显示，点击穿透到 td-check 的 mousedown 处理（避免 @change 反向触发竞态） */
.td-check .row-cb { pointer-events: none; }
.td-name { flex: 1; min-width: 0; gap: 6px; color: var(--text); }
.td-mono { font-family: var(--font-mono, ui-monospace, Consolas, monospace); color: var(--text-3); justify-content: center; }
.td-source { overflow: hidden; }
.td-note { overflow: hidden; }
.td-num { justify-content: flex-start; text-align: left; font-variant-numeric: tabular-nums; }
.td-progress { gap: 8px; }
.td-status { justify-content: flex-start; }

/* 文件名单元格内容 */
.ficon { flex: none; }
.ficon.pending { color: var(--text-3); }
.ficon.running { color: var(--accent); }
.ficon.done { color: var(--green); }
.ficon.failed { color: var(--red); }
.fname { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-weight: 500; user-select: none; }
.src-name { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.note-text { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-size: 11.5px; color: var(--text-3); }

/* 进度单元格 */
.td-progress :deep(.el-progress) { flex: 1; }
.pct-text { width: 36px; text-align: left; font-variant-numeric: tabular-nums; font-size: 11.5px; color: var(--text-2); }

/* 状态文字 */
.fstat { font-size: 11.5px; font-weight: 500; }
.fstat.pending { color: var(--text-3); }
.fstat.running { color: var(--accent); }
.fstat.done { color: var(--green); }
.fstat.failed { color: var(--red); }

/* === 右键菜单 === */
.ctx-menu {
  position: fixed; z-index: 9999;
  min-width: 180px;
  background: var(--bg-panel);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow);
  padding: 4px 0;
  user-select: none;
}
.ctx-item {
  display: block; width: 100%; text-align: left;
  padding: 8px 16px; font-size: 13px; color: var(--text);
  background: none; border: none; cursor: pointer;
}
.ctx-item:hover { background: var(--bg-hover); color: var(--accent); }

/* === 底部状态栏 === */
.dl-statusbar {
  flex: none;
  display: flex; align-items: center; gap: 8px;
  padding: 6px 16px;
  background: var(--bg-panel);
  border-top: 1px solid var(--border);
  font-size: 11.5px; color: var(--text-2);
  font-variant-numeric: tabular-nums;
}
.sb-sep { color: var(--text-3); }
.sb-speed { color: var(--accent); font-weight: 500; }

/* === 删除对话框（复用现有样式） === */
:deep(.del-dialog) { border-radius: var(--radius) !important; box-shadow: var(--shadow) !important; overflow: hidden; }
:deep(.del-dialog .el-dialog__header) { padding: 16px 20px 8px; margin-right: 0; }
:deep(.del-dialog .el-dialog__body) { padding: 8px 20px 16px; }
:deep(.del-dialog .el-dialog__footer) { padding: 8px 20px 16px; }
.del-header { display: flex; align-items: center; gap: 8px; font-size: 15px; font-weight: 600; color: var(--text); }
.del-warn-icon { color: var(--orange); flex: none; }
.del-body { display: flex; flex-direction: column; gap: 14px; }
.del-msg { margin: 0; font-size: 13.5px; color: var(--text-2); line-height: 1.5; }
.del-msg code { background: var(--bg-panel); border: 1px solid var(--border); border-radius: 4px; padding: 1px 5px; font-size: 12.5px; color: var(--accent); font-family: ui-monospace, monospace; }
.del-tip { font-size: 12.5px; color: var(--text-3); }
.del-preview { font-size: 12.5px; color: var(--text-3); background: var(--bg-panel); padding: 8px 10px; border-radius: var(--radius-sm); border: 1px solid var(--border); word-break: break-all; }
.del-file-opt {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 12px; background: var(--bg-panel);
  border: 1px solid var(--border); border-radius: var(--radius-sm);
}
.del-opt-label { font-size: 13px; color: var(--text-2); }
:deep(.del-file-opt .el-radio__inner) { width: 16px; height: 16px; }
:deep(.del-file-opt .el-radio__label) { font-size: 13px; color: var(--text-2); }

/* === checkbox 统一 === */
:deep(.el-checkbox) { margin-right: 0; }
</style>
