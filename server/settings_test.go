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
