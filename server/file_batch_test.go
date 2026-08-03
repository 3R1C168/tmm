package server

import (
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
