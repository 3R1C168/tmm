package chat

import (
	"context"
	"sort"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/util/tutil"
)

// DialogInfo is a chat/dialog summary suitable for a service API (it adds
// unread count and pinned flag to the CLI's Dialog).
type DialogInfo struct {
	ID          int64  `json:"id"`
	Type        string `json:"type"` // private | group | channel
	VisibleName string `json:"visibleName"`
	Username    string `json:"username,omitempty"`
	UnreadCount int    `json:"unreadCount"`
	Pinned      bool   `json:"pinned,omitempty"`
	// ReadInboxMaxID 是该会话已读到的最大消息 ID（未读起点 = +1）。
	// 来自 gotd Dialog.ReadInboxMaxID，随对话列表免费附带。
	ReadInboxMaxID int `json:"readInboxMaxId,omitempty"`
}

// FetchDialogs lists the account's dialogs for the service layer. It reuses
// the same resilient pagination as `chat ls` (skipping inaccessible dialogs),
// warms the peer cache, and returns display info. Blocked peers are excluded.
//
// Unlike List, it takes no filter and returns data instead of printing.
func FetchDialogs(ctx context.Context, c *telegram.Client, kvd storage.Storage) ([]*DialogInfo, error) {
	log := logctx.From(ctx)

	dialogs, skipped := fetchDialogsWithErrorHandling(ctx, c.API())
	if skipped > 0 {
		log.Warn("skipped problematic dialogs", zap.Int("skipped", skipped))
	}

	blocked, err := tutil.GetBlockedDialogs(ctx, c.API())
	if err != nil {
		return nil, err
	}

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	result := make([]*DialogInfo, 0, len(dialogs))
	for _, d := range dialogs {
		id := tutil.GetInputPeerID(d.Peer)

		if err = applyPeers(ctx, manager, d.Entities, id); err != nil {
			log.Warn("failed to apply peer updates", zap.Int64("id", id), zap.Error(err))
		}

		if _, ok := blocked[id]; ok {
			continue
		}

		var r *Dialog
		switch t := d.Peer.(type) {
		case *tg.InputPeerUser:
			r = processUser(t.UserID, d.Entities)
		case *tg.InputPeerChannel:
			r = processChannel(ctx, c.API(), t.ChannelID, d.Entities)
		case *tg.InputPeerChat:
			r = processChat(t.ChatID, d.Entities)
		}
		if r == nil {
			continue
		}

		info := &DialogInfo{
			ID:          r.ID,
			Type:        r.Type,
			VisibleName: r.VisibleName,
			Username:    r.Username,
		}
		if dlg, ok := d.Dialog.(*tg.Dialog); ok {
			info.UnreadCount = dlg.UnreadCount
			info.Pinned = dlg.Pinned
			info.ReadInboxMaxID = dlg.GetReadInboxMaxID()
			if info.UnreadCount > 0 && info.ReadInboxMaxID == 0 {
				log.Warn("dialog unread but readInboxMaxID=0", zap.Int64("peer", r.ID), zap.Int("unread", info.UnreadCount))
			}
		}
		result = append(result, info)
	}

	// 排序：置顶对话在前，其余保留 Telegram 返回顺序（本就是最后活动倒序）。
	// 用 stable sort 保证非置顶之间不重排。
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Pinned && !result[j].Pinned
	})

	return result, nil
}

// FetchMarkRead 标记会话 ≤maxID 的消息已读。
// 频道（channel/supergroup）用 channels.readHistory（messages.readHistory 对频道返回
// PEER_ID_INVALID）；用户/群用 messages.readHistory。
func FetchMarkRead(ctx context.Context, c *telegram.Client, kvd storage.Storage, chatID string, maxID int) error {
	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return errors.Wrap(err, "resolve chat")
	}
	if ch, ok := peer.(peers.Channel); ok {
		// 频道：channels.readHistory（Channel InputChannelClass + MaxID）
		if _, err := c.API().ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{
			Channel: ch.InputChannel(),
			MaxID:   maxID,
		}); err != nil {
			return errors.Wrap(err, "mark read")
		}
		return nil
	}
	if _, err := c.API().MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{
		Peer:  peer.InputPeer(),
		MaxID: maxID,
	}); err != nil {
		return errors.Wrap(err, "mark read")
	}
	return nil
}
