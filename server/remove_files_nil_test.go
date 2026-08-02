package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 复现：store 从 JSON 加载后，task.Files 元素是否为 nil。
// 这是删除文件失败（removed:0, first file nil=true）的根因定位。
func TestStoreLoadFilesNotNil(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "tasks.json")
	// 构造一个含 files 的 store JSON（模拟真实 store）
	data := map[string]any{
		"tasks": []map[string]any{
			{
				"id":     "abc123",
				"status": "done",
				"opts":   map[string]any{"dir": dir},
				"files": []map[string]any{
					{"key": "111/222", "name": "f.mp4", "path": filepath.Join(dir, "111_222_f.mp4"), "status": "done"},
					{"key": "111/223", "name": "", "path": "", "status": "done"},
				},
			},
		},
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	os.WriteFile(storePath, b, 0o644)

	s := newStore(storePath)
	if err := s.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	task, ok := s.get("abc123")
	if !ok {
		t.Fatal("task not found")
	}
	if len(task.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(task.Files))
	}
	for i, f := range task.Files {
		if f == nil {
			t.Fatalf("files[%d] is nil", i)
		}
		t.Logf("files[%d]: key=%s path=%s", i, f.Key, f.Path)
	}
}

// channelDir 开启后，下载文件落在 {dir}/{channelDir 子目录}/ 下。
// removeTaskFiles 必须递归匹配 {peer}_{msg}_*，否则频道子目录里的文件删不掉。
func TestRemoveTaskFiles_ChannelDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "#2070986972 频道")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	f1 := filepath.Join(sub, "2070986972_22713_video.mp4")
	require.NoError(t, os.WriteFile(f1, []byte("x"), 0o644))
	f2 := filepath.Join(dir, "other.txt") // 不应被删
	require.NoError(t, os.WriteFile(f2, []byte("x"), 0o644))

	// 调用 removeTaskFiles 的 glob helper
	paths := globTaskFiles(dir, "2070986972", "22713")
	require.ElementsMatch(t, []string{f1}, paths)
}
