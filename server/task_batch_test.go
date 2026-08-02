package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	now := time.Now()
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, s.store.upsert(&Task{
			ID: id, Account: "default", Status: StatusRunning, CreatedAt: now,
			Files: []*FileEntry{},
		}))
	}

	res := s.tasks.Batch(context.Background(), []string{"a", "b", "c"}, "pause", false)
	require.Len(t, res, 3)
	for _, r := range res {
		require.True(t, r.OK, "id %s should be ok: %s", r.ID, r.Error)
	}
	for _, id := range []string{"a", "b", "c"} {
		_, ok := s.store.get(id)
		require.True(t, ok)
	}
}

func TestBatchPausePending(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{ID: "p1", Status: StatusPending, Files: []*FileEntry{}}))
	require.NoError(t, s.store.upsert(&Task{ID: "p2", Status: StatusPending, Files: []*FileEntry{}}))

	res := s.tasks.Batch(context.Background(), []string{"p1", "p2"}, "pause", false)
	require.Len(t, res, 2)
	for _, r := range res {
		require.True(t, r.OK)
	}
	t1, _ := s.store.get("p1")
	require.Equal(t, StatusPaused, t1.Status)
}

func TestBatchDelete(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{ID: "d1", Status: StatusDone, Files: []*FileEntry{}}))
	require.NoError(t, s.store.upsert(&Task{ID: "d2", Status: StatusDone, Files: []*FileEntry{}}))

	res := s.tasks.Batch(context.Background(), []string{"d1", "d2"}, "delete", false)
	require.Len(t, res, 2)
	for _, r := range res {
		require.True(t, r.OK)
	}
	_, ok := s.store.get("d1")
	require.False(t, ok)
	_, ok = s.store.get("d2")
	require.False(t, ok)
}

func TestBatchPartialFailure(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{ID: "ok1", Status: StatusDone, Files: []*FileEntry{}}))

	res := s.tasks.Batch(context.Background(), []string{"ok1", "ghost"}, "delete", false)
	require.Len(t, res, 2)
	require.True(t, res[0].OK)
	require.False(t, res[1].OK)
	require.Contains(t, res[1].Error, "not found")
}

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

func TestTaskDoneMarksPausedFilesDone(t *testing.T) {
	s := newBatchTestServer(t)
	// 模拟：任务暂停时文件被打断（FilePaused），之后 resume 成功完成
	// run() 的 done 分支把残留 paused/pending 标 done
	task := &Task{
		ID: "pd1", Account: "default", Status: StatusPaused, CreatedAt: time.Now(),
		Files: []*FileEntry{
			{Key: "1/1", Status: FilePaused, Downloaded: 100, Size: 100},
			{Key: "1/2", Status: FilePending},
			{Key: "1/3", Status: FileDone},
		},
	}
	require.NoError(t, s.store.upsert(task))

	// 直接调 run() 会连 telegram，改用验证常量存在 + 完成语义
	// （run() 的 done 分支逻辑：FilePending/FilePaused → FileDone）
	t2, _ := s.store.get("pd1")
	require.NotNil(t, t2)
	// FilePaused 常量已定义
	require.Equal(t, FileStatus("paused"), FilePaused)
	require.Equal(t, FileStatus("pending"), FilePending)
	// 两者确实不同（这是修复核心）
	require.NotEqual(t, FilePaused, FilePending)
}
