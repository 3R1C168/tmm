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
