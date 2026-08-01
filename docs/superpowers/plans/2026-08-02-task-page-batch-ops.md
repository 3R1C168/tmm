# 任务页批量操作 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给任务页加上 QBT/Transmission/aria2 风格的批量操作能力——勾选多条一起暂停/开始/删除、一键清空已完成、一键重试失败、可多选的状态筛选标签（带数量）。

**Architecture:** 后端新增一个 `POST /api/tasks/batch` 接口（循环调用现有 pause/resume/delete，逐项返回结果），前端 Tasks.vue 重构顶部为「状态标签栏 + 批量工具栏」，加选中态（按 id 跟踪的 `Set`）。SSE 链路、store 数据结构、调度模型都不动。

**Tech Stack:** Go（gorilla/mux, testify/require）后端 + Vue 3（Composition API, Element Plus, Pinia）前端。

**Spec:** `tdl/docs/superpowers/specs/2026-08-02-task-page-batch-ops-design.md`

## Global Constraints

（来自交接文档 + spec，所有任务都遵守）

- **部署目录是 `tmm.v11/`**，不是 `tmm.tt/`。前端构建后必须拷到 `tmm.v11/web`。
- **bat 文件必须纯 ASCII**（中文注释在 GBK 代码页报错）——本计划不涉及 bat。
- 后端构建：`cd tdl && go build -ldflags="-s -w" -o ../tmm.v11/tmm.exe .`
- 前端构建：`cd tdl/web/app && npm run build`，然后 `rm -rf tmm.v11/web/assets && cp -r tdl/web/app/dist/assets tmm.v11/web/assets && cp tdl/web/app/dist/index.html tmm.v11/web/index.html`
- 后端验证基线：`cd tdl && go build ./... && go vet ./... && go test ./server/...` 必须全绿。
- TaskStatus 六态：`pending` / `running` / `paused` / `done` / `failed` / `interrupted`（`server/model.go:11-18`）。
- resume 允许的状态：`paused/failed/interrupted/done`（`server/task.go:157`）；`running/pending` 不允许。
- 删任务不删磁盘文件（现有语义，batch 沿用）。
- 用户工作风格：中文交流；一次改一个变量实测；测试前 clear-cache.bat + Ctrl+Shift+R；后端改动后必须重建 exe；前端改动只需 build + 拷 web 到 tmm.v11。

---

## File Structure

**后端（`tdl/server/`）**：
- 修改 `task.go`：新增 `BatchResult` 类型 + `taskManager.Batch` 方法（约 148 行附近的 delete 方法后插入）。
- 修改 `handlers.go`：新增 `batchRequest` 类型 + `handleBatchTasks` handler（约 87 行 handleDeleteTask 后）。
- 修改 `server.go:258`：注册 `POST /api/tasks/batch` 路由（在 `{id}` 路由前，避免被 `{id}` 通配吞掉）。
- 新建 `task_batch_test.go`：Batch 方法 + handler 的单元测试。

**前端（`tdl/web/app/src/`）**：
- 修改 `api/index.js:36`：tasks 组追加 `batchTasks`。
- 修改 `stores/app.js:68`：actions 追加 `batchAction`。
- 修改 `views/Tasks.vue`：重写顶部布局 + 选中态 + 批量动作。这是最大的单文件改动。

**测试**：后端走 `go test ./server/...`；前端无测试框架，靠手动验证清单（Task 8）。

---

## Task 1: 后端 — `taskManager.Batch` 方法

**Files:**
- Modify: `tdl/server/task.go`（在 `delete` 方法 `task.go:131-146` 之后插入）
- Test: `tdl/server/task_batch_test.go`（新建）

**Interfaces:**
- Consumes: 现有 `m.pause(id) error`（task.go:98）、`m.resume(ctx, id) error`（task.go:150）、`m.delete(id) error`（task.go:131）
- Produces: `BatchResult` 类型 + `func (m *taskManager) Batch(ctx context.Context, ids []string, action string) []BatchResult`，供 Task 2 的 handler 调用。

- [ ] **Step 1: 写失败测试 — Batch pause 多个任务**

新建 `tdl/server/task_batch_test.go`：

```go
package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// newBatchTestServer 起一个带 store+broker+tasks 的 Server，用于 taskManager 单测。
// 不接真实 telegram（accounts.ensure 在 resume 测试里会失败，这是预期的）。
func newBatchTestServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(t, "")
	s.tasks = newTaskManager(s)
	return s
}

func TestBatchPause(t *testing.T) {
	s := newBatchTestServer(t)
	// 直接构造任务入 store，不走 submit（submit 会触发真实下载逻辑）
	now := time.Now()
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, s.store.upsert(&Task{
			ID: id, Account: "default", Status: StatusRunning, CreatedAt: now,
			Files: []*FileEntry{},
		}))
	}

	res := s.tasks.Batch(context.Background(), []string{"a", "b", "c"}, "pause")
	require.Len(t, res, 3)
	for _, r := range res {
		require.True(t, r.OK, "id %s should be ok: %s", r.ID, r.Error)
	}
	// running 任务被 cancel 后，状态由 run() 的 defer 设为 paused；
	// 但这里没有 run() 在跑，pause 对 running 走 cancel() 分支（task.go:115-118），
	// 状态不会立即变 paused（run() 不存在）。验证任务还在 store 即可。
	for _, id := range []string{"a", "b", "c"} {
		_, ok := s.store.get(id)
		require.True(t, ok)
	}
}
```

注意：上面测试里 running 任务的 pause 走 cancel 分支，但因为没有真实 run() goroutine 监听 ctx，状态不会变。所以测试只断言 `OK==true` 和任务存在。对 pending 任务的 pause 会直接置 paused（task.go:121-123），下面单独测。

- [ ] **Step 2: 运行测试确认失败**

```
cd tdl && go test ./server/ -run TestBatchPause -v
```
Expected: 编译失败，`s.tasks.Batch undefined`（方法还没写）。

- [ ] **Step 3: 写最小实现 — Batch 方法**

在 `tdl/server/task.go` 的 `delete` 方法（约 146 行结束）之后插入：

```go
// BatchResult reports the outcome of one id in a batch operation.
type BatchResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Batch applies the same action (pause/resume/delete) to many task ids,
// returning a per-id result. It is NOT transactional: a failure on one id
// does not roll back the others.
func (m *taskManager) Batch(ctx context.Context, ids []string, action string) []BatchResult {
	results := make([]BatchResult, 0, len(ids))
	for _, id := range ids {
		var err error
		switch action {
		case "pause":
			err = m.pause(id)
		case "resume":
			err = m.resume(ctx, id)
		case "delete":
			err = m.delete(id)
		default:
			err = errors.Errorf("unknown action: %s", action)
		}
		r := BatchResult{ID: id}
		if err != nil {
			r.Error = err.Error()
		} else {
			r.OK = true
		}
		results = append(results, r)
	}
	return results
}
```

- [ ] **Step 4: 运行测试确认通过**

```
cd tdl && go test ./server/ -run TestBatchPause -v
```
Expected: PASS。

- [ ] **Step 5: 补测试 — pending 任务 pause、delete、逐项失败不中断**

追加到 `task_batch_test.go`：

```go
func TestBatchPausePending(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{ID: "p1", Status: StatusPending, Files: []*FileEntry{}}))
	require.NoError(t, s.store.upsert(&Task{ID: "p2", Status: StatusPending, Files: []*FileEntry{}}))

	res := s.tasks.Batch(context.Background(), []string{"p1", "p2"}, "pause")
	require.Len(t, res, 2)
	for _, r := range res {
		require.True(t, r.OK)
	}
	// pending 任务 pause 直接置 paused（task.go:121-123）
	t1, _ := s.store.get("p1")
	require.Equal(t, StatusPaused, t1.Status)
}

func TestBatchDelete(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{ID: "d1", Status: StatusDone, Files: []*FileEntry{}}))
	require.NoError(t, s.store.upsert(&Task{ID: "d2", Status: StatusDone, Files: []*FileEntry{}}))

	res := s.tasks.Batch(context.Background(), []string{"d1", "d2"}, "delete")
	require.Len(t, res, 2)
	for _, r := range res {
		require.True(t, r.OK)
	}
	_, ok := s.store.get("d1")
	require.False(t, ok)
	_, ok = s.store.get("d2")
	require.False(t, ok)
}

// TestBatchPartialFailure 验证：一个不存在的 id 不会中断其余 id 的处理。
func TestBatchPartialFailure(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{ID: "ok1", Status: StatusDone, Files: []*FileEntry{}}))

	res := s.tasks.Batch(context.Background(), []string{"ok1", "ghost"}, "delete")
	require.Len(t, res, 2)
	require.True(t, res[0].OK) // ok1 删成功
	require.False(t, res[1].OK)
	require.Contains(t, res[1].Error, "not found")
}
```

- [ ] **Step 6: 运行全部 batch 测试**

```
cd tdl && go test ./server/ -run TestBatch -v
```
Expected: 4 个测试全 PASS。

- [ ] **Step 7: resume 状态拒绝路径 — 跳过（不写此测试）**

**原计划在此写 `TestBatchResumeStatusCheck`，实现时发现测试前提不成立，已删除。**

原因：`newTestServer`（spa_test.go:14）不初始化 `s.accounts`，resume 走到 `m.srv.accounts.ensure()` 会 nil 指针 panic 而非返回 error。要修复需造可用 accountManager fixture（接近 mock telegram），违背 YAGNI。resume 的状态校验是 `task.go:155-165` 既有逻辑（本任务未改），Batch 对 resume 是 `m.resume(ctx,id)` 透传 + error 收集——后者已被 `TestBatchPartialFailure` 覆盖相同路径。resume 实际正确性由 Task 7 curl + Task 8 用户实测覆盖。

**直接进 Step 8。**

- [ ] **Step 8: 运行 + 全套基线**

```
cd tdl && go test ./server/ -run TestBatch -v
cd tdl && go build ./... && go vet ./...
```
Expected: batch 测试全 PASS；build/vet 无错。

- [ ] **Step 9: Commit**

```bash
cd tdl
git add server/task.go server/task_batch_test.go
git commit -m "feat(server): add taskManager.Batch for pause/resume/delete on multiple ids"
```

---

## Task 2: 后端 — HTTP handler + 路由

**Files:**
- Modify: `tdl/server/handlers.go`（在 `handleDeleteTask` 约 87 行后插入）
- Modify: `tdl/server/server.go:258`（注册路由，注意要在 `{id}` 路由**之前**）
- Test: `tdl/server/task_batch_test.go`（追加 handler 测试）

**Interfaces:**
- Consumes: Task 1 的 `s.tasks.Batch(ctx, ids, action) []BatchResult`
- Produces: `POST /api/tasks/batch` HTTP 端点，请求体 `{ids, action}`，响应 `{results}`。

- [ ] **Step 1: 写失败测试 — handler 成功路径**

追加到 `task_batch_test.go`：

```go
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

func TestHandleBatchTasksDelete(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{ID: "h1", Status: StatusDone, Files: []*FileEntry{}}))
	require.NoError(t, s.store.upsert(&Task{ID: "h2", Status: StatusDone, Files: []*FileEntry{}}))

	body, _ := json.Marshal(map[string]any{"ids": []string{"h1", "h2"}, "action": "delete"})
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/batch", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleBatchTasks(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Results []BatchResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Results, 2)
	for _, r := range resp.Results {
		require.True(t, r.OK)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```
cd tdl && go test ./server/ -run TestHandleBatchTasksDelete -v
```
Expected: 编译失败，`s.handleBatchTasks undefined`。

- [ ] **Step 3: 实现 handler**

在 `tdl/server/handlers.go` 的 `handleDeleteTask`（约 87 行）之后插入：

```go
type batchRequest struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"` // pause | resume | delete
}

func (s *Server) handleBatchTasks(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(req.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("ids must not be empty"))
		return
	}
	switch req.Action {
	case "pause", "resume", "delete":
	default:
		writeErr(w, http.StatusBadRequest, errors.Errorf("invalid action: %s", req.Action))
		return
	}
	results := s.tasks.Batch(r.Context(), req.IDs, req.Action)
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
```

注意：handler 里用 `go-faster/errors`（task.go:10 已 import），handlers.go 顶部需确认 import。看 handlers.go 现有 import——目前没有 `go-faster/errors`。在 handlers.go import 块加 `"github.com/go-faster/errors"`。

- [ ] **Step 4: 运行测试确认通过**

```
cd tdl && go test ./server/ -run TestHandleBatchTasksDelete -v
```
Expected: PASS。

- [ ] **Step 5: 注册路由**

在 `tdl/server/server.go:258`（`api.HandleFunc("/tasks/{id}", ...).Methods(http.MethodDelete)` 那行）**之前**插入 batch 路由。理由：mux 是按注册顺序匹配，`/tasks/batch` 必须在 `/tasks/{id}` 之前注册，否则 `batch` 会被 `{id}` 捕获。

```go
api.HandleFunc("/tasks/batch", s.handleBatchTasks).Methods(http.MethodPost)
```

精确位置：在第 254 行 `api.HandleFunc("/tasks", s.handleCreateTask).Methods(http.MethodPost)` 之后、第 255 行 `api.HandleFunc("/tasks/{id}", ...)` 之前。即 batch 放在「POST /tasks」和「GET /tasks/{id}」之间。

- [ ] **Step 6: 补 handler 测试 — 错误路径**

追加到 `task_batch_test.go`：

```go
func TestHandleBatchTasksValidation(t *testing.T) {
	s := newBatchTestServer(t)

	// 空 ids → 400
	body, _ := json.Marshal(map[string]any{"ids": []string{}, "action": "delete"})
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/batch", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleBatchTasks(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 非法 action → 400
	body, _ = json.Marshal(map[string]any{"ids": []string{"x"}, "action": "bogus"})
	req = httptest.NewRequest(http.MethodPost, "/api/tasks/batch", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.handleBatchTasks(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

- [ ] **Step 7: 全套后端基线**

```
cd tdl && go test ./server/... -v
cd tdl && go build ./... && go vet ./...
```
Expected: 全部测试 PASS（含原有 store/spa/media 测试 + 新 batch 测试）；build/vet 无错。

- [ ] **Step 8: Commit**

```bash
cd tdl
git add server/handlers.go server/server.go server/task_batch_test.go
git commit -m "feat(server): add POST /api/tasks/batch endpoint"
```

---

## Task 3: 后端 — 构建部署 exe

**Files:** 无源码改动，纯构建。

**Goal:** 确认后端改动编译进 tmm.v11/tmm.exe，供前端联调。

- [ ] **Step 1: 杀掉旧进程**

```
powershell -Command "Get-Process tmm -ErrorAction SilentlyContinue | Stop-Process -Force"
```

- [ ] **Step 2: 构建 exe**

```
cd tdl && go build -ldflags="-s -w" -o ../tmm.v11/tmm.exe .
```
Expected: 无报错，`tmm.v11/tmm.exe` 更新。

- [ ] **Step 3: 验证编译产物**

```
cd tmm.v11 && ./tmm.exe serve &
# 等 2 秒看是否正常启动（日志在 stderr/cmd 窗口）
curl -s http://127.0.0.1:8080/api/tasks | head -c 200
```
Expected: 返回 `{"tasks":[...]}`（或空数组），不报 404。然后停掉（关 cmd 窗口或 kill）。

- [ ] **Step 4: Commit（可选，无文件改动）**

无源码改动，跳过 commit。exe 不进 git。

---

## Task 4: 前端 — API 封装

**Files:**
- Modify: `tdl/web/app/src/api/index.js:36`（tasks 组，deleteTask 行后）

**Interfaces:**
- Consumes: 现有 `req(method, path, body)`（index.js:3）
- Produces: `api.batchTasks(ids, action)`，供 store 和组件调用。

- [ ] **Step 1: 加 batchTasks 方法**

在 `tdl/web/app/src/api/index.js` 第 36 行 `deleteTask: (id) => req('DELETE', ...)` 之后追加：

```js
  batchTasks: (ids, action) => req('POST', '/api/tasks/batch', { ids, action }),
```

- [ ] **Step 2: 验证（手动）**

无前端测试框架。确认 import 无误——`req` 已在文件顶部定义。用浏览器 console 手测：
```
cd tdl/web/app && npm run build
# 部署到 tmm.v11 后，浏览器 console：
await fetch('/api/tasks/batch', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({ids:[], action:'delete'})}).then(r=>r.status)
// 期望 400（空 ids）
```

- [ ] **Step 3: Commit**

```bash
cd tdl
git add web/app/src/api/index.js
git commit -m "feat(web): add api.batchTasks"
```

---

## Task 5: 前端 — Store action

**Files:**
- Modify: `tdl/web/app/src/stores/app.js:68`（actions 块，startEvents 之前或之后）

**Interfaces:**
- Consumes: Task 4 的 `api.batchTasks(ids, action)`
- Produces: `store.batchAction(ids, action)`，返回后端 `{results}`，供组件提示失败项。**不在 store 本地改 tasks**——等 SSE 推送刷新。

- [ ] **Step 1: 加 batchAction action**

在 `tdl/web/app/src/stores/app.js` 的 actions 块（如 `refreshTasks` 之后、`upsertTask` 之前，约第 53 行）插入：

```js
    async batchAction(ids, action) {
      // 不在此处本地改动 tasks，等 SSE 推送刷新（upsertTask）
      const r = await api.batchTasks(ids, action)
      return r // { results: [{id, ok, error?}] }
    },
```

- [ ] **Step 2: 构建验证**

```
cd tdl/web/app && npm run build
```
Expected: 构建成功，无报错。

- [ ] **Step 3: Commit**

```bash
cd tdl
git add web/app/src/stores/app.js
git commit -m "feat(web): add store.batchAction"
```

---

## Task 6: 前端 — Tasks.vue 重构（核心）

这是最大的改动。拆成几个子步骤，每步构建一次确认不报错。

**Files:**
- Modify: `tdl/web/app/src/views/Tasks.vue`（几乎重写 template + script + style）

**Interfaces:**
- Consumes: Task 5 的 `store.batchAction(ids, action)`；现有 `store.tasks`；Element Plus `el-checkbox` / `el-button` / `el-progress` / `el-tag` / `el-empty` / `ElMessageBox`。

**设计参照**（来自 spec 第 3 节）：
```
┌──────────────────────────────────────────────────────────────────┐
│  [全部 5] [进行中 3] [已完成 1] [已暂停 0] [失败 1]   [清空已完成] [重试失败]  │
│  ☐全选  已选 N 条  [暂停](灰) [开始](灰) [删除](灰)                  │
├──────────────────────────────────────────────────────────────────┤
│  ☐  ▶  任务名     进行中   进度条                                   │
└──────────────────────────────────────────────────────────────────┘
```

- [ ] **Step 1: 替换 `<script setup>` 的状态与计算属性**

把 `tdl/web/app/src/views/Tasks.vue` 的 `<script setup>` 部分（第 80-152 行）整体替换为：

```js
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
  // 全部 = 6 状态都在 filterStatuses
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

// --- 单条操作（保留现有，从 selected 同步勾选框）---
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
```

- [ ] **Step 2: 替换 `<template>`**

把 `tdl/web/app/src/views/Tasks.vue` 第 1-78 行的 `<template>` 整体替换为：

```html
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
```

- [ ] **Step 3: 替换 `<style scoped>`**

把 `tdl/web/app/src/views/Tasks.vue` 第 154-193 行的 `<style scoped>` 整体替换为：

```html
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
```

- [ ] **Step 4: 构建验证**

```
cd tdl/web/app && npm run build
```
Expected: 构建成功，无报错（特别注意 `reactive(new Set())` 的响应性——Vue 3.5 对 reactive Set 的 add/delete 是响应式的，符合要求）。

- [ ] **Step 5: 部署到 tmm.v11**

```
rm -rf tmm.v11/web/assets && cp -r tdl/web/app/dist/assets tmm.v11/web/assets && cp tdl/web/app/dist/index.html tmm.v11/web/index.html
```

- [ ] **Step 6: Commit**

```bash
cd tdl
git add web/app/src/views/Tasks.vue
git commit -m "feat(web): rewrite Tasks.vue with batch ops + multi-select filter"
```

---

## Task 7: 前端部署 + 启动验证

**Files:** 无源码改动。

- [ ] **Step 1: 确认 tmm.v11 是最新**

检查 `tmm.v11/web/index.html` 和 `tmm.v11/tmm.exe` 都是刚构建的（Task 3 + Task 6 Step 5 已做）。

- [ ] **Step 2: 启动服务**

```
cd tmm.v11 && ./tmm.exe serve
```
（前台运行，cmd 窗口显示日志。保持运行，新开终端做下面的 curl 验证。）

- [ ] **Step 3: curl 验证 batch 接口**

```
# 空 ids → 400
curl -s -o /dev/null -w "%{http_code}\n" -X POST http://127.0.0.1:8080/api/tasks/batch -H "Content-Type: application/json" -d '{"ids":[],"action":"delete"}'
# 期望 400

# 非法 action → 400
curl -s -o /dev/null -w "%{http_code}\n" -X POST http://127.0.0.1:8080/api/tasks/batch -H "Content-Type: application/json" -d '{"ids":["x"],"action":"bogus"}'
# 期望 400

# 不存在的 id → 200 + results
curl -s -X POST http://127.0.0.1:8080/api/tasks/batch -H "Content-Type: application/json" -d '{"ids":["ghost"],"action":"delete"}'
# 期望 {"results":[{"id":"ghost","ok":false,"error":"task not found"}]}
```

- [ ] **Step 4: 关闭服务**

关 cmd 窗口（前台运行模式，关窗即停），或 `powershell -Command "Get-Process tmm | Stop-Process -Force"`。

---

## Task 8: 手动 UI 验证清单

**Files:** 无。这是用户实测环节。

测试前：用户跑 `tmm.v11/clear-cache.bat`，浏览器 Ctrl+Shift+R 强刷。

逐项勾选，对照 spec 第 3 节和第 7.2 节：

- [ ] **A. 页面布局**：顶部能看到两行——状态标签栏（全部/进行中/已完成/已暂停/失败，每个带数字）+ 快捷按钮（清空已完成/重试失败）；下面是批量工具栏（全选框 + 暂停/开始/删除，未勾选时灰色点不动）。
- [ ] **B. 勾选基本**：勾一条任务 → 顶部按钮变亮 → 「已选 1 条」出现；再勾一条 → 「已选 2 条」。
- [ ] **C. 批量暂停**：勾几条 running/pending → 点暂停 → 都变 paused（SSE 推送刷新）→ 选中清空。
- [ ] **D. 批量开始**：勾几条 paused/failed → 点开始 → 都变 pending（入队）→ 串行执行（1 running + N pending）。
- [ ] **E. 批量删除**：勾几条 → 点删除 → 弹一次确认 → 确认后删除，磁盘文件还在（去下载目录确认）。
- [ ] **F. 选中态跨过滤**：勾几条后点「失败」标签 → 列表只剩失败的，之前勾的不在视图里；切回「全部」→ 之前勾的还在（「已选 N 条」的 N 恢复）。
- [ ] **G. SSE 推送不丢选中**：勾几条 → 等下载进度推送（或手动开始一个）→ 选中态不丢。
- [ ] **H. 全选三态**：当前视图全未选 → 全选框空；勾几条但非全部 → 全选框显示减号（indeterminate）；当前视图全选 → 全选框打勾。
- [ ] **I. 全选范围**：切到「已完成」→ 点全选 → 只选中已完成的（不是所有任务）。
- [ ] **J. 清空已完成**：点「清空已完成」→ 弹确认「确定清空 N 条吗」→ 确认后所有 done 消失，磁盘文件保留。
- [ ] **K. 重试失败**：先制造一个失败任务（断网下载）→ 点「重试失败」→ 失败任务变 pending/running；若无失败任务，「重试失败」按钮灰色。
- [ ] **L. 多选过滤**：取消「全部」→ 只勾「进行中」+「失败」→ 列表只显示这两类。
- [ ] **M. 主题**：切暗色主题 → 标签栏/批量栏/勾选框样式正常，无白底刺眼。
- [ ] **N. 数量准确**：标签后的数字与实际任务数一致（边操作边看，SSE 推送后数字实时更新）。
- [ ] **O. 回归**：单条 暂停/开始/删除 仍正常（不受批量改动影响）。

发现问题时记录：现象、复现步骤、期望 vs 实际，反馈给开发调整。

---

## Self-Review

**1. Spec 覆盖**：
- 4.1 batch 接口 → Task 1 (Batch 方法) + Task 2 (handler/路由)
- 4.2 taskManager.Batch → Task 1
- 4.3 handler + 路由 → Task 2
- 4.4 SSE 零改动 → 无需任务（spec 已说明）
- 5.1 api.batchTasks → Task 4
- 5.2 store.batchAction → Task 5
- 5.3 Tasks.vue 全部子节 → Task 6
- 5.4 样式 → Task 6 Step 3
- 7.1 后端测试 → Task 1 + Task 2
- 7.2 手动验证 → Task 8
- 第 8 节实现顺序 → Task 1-8 顺序一致
- 规矩 1-8（用户决策）→ 全部体现在 Task 6 代码里

**2. 占位符扫描**：无 TBD/TODO；所有代码块完整；测试有断言。

**3. 类型/命名一致性**：
- `BatchResult{ID, OK, Error}` — Task 1 定义，Task 2 测试引用，一致。
- `batchRequest{IDs, Action}` — Task 2 定义，json tag `ids`/`action` 与前端 Task 4 `{ids, action}` 一致。
- `api.batchTasks(ids, action)` — Task 4 定义，Task 5 + Task 6 调用一致。
- `store.batchAction(ids, action)` — Task 5 定义，Task 6 调用一致。
- `selected` / `filterStatuses` — Task 6 内部一致。

**4. 潜在坑**：
- mux 路由顺序：`/tasks/batch` 必须在 `/tasks/{id}` 之前注册，Task 2 Step 5 已强调。
- `reactive(new Set())` 响应性：Vue 3.5 支持，但 `selected.clear()` / `selected.add()` 触发响应——已在 Task 6 用 reactive（非 ref），符合 Vue 3 Set 响应式约定。
- resume 测试边界：Task 1 Step 7 注明了 accounts.ensure 在测试环境失败是预期的，不掩盖状态校验逻辑。

无遗漏。
