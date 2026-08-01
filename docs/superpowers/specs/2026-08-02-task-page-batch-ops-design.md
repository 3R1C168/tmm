# 任务页改进设计（批量操作 + 筛选增强）

- **日期**：2026-08-02
- **范围**：子系统 B（任务页改进）。其余三个子系统（A 已下载标记 / C 文件管理 / D 续传可视化）留作后续，不在本 spec 内。
- **目标用户**：项目所有者本人（非程序员，中文交流，用 tmm.v11 实际运行）。
- **交付目录**：`tmm.v11/`（**不是** `tmm.tt/`）。

---

## 1. 背景与现状

当前任务页 `tdl/web/app/src/views/Tasks.vue` 已有：
- 任务卡片列表（按 `createdAt` 倒序），每张卡含状态图标、`el-tag` 状态、`el-progress` 进度条、meta 行、可展开文件列表
- 逐任务的 暂停 / 开始 / 删除 按钮（删除有 `el-dialog` 确认）
- 顶部单选 radio 过滤：全部 / 进行中 / 已完成（三选一）

缺口：
1. **无批量操作** —— 一次只能操作一条，积压任务多时极繁琐。
2. **无「清空已完成」「重试失败」快捷键** —— 必须逐条点。
3. **过滤只能单选** —— 无法同时查看"进行中 + 失败"以配合批量重试。

后端现状（`tdl/server/`）：
- `taskManager` 串行调度（`task.go:26-27`），同一时刻最多 1 个 running，其余入队。
- 单任务 API：`pause` / `resume` / `delete`（`handlers.go:62-87`）。
- `TaskStatus` 六态：`pending` / `running` / `paused` / `done` / `failed` / `interrupted`（`model.go:11-18`）。
- 删除任务保留磁盘文件（`task.go:131-146` 注释明确）。
- `resume` 接受 `paused/failed/interrupted/done`（`task.go:157-163`）。
- SSE 全局事件流 `/api/events`，`type==='task'||'file'` 时前端 `upsertTask`（`stores/app.js:59-68`）。

---

## 2. 目标

**非目标**（YAGNI，明确排除）：
- 不做"已下载标记"（子系统 A）。
- 不做文件管理页（子系统 C）。
- 不做续传进度可视化（子系统 D）。
- 不加 `clearCompleted` / `retryFailed` 专用后端接口 —— 这两个语义由"按状态算 ids → 调 batch 接口"实现。
- 不做任务分页、不做任务搜索 —— 当前任务量级（个人用，几十条）不需要。
- 不改排序（保持 `createdAt` 倒序）。

**目标**：在不动调度模型、不引入新持久化的前提下，给任务页加上 QBT / Transmission / aria2 风格的批量操作能力。

---

## 3. 用户视角（给非程序员的描述）

改完后任务页长这样：

```
┌──────────────────────────────────────────────────────────────────┐
│  下载                                                            │
│                                                                  │
│  [全部 5] [进行中 3] [已完成 1] [已暂停 0] [失败 1]   [清空已完成] [重试失败]  │
├──────────────────────────────────────────────────────────────────┤
│  ☐  [暂停](灰) [开始](灰) [删除](灰)                              │  ← 勾了才亮
├──────────────────────────────────────────────────────────────────┤
│  ☐  ▶  封面图集.zip     进行中   ━━━━━━━━░░░  60%   2.3GB/3.8GB    │
│  ☐  ▶  演唱会视频.mp4   已暂停   ━━━░░░░░░░░  30%   1.1GB/3.8GB    │
│  ☐  ✓  老照片.zip       已完成   ━━━━━━━━━━  100%  3.8GB          │
│  ☐  ✗  会议录像.mp4     失败     ━░░░░░░░░░  5%    0.2GB/3.8GB     │
└──────────────────────────────────────────────────────────────────┘
```

**操作方式**：
- **精确操作某几条**：勾左边方框 → 顶部「暂停 / 开始 / 删除」点亮 → 点操作 → 批量处理。
- **一键清空已完成**：点「清空已完成」→ 弹一次确认 → 所有 done 任务删除（保留磁盘文件）。
- **一键重试失败**：点「重试失败」→ 所有 failed / interrupted 任务 resume。
- **按状态看**：点「失败 1」标签 → 列表只剩失败那条；可同时勾多个标签。

**用户已确认的规矩**（brainstorming 阶段定下）：
1. 勾选状态按任务 id 跟踪（`Set<id>`），切换过滤标签、SSE 推送刷新都不丢；不在当前视图内的 id 保留但不计入"已选 N 条"的 N。
2. 「全选」= 选中当前过滤视图内的全部任务（不是所有任务）。
3. 顶部批量按钮（暂停/开始/删除）一直显示，没勾时变灰禁用；勾了至少一条才点亮。
4. 「批量删除」弹一次确认（不一条条问），与现有单条删除体验一致。
5. 状态筛选标签可多选，每个标签后带数量（Transmission 风格）；默认全选 = 显示全部。
6. 「清空已完成」「重试失败」放状态标签那排的最右边。
7. 批量开始：所有 resume 入队，串行执行（复用现有调度，1 running + N pending）。
8. 删任务只删记录，磁盘文件不动（与现有 `DELETE /api/tasks/{id}` 一致）。

---

## 4. 后端设计

### 4.1 新增批量接口 `POST /api/tasks/batch`

**请求体**：
```json
{
  "ids": ["task-abc", "task-def"],
  "action": "pause"
}
```
- `action ∈ {pause, resume, delete}`
- `ids`：任务 id 数组

**响应 200**（逐项结果，**非原子事务**）：
```json
{
  "results": [
    {"id": "task-abc", "ok": true},
    {"id": "task-xyz", "ok": false, "error": "not found"}
  ]
}
```

**为什么逐项结果而非原子事务**：任务操作天然非事务——删 task A 成功、task B 因并发状态变化失败时，应让 A 生效、B 单独报错，不能因 B 回滚 A。原子事务会让单个坏任务卡住整批。

**错误处理**：
- `ids` 为空或 `action` 不合法 → `400 Bad Request`（整体请求级错误）。
- 单个 id 失败（not found / 状态不允许 resume）→ 记入 `results`，**不中断**其余 id 的处理。

**语义**（与现有单任务 API 完全一致，逐个调用底层逻辑）：
- `pause` → running 的 cancel，pending 的从队列移除
- `resume` → 状态校验（**源码 `task.go:157`：`paused/failed/interrupted/done` 才允许**；`running`/`pending` 不允许）→ 全部入队，串行执行
- `delete` → 删 store 记录，保留磁盘文件

### 4.2 `taskManager` 新增方法

在 `server/task.go` 的 `taskManager` 上新增：

```go
type BatchResult struct {
    ID    string `json:"id"`
    OK    bool   `json:"ok"`
    Error string `json:"error,omitempty"`
}

func (m *taskManager) Batch(ctx context.Context, ids []string, action string) []BatchResult
```

实现：遍历 `ids`，按 `action` 调用现有 `pause` / `resume` / `delete` 方法，每次捕获 error，收集到 `[]BatchResult`。**不新增持久化逻辑**（单任务方法内部已 upsert/delete store）。

复用现有方法签名（**不改它们**）：
- `pause(id) error`（`task.go:98-127`）
- `resume(ctx, id) error`（`task.go:150-186`）
- `delete(id) error`（`task.go:131-146`）

`resume` 需要 `ctx`（内部调 account ready 校验 + notify），其余两个不需要。Batch 方法接受一个 `ctx` 透传。

### 4.3 Handler 与路由

在 `server/handlers.go` 新增 `handleBatchTasks`：

```go
type batchRequest struct {
    IDs    []string `json:"ids"`
    Action string   `json:"action"`
}
```

解析 body → 校验 `action` ∈ {pause,resume,delete} 且 `ids` 非空 → 调 `srv.tasks.Batch(ctx, ids, action)` → 返回 `{results: [...]}`。

路由注册（`server/server.go:253-260` 一组任务路由后追加）：
```
POST /api/tasks/batch → handleBatchTasks
```

### 4.4 SSE 行为（无需改动）

批量操作触发的状态变化走现有 `publishTask` 机制：每个被 pause/resume/delete 的任务都会发 SSE `task` 事件，前端 `upsertTask` 自动刷新。**SSE 链路零改动**。

---

## 5. 前端设计

### 5.1 API 封装

`src/api/index.js` 任务相关一组后追加：
```js
batchTasks(ids, action)  // POST /api/tasks/batch, body={ids, action}
```

### 5.2 Store 层（`src/stores/app.js`）

**不改 state 结构**（`tasks` 仍是数组）。选中态是纯 UI 状态，放组件内，不污染全局 store。

若需要 action 包装批量调用（可选），加：
```js
async batchAction(ids, action) {
  const res = await api.batchTasks(ids, action)
  // 不在此处本地改动 tasks，等 SSE 推送刷新（upsertTask）
  return res  // 返回给组件用于提示失败项
}
```
**不在 store 本地乐观更新 tasks**：避免与 SSE 推送的状态竞争。统一靠 SSE 刷新。

### 5.3 `Tasks.vue` 改动（核心）

#### 状态新增

```js
const selected = ref(new Set())        // 选中的任务 id 集合
const filterStatuses = ref(new Set(['pending','running','paused','done','failed','interrupted']))  // 默认全选
```

`filterStatuses` 默认包含全部 6 个状态 = "显示全部"。标签 UI 上把状态归并为 4 组展示（见 5.3.2），但底层是 6 状态集合，便于和后端 status 对齐。

#### 5.3.1 顶部布局重构

模板结构（自上而下）：

```html
<!-- 行 1：状态筛选标签 + 快捷按钮 -->
<div class="filter-bar">
  <!-- 状态标签组（多选） -->
  <div class="status-tabs">
    <button v-for="g in statusGroups"
            :class="['tab', {active: isGroupActive(g)}]"
            @click="toggleGroup(g)">
      {{ g.label }} <span class="count">{{ countByGroup(g) }}</span>
    </button>
  </div>
  <!-- 快捷按钮（右侧） -->
  <div class="quick-actions">
    <button @click="clearCompleted" :disabled="countDone===0">清空已完成</button>
    <button @click="retryFailed" :disabled="countFailedInterrupted===0">重试失败</button>
  </div>
</div>

<!-- 行 2：批量操作工具栏（一直显示，没勾时禁用） -->
<div class="batch-bar">
  <el-checkbox class="select-all"
               :model-value="allInViewSelected"
               :indeterminate="someInViewSelected"
               @change="toggleSelectAllInView">全选</el-checkbox>
  <span class="sel-count" v-if="selected.size>0">已选 {{ selectedCountInView }} 条</span>
  <div class="batch-actions">
    <button :disabled="selected.size===0" @click="batchPause">暂停</button>
    <button :disabled="selected.size===0" @click="batchStart">开始</button>
    <button :disabled="selected.size===0" @click="confirmBatchDelete">删除</button>
  </div>
</div>
```

**状态归并 4 组**（UI 展示用，底层 `filterStatuses` 仍是 6 状态）：

| 标签 | 对应 status | count 计算 |
|------|------------|-----------|
| 进行中 | pending + running | 两者之和 |
| 已完成 | done | done 数 |
| 已暂停 | paused | paused 数 |
| 失败 | failed + interrupted | 两者之和 |

加上"全部"标签 = 6 状态全选。共 **5 个标签**。

`toggleGroup(g)`：点击标签切换该组所有 status 的选中态。
`isGroupActive(g)`：该组所有 status 都在 `filterStatuses` 内。
`countByGroup(g)`：从 `store.tasks` 按组统计。

#### 5.3.2 任务列表过滤

```js
const filteredTasks = computed(() =>
  store.tasks
    .filter(t => filterStatuses.value.has(t.status))
    .sort((a,b) => b.createdAt - a.createdAt)  // 保持现状
)
```

#### 5.3.3 每条任务的勾选框

在 `.task-card` 最左侧加（与 `Messages.vue:121` 的 `pick` 复选框写法一致）：
```html
<el-checkbox class="pick" :model-value="selected.has(t.id)" @change="toggleSelect(t.id)" />
```

`toggleSelect(id)`：`selected.value.has(id) ? delete : add`。

#### 5.3.4 全选 / 已选计数逻辑

```js
const allInViewSelected = computed(() =>
  filteredTasks.value.length > 0 &&
  filteredTasks.value.every(t => selected.value.has(t.id))
)
const someInViewSelected = computed(() =>
  filteredTasks.value.some(t => selected.value.has(t.id)) && !allInViewSelected.value
)
const selectedCountInView = computed(() =>
  filteredTasks.value.filter(t => selected.value.has(t.id)).length
)
function toggleSelectAllInView(checked) {  // el-checkbox @change 直接传 boolean
  const ids = filteredTasks.value.map(t => t.id)
  if (checked) ids.forEach(id => selected.value.add(id))
  else ids.forEach(id => selected.value.delete(id))
}
```

**"已选 N 条"显示 `selectedCountInView`**（当前视图内被选中的数量），不是 `selected.size`（含视图外）。符合规矩 1。

#### 5.3.5 批量动作处理

```js
async function batchPause() {
  const ids = [...selected.value]
  await store.batchAction(ids, 'pause')
  selected.value = new Set()  // 操作后清空选中
}
// batchStart / confirmBatchDelete 同构

async function confirmBatchDelete() {
  try {
    await ElMessageBox.confirm(`确定删除选中的 ${selected.size} 条任务吗？`, ...)
    const ids = [...selected.value]
    await store.batchAction(ids, 'delete')
    selected.value = new Set()
  } catch {} // 用户取消
}
```

**操作后清空选中**：避免操作完任务状态变了、勾选还挂着造成困惑。

#### 5.3.6 快捷按钮实现

```js
async function clearCompleted() {
  const ids = store.tasks.filter(t => t.status === 'done').map(t => t.id)
  if (ids.length === 0) return
  try {
    await ElMessageBox.confirm(`确定清空 ${ids.length} 条已完成任务吗？（磁盘文件保留）`, ...)
    await store.batchAction(ids, 'delete')
  } catch {}
}
async function retryFailed() {
  const ids = store.tasks.filter(t => t.status==='failed' || t.status==='interrupted').map(t => t.id)
  if (ids.length === 0) return
  await store.batchAction(ids, 'resume')
}
```

**快捷按钮不预选**：直接按状态算 ids 调 batch，不经过 `selected` 集合。理由：快捷按钮语义是"按状态一键操作"，与"手动勾选"是两条独立路径，混用会让"已选 N 条"的 N 与实际操作的条数不符。

#### 5.3.7 选中态与任务存在性的同步

任务被删除（批量删 / 快捷清空 / 别处删除）后，`selected` 里会残留不存在的 id。处理方式：

- **不在删除时主动清理 `selected`**（批量删除已清空选中；快捷清空不经 selected；单条删除走原逻辑）。
- **在读取 `selectedCountInView` / `allInViewSelected` 等 computed 时自然过滤**：这些 computed 都基于 `filteredTasks`，已删除的 id 不在 `filteredTasks` 里，自然不计入。
- `selected.has(t.id)`（每条任务的勾选框）也是基于当前存在的任务，不存在的 id 不渲染、无害。
- **可接受的冗余**：`selected` 集合可能累积已删除 id，但永远不显示、不计数、不影响逻辑。下次切换过滤/刷新时也无所谓。如追求干净，可在 `store.tasks` 变化的 watch 里清掉不存在的 id（可选优化，非必须）。

### 5.4 样式约定

遵循现有设计 token（`src/styles/main.css`）：
- 标签激活态：`background: var(--accent-soft); color: var(--accent); border:1px solid var(--accent)`
- 禁用按钮：`opacity:.5; cursor:not-allowed`
- 勾选框跟随 Element Plus 默认样式（`el-checkbox`），与 Messages.vue 的 `pick` 复选框视觉一致
- 批量操作工具栏与状态标签栏用 `border-bottom:1px solid var(--border)` 分隔（参考现有 `.task-head`）

主题（亮/暗）通过 CSS 变量自动适配，无需额外处理。

---

## 6. 数据流总结

```
用户勾选 / 点标签
      ↓
Tasks.vue 本地状态（selected Set, filterStatuses Set）
      ↓
点批量按钮
      ↓
store.batchAction(ids, action)
      ↓
api.batchTasks → POST /api/tasks/batch
      ↓
handleBatchTasks → taskManager.Batch
      ↓
循环调用现有 pause/resume/delete
      ↓
每个任务的 store 改动触发 publishTask → SSE event
      ↓
前端 upsertTask 刷新 store.tasks
      ↓
Tasks.vue 的 filteredTasks / countByGroup computed 自动更新
      ↓
UI 刷新（进度条 / 状态标签数量 / 选中计数）
```

**关键不变量**：
- `selected` 和 `filterStatuses` 是纯前端 UI 状态，后端无感知。
- 批量操作不改 SSE 协议，不改 store 数据结构。
- 选中态不持久化（刷新页面重置，符合 QBT/Transmission 行为）。

---

## 7. 测试策略

### 7.1 后端单元测试（`server/` 包，`go test`）

新建 `task_batch_test.go` 或在现有 `task_test.go` 追加：

1. **Batch pause**：构造 pending + running 任务各若干，调 `Batch(ids, "pause")`，断言都变 paused、results 全 ok。
2. **Batch resume**：构造 paused/failed/interrupted/done 各一，调 `Batch(ids, "resume")`，断言都变 pending 并入队，串行执行（drain 后逐个 running→done 或按测试桩返回）。
3. **Batch delete**：构造若干任务，调 `Batch(ids, "delete")`，断言从 store 消失、磁盘文件保留（用 temp dir + 真实文件验证）。
4. **逐项失败不中断**：ids 含一个不存在 id + 若干合法 id，断言不存在项 `ok:false, error:"not found"`，其余 `ok:true`。
5. **混合状态 resume**：ids 含 running 或 pending（不允许 resume）+ paused，断言 running/pending 项 `ok:false`，paused 项 `ok:true`。注意：`done` 在源码里**允许** resume（`task.go:157-158`，注释 "Done re-runs to pick up anything new"），不要误判。
6. **空 ids / 非法 action**：handler 层返回 400（用 `httptest` 打全请求）。
7. **并发安全**：Batch 执行期间另一 goroutine 调单任务 API，无 race（`go test -race`）。

现有 `go test ./server/...` 全绿基线必须保持。

### 7.2 前端

项目无前端测试框架（`package.json` 无 vitest/jest）。**手动验证清单**（写入设计文档供执行阶段用）：

- [ ] 勾 3 条任务 → 顶部按钮点亮 → 点暂停 → 3 条都变已暂停 → 选中清空
- [ ] 勾选后切到"已完成"标签 → 切回 → 勾选仍在
- [ ] SSE 推送新任务时，已选状态不丢，新任务默认不选
- [ ] 点"全选" → 当前视图全选 → 再点 → 全不选
- [ ] 部分选中时全选框显示减号（indeterminate）
- [ ] 点"清空已完成" → 弹确认 → 确认后 done 任务消失，磁盘文件还在
- [ ] 点"重试失败" → failed/interrupted 任务变进行中
- [ ] 同时勾"进行中"+"失败"两个标签 → 列表显示两类
- [ ] 批量删除弹确认，取消则不删
- [ ] 亮/暗主题下批量栏、标签、勾选框样式正常
- [ ] 任务量大（20+）时列表滚动、勾选不卡顿

### 7.3 回归

- 现有单条 暂停/开始/删除 不受影响（batch 方法内部调用同一逻辑）。
- SSE 链路无改动。
- 不改 store 持久化格式，旧 JSON 兼容。

---

## 8. 实现顺序（建议）

1. **后端**：`taskManager.Batch` 方法 + `BatchResult` 类型 → `handleBatchTasks` handler → 路由注册 → 单元测试。
2. **后端验证**：`go build ./... && go vet ./... && go test ./server/...` 全绿。
3. **前端 API**：`api.batchTasks`。
4. **前端 Store**：`store.batchAction` action。
5. **前端 Tasks.vue**：状态标签栏 + 批量栏 + 勾选框 + 计算属性 + 动作处理 + 样式。
6. **构建部署**：后端 build exe → 前端 build → 拷贝 web 到 **tmm.v11**（**不是 tmm.tt**）。
7. **手动验证**：按 7.2 清单逐项过。
8. **实测**：用户 clear-cache.bat + Ctrl+Shift+R 后体验。

---

## 9. 风险与边界

| 风险 | 缓解 |
|------|------|
| 批量 resume 大量任务，队列堆积 | 现有调度本就串行，pending 是预期状态；UI 会显示"进行中 (N)"含 pending+running 让用户看到积压 |
| 批量删除时 SSE 推送与本地状态竞争 | 统一靠 SSE 刷新 store.tasks，不在前端本地乐观更新 |
| `selected` 残留已删除 id | computed 基于 filteredTasks 自然过滤，无害；可选 watch 清理 |
| 用户勾选后任务被别处删除（如重启转 interrupted） | 任务还在，只是状态变；勾选仍有效，可对其操作 |
| `indeterminate` 三态显示 | 用 Element Plus `el-checkbox` 的 `:indeterminate` prop，原生支持，与 Messages.vue 复选框组件统一 |

---

## 10. 不在本 spec 范围

明确推迟到后续 spec：
- A 已下载标记（消息气泡徽标 + chatID+msgID 持久化）
- C 下载文件管理页（浏览/打开/清理下载目录）
- D 续传进度可视化（暴露 state.go piece 进度）
- 任务排序选项、任务搜索、任务分页
- 批量任务的撤销 / 重做
- 后端 batch 接口的事务化（当前逐项结果已够用）

---

## 附录：决策记录（brainstorming 阶段）

| # | 问题 | 决策 |
|---|------|------|
| 1 | 起始子系统 | B 任务页改进 |
| 2 | 能力范围 | 批量操作 + 清空已完成 + 重试失败 + 过滤增强 |
| 3 | 选中态存储 | 按 id 跟踪（Set），视图外保留 |
| 4 | 全选范围 | 当前过滤视图 |
| 5 | 实现路线 | 后端加批量接口 |
| 6 | 批量开始语义 | 全部入队串行 |
| 7 | 过滤形态 | 多选状态筛选 |
| 9 | 排序 | 保持 createdAt 倒序 |
| 10 | 批量按钮显示 | 一直显示，没勾时变灰禁用 |
| 11 | 删除确认 | 弹一次确认框 |
| 12 | 筛选标签位置 | 顶部一排 + 数量 |
| 13 | 快捷按钮位置 | 状态标签排右侧 |
