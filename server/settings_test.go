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

func TestRenamePrefix(t *testing.T) {
	cases := []struct {
		force, channelOn bool
		key              string
		want             string // full prefix incl. trailing space
	}{
		{false, false, "2070986972/22713", ""},
		{true, false, "2070986972/22713", "#22713 "},
		{false, true, "2070986972/22713", ""}, // 单文件不强制，开关也不影响（needPrefix=false）
		{true, true, "2070986972/22713", "#2070986972 #22713 "},
	}
	for _, c := range cases {
		got := renamePrefix(c.force, c.channelOn, c.key)
		require.Equal(t, c.want, got, "force=%v channelOn=%v key=%v", c.force, c.channelOn, c.key)
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
