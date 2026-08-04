package chat

import (
	"context"
	"sort"
	"strings"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
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
//
// 无 # 并集搜索时有两个独立游标：NextOffset（全文 q 的游标）与 NextOffsetTag
// （标签 #q 的游标）。两个来源的结果集在 msgID 空间上互相穿插，单一游标无法
// 同时推进两源（全文游标传给标签查询可能排他排除标签的全部结果，反之亦然），
// 所以续拉必须各自带自己的游标，任一源拉完（游标为 0/负）则跳过该源。
type SearchResult struct {
	Results       []*MessageInfo `json:"results"`
	NextOffset    int            `json:"nextOffset"`
	NextOffsetTag int            `json:"nextOffsetTag"`
	Count         int            `json:"count"`
}

// SearchMessages 在单个频道内搜索消息文本（含 # 标签）。
// 服务器端索引（messages.search），频道多大都是亚秒级。
//
// 无 # 前缀时（如「热带鱼」）搜「全文 + 标签」并集：Telegram 全文搜索对中文词
// 匹配不到 #标签 里的内容（热带鱼 0 条 vs #热带鱼 24 条，实测），所以同时搜
// q（全文）和 #q（标签）合并去重，用户无论输入 # 与否都能找到标签消息。
// 有 # 前缀时只搜原样（标签精确匹配）。
//
// offsetID/offsetTag 是两源各自的续拉游标（传上次返回的 nextOffset/nextOffsetTag），
// 为 0 表示从最新开始，为负表示该源已拉完、跳过不再查询。
func SearchMessages(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID, q string, offsetID, offsetTag, limit int,
) (*SearchResult, error) {
	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return nil, errors.Wrap(err, "resolve chat")
	}
	if limit <= 0 || limit > 100 {
		limit = 50 // 默认/上限
	}

	hasHash := strings.Contains(q, "#")
	if !hasHash {
		// 无 #：搜 q（全文）+ #q（标签）并集，去重合并；两源游标独立推进
		full, ferr := searchOnce(ctx, c, manager, peer, q, offsetID, limit)
		if ferr != nil {
			return nil, ferr
		}
		tag, terr := searchOnce(ctx, c, manager, peer, "#"+q, offsetTag, limit)
		if terr != nil {
			return nil, terr
		}
		return mergeResults(full, tag), nil
	}

	// 有 #：只搜原样（标签精确匹配），单游标
	res, err := searchOnce(ctx, c, manager, peer, q, offsetID, limit)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// searchOnce 执行一次 messages.search 并转成 SearchResult。
// offsetID < 0 表示该源已拉完（调用方跳过），直接返回空结果，不发起 RPC。
func searchOnce(ctx context.Context, c *telegram.Client, manager *peers.Manager,
	peer peers.Peer, q string, offsetID, limit int,
) (*SearchResult, error) {
	if offsetID < 0 {
		return &SearchResult{}, nil
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

// mergeResults 合并两个搜索结果（无 # 并集），按 msgID 去重并按 msgID 倒序排列。
// 返回两源各自的续拉游标（NextOffset=全文、NextOffsetTag=标签）——两源结果集在
// msgID 空间互相穿插，续拉必须各自带自己的游标才能保证不丢结果（单一游标会把
// 另一源的结果排他排除）。Count 取两源 count 之和减重复（近似，仅展示用）。
func mergeResults(a, b *SearchResult) *SearchResult {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	seen := make(map[int]bool, len(a.Results)+len(b.Results))
	merged := make([]*MessageInfo, 0, len(a.Results)+len(b.Results))
	for _, m := range append(append([]*MessageInfo{}, a.Results...), b.Results...) {
		if m == nil || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		merged = append(merged, m)
	}
	// 合并后按 msgID 倒序（新→旧）：两源页面本身各自有序，但并集穿插后
	// 顺序错乱（可能先显示旧结果再显示新结果），排序后展示更符合直觉。
	sort.Slice(merged, func(i, j int) bool { return merged[i].ID > merged[j].ID })
	return &SearchResult{
		Results:       merged,
		NextOffset:    a.NextOffset, // 全文源游标
		NextOffsetTag: b.NextOffset, // 标签源游标
		Count:         a.Count + b.Count - (len(a.Results) + len(b.Results) - len(merged)),
	}
}

// FetchMessageAround 按 msgid 取该消息及其往前 limit 条历史，用于跳转定位。
//
// 实测（2026-08-03，频道 2070986972）：messages.getHistory 的 offset_id 是排他游标
// （返回比 offset_id 旧的），要含目标 msgID 需传 msgID+1。
// 不用 query.NewQuery + messages.NewIterator——迭代器 batchSize=100 会覆盖 limit，
// 且内部维护 offsetPeer/offsetDate 副作用；跳转定位只需一次性原始 RPC。
func FetchMessageAround(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID string, msgID, limit int,
) ([]*MessageInfo, error) {
	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return nil, errors.Wrap(err, "resolve chat")
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	res, err := c.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:     peer.InputPeer(),
		OffsetID: msgID + 1, // 排他游标：offset=X 返回比 X 旧的，+1 含目标 msgID
		Limit:    limit,
	})
	if err != nil {
		return nil, errors.Wrap(err, "fetch history around")
	}

	ch, ok := res.(*tg.MessagesChannelMessages)
	if !ok {
		return nil, errors.Errorf("unexpected history result type %T", res)
	}

	out := make([]*MessageInfo, 0, len(ch.Messages))
	for _, mc := range ch.Messages {
		m, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		out = append(out, messageInfoFrom(m))
	}
	return out, nil
}
