package chat

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

// LinkPreview 提取是纯逻辑（从 tg.Message 转 MessageInfo），可单测。
// 真实 RPC 拉取（resolveMedia 等）由端到端验证。

// buildWebPageMessage builds a tg.Message carrying a MessageMediaWebPage
// preview, the shape Telegram attaches to messages with links (e.g. Telegraph).
// Media must go through SetMedia so the flag bit (9) is set — GetMedia checks
// the flag, not the field, on tg.Message.
func buildWebPageMessage(url string) *tg.Message {
	m := &tg.Message{
		Message: "https://telegra.ph/Some-Article-01-01",
	}
	m.SetMedia(&tg.MessageMediaWebPage{
		Webpage: &tg.WebPage{
			Title:       "Some Article",
			Description: "A teaser",
			SiteName:    "telegra.ph",
			URL:         url,
		},
	})
	return m
}

func TestLinkPreviewFromWebPage(t *testing.T) {
	m := buildWebPageMessage("https://telegra.ph/Some-Article-01-01")
	info := messageInfoFrom(m)
	require.NotNil(t, info.LinkPreview, "MessageMediaWebPage 应提取出 LinkPreview")
	require.Equal(t, "Some Article", info.LinkPreview.Title)
	require.Equal(t, "A teaser", info.LinkPreview.Desc)
	require.Equal(t, "telegra.ph", info.LinkPreview.Site)
	require.Equal(t, "https://telegra.ph/Some-Article-01-01", info.LinkPreview.URL)
	require.False(t, info.LinkPreview.HasPhoto)
	require.False(t, info.HasMedia, "网页预览不是可下载媒体，HasMedia 应保持 false")
	require.Nil(t, info.Media)
}

func TestLinkPreviewWithPhoto(t *testing.T) {
	m := buildWebPageMessage("https://telegra.ph/Some-Article-01-01")
	wp := m.Media.(*tg.MessageMediaWebPage)
	wp.Webpage.(*tg.WebPage).Photo = &tg.Photo{ID: 123, AccessHash: 456, DCID: 2}
	info := messageInfoFrom(m)
	require.NotNil(t, info.LinkPreview)
	require.True(t, info.LinkPreview.HasPhoto)
}

func TestLinkPreviewAbsent(t *testing.T) {
	// 无媒体、文档媒体、纯文本都不应有 LinkPreview
	cases := []*tg.Message{
		{Message: "plain text"},
		{Message: "no preview"},
	}
	cases[1].SetMedia(&tg.MessageMediaDocument{Document: &tg.Document{ID: 1}})
	for _, m := range cases {
		info := messageInfoFrom(m)
		require.Nil(t, info.LinkPreview)
	}
}

func TestLinkPreviewNonTelegraphWebPage(t *testing.T) {
	// 任意网页预览都生成卡片（通用 LinkPreview），Telegraph 限制只在
	// 阅读/下载接口处判定，不在消息提取处。
	m := &tg.Message{Message: "link"}
	m.SetMedia(&tg.MessageMediaWebPage{Webpage: &tg.WebPage{URL: "https://example.com/x", Title: "X"}})
	info := messageInfoFrom(m)
	require.NotNil(t, info.LinkPreview)
	require.Equal(t, "X", info.LinkPreview.Title)
	require.Equal(t, "https://example.com/x", info.LinkPreview.URL)
	require.False(t, info.HasMedia)
}

func TestLinkPreviewNonWebPageMedia(t *testing.T) {
	// MessageMediaWebPage 之外的其他媒体类型，不应误提取
	m := &tg.Message{}
	photo := &tg.Photo{ID: 1, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", Size: 100}}}
	m.SetMedia(&tg.MessageMediaPhoto{Photo: photo})
	info := messageInfoFrom(m)
	require.Nil(t, info.LinkPreview)
	require.True(t, info.HasMedia)
}
