package chat

import (
	"context"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/tutil"
)

// CommentInfo is one reply in a channel post's comment section. Author info
// comes from the getReplies response's own Users/Chats entities — no extra RPC.
type CommentInfo struct {
	ID        int               `json:"id"`
	Date      int               `json:"date"`
	Text      string            `json:"text"`
	AuthorID  int64             `json:"authorId"`
	Name      string            `json:"name"`               // user visibleName or chat/channel title
	Username  string            `json:"username,omitempty"` // empty for groups/channels without username
	HasMedia  bool              `json:"hasMedia"`
	Media     *MessageMedia     `json:"media,omitempty"`
	Reactions []MessageReaction `json:"reactions,omitempty"`
}

// FetchReplies pages a channel post's comment section (newest first).
// chatID is the post's original channel; msgID is the post. offsetID is the
// cursor: pass 0 for the latest page, otherwise the smallest comment id from
// the previous page (exclusive — the server maps the channel post to its
// discussion group automatically, same as tdl's export --thread path).
// limit<=0 or >100 falls back to 50. Returns up to limit comments and the
// next cursor (0 = no more).
func FetchReplies(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID string, msgID, offsetID, limit int) ([]*CommentInfo, int, error) {

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return nil, 0, errors.Wrap(err, "resolve chat")
	}

	res, err := c.API().MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{
		Peer:       peer.InputPeer(),
		MsgID:      msgID,
		OffsetID:   offsetID,
		OffsetDate: 0,
		AddOffset:  0,
		Limit:      limit,
	})
	if err != nil {
		return nil, 0, errors.Wrap(err, "get replies")
	}

	ch, ok := res.(*tg.MessagesChannelMessages)
	if !ok {
		return nil, 0, errors.New("unexpected replies response")
	}

	// 作者实体跟随响应自带，零额外 RPC
	users := make(map[int64]*tg.User, len(ch.Users))
	for _, u := range ch.Users {
		if user, ok := u.(*tg.User); ok {
			users[user.ID] = user
		}
	}
	chats := make(map[int64]tg.ChatClass, len(ch.Chats))
	for _, cc := range ch.Chats {
		chats[cc.GetID()] = cc
	}

	out := make([]*CommentInfo, 0, len(ch.Messages))
	next := 0
	for i, mc := range ch.Messages {
		m, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		out = append(out, commentInfoFrom(m, users, chats))
		if i == len(ch.Messages)-1 {
			next = m.ID // 新→旧，最后一条最旧，作下一页排他游标
		}
	}
	if len(out) < limit {
		next = 0 // 不足一页 = 到底
	}
	return out, next, nil
}

// commentInfoFrom converts a comment message into CommentInfo, resolving the
// author from the response's entity maps. users/chats may be nil/empty.
func commentInfoFrom(m *tg.Message, users map[int64]*tg.User, chats map[int64]tg.ChatClass) *CommentInfo {
	info := &CommentInfo{
		ID:   m.ID,
		Date: m.Date,
		Text: m.Message,
	}
	if from, ok := m.GetFromID(); ok {
		info.AuthorID = tutil.GetPeerID(from)
		info.Name, info.Username = authorFrom(from, users, chats)
	}
	if media, has := tmedia.GetMedia(m); has {
		info.HasMedia = true
		info.Media = messageMediaFrom(m, media)
	}
	if rc := reactionsFrom(m); len(rc) > 0 {
		info.Reactions = rc
	}
	return info
}

// authorFrom resolves a comment author's display name + username from the
// response entities. Users get visibleName(first,last); chats/channels get
// their title (+username). Unknown authors fall back to empty strings.
func authorFrom(from tg.PeerClass, users map[int64]*tg.User, chats map[int64]tg.ChatClass) (name, username string) {
	id := tutil.GetPeerID(from)
	switch from.(type) {
	case *tg.PeerUser:
		if u, ok := users[id]; ok {
			return visibleName(u.FirstName, u.LastName), u.Username
		}
	case *tg.PeerChat, *tg.PeerChannel:
		if cc, ok := chats[id]; ok {
			switch c := cc.(type) {
			case *tg.Chat:
				return c.Title, ""
			case *tg.Channel:
				return c.Title, c.Username
			}
		}
	}
	return "", ""
}
