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

// 文件名前缀（channelIdPrefix/msgIdPrefix）开启时，磁盘文件名是
// "#<频道ID> #<msgID> <peer>_<msg>_<原名>"，{peer}_{msg}_ 不再在开头。
// glob 必须仍能匹配（空格后命中），同时 other_<peer>_<msg>_x.mp4 不应误配。
func TestGlobTaskFiles_WithFilenamePrefix(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "#2070986972 频道")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	// 命中：频道ID+消息ID前缀同时开启（空格后出现 {peer}_{msg}_）
	f1 := filepath.Join(sub, "#2070986972 #22713 2070986972_22713_video.mp4")
	require.NoError(t, os.WriteFile(f1, []byte("x"), 0o644))
	// 命中：仅消息ID前缀（也含空格 + {peer}_{msg}_）
	f2 := filepath.Join(sub, "#22713 2070986972_22713_photo.jpg")
	require.NoError(t, os.WriteFile(f2, []byte("x"), 0o644))
	// 命中：仅频道ID前缀
	f3 := filepath.Join(sub, "#2070986972 2070986972_22713_doc.pdf")
	require.NoError(t, os.WriteFile(f3, []byte("x"), 0o644))
	// 命中：前缀 + .tmp 半成品
	f4 := filepath.Join(sub, "#2070986972 #22713 2070986972_22713_video.mp4.tmp")
	require.NoError(t, os.WriteFile(f4, []byte("x"), 0o644))
	// 命中：前缀 + .tdlstate 控制文件
	f5 := filepath.Join(sub, "#2070986972 #22713 2070986972_22713_video.mp4.tdlstate")
	require.NoError(t, os.WriteFile(f5, []byte("x"), 0o644))

	// 不应命中：{peer}_{msg}_ 出现在名字中间、前无空格
	miss1 := filepath.Join(sub, "other_2070986972_22713_thing.mp4")
	require.NoError(t, os.WriteFile(miss1, []byte("x"), 0o644))
	// 不应命中：peer/msg 无关文件
	miss2 := filepath.Join(sub, "random.txt")
	require.NoError(t, os.WriteFile(miss2, []byte("x"), 0o644))

	paths := globTaskFiles(dir, "2070986972", "22713")
	require.ElementsMatch(t, []string{f1, f2, f3, f4, f5}, paths)
}

// 无前缀（两个开关都关）时仍只匹配开头的 {peer}_{msg}_。
func TestGlobTaskFiles_FlatNoPrefix(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "2070986972_22713_video.mp4")
	require.NoError(t, os.WriteFile(f1, []byte("x"), 0o644))
	f2 := filepath.Join(dir, "2070986972_22713_video.mp4.tdlstate")
	require.NoError(t, os.WriteFile(f2, []byte("x"), 0o644))
	miss := filepath.Join(dir, "x2070986972_22713_video.mp4") // 非开头且前无空格
	require.NoError(t, os.WriteFile(miss, []byte("x"), 0o644))

	paths := globTaskFiles(dir, "2070986972", "22713")
	require.ElementsMatch(t, []string{f1, f2}, paths)
}
