package chat

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

// commentInfoFrom 是纯转换（从 tg.Message + 实体 map 转 CommentInfo），可单测。
// FetchReplies 的真实 RPC 拉取由端到端验证。

func buildCommentMessage(text string, from tg.PeerClass) *tg.Message {
	m := &tg.Message{Message: text, ID: 100}
	if from != nil {
		m.SetFromID(from)
	}
	return m
}

func TestCommentInfoFromUserAuthor(t *testing.T) {
	m := buildCommentMessage("nice", &tg.PeerUser{UserID: 5401443218})
	users := map[int64]*tg.User{
		5401443218: {ID: 5401443218, FirstName: "Eric", LastName: "Chan", Username: "ericchan"},
	}
	c := commentInfoFrom(m, users, nil)
	require.Equal(t, 100, c.ID)
	require.Equal(t, "nice", c.Text)
	require.Equal(t, int64(5401443218), c.AuthorID)
	require.Equal(t, "Eric Chan", c.Name) // visibleName(first, last)
	require.Equal(t, "ericchan", c.Username)
}

func TestCommentInfoFromChannelAuthor(t *testing.T) {
	m := buildCommentMessage("from channel", &tg.PeerChannel{ChannelID: 1424384184})
	chats := map[int64]tg.ChatClass{
		1424384184: &tg.Channel{ID: 1424384184, Title: "Exhentai 五星图鉴", Username: "exhentai5"},
	}
	c := commentInfoFrom(m, nil, chats)
	require.Equal(t, "Exhentai 五星图鉴", c.Name)
	require.Equal(t, "exhentai5", c.Username)
}

func TestCommentInfoFromNoAuthor(t *testing.T) {
	m := buildCommentMessage("anonymous", nil)
	c := commentInfoFrom(m, nil, nil)
	require.Empty(t, c.Name)
	require.Empty(t, c.Username)
}

func TestCommentInfoReactions(t *testing.T) {
	m := buildCommentMessage("with reactions", &tg.PeerUser{UserID: 1})
	m.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{
			{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 2},
		},
	})
	c := commentInfoFrom(m, nil, nil)
	require.Len(t, c.Reactions, 1)
	require.Equal(t, "👍", c.Reactions[0].Emoji)
	require.Equal(t, 2, c.Reactions[0].Count)
}
