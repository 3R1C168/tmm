package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettings_NewFieldsDefaults(t *testing.T) {
	st := defaultSettings("test.json")
	require.False(t, st.channelIdPrefix())
	require.False(t, st.channelDir())
	require.Equal(t, "", st.downloadDir())
}

func TestSettings_LoadPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	write := `{"channelIdPrefix":true,"downloadDir":"D:\\dl\\x"}`
	require.NoError(t, os.WriteFile(path, []byte(write), 0o644))
	st := defaultSettings(path)
	require.NoError(t, st.load())
	require.True(t, st.channelIdPrefix())
	require.False(t, st.channelDir(), "未设置字段保持默认")
	require.Equal(t, `D:\dl\x`, st.downloadDir())
}

func TestSettings_SnapshotIncludesNewFields(t *testing.T) {
	st := defaultSettings("t.json")
	st.ChannelIdPrefix = true
	st.ChannelDir = true
	st.DownloadDir = "/dl"
	snap := st.snapshot()
	require.True(t, snap["channelIdPrefix"].(bool))
	require.True(t, snap["channelDir"].(bool))
	require.Equal(t, "/dl", snap["downloadDir"])
}

// strptr returns a pointer to s for tests that exercise pointer-typed fields.
func strptr(s string) *string { return &s }

// 下载目录清除分支：update 收到 downloadDir 空字符串时必须清空设置值（回落到默认目录），
// 而不是被 nil 判断跳过。UI 侧修复（Settings.vue 发空串而非省略该字段）依赖此分支。
func TestSettings_UpdateDownloadDir_Clear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	st := defaultSettings(path)
	st.DownloadDir = `D:\dl\custom`
	require.NoError(t, st.update(&updateSettingsRequest{DownloadDir: strptr("")}))
	require.Equal(t, "", st.downloadDir())
}

// 下载目录设置分支：非空值（绝对路径）应被接受并保存。
func TestSettings_UpdateDownloadDir_Set(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	st := defaultSettings(path)
	require.NoError(t, st.update(&updateSettingsRequest{DownloadDir: strptr(`E:\dl\new`)}))
	require.Equal(t, `E:\dl\new`, st.downloadDir())
}

// 旧位置（exe 旁 settings.json）→ 新位置（data/settings.json）迁移：
// 新路径不存在且旧路径存在时，load 应把旧文件移过去并读取其设置。
func TestSettings_MigrateFromLegacyPath(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "settings.json") // 旧路径 = exe 旁
	newPath := filepath.Join(dir, "data", "settings.json")

	write := `{"msgIdPrefix":true,"channelIdPrefix":true,"downloadDir":"D:\\dl\\x"}`
	require.NoError(t, os.MkdirAll(filepath.Dir(newPath), 0o755))
	require.NoError(t, os.WriteFile(legacy, []byte(write), 0o644))

	st := defaultSettings(newPath)
	st.legacyPath = legacy // 测试里固定 legacy 路径（绕过 os.Executable）
	require.NoError(t, st.load())

	// 旧文件被移动走，新路径读取到原设置
	_, err := os.Stat(legacy)
	require.True(t, os.IsNotExist(err), "旧 settings.json 应被移走")
	b, err := os.ReadFile(newPath)
	require.NoError(t, err)
	require.Contains(t, string(b), `"msgIdPrefix":true`)
	require.True(t, st.msgIdPrefix())
	require.True(t, st.channelIdPrefix())
	require.Equal(t, `D:\dl\x`, st.downloadDir())
}

func TestRenamePrefix(t *testing.T) {
	cases := []struct {
		addMsgId, channelOn bool
		key                 string
		want                string // full prefix incl. trailing space
	}{
		{false, false, "2070986972/22713", ""},
		{true, false, "2070986972/22713", "#22713 "},
		{false, true, "2070986972/22713", "#2070986972 "}, // 只开频道ID：单文件重命名保留频道前缀（修复点）
		{true, true, "2070986972/22713", "#2070986972 #22713 "},
	}
	for _, c := range cases {
		got := renamePrefix(c.addMsgId, c.channelOn, c.key)
		require.Equal(t, c.want, got, "addMsgId=%v channelOn=%v key=%v", c.addMsgId, c.channelOn, c.key)
	}
}

func TestBuildDlOptions_TemplatePrefix(t *testing.T) {
	// 直接测模板拼装逻辑：抽成 helper 便于测
	cases := []struct {
		channel, msg bool
		want         string
	}{
		{false, false, ""},
		{true, false, "#{{ .DialogID }} "},
		{false, true, "#{{ .MessageID }} "},
		{true, true, "#{{ .DialogID }} #{{ .MessageID }} "},
	}
	for _, c := range cases {
		got := buildFilenamePrefix(c.channel, c.msg)
		require.Equal(t, c.want, got, "prefix for channel=%v msg=%v", c.channel, c.msg)
	}
}

func TestHasExt(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"video.mp4", true},
		{"video", false},
		{"video.", false},  // 只有结尾点，无有效扩展名
		{"张碧晨.", false},  // 同上（中文名结尾点）
		{".mp4", true},     // 点开头但有内容
		{"a.b.c", true},    // 多段，ext=.c
		{"", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, hasExt(c.s), "hasExt(%q)", c.s)
	}
}

func TestRenameExtCompletion(t *testing.T) {
	// 验证补全判断：结尾带点的名字应触发补全（有有效扩展名时才跳过）
	cases := []struct {
		name string
		want bool // 是否应触发补全
	}{
		{"video", true},
		{"video.", true},  // 结尾点 → 需补全
		{"video.mp4", false},
		{"张碧晨.", true},
	}
	for _, c := range cases {
		got := !hasExt(c.name)
		require.Equal(t, c.want, got, "needsCompletion(%q)", c.name)
	}
}
