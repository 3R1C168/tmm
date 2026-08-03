package chat

import (
	"context"
	"strings"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/util/tutil"
)

// ValidSearchQuery 判定搜索查询是否有效：去首尾空白 + 去掉所有 # 后
// 剩余仍有至少 1 个非空白字符。纯 #/##/空白 服务器返回 SEARCH_QUERY_EMPTY。
func ValidSearchQuery(q string) bool {
	s := strings.TrimSpace(strings.ReplaceAll(q, "#", ""))
	return s != ""
}

// SearchResult 是一次搜索的完整结果。
type SearchResult struct {
	Results    []*MessageInfo `json:"results"`
	NextOffset int            `json:"nextOffset"`
	Count      int            `json:"count"`
}

// SearchMessages 在单个频道内搜索消息文本（含 # 标签）。
// 服务器端索引（messages.search），频道多大都是亚秒级。
func SearchMessages(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID, q string, offsetID, limit int) (*SearchResult, error) {

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return nil, errors.Wrap(err, "resolve chat")
	}

	if limit <= 0 || limit > 100 {
		limit = 50 // 默认/上限
	}

	res, err := c.API().MessagesSearch(ctx, &tg.MessagesSearchRequest{
		Peer:     peer.InputPeer(),
		Q:        q,
		OffsetID: offsetID,
		Limit:    limit,
		Filter:   &tg.InputMessagesFilterEmpty{},
	})
	if err != nil {
		return nil, errors.Wrap(err, "search messages")
	}

	ch, ok := res.(*tg.MessagesChannelMessages)
	if !ok {
		return nil, errors.Errorf("unexpected search result type %T", res)
	}

	// 预热 peer 缓存：search 返回 Users/Chats 两个 slice，直接用 gotd peers.Manager.Apply
	// （与 applyEntities 内部同款，messages.go:248）。
	if len(ch.Users)+len(ch.Chats) > 0 {
		_ = manager.Apply(ctx, ch.Users, ch.Chats)
	}

	out := make([]*MessageInfo, 0, len(ch.Messages))
	nextOffset := 0
	for _, mc := range ch.Messages {
		m, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		nextOffset = m.ID // search 的 offset 用 msgid
		out = append(out, messageInfoFrom(m))
	}
	return &SearchResult{Results: out, NextOffset: nextOffset, Count: ch.Count}, nil
}

// FetchMessageAround 按 msgid 取该消息及其往前 limit 条历史，用于跳转定位。
func FetchMessageAround(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID string, msgID, limit int) ([]*MessageInfo, error) {

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return nil, errors.Wrap(err, "resolve chat")
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// messages.getHistory offset=msgID 往回取，含 msgID 本身
	q := query.NewQuery(c.API()).Messages().GetHistory(peer.InputPeer()).OffsetID(msgID)
	iter := messages.NewIterator(q, 100)

	out := make([]*MessageInfo, 0, limit)
	for iter.Next(ctx) && len(out) < limit {
		elem := iter.Value()
		applyEntities(ctx, manager, elem.Entities)
		if m, ok := elem.Msg.(*tg.Message); ok {
			out = append(out, messageInfoFrom(m))
		}
	}
	if err := iter.Err(); err != nil {
		return nil, errors.Wrap(err, "fetch around")
	}
	return out, nil
}
