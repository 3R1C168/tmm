package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// finalizeDoneLocked 的收尾语义（任务成功完成时）：
//   - FilePending → FileDone：SkipSame/无媒体/filter 跳过的预填残留
//   - FilePaused && !UserPaused → FileDone：任务级暂停中断、恢复重跑时被
//     SkipSame 命中的文件（磁盘已有 → 语义上已完成）
//   - FilePaused && UserPaused → 保持 paused：用户文件级手动暂停，保留恢复能力
//   - FileDone/FileFailed/FileRunning → 不动
func TestFinalizeDoneLocked(t *testing.T) {
	task := &Task{Files: []*FileEntry{
		{Key: "1", Status: FilePending},                   // SkipSame 残留 → done
		{Key: "2", Status: FilePaused, UserPaused: false}, // 任务级暂停 + SkipSame 残留 → done
		{Key: "3", Status: FilePaused, UserPaused: true},  // 手动暂停 → 保持 paused
		{Key: "4", Status: FileDone},                      // 已完成 → 不动
		{Key: "5", Status: FileFailed},                    // 失败 → 不动
		{Key: "6", Status: FileRunning},                   // 运行中（理论上 done 收尾不该有）→ 不动
		nil,                                               // nil 条目 → 不 panic
	}}

	task.mu.Lock()
	finalizeDoneLocked(task)
	task.mu.Unlock()

	want := []struct {
		key    string
		status FileStatus
	}{
		{"1", FileDone},
		{"2", FileDone},
		{"3", FilePaused}, // UserPaused 保持
		{"4", FileDone},
		{"5", FileFailed},
		{"6", FileRunning},
	}
	for _, w := range want {
		for _, f := range task.Files {
			if f != nil && f.Key == w.key {
				require.Equalf(t, w.status, f.Status, "file %s", w.key)
			}
		}
	}
}

// 手动暂停的文件在收尾后仍保留 UserPaused 标记（恢复能力不被破坏）。
func TestFinalizeDoneKeepsUserPausedFlag(t *testing.T) {
	task := &Task{Files: []*FileEntry{
		{Key: "a", Status: FilePaused, UserPaused: true},
	}}
	task.mu.Lock()
	finalizeDoneLocked(task)
	task.mu.Unlock()

	f := task.Files[0]
	require.Equal(t, FilePaused, f.Status)
	require.True(t, f.UserPaused)
}
