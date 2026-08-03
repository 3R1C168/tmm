package server

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Telegraph URL 解析 + 图片收集是纯逻辑，可单测。
// fetchTelegraphPage / 下载等涉及真实网络，由端到端验证。

func TestTelegraphPathFromURL(t *testing.T) {
	cases := []struct {
		u    string
		want string
	}{
		{"https://telegra.ph/New-Thoughts-07-04", "New-Thoughts-07-04"},
		{"https://telegra.ph/New-Thoughts-07-04?edit", "New-Thoughts-07-04"},
		{"https://telegra.ph/New-Thoughts-07-04#comments", "New-Thoughts-07-04"},
		{"http://telegra.ph/Some-Article", "Some-Article"}, // http 也接受
		{"https://telegra.ph/", ""},
		{"https://telegra.ph", ""},
		{"https://example.com/telegra.ph/x", ""}, // 非 telegra.ph 域名
		{"https://t.me/something", ""},
		{"", ""},
		{"not a url", ""},
	}
	for _, c := range cases {
		require.Equal(t, c.want, telegraphPathFromURL(c.u), "URL %q", c.u)
	}
}

func TestValidateProxiedImageURL(t *testing.T) {
	// 允许：公共 http(s) 外链（Telegraph 文章图片可能在任意图床）
	require.NoError(t, validateProxiedImageURL("https://telegra.ph/file/abc123.jpg"))
	require.NoError(t, validateProxiedImageURL("https://pic.850123.xyz/d/BQACxxx"))
	require.NoError(t, validateProxiedImageURL("http://cdn.example.com/a/b.png"))

	// 拒绝：非 http(s)、内网、本机、保留地址（SSRF 防护）
	require.Error(t, validateProxiedImageURL("file:///etc/passwd"))
	require.Error(t, validateProxiedImageURL("ftp://example.com/a.jpg"))
	require.Error(t, validateProxiedImageURL(""))
	require.Error(t, validateProxiedImageURL("http://127.0.0.1:8080/x"))
	require.Error(t, validateProxiedImageURL("http://localhost/x"))
	require.Error(t, validateProxiedImageURL("http://169.254.169.254/latest/meta-data"))
	require.Error(t, validateProxiedImageURL("http://[::1]:8080/x"))
}

func TestCollectTelegraphImages(t *testing.T) {
	// 构造一个真实 Telegraph content 形状的节点树：文本、img、嵌套 figure、链接
	raw := []json.RawMessage{
		json.RawMessage(`{"tag":"p","children":["hello"]}`),
		json.RawMessage(`{"tag":"figure","children":[{"tag":"img","attrs":{"src":"https://telegra.ph/file/abc123.jpg"}}]}`),
		json.RawMessage(`{"tag":"img","attrs":{"src":"https://telegra.ph/file/def456.png"}}`),
		json.RawMessage(`{"tag":"p","children":[{"tag":"a","attrs":{"href":"https://telegra.ph/Other"},"children":["link"]}]}`),
		json.RawMessage(`"just a string node"`),
	}
	imgs := collectTelegraphImages(raw)
	require.Equal(t, []string{
		"https://telegra.ph/file/abc123.jpg",
		"https://telegra.ph/file/def456.png",
	}, imgs)
}

func TestCollectTelegraphImagesEmpty(t *testing.T) {
	require.Empty(t, collectTelegraphImages(nil))
	require.Empty(t, collectTelegraphImages([]json.RawMessage{
		json.RawMessage(`{"tag":"p","children":["no images"]}`),
	}))
}
