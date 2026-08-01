package server

import (
	"context"
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

	res := s.tasks.Batch(context.Background(), []string{"a", "b", "c"}, "pause")
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

	res := s.tasks.Batch(context.Background(), []string{"p1", "p2"}, "pause")
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

func TestBatchPartialFailure(t *testing.T) {
	s := newBatchTestServer(t)
	require.NoError(t, s.store.upsert(&Task{ID: "ok1", Status: StatusDone, Files: []*FileEntry{}}))

	res := s.tasks.Batch(context.Background(), []string{"ok1", "ghost"}, "delete")
	require.Len(t, res, 2)
	require.True(t, res[0].OK)
	require.False(t, res[1].OK)
	require.Contains(t, res[1].Error, "not found")
}
