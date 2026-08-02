package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
