package chat

import (
	"context"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/util/tutil"
)

// DialogInfo is a chat/dialog summary suitable for a service API (it adds
// unread count to the CLI's Dialog).
type DialogInfo struct {
	ID          int64  `json:"id"`
	Type        string `json:"type"` // private | group | channel
	VisibleName string `json:"visibleName"`
	Username    string `json:"username,omitempty"`
	UnreadCount int    `json:"unreadCount"`
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
		}
		result = append(result, info)
	}

	return result, nil
}
