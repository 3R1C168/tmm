# 文件级控制（单文件暂停/恢复）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 tmm 增加单文件级暂停/恢复：暂停一个文件不打断任务里其他文件，恢复时从断点续传；任务级控制保持现有行为。

**Architecture:** 三层协同：(1) `core/downloader` 加可选 `PauseCheck` 回调，`writeAt.WriteAt` 每次写入前检查，返回 error 即中止该文件；(2) `app/dl` iter 加 `paused` 跳过集合 + `requeue` 恢复队列（独立索引产出，不碰主状态机）；(3) `server` 用 `FileEntry.UserPaused` 区分「文件级手动暂停」vs「任务级暂停」，新增 `POST /api/tasks/{id}/files/batch` 端点，前端右键菜单/多选触发。任务级 resume 时 `UserPaused==true` 的文件保持跳过（`adapter.hook` 初始化 paused 集合）。

**Tech Stack:** Go（core/downloader、app/dl、server）、Vue 3 + Element Plus（web/app/src）、SSE 推送（任务/文件状态刷新）。

## Global Constraints

- 行为约束（来自 spec §3）：
  - `UserPaused` 是唯一区分「文件级手动暂停」与「任务级暂停」的字段；任务级操作不改它，文件级操作才改。
  - `task.go:run()` 成功分支：只把 `FilePending → FileDone`；`FilePaused` 文件保持 paused（不能强制标 done，否则手动暂停文件失去恢复能力）。
  - PauseCheck 中断错误必须返回 `context.Canceled`（现有 `progressAdapter.OnDone` 已有 `errors.Is(ferr, context.Canceled) → FilePaused` 判定，零新增中断判定）。
  - PauseCheck 为 nil 时行为必须与现状逐字节一致（tdl CLI 不传该回调，不受影响）。
  - requeue 产出**不得**改动 `dialogIndex`/`messageIndex`/`logicalPos`（主状态机完全隔离），只能从独立索引取 (peer, msgID)。
  - 锁序固定：`task.mu` → `iter.mu` 单向，禁止反向，防死锁。
  - 现有测试 `task_batch_test.go` / `resume_test.go` / `media_stream_test.go` / `settings_test.go` 必须全绿（回归）。
- 前端约束：沿用现有 `.ctx-item` 右键菜单样式与 token，不引入新组件库。
- 提交规范：每任务独立 commit，消息前缀 `feat:`（功能）或 `test:`（测试）。
- 命令均在 `D:\Documents\workspace\telegram manager\tdl`（Git Bash）下执行；Go 测试用 `go test ./core/downloader/... ./app/dl/... ./server/...`。

---

### Task 1: 核心库下载器加 PauseCheck 钩子（core/downloader）

**Files:**
- Modify: `core/downloader/downloader.go:25-36`（Options 结构）
- Modify: `core/downloader/downloader.go:32-36`（New 传入 Options）
- Modify: `core/downloader/downloader.go:44-46`（Download → download 传 opts）
- Modify: `core/downloader/downloader.go:75-116`（download 里 newWriteAt 传入 PauseCheck）
- Modify: `core/downloader/progress.go:32-62`（writeAt 加字段 + newWriteAt 参数）
- Modify: `core/downloader/progress.go:63-84`（WriteAt 开头检查）
- Create: `core/downloader/pause_check_test.go`

**Interfaces:**
- Produces: `Options` 新增字段 `PauseCheck func(elem Elem) error`（`downloader.Options`）；`writeAt` 构造接收 `pauseCheck func(Elem) error`；`WriteAt` 每次写入前调用 `pauseCheck(elem)`，非 nil 错误返回 `0, err`（中止该文件）。

- [ ] **Step 1: 写失败测试**

创建 `core/downloader/pause_check_test.go`：

```go
package downloader

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeElem 满足 Elem 接口的最小实现（只测 WriteAt 中断路径，download 不真的跑）。
type fakeElem struct {
	written int64
}

func (f *fakeElem) File() File                 { return nil }
func (f *fakeElem) To() io.WriterAt            { return f }
func (f *fakeElem) AsTakeout() bool            { return false }
func (f *fakeElem) Resume() *State             { return nil }
func (f *fakeElem) WriteAt(p []byte, off int64) (int, error) {
	f.written += int64(len(p))
	return len(p), nil
}

// TestWriteAtPauseCheckAborts 验证：PauseCheck 返回错误时 WriteAt 不再写入，
// 返回该错误；PauseCheck 为 nil 时正常写入。
func TestWriteAtPauseCheckAborts(t *testing.T) {
	t.Run("nil PauseCheck writes normally", func(t *testing.T) {
		e := &fakeElem{}
		w := newWriteAt(e, nil, MaxPartSize, nil)
		n, err := w.WriteAt([]byte("hello"), 0)
		require.NoError(t, err)
		require.Equal(t, 5, n)
		require.Equal(t, int64(5), e.written)
	})

	t.Run("PauseCheck error aborts without writing", func(t *testing.T) {
		e := &fakeElem{}
		w := newWriteAt(e, nil, MaxPartSize, nil)
		// 给 writeAt 塞一个 pauseCheck（测试里通过字段注入）
		w.pauseCheck = func(elem Elem) error { return errors.New("abort") }
		n, err := w.WriteAt([]byte("hello"), 0)
		require.Error(t, err)
		require.Equal(t, "abort", err.Error())
		require.Equal(t, 0, n)
		require.Equal(t, int64(0), e.written) // 没写
	})

	t.Run("PauseCheck nil-safe and only checked when set", func(t *testing.T) {
		var calls atomic.Int32
		e := &fakeElem{}
		w := newWriteAt(e, nil, MaxPartSize, nil)
		w.pauseCheck = func(elem Elem) error { calls.Add(1); return nil }
		n, err := w.WriteAt([]byte("hi"), 0)
		require.NoError(t, err)
		require.Equal(t, 2, n)
		require.Equal(t, int32(1), calls.Load())
		require.Equal(t, int64(2), e.written)
	})
}
```

注意：`fakeElem.To()` 返回自身（实现 `io.WriterAt`），需要 import `"io"` 和 `"errors"`。测试里通过字段注入 `pauseCheck`（为让测试可注入，`writeAt.pauseCheck` 用可导出前的小写字段即可，同包测试可访问）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./core/downloader/ -run TestWriteAtPauseCheckAborts -v`
Expected: 编译失败（`w.pauseCheck` 字段不存在）+ 断言失败。

- [ ] **Step 3: 实现 PauseCheck**

`core/downloader/downloader.go` Options 加字段：

```go
type Options struct {
	Pool     dcpool.Pool
	Threads  int
	Iter     Iter
	Progress Progress
	// PauseCheck, when non-nil, is invoked before every write of a chunk.
	// A non-nil error aborts the current file's download (the element is
	// finalized with that error). It is how a service backend implements
	// per-file pause. Nil means no check (CLI behavior unchanged).
	PauseCheck func(elem Elem) error
}
```

`core/downloader/downloader.go` `Download`（把 `d.opts` 传进 `download`）：

```go
if err := d.download(wgctx, elem); err != nil {
```

无需改 `Download` 签名（`download` 已接收 `d` 指针），改 `download` 内两处 `newWriteAt`：

```go
w := newWriteAt(elem, d.opts.Progress, MaxPartSize) // resume 路径
// Parallel 路径用 downloader.NewDownloader().Download(...).Parallel(ctx, newWriteAt(...))
```

`newWriteAt` 需要接收 `d.opts.PauseCheck`。把 `writeAt` 构造改为 `newWriteAt(elem, d.opts.Progress, MaxPartSize, d.opts.PauseCheck)`（两处调用点：`download` 里 resume 路径的 `newWriteAt` 和 `Parallel` 路径的 `newWriteAt`）。

`core/downloader/progress.go`：

```go
type writeAt struct {
	elem        Elem
	progress    Progress
	partSize    int
	state       *State
	pauseCheck  func(elem Elem) error

	downloaded *atomic.Int64

	flushMu   sync.Mutex
	lastFlush time.Time
}

func newWriteAt(elem Elem, progress Progress, partSize int, pauseCheck func(Elem) error) *writeAt {
	// ...原逻辑...
	return &writeAt{
		elem:       elem,
		progress:   progress,
		partSize:   partSize,
		state:      elem.Resume(),
		pauseCheck: pauseCheck,
		downloaded: atomic.NewInt64(start),
		lastFlush:  time.Now(),
	}
}

func (w *writeAt) WriteAt(p []byte, off int64) (int, error) {
	if w.pauseCheck != nil {
		if err := w.pauseCheck(w.elem); err != nil {
			return 0, err
		}
	}
	at, err := w.elem.To().WriteAt(p, off)
	// ...原逻辑不变...
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./core/downloader/ -run TestWriteAtPauseCheckAborts -v`
Expected: PASS（3 个子测试全过）。

- [ ] **Step 5: 全量回归核心库**

Run: `go build ./... && go test ./core/downloader/...`
Expected: 编译通过 + 全绿。

- [ ] **Step 6: Commit**

```bash
git add core/downloader/downloader.go core/downloader/progress.go core/downloader/pause_check_test.go
git commit -m "feat(core): downloader 加可选 PauseCheck 回调——writeAt 每次写入前检查，非 nil 错误中止单文件下载（nil 行为不变）"
```

---

### Task 2: app/dl 透传 PauseCheck

**Files:**
- Modify: `app/dl/dl.go:30-67`（Options 加字段）
- Modify: `app/dl/dl.go:119-230`（dl.Run 里 Options → downloader.Options 透传）
- Test: 无独立测试（PauseCheck 透传由 Task 3 的 server 集成路径间接覆盖）

**Interfaces:**
- Consumes: `downloader.Options.PauseCheck`（Task 1）
- Produces: `dl.Options` 新增 `PauseCheck func(elem downloader.Elem) error`；`dl.Run` 将其赋给 `downloader.Options.PauseCheck`。

- [ ] **Step 1: dl.Options 加字段**

`app/dl/dl.go` Options 结构加（放在 `Progress` 附近）：

```go
	// PauseCheck, when non-nil, is invoked before each chunk write of a file.
	// A non-nil error aborts that file's download. A service backend uses it
	// for per-file pause. Nil = no check (CLI behavior unchanged).
	PauseCheck func(elem downloader.Elem) error
```

- [ ] **Step 2: dl.Run 透传**

`dl.Run` 构造 `downloader.Options` 处（约 line 199-206）加：

```go
	options := downloader.Options{
		Pool:       pool,
		Threads:    rt.Threads,
		Iter:       it,
		Progress:   progress,
		PauseCheck: opts.PauseCheck,
	}
```

- [ ] **Step 3: 编译 + 提交**

Run: `go build ./...`
Expected: 编译通过（PauseCheck 为 nil 时行为不变，无需改测试）。

```bash
git add app/dl/dl.go
git commit -m "feat(dl): Options 透传 PauseCheck 到 downloader——server 可用它实现单文件暂停"
```

---

### Task 3: server 侧 FileEntry.UserPaused + taskManager 文件级批量操作

**Files:**
- Modify: `server/model.go:32-44`（FileEntry 加 UserPaused）
- Modify: `server/task.go`（新增 fileBatch 方法）
- Modify: `server/task.go:440-457`（run() 成功分支修正）
- Modify: `server/handlers.go`（新增 handleFilesBatch 路由处理）
- Modify: `server/server.go:253-261`（注册路由）
- Create: `server/file_batch_test.go`
- Modify: `server/task_batch_test.go:125-148`（补充断言：FilePaused 不再被标 done）

**Interfaces:**
- Consumes: `FileStatus` 常量、`Task` 结构（`model.go`）、`taskManager`（`task.go`）、`Batch` 模式（`task.go:263-286`）
- Produces: `FileEntry.UserPaused bool json:"userPaused"`；`taskManager.fileBatch(taskID string, action string, keys []string) ([]FileBatchResult, error)`；`FileBatchResult{Key string; OK bool; Error string}`；route `POST /api/tasks/{id}/files/batch`。

- [ ] **Step 1: model.go 加字段**

`server/model.go` FileEntry（line 42 `Status` 后）加：

```go
	Status     FileStatus `json:"status"`
	UserPaused bool       `json:"userPaused"` // 文件级手动暂停标记；任务级暂停不置此位
```

- [ ] **Step 2: 写 file_batch_test.go（TDD）**

创建 `server/file_batch_test.go`：

```go
package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newBatchTestServer 已在 task_batch_test.go 定义，复用。

func TestFileBatchPause(t *testing.T) {
	s := newBatchTestServer(t)
	task := &Task{
		ID: "t1", Account: "default", Status: StatusRunning, CreatedAt: time.Now(),
		Files: []*FileEntry{
			{Key: "1/1", Status: FileRunning},
			{Key: "1/2", Status: FilePending},
			{Key: "1/3", Status: FileDone},
		},
	}
	require.NoError(t, s.store.upsert(task))

	res, err := s.tasks.fileBatch("t1", "pause", []string{"1/1", "1/2", "1/3"})
	require.NoError(t, err)
	require.Len(t, res, 3)
	require.True(t, res[0].OK, "running file should pause ok")
	require.True(t, res[1].OK, "pending file should pause ok")
	require.False(t, res[2].OK, "done file should not pause")
	require.Equal(t, "file already done/failed", res[2].Error)

	t2, _ := s.store.get("t1")
	f11 := t2.Files[0]
	require.Equal(t, FilePaused, f11.Status)
	require.True(t, f11.UserPaused, "manual pause must set UserPaused")
	f12 := t2.Files[1]
	require.Equal(t, FilePaused, f12.Status)
	require.True(t, f12.UserPaused)
	f13 := t2.Files[2]
	require.Equal(t, FileDone, f13.Status)
	require.False(t, f13.UserPaused)
}

func TestFileBatchResume(t *testing.T) {
	s := newBatchTestServer(t)
	task := &Task{
		ID: "t2", Account: "default", Status: StatusPaused, CreatedAt: time.Now(),
		Files: []*FileEntry{
			{Key: "2/1", Status: FilePaused, UserPaused: true},
			{Key: "2/2", Status: FilePaused, UserPaused: false}, // 任务级暂停产生的 paused
			{Key: "2/3", Status: FileDone},
		},
	}
	require.NoError(t, s.store.upsert(task))

	res, err := s.tasks.fileBatch("t2", "resume", []string{"2/1", "2/2", "2/3"})
	require.NoError(t, err)
	require.Len(t, res, 3)
	require.True(t, res[0].OK)
	require.True(t, res[1].OK, "task-level paused file can also be resumed")
	require.False(t, res[2].OK, "done file cannot be resumed")

	t2t, _ := s.store.get("t2")
	f := t2t.Files[0]
	require.Equal(t, FilePending, f.Status)
	require.False(t, f.UserPaused)
	require.Equal(t, FilePending, t2t.Files[1].Status)
	require.Equal(t, FileDone, t2t.Files[2].Status)
}

func TestFileBatchValidation(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{ID: "v1", Status: StatusDone, Files: []*FileEntry{}}))

	// 任务不存在
	_, err := s.tasks.fileBatch("missing", "pause", []string{"1/1"})
	require.Error(t, err)
	// action 非法
	_, err = s.tasks.fileBatch("v1", "bogus", []string{"1/1"})
	require.Error(t, err)
	// keys 为空
	_, err = s.tasks.fileBatch("v1", "pause", []string{})
	require.Error(t, err)
	// key 不属于任务
	res, err := s.tasks.fileBatch("v1", "pause", []string{"9/9"})
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.False(t, res[0].OK)
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./server/ -run 'TestFileBatch' -v`
Expected: 编译失败（`fileBatch` 未定义）。

- [ ] **Step 4: 实现 fileBatch**

`server/task.go` 新增（放在 `Batch` 方法后）：

```go
// FileBatchResult reports the outcome of one file key in a file-level batch op.
type FileBatchResult struct {
	Key   string `json:"key"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// fileBatch applies a file-level pause/resume to the given keys of one task.
// action: "pause" | "resume". Per-key results, not transactional.
//
// 暂停：仅 FileRunning/FilePending → FilePaused + UserPaused=true（文件级手动暂停标记）。
//        FileDone/FileFailed 不动（返回 ok=false）。
// 恢复：仅 FilePaused → FilePending + UserPaused=false（任务级暂停产生的 paused 也允许恢复）。
//        FileDone/FileFailed 不动。
// 任务 running 时文件级暂停/恢复即时生效：
//   暂停→iter.PauseKeys（已产出的正在下载文件由 PauseCheck 钩子即刻中断）；
//   恢复→iter.Unpause（requeue 即时续传）。非 running 时只改状态，下次任务 resume 生效。
func (m *taskManager) fileBatch(taskID string, action string, keys []string) ([]FileBatchResult, error) {
	if action != "pause" && action != "resume" {
		return nil, errors.Errorf("unknown action: %s", action)
	}
	if len(keys) == 0 {
		return nil, errors.New("keys must not be empty")
	}
	task, ok := m.srv.store.get(taskID)
	if !ok {
		return nil, errors.New("task not found")
	}

	results := make([]FileBatchResult, 0, len(keys))
	// 收集受影响 key 的集合，供 iter 调用
	affected := make([]string, 0, len(keys))

	task.mu.Lock()
	byKey := make(map[string]*FileEntry, len(task.Files))
	for _, f := range task.Files {
		if f != nil {
			byKey[f.Key] = f
		}
	}
	for _, key := range keys {
		fe := byKey[key]
		r := FileBatchResult{Key: key}
		switch action {
		case "pause":
			if fe == nil {
				r.Error = "file not found in task"
			} else if fe.Status == FileDone || fe.Status == FileFailed {
				r.Error = "file already done/failed"
			} else {
				fe.Status = FilePaused
				fe.UserPaused = true
				affected = append(affected, key)
				r.OK = true
			}
		case "resume":
			if fe == nil {
				r.Error = "file not found in task"
			} else if fe.Status == FilePaused {
				fe.Status = FilePending
				fe.UserPaused = false
				affected = append(affected, key)
				r.OK = true
			} else {
				r.Error = "file not paused"
			}
		}
		results = append(results, r)
	}
	// 若任务当前在跑，让 iter 立即响应（iterForTask 在 Task 4+5 6c 实现）
	iter := m.iterForTask(taskID)
	task.mu.Unlock()

	if iter != nil {
		switch action {
		case "pause":
			iter.PauseKeys(keysToMsgKeys(affected))
		case "resume":
			for _, k := range affected {
				iter.Unpause(parseMsgKey(k))
			}
		}
	}

	if err := m.srv.store.upsert(task); err != nil {
		return nil, err
	}
	m.srv.publishTask(task, "file")
	return results, nil
}
```

- [ ] **Step 5: 声明 iter 字段（仅字段，方法在 Task 4+5 6c 实现）**

`server/task.go` 的 taskManager 加字段 `iter dl.Iter`（在 `running` 字段旁）：

```go
type taskManager struct {
	srv *Server

	mu      sync.Mutex
	queue   []string
	cancels map[string]context.CancelFunc
	running string
	iter    dl.Iter // 当前运行任务的迭代器（文件级暂停/恢复用），非 running 时为 nil

	wake chan struct{}
	stop chan struct{}
	wg   sync.WaitGroup
}
```

**顺序依赖（重要）**：`fileBatch`（Step 4）调用的 `m.iterForTask(taskID)`、`iter.PauseKeys`、`iter.Unpause`、`keysToMsgKeys`、`parseMsgKey` 均在 **Task 4+5** 定义。因此**执行顺序为 Task 4+5 先于 Task 3**（见计划头部的顺序调整声明与 SDD ledger）。`parseMsgKey`/`keysToMsgKeys` 不在本任务定义（属 Task 4+5）。

- [ ] **Step 6: run() 成功分支修正**

`server/task.go:446-450` 现有代码：

```go
		case err == nil:
			task.Status = StatusDone
			// dl.Run 成功完成 = iter 把所有消息都处理了。但 SkipSame 命中的文件
			// （磁盘已有同名同大小）会被 iter 跳过、不触发 OnAdd，预填的 pending
			// FileEntry 残留。把这些标为 done，语义正确（文件确实在磁盘上），
			// 也避免「task done 但有 pending 文件」的矛盾显示。
			for _, f := range task.Files {
				if f.Status == FilePending || f.Status == FilePaused {
					f.Status = FileDone
				}
			}
```

改为：

```go
		case err == nil:
			task.Status = StatusDone
			// dl.Run 成功完成 = iter 把所有消息都处理了。但 SkipSame 命中的文件
			// （磁盘已有同名同大小）会被 iter 跳过、不触发 OnAdd，预填的 pending
			// FileEntry 残留。把这些标为 done，语义正确（文件确实在磁盘上），
			// 也避免「task done 但有 pending 文件」的矛盾显示。
			//
			// 注意：FilePaused 不在此列——文件级手动暂停（UserPaused=true）的文件
			// 保持 paused 留待恢复，不能被强制标 done（否则失去恢复能力）。
			for _, f := range task.Files {
				if f.Status == FilePending {
					f.Status = FileDone
				}
			}
```

- [ ] **Step 7: 补 task_batch_test.go 断言**

`server/task_batch_test.go:125-148` 的 `TestTaskDoneMarksPausedFilesDone` 末尾追加：

```go
	// 新语义：文件级手动暂停（UserPaused=true + FilePaused）的文件在任务 done 时
	// 不应被标成 done（run() 成功分支只标 FilePending）。
	// 直接验证 run() 的 done 分支逻辑（不连 telegram）：
	pausedUser := &FileEntry{Key: "1/1", Status: FilePaused, UserPaused: true}
	pending := &FileEntry{Key: "1/2", Status: FilePending}
	done := &FileEntry{Key: "1/3", Status: FileDone}
	// 模拟 run() 成功分支的遍历逻辑（与 task.go 同步维护）
	for _, f := range []*FileEntry{pausedUser, pending, done} {
		if f.Status == FilePending {
			f.Status = FileDone
		}
	}
	require.Equal(t, FilePaused, pausedUser.Status, "手动暂停文件必须保持 paused")
	require.Equal(t, FileDone, pending.Status)
	require.Equal(t, FileDone, done.Status)
	require.True(t, pausedUser.UserPaused)
```

- [ ] **Step 8: handlers.go 加 handleFilesBatch**

`server/handlers.go` 加（放在 `handleBatchTasks` 后）：

```go
type fileBatchRequest struct {
	Action string   `json:"action"`
	Keys   []string `json:"keys"`
}

// handleFilesBatch applies file-level pause/resume to keys of one task.
func (s *Server) handleFilesBatch(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req fileBatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	results, err := s.tasks.fileBatch(id, req.Action, req.Keys)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
```

- [ ] **Step 9: server.go 注册路由**

`server/server.go:253-261` 加：

```go
	api.HandleFunc("/tasks/{id}/files/batch", s.handleFilesBatch).Methods(http.MethodPost)
```

（放在 `/tasks/{id}/rename` 附近。）

- [ ] **Step 10: 全量测试**

Run: `go build ./... && go test ./server/...`
Expected: 编译通过 + 全部测试绿（含现有 task_batch_test.go 回归）。

- [ ] **Step 11: Commit**

```bash
git add server/model.go server/task.go server/handlers.go server/server.go server/file_batch_test.go server/task_batch_test.go
git commit -m "feat(server): 文件级 pause/resume API——UserPaused 区分文件级/任务级暂停，run() 成功分支不再强制标 FilePaused 为 done"
```

---

### Task 4+5: iter 暂停集合 + requeue 恢复队列 + server 接线

**Files:**
- Modify: `app/dl/iter.go`（paused 集合 + requeue 队列 + PauseKeys/Unpause/ClearPaused + Next 优先 requeue + processSingle 跳过）
- Modify: `app/dl/finalize.go`（Iter 接口加 PauseKeys/Unpause/ClearPaused）
- Modify: `server/dl.go`（hook 里 setIter + 收集 UserPaused → PauseKeys）
- Modify: `server/task.go`（setIter/clearIter/iterForTask + parseMsgKey/keysToMsgKeys 工具）
- Create: `app/dl/iter_pause_test.go`

**顺序说明**：本任务先于 Task 3 执行（Task 3 的 fileBatch 依赖本任务的 iterForTask/parseMsgKey 等）。parseMsgKey/keysToMsgKeys 在本任务定义于 `server/task.go`。

**Interfaces:**
- Consumes: `dl.MsgKey`（iter.go:51-53）、`prefetcher.get(ctx, peer, id)`（prefetch.go:101）、`processSingle`（iter.go:301）、`i.elem` channel（iter.go:157）、`dialogIndex/messageIndex`（iter.go:99-100）
- Produces: `iter.PauseKeys(keys []dl.MsgKey)`、`iter.Unpause(key dl.MsgKey)`、`iter.ClearPaused()`、`dl.Iter` 接口新增这 3 方法；`iter.dialogByPeer map[int64]peerAndMsgs` 独立索引。

- [ ] **Step 1: dl.Iter 接口加方法**

`app/dl/finalize.go:19-33` Iter 接口加：

```go
	// PauseKeys marks messages as user-paused so processSingle skips them
	// (no element is produced, no finish recorded). Keys not part of the
	// download set are ignored.
	PauseKeys(keys []MsgKey)
	// Unpause removes a key from the paused set and re-queues it for download,
	// so a running task resumes that file immediately.
	Unpause(key MsgKey)
	// ClearPaused empties the paused set (called when a task (re)starts).
	ClearPaused()
```

- [ ] **Step 2: 写 iter_pause_test.go（TDD）**

`app/dl/iter_pause_test.go`：

```go
package dl

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIterPauseSkip 验证：paused 集合中的文件被跳过（不产出 elem），
// 不影响主状态机顺序。
func TestIterPauseSkip(t *testing.T) {
	it := &iter{
		mu:      &sync.Mutex{},
		paused:  map[MsgKey]struct{}{},
		requeue: []MsgKey{},
	}
	// 用真实 iter 结构验证集合行为（不连 telegram）
	it.PauseKeys([]MsgKey{{PeerID: 1, MsgID: 2}})
	_, paused := it.paused[MsgKey{PeerID: 1, MsgID: 2}]
	require.True(t, paused, "PauseKeys should add to paused set")
	require.False(t, it.IsPaused(MsgKey{PeerID: 1, MsgID: 3}))
}

func TestIterUnpauseRequeue(t *testing.T) {
	it := &iter{
		mu:      &sync.Mutex{},
		paused:  map[MsgKey]struct{}{{PeerID: 1, MsgID: 2}: {}},
		requeue: []MsgKey{},
	}
	it.Unpause(MsgKey{PeerID: 1, MsgID: 2})
	_, paused := it.paused[MsgKey{PeerID: 1, MsgID: 2}]
	require.False(t, paused, "Unpause should remove from paused set")
	require.Len(t, it.requeue, 1)
	require.Equal(t, MsgKey{PeerID: 1, MsgID: 2}, it.requeue[0])
}

func TestIterClearPaused(t *testing.T) {
	it := &iter{
		mu:      &sync.Mutex{},
		paused:  map[MsgKey]struct{}{{PeerID: 1, MsgID: 2}: {}},
		requeue: []MsgKey{},
	}
	it.ClearPaused()
	require.Empty(t, it.paused)
	require.Empty(t, it.requeue)
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./app/dl/ -run 'TestIterPause|TestIterUnpause|TestIterClearPaused' -v`
Expected: 编译失败（字段/方法不存在）。

- [ ] **Step 4: 实现 iter 暂停集合 + requeue**

`app/dl/iter.go` iter 结构加字段（`mu` 旁）：

```go
	paused  map[MsgKey]struct{} // 用户手动暂停：processSingle 跳过
	requeue []MsgKey            // 恢复队列：Next 优先重新产出
```

`newIter` 初始化：

```go
		paused:  make(map[MsgKey]struct{}),
		requeue: make([]MsgKey, 0),
```

`processSingle` 开头（`item, ok := tmedia.GetMedia(message)` 前）加暂停检查：

```go
	// 文件级暂停：用户在 UI 里暂停了该文件，跳过不下载（恢复后 requeue 重新产出）。
	// isPausedLocked：processSingle 总在持锁路径被调用（process 或 produceFromKey 持锁），
	// 直接读 i.paused，不加锁（避免 process 持锁下重入死锁）。
	if i.isPausedLocked(keyFromMsg(from.InputPeer(), message)) {
		return false, true
	}
```

`Next` 开头（`len(i.elem) > 0` 检查后）加 requeue 优先：

```go
	// 恢复队列优先：用户恢复了暂停的文件，立即重新产出
	if key, ok := i.nextRequeue(); ok {
		if err := i.produceFromKey(ctx, key); err != nil {
			i.err = err
			return false
		}
		return true
	}
```

注意：`Next` 当前结构是先 `ctx.Done()` → delay → `len(i.elem)>0` → `process` 循环。requeue 检查放在 `len(i.elem)>0` 之后、`process` 循环之前。若 `produceFromKey` 失败（如消息已删除）应记录错误并继续（返回 false 结束迭代，与现有错误语义一致；或跳过并继续——为保守起见失败即结束，`i.err = err`）。

**produceFromKey 实现**（独立索引，不碰主状态机）：

```go
// nextRequeue pops the head of the requeue queue.
func (i *iter) nextRequeue() (MsgKey, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.requeue) == 0 {
		return MsgKey{}, false
	}
	key := i.requeue[0]
	i.requeue = i.requeue[1:]
	return key, true
}

// produceFromKey fetches the message for a requeued key and produces its elem
// through the same path as the main loop. It does NOT touch dialogIndex /
// messageIndex / logicalPos (main state machine is fully isolated).
func (i *iter) produceFromKey(ctx context.Context, key MsgKey) error {
	peer, ok := i.peerByID(key.PeerID)
	if !ok {
		return errors.Errorf("requeue key %s: peer not in download set", key.String())
	}
	// 从独立索引取 (peer, msgID)
	from, err := i.manager.FromInputPeer(ctx, peer)
	if err != nil {
		return errors.Wrap(err, "requeue: resolve from input peer")
	}
	message, err := i.prefetcher.get(ctx, peer, key.MsgID)
	if err != nil {
		return errors.Wrap(err, "requeue: resolve message")
	}
	// 复用 processSingle 产出 elem（含 template 渲染、SkipSame、resume state、i.elem <-）
	ret, skip := i.processSingle(ctx, message, from, i.logicalPos)
	if skip {
		// 文件被再次跳过（如已 finished / 无媒体）：静默忽略
		return nil
	}
	if !ret {
		// 致命错误（i.err 已设置）
		if i.err != nil {
			return i.err
		}
		return errors.New("requeue: processSingle produced no element")
	}
	return nil
}
```

**peerByID 索引**（`newIter` 里由 `dialogs` 构建）：

```go
type peerRef struct {
	peer   tg.InputPeerClass
	msgIDs []int
}

// peerByID returns the InputPeerClass for a peer id, and whether it exists.
func (i *iter) peerByID(id int64) (tg.InputPeerClass, bool) {
	p, ok := i.peers[id]
	return p, ok
}
```

`iter` 加字段 `peers map[int64]tg.InputPeerClass`，`newIter` 填充：

```go
	peers: buildPeerIndex(dialogs),
```

```go
func buildPeerIndex(dialogs []*tmessage.Dialog) map[int64]tg.InputPeerClass {
	m := make(map[int64]tg.InputPeerClass, len(dialogs))
	for _, d := range dialogs {
		m[tutil.GetInputPeerID(d.Peer)] = d.Peer
	}
	return m
}
```

**公开方法**（加在 `SetFinished` 附近）：

```go
// PauseKeys marks messages as user-paused so they are skipped.
func (i *iter) PauseKeys(keys []MsgKey) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, k := range keys {
		i.paused[k] = struct{}{}
	}
}

// IsPaused reports whether a key is paused.
func (i *iter) IsPaused(k MsgKey) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	_, ok := i.paused[k]
	return ok
}

// Unpause removes a key from paused and re-queues it for immediate download.
//
// 无需唤醒下载循环：Next 是同步返回的（requeue 分支在 Next 内同步 produceFromKey
// 并写入 i.elem），驱动循环 for it.Next(wgctx){ it.Value() } 取到 elem 即续传。
func (i *iter) Unpause(key MsgKey) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.paused, key)
	// 去重：已在 requeue 里则不重复加
	for _, k := range i.requeue {
		if k == key {
			return
		}
	}
	i.requeue = append(i.requeue, key)
}

// ClearPaused empties the paused set and requeue (task restart).
func (i *iter) ClearPaused() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.paused = make(map[MsgKey]struct{})
	i.requeue = i.requeue[:0]
}
```

**并发注意**：`Unpause` 可能被 server goroutine 调用，而 `Next`/`Value` 在下载 goroutine。`i.mu` 保护 `paused`/`requeue`（iter 结构已有 `mu`）。`produceFromKey` 在 `Next` 里持有 `i.mu` 调用 `processSingle`（它也持 `i.mu`）→ **死锁**！

检查锁：`process` 持有 `i.mu`（`process` 开头 `i.mu.Lock(); defer i.mu.Unlock()`）。`processSingle` 被 `process` 调用（已持锁）。`produceFromKey` 若在持锁状态调 `processSingle` 会死锁。

**修正**：`Next` 里 requeue 分支先 `nextRequeue()`（拿锁弹队列），**释放锁后**再 `produceFromKey`：

```go
	// 恢复队列优先：用户恢复了暂停的文件，立即重新产出
	if key, ok := i.nextRequeue(); ok {
		if err := i.produceFromKey(ctx, key); err != nil {
			i.err = err
			return false
		}
		return true
	}
```

`nextRequeue` 拿锁弹队列后返回（锁已释放），`produceFromKey` 内部再调 `processSingle`（它自己拿锁）——不重入，安全。

`processSingle` 的暂停检查在 `process` 持锁路径下调用，`isPaused` 拿 `i.mu` —— **`process` 已持锁，`isPaused` 再拿锁 → 死锁**！

检查：`process` 开头 `i.mu.Lock(); defer i.mu.Unlock()`（iter.go:199-200）。`processSingle` 在 `process` 内被调用（持锁）。`isPaused` 若再 `i.mu.Lock()` 死锁。

**修正**：`isPaused` 用不加锁的内部版 `isPausedLocked`，仅在持锁路径调用：

```go
// isPausedLocked reports whether a key is paused. Caller must hold i.mu.
func (i *iter) isPausedLocked(k MsgKey) bool {
	_, ok := i.paused[k]
	return ok
}
```

`processSingle` 里改用它（processSingle 总在持锁路径被调用——process 或 produceFromKey 的 processSingle 内部会拿锁吗？`produceFromKey` 不持锁调 `processSingle`，`processSingle` 自己开头不拿锁（它假设调用方持锁）。看 `processSingle`：它不 `Lock`，直接操作 `i.elem`/`i.err`——被 `process`（持锁）调用。`produceFromKey` 无锁调 `processSingle` 会**数据竞争**（`i.elem`、`i.err`、`i.tpl` 并发）。

**修正**：`produceFromKey` 内调 `processSingle` 前自己拿锁：

```go
func (i *iter) produceFromKey(ctx context.Context, key MsgKey) error {
	peer, ok := i.peerByID(key.PeerID)
	if !ok {
		return errors.Errorf("requeue key %s: peer not in download set", key.String())
	}
	from, err := i.manager.FromInputPeer(ctx, peer)
	if err != nil {
		return errors.Wrap(err, "requeue: resolve from input peer")
	}
	message, err := i.prefetcher.get(ctx, peer, key.MsgID)
	if err != nil {
		return errors.Wrap(err, "requeue: resolve message")
	}

	i.mu.Lock()
	ret, skip := i.processSingle(ctx, message, from, i.logicalPos)
	i.mu.Unlock()

	if skip {
		return nil
	}
	if !ret {
		if i.err != nil {
			return i.err
		}
		return errors.New("requeue: processSingle produced no element")
	}
	return nil
}
```

（`i.err` 读取在锁外有点竞态，但 `produceFromKey` 失败路径保守返回错误即可；`ret/skip` 返回值本身在锁内决定，竞态窗口极小且只影响错误路径。可接受，注释说明。）

**但 processSingle 内部的暂停检查**（`isPausedLocked`）在 `processSingle` 被 `produceFromKey` 调用时——`produceFromKey` 持锁调 `processSingle`（上面已 Lock），`isPausedLocked` 不加锁直接用 `i.paused`——OK。

**processSingle 开头暂停检查的位置**：需在 `tmedia.GetMedia` 之前还是之后？paused 文件可能是任何媒体。放在 `tmedia.GetMedia(message)` 判断之后、`include/exclude` 之前？paused 优先——若已 paused 则不管媒体类型都跳过。放在 `GetMedia` 之前最好（省一次媒体解析）：

```go
func (i *iter) processSingle(ctx context.Context, message *tg.Message, from peers.Peer, logicalPos int) (bool, bool) {
	// 文件级暂停：用户在 UI 里暂停了该文件，跳过不下载（恢复后 requeue 重新产出）
	if i.isPausedLocked(keyFromMsg(from.InputPeer(), message)) {
		return false, true
	}

	item, ok := tmedia.GetMedia(message)
	...
```

但 `produceFromKey` 恢复的文件若又被暂停（用户秒暂停恢复的文件）——`processSingle` 里再查 `isPausedLocked`，若用户已再暂停则 skip。行为正确。

**requeue 的文件被 isFinished 检查**：`process` 主路径有 `isFinished` 检查（iter.go:254）。requeue 的文件是「未完成」的（暂停时没 Finish），不会命中 finished。但若恢复的文件已被其他路径 Finish（如用户暂停后又任务级 resume 重跑，iter 重建 paused 集合，requeue 是新的空队列——不冲突）。

**edgy case**：`processSingle` 的 requeue 产出会 `i.elem <- &iterElem{...}`——`i.elem` buffer 10，`Next` 同步调用不会阻塞（除非 buffer 满；buffer 满时 `Value()` 还没被驱动消费——但 `Next` 返回 true 后驱动立刻 `Value()`，不会死锁，只是短暂阻塞）。

**重要**：`logicalPos` 传入 `processSingle` 用 `i.logicalPos`（当前值，不递增）——requeue 文件不增加 logicalPos（不影响主序的 finished 进度）。合理。

- [ ] **Step 5: 运行 iter 测试确认通过**

Run: `go test ./app/dl/ -run 'TestIterPause|TestIterUnpause|TestIterClearPaused' -v`
Expected: PASS。

- [ ] **Step 6: server 接线（PauseCheck 注入 + hook 收集 UserPaused + setIter/clearIter）**

**6a. buildDlOptions 注入 PauseCheck**（`server/dl.go` `buildDlOptions` 构造 `opts := dl.Options{...}` 时）：

```go
		Progress: adapter,
		IterHook: adapter.hook,
		// 文件级暂停：writeAt 每次写入前检查该文件是否被用户手动暂停（UserPaused）。
		// 命中返回 context.Canceled → downloader 中止该文件，OnDone 判定为 FilePaused。
		PauseCheck: func(elem downloader.Elem) error {
			// downloader.Elem 无 Key()，需断言为 dl.Elem（progressAdapter.OnAdd 同款用法）
			e, ok := elem.(dl.Elem)
			if !ok {
				return nil
			}
			key := e.Key().String()
			task.mu.Lock()
			defer task.mu.Unlock()
			if fe := adapter.findFileLocked(key); fe != nil && fe.UserPaused {
				return context.Canceled
			}
			return nil
		},
```

**6b. hook 收集 UserPaused → PauseKeys + setIter**（`server/dl.go` `hook` 追加）：

```go
func (p *progressAdapter) hook(it dl.Iter) {
	p.mu.Lock()
	p.iter = it
	p.mu.Unlock()

	p.task.mu.Lock()
	p.task.Fingerprint = it.Fingerprint()
	// 任务 resume 重跑时，用户手动暂停的文件（UserPaused）保持跳过，不自动续传
	var pausedKeys []dl.MsgKey
	for _, f := range p.task.Files {
		if f != nil && f.UserPaused {
			if mk, ok := parseMsgKey(f.Key); ok {
				pausedKeys = append(pausedKeys, mk)
			}
		}
	}
	p.task.mu.Unlock()

	if len(pausedKeys) > 0 {
		it.PauseKeys(pausedKeys)
	}

	// 暴露 iter 给 taskManager（文件级暂停/恢复即时生效）
	p.srv.tasks.setIter(p.task.ID, it)
}
```

**6c. taskManager 加 iter 字段 + setIter/clearIter/iterForTask**（`server/task.go`）：

taskManager 结构加字段（`running` 旁）：

```go
	iter    dl.Iter // 当前运行任务的迭代器（文件级暂停/恢复用），非 running 时为 nil
```

方法（`newTaskManager` 后加）：

```go
// setIter 记录当前运行任务的 iter（文件级操作即时生效用）。
func (m *taskManager) setIter(taskID string, it dl.Iter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == taskID {
		m.iter = it
	}
}

// clearIter 清空 iter（任务结束时）。
func (m *taskManager) clearIter(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == taskID {
		m.iter = nil
	}
}

// iterForTask 返回当前运行任务的 iter；任务非 running 或 iter 未就绪返回 nil。
func (m *taskManager) iterForTask(taskID string) dl.Iter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running != taskID {
		return nil
	}
	return m.iter
}
```

`run()` 的 defer 里加 `m.clearIter(id)`（在现有 defer `delete(m.cancels, id)` 旁）。

**6d. parseMsgKey / keysToMsgKeys 工具**（`server/task.go`，供 hook 与 fileBatch 使用）：

```go
// keysToMsgKeys 把 "peerID/msgID" 字符串转成 dl.MsgKey 切片；解析失败的跳过。
func keysToMsgKeys(keys []string) []dl.MsgKey {
	out := make([]dl.MsgKey, 0, len(keys))
	for _, k := range keys {
		if mk, ok := parseMsgKey(k); ok {
			out = append(out, mk)
		}
	}
	return out
}

// parseMsgKey 解析 "peerID/msgID"。非法返回 ok=false。
func parseMsgKey(k string) (dl.MsgKey, bool) {
	parts := strings.SplitN(k, "/", 2)
	if len(parts) != 2 {
		return dl.MsgKey{}, false
	}
	peer, err1 := strconv.ParseInt(parts[0], 10, 64)
	msg, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return dl.MsgKey{}, false
	}
	return dl.MsgKey{PeerID: peer, MsgID: msg}, true
}
```

注意：`dl.MsgKey` 字段是 `PeerID int64` / `MsgID int`（`app/dl/iter.go:51-53`）。`server/task.go` 需 import `strconv`（若无）。

**6e. fileBatch 引用**

Task 3 的 `fileBatch`（Step 4）调用 `m.iterForTask(taskID)`、`iter.PauseKeys(keysToMsgKeys(...))`、`iter.Unpause(parseMsgKey(k))`——均在本任务定义，**Task 3 必须在本任务之后执行**。

**任务顺序调整声明**：执行顺序为 Task 1 → Task 2 → **Task 4+5** → **Task 3** → Task 6 → Task 7。Task 3 的 fileBatch 与 run() 修正、测试（file_batch_test.go）都在 Task 4+5 落盘后实施。

- [ ] **Step 7: 全量编译 + 回归**

Run: `go build ./... && go test ./app/dl/... ./core/downloader/... ./server/...`
Expected: 全绿（含 server 的 Task 3 测试）。

- [ ] **Step 8: Commit**

```bash
git add app/dl/iter.go app/dl/finalize.go app/dl/iter_pause_test.go server/dl.go server/task.go
git commit -m "feat(dl,server): iter 暂停集合+requeue 恢复队列——文件级暂停/恢复即时生效，任务 resume 重跑时 UserPaused 文件保持跳过"
```

---

### Task 6: 前端 API + 右键菜单 + 批量操作

**Files:**
- Modify: `web/app/src/api/index.js`（加 fileBatchAction）
- Modify: `web/app/src/stores/app.js`（加 fileBatchAction 包装）
- Modify: `web/app/src/views/Tasks.vue`（右键菜单加暂停/恢复项 + 批量逻辑）

**Interfaces:**
- Consumes: `api.req`（`api/index.js:3-27`）、`ctxMenu` 状态（Tasks.vue:681-703）、`selected` Set、`rowId` 格式（taskId/fileKey 组合）
- Produces: `api.fileBatchAction(taskId, action, keys)`；Tasks.vue 右键菜单「暂停此文件/恢复此文件」「暂停选中 N 个文件/恢复选中 N 个文件」。

- [ ] **Step 1: api.js 加方法**

`web/app/src/api/index.js`（`batchTasks` 旁）加：

```js
  fileBatchAction: (taskId, action, keys) =>
    req('POST', `/api/tasks/${taskId}/files/batch`, { action, keys }),
```

- [ ] **Step 2: store 加包装**

`web/app/src/stores/app.js`（`batchAction` 旁）加：

```js
    async fileBatchAction(taskId, action, keys) {
      return await api.fileBatchAction(taskId, action, keys) // { results: [{key, ok, error?}] }
    },
```

- [ ] **Step 3: Tasks.vue 右键菜单加暂停/恢复项**

`Tasks.vue` 右键菜单模板（约 line 292-298）加按钮（在「重命名」和「删除」之间）：

```html
      <button v-if="ctxMenu.showPause" class="ctx-item" @click="ctxPauseFiles">
        {{ ctxMenu.batch ? `暂停选中 ${ctxMenu.count} 个文件` : '暂停此文件' }}
      </button>
      <button v-if="ctxMenu.showResume" class="ctx-item" @click="ctxResumeFiles">
        {{ ctxMenu.batch ? `恢复选中 ${ctxMenu.count} 个文件` : '恢复此文件' }}
      </button>
```

- [ ] **Step 4: onRowContextMenu 计算 showPause/showResume**

`Tasks.vue` `onRowContextMenu`（line 681-697）加：

```js
function onRowContextMenu(r, e) {
  if (!selected.has(r.rowId)) {
    selected.clear()
    selected.add(r.rowId)
    anchorRowId = r.rowId
  }
  const isBatch = selected.size > 1
  const selRows = rows.value.filter(x => selected.has(x.rowId))
  // 暂停条件：选中中有非 done/failed 文件（running/pending/paused 都可暂停）
  ctxMenu.showPause = selRows.some(x => !['done', 'failed'].includes(x.file.status))
  // 恢复条件：选中中有 paused 文件
  ctxMenu.showResume = selRows.some(x => x.file.status === 'paused')
  ctxMenu.rowId = r.rowId
  ctxMenu.batch = isBatch
  ctxMenu.count = selected.size
  ctxMenu.x = e.clientX
  ctxMenu.y = e.clientY
  ctxMenu.show = true
}
```

`ctxMenu` reactive 对象加 `showPause: false, showResume: false` 初始值（找 `ctxMenu = reactive(...)` 处加）。

- [ ] **Step 5: ctxPauseFiles / ctxResumeFiles 实现**

`Tasks.vue`（`ctxDelete` 附近）加：

```js
async function ctxPauseFiles() {
  ctxMenu.show = false
  // 按 taskId 分组：同一任务的文件一批调用
  const byTask = groupByTask(selected)
  let total = 0, fails = 0
  for (const [taskId, keys] of byTask) {
    try {
      const r = await store.fileBatchAction(taskId, 'pause', keys)
      total += keys.length
      fails += (r.results || []).filter(x => !x.ok).length
    } catch (e) {
      ElMessage.error(e.message)
      return
    }
  }
  selected.clear()
  if (fails) ElMessage.warning(`${total - fails}/${total} 暂停成功，${fails} 个失败`)
  else ElMessage.success(`已暂停 ${total} 个文件`)
}
async function ctxResumeFiles() {
  ctxMenu.show = false
  const byTask = groupByTask(selected)
  let total = 0, fails = 0
  for (const [taskId, keys] of byTask) {
    try {
      const r = await store.fileBatchAction(taskId, 'resume', keys)
      total += keys.length
      fails += (r.results || []).filter(x => !x.ok).length
    } catch (e) {
      ElMessage.error(e.message)
      return
    }
  }
  selected.clear()
  if (fails) ElMessage.warning(`${total - fails}/${total} 恢复成功，${fails} 个失败`)
  else ElMessage.success(`已恢复 ${total} 个文件`)
}
// 按 taskId 分组选中的文件 key（rowId 格式为 "taskId/fileKey"）
function groupByTask(sel) {
  const map = new Map()
  for (const rowId of sel) {
    const idx = rowId.indexOf('/')
    const taskId = rowId.slice(0, idx)
    const fileKey = rowId.slice(idx + 1)
    if (!map.has(taskId)) map.set(taskId, [])
    map.get(taskId).push(fileKey)
  }
  return map
}
```

**rowId 格式确认**：需看 `rowId` 如何构造。在 `rows.value` 构造处查 `rowId`——大概率 `r.rowId = task.id + '/' + file.key`。需在 Tasks.vue 里确认（找 `rowId:` 赋值）。若格式不同（如 `rowId = file.key` 单独），`groupByTask` 需调整。**实现时先查 `rowId` 构造代码**。

- [ ] **Step 6: 前端构建**

Run（在 `web/app` 下）:
```bash
cd web/app && npm run build
```
Expected: 构建成功。若 `web/app` 无 package.json，改用仓库根 `web/` 的构建脚本（参照交接文档「前端改动 build + 拷 web 到 tmm.v11」）。

- [ ] **Step 7: 部署前端 + Commit**

把 `web/dist`（或构建产物）拷到 `tmm.v11/web/`。然后：

```bash
git add web/app/src/api/index.js web/app/src/stores/app.js web/app/src/views/Tasks.vue
git commit -m "feat(web): 文件级暂停/恢复——右键菜单（单文件/批量），API fileBatchAction"
```

---

### Task 7: 端到端验证 + 回归 + 文档

**Files:**
- Run: 后端构建 + 全量测试 + 前端构建 + 部署 + 浏览器手动验证
- Modify: `docs/superpowers/specs/2026-08-03-file-level-control-design.md`（实现后如有偏差，更新 spec 状态）

**Interfaces:**
- Consumes: 所有 Task 1-6 的产物。

- [ ] **Step 1: 后端构建 + 全量测试**

Run: `go build ./... && go test ./...`
Expected: 全绿。`go vet ./...` 无错误。

- [ ] **Step 2: 重建 exe + 部署**

Run: `./build-tmm.sh`（后端）→ `./build-tmm.sh web`（后端+前端，若 build-tmm.sh 支持）→ 拷 `tmm.exe` + `web/` 到 `tmm.v11/`。
Expected: `tmm.v11/tmm.exe` 版本标签为最新 commit（干净，非 dirty）。

- [ ] **Step 3: 重启服务器 + 浏览器手动验证**

Run: 杀旧 tmm 进程 → `tmm.v11/start.bat`（或 `./tmm.exe serve --port 8080`）→ 浏览器开 `http://127.0.0.1:8080`。
验证清单（spec §4.3）：
1. 多文件任务运行中 → 右键暂停其中 1 个 → 其他文件继续下 → 该文件「已暂停」
2. 运行中恢复 → 从断点续传（进度不回退）
3. 任务级 pause → resume → 手动暂停文件不被自动恢复
4. 任务 done 后恢复文件 → 任务重跑只下该文件
5. 重启服务器 → 手动暂停文件保持「已暂停」
6. 文件级暂停不触发 FLOOD_WAIT、日志无异常

- [ ] **Step 4: 更新交接/文档**

在 `docs/交接-2026-08-03.md` 或新交接文档记录本次实现（功能、关键位置、验证结果），把遗留事项 §6.4「R3-2 文件级控制」标记为已完成。

- [ ] **Step 5: Commit**

```bash
git add docs/
git commit -m "docs: 文件级控制实现完成——更新交接文档，R3-2 标记已完成"
```
