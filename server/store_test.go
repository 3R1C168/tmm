package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/logctx"
)

func testLoggerCtx() context.Context {
	return logctx.With(context.Background(), testLogger())
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server-tasks.json")

	s := newStore(path)
	require.NoError(t, s.load(testLoggerCtx()))

	now := time.Now()
	task := &Task{
		ID:        "abc123",
		Account:   "default",
		URLs:      []string{"https://t.me/c/1/2"},
		Status:    StatusDone,
		CreatedAt: now,
		Files: []*FileEntry{
			{Key: "1/2", Name: "a.mp4", Size: 100, Downloaded: 100, Status: FileDone},
		},
	}
	require.NoError(t, s.upsert(task))

	// reload from disk
	s2 := newStore(path)
	require.NoError(t, s2.load(testLoggerCtx()))

	got, ok := s2.get("abc123")
	require.True(t, ok)
	require.Equal(t, "default", got.Account)
	require.Equal(t, StatusDone, got.Status)
	require.Len(t, got.Files, 1)
	require.Equal(t, int64(100), got.TotalDownloaded())
}

func TestStoreMarksInterrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server-tasks.json")

	s := newStore(path)
	require.NoError(t, s.upsert(&Task{ID: "run1", Status: StatusRunning, Files: []*FileEntry{}}))
	require.NoError(t, s.upsert(&Task{ID: "pend1", Status: StatusPending, Files: []*FileEntry{}}))
	require.NoError(t, s.upsert(&Task{ID: "done1", Status: StatusDone, Files: []*FileEntry{}}))

	s2 := newStore(path)
	require.NoError(t, s2.load(testLoggerCtx()))

	run, _ := s2.get("run1")
	require.Equal(t, StatusInterrupted, run.Status)
	pend, _ := s2.get("pend1")
	require.Equal(t, StatusInterrupted, pend.Status)
	done, _ := s2.get("done1")
	require.Equal(t, StatusDone, done.Status)
}

func TestStoreListOrder(t *testing.T) {
	dir := t.TempDir()
	s := newStore(filepath.Join(dir, "tasks.json"))
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, s.upsert(&Task{ID: id, Status: StatusPending, Files: []*FileEntry{}}))
	}
	list := s.list()
	require.Len(t, list, 3)
	require.Equal(t, "a", list[0].ID)
	require.Equal(t, "c", list[2].ID)
}
