package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

// newBatchTestServer 已在 task_batch_test.go 定义，复用。
// 注意：newBatchTestServer 不接真实 telegram（accounts.ensure 会失败）。
// 因此任何会触发 m.resume（内部调 accounts.ensure）的 fileBatch 测试路径都
// 不能在这里跑——非 running 任务触发 resume 的行为已由 handler 测试
// TestHandleFilesBatchNonRunningResume（stub 账号）覆盖。

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

	res, err := s.tasks.fileBatch(context.Background(), "t1", "pause", []string{"1/1", "1/2", "1/3"})
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
		// StatusRunning：running 任务 resume 走 iter 即时续传，不触发 m.resume
		// （不需要 telegram）。「非 running 任务 resume 触发任务 resume 重跑」
		// 的路径见 TestHandleFilesBatchNonRunningResume（stub 账号）。
		ID: "t2", Account: "default", Status: StatusRunning, CreatedAt: time.Now(),
		Files: []*FileEntry{
			{Key: "2/1", Status: FilePaused, UserPaused: true},
			{Key: "2/2", Status: FilePaused, UserPaused: false}, // 任务级暂停产生的 paused
			{Key: "2/3", Status: FileDone},
		},
	}
	require.NoError(t, s.store.upsert(task))

	res, err := s.tasks.fileBatch(context.Background(), "t2", "resume", []string{"2/1", "2/2", "2/3"})
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
	_, err := s.tasks.fileBatch(context.Background(), "missing", "pause", []string{"1/1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "task not found")
	// action 非法
	_, err = s.tasks.fileBatch(context.Background(), "v1", "bogus", []string{"1/1"})
	require.Error(t, err)
	// keys 为空
	_, err = s.tasks.fileBatch(context.Background(), "v1", "pause", []string{})
	require.Error(t, err)
	// key 不属于任务
	res, err := s.tasks.fileBatch(context.Background(), "v1", "pause", []string{"9/9"})
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.False(t, res[0].OK)
}

// fakePool 实现 dcpool.Pool，让 stub 账号通过 ready() 检查（不接真实网络）。
type fakePool struct{}

func (fakePool) Client(ctx context.Context, dc int) *tg.Client  { return nil }
func (fakePool) Takeout(ctx context.Context, dc int) *tg.Client { return nil }
func (fakePool) Default(ctx context.Context) *tg.Client         { return nil }
func (fakePool) Close() error                                   { return nil }

// TestFileBatchResumeNoAffectedDoesNotTriggerResume：恢复请求里没有任何文件
// 真正被恢复（全部 done/failed/不存在）时，即使任务非 running 也绝不触发
// m.resume（否则会凭空把整个任务重跑一遍）。
func TestFileBatchResumeNoAffectedDoesNotTriggerResume(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{
		ID: "nr2", Account: "default", Status: StatusPaused, CreatedAt: time.Now(),
		Files: []*FileEntry{{Key: "4/1", Status: FileDone}},
	}))

	res, err := s.tasks.fileBatch(context.Background(), "nr2", "resume", []string{"4/1"})
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.False(t, res[0].OK)

	tk, _ := s.store.get("nr2")
	require.Equal(t, StatusPaused, tk.Status, "no affected file → task must stay paused")
	s.tasks.mu.Lock()
	require.NotContains(t, s.tasks.queue, "nr2")
	s.tasks.mu.Unlock()
}

// TestHandleFilesBatchNonRunningResume：非 running 任务恢复文件 → 触发任务
// resume 重跑（spec §3.4 step 3）。用 stub 账号避免连接真实 telegram。
func TestHandleFilesBatchNonRunningResume(t *testing.T) {
	s := newBatchTestServer(t)
	// stub 账号：resume 会调 accounts.ensure + ready()，直接注入已就绪账号
	s.accounts = &accountManager{accounts: make(map[string]*account)}
	a := &account{ns: "default", done: make(chan struct{}), readyCh: make(chan struct{})}
	a.mu.Lock()
	a.authorized = true
	a.pool = fakePool{}
	a.mu.Unlock()
	s.accounts.mu.Lock()
	s.accounts.accounts["default"] = a
	s.accounts.mu.Unlock()

	require.NoError(t, s.store.upsert(&Task{
		ID: "nr1", Account: "default", Status: StatusPaused, CreatedAt: time.Now(),
		Files: []*FileEntry{
			{Key: "3/1", Status: FilePaused, UserPaused: true},
			{Key: "3/2", Status: FileDone},
		},
	}))

	body, _ := json.Marshal(map[string]any{"action": "resume", "keys": []string{"3/1", "3/2"}})
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/nr1/files/batch", bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"id": "nr1"})
	rec := httptest.NewRecorder()
	s.handleFilesBatch(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Results []FileBatchResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Results, 2)
	require.True(t, resp.Results[0].OK)
	require.False(t, resp.Results[1].OK, "done file cannot be resumed")

	// 非 running + 确有恢复文件 → 任务被 resume 重入队（Status → pending）
	tk, _ := s.store.get("nr1")
	require.Equal(t, StatusPending, tk.Status)
	s.tasks.mu.Lock()
	queued := false
	for _, id := range s.tasks.queue {
		if id == "nr1" {
			queued = true
		}
	}
	s.tasks.mu.Unlock()
	require.True(t, queued, "non-running resume must re-enqueue the task")
}

// TestHandleFilesBatchNotFound：任务不存在时 file batch 返回 404（spec §3.4）。
func TestHandleFilesBatchNotFound(t *testing.T) {
	s := newBatchTestServer(t)
	body, _ := json.Marshal(map[string]any{"action": "resume", "keys": []string{"1/1"}})
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/ghost/files/batch", bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"id": "ghost"})
	rec := httptest.NewRecorder()
	s.handleFilesBatch(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
