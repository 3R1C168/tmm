package chat

import (
	"context"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/tutil"
)

// MessageMedia describes a message's downloadable attachment.
type MessageMedia struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	DC     int    `json:"dc"`
	Width  int    `json:"width,omitempty"`  // video/photo width (best-effort)
	Height int    `json:"height,omitempty"` // video/photo height
	Mime   string `json:"mime,omitempty"`   // document mime type, e.g. audio/mpeg ("" for photos)

	// Thumb carries the small preview location so the server can pre-download
	// thumbnails without a second RPC to re-fetch the message. Not serialized
	// to the browser (no JSON tag → omitted).
	Thumb *ThumbInfo `json:"-"`

	// Stripped is the inline low-res blurred preview (data URI), shown
	// instantly while the real thumbnail downloads — telegram-tt's trick.
	// Serialized so the browser can render it with zero network.
	Stripped *StrippedThumb `json:"stripped,omitempty"`
}

// StrippedThumb is the inline blurred preview embedded in the message.
type StrippedThumb struct {
	DataURI string `json:"dataUri"`
	W       int    `json:"w,omitempty"`
	H       int    `json:"h,omitempty"`
}

// ThumbInfo is the downloadable location of a message's small thumbnail.
type ThumbInfo struct {
	Loc  tg.InputFileLocationClass
	Size int64
	DC   int
}

// MessageInfo is one chat message for the service message-browser.
type MessageInfo struct {
	ID        int           `json:"id"`
	Date      int           `json:"date"`
	Text      string        `json:"text"`
	Views     int           `json:"views"`
	Forwards  int           `json:"forwards"`
	GroupedID int64         `json:"groupedId,omitempty"`
	Out       bool          `json:"out"` // sent by the logged-in account
	HasMedia  bool          `json:"hasMedia"`
	Media     *MessageMedia `json:"media,omitempty"`
}

// FetchMessages pages a chat's history (newest first). offsetID is the cursor:
// pass 0 for the latest page, otherwise the smallest message id from the
// previous page to fetch older messages. It warms the peer cache with each
// batch's entities so later media resolution works.
//
// If mediaOnly is true, only messages that carry a downloadable file/photo are
// returned. It returns up to limit messages and the next cursor (0 = no more).
// limit<=0 means "fetch all".
func FetchMessages(ctx context.Context, c *telegram.Client, kvd storage.Storage,
	chatID string, offsetID, limit int, mediaOnly bool) ([]*MessageInfo, int, error) {

	fetchAll := limit <= 0
	if !fetchAll && limit > 500 {
		limit = 500 // cap a single page
	}

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(c.API())
	peer, err := tutil.GetInputPeer(ctx, manager, chatID)
	if err != nil {
		return nil, 0, errors.Wrap(err, "resolve chat")
	}

	q := query.NewQuery(c.API()).Messages().GetHistory(peer.InputPeer())
	iter := messages.NewIterator(q, 100)
	if offsetID > 0 {
		iter = iter.OffsetID(offsetID)
	}

	out := make([]*MessageInfo, 0, 64)
	nextCursor := 0

	for iter.Next(ctx) {
		elem := iter.Value()
		// warm the peer cache so media/dl can resolve access hashes later
		applyEntities(ctx, manager, elem.Entities)

		m, ok := elem.Msg.(*tg.Message)
		if !ok {
			continue
		}
		nextCursor = m.ID // track the oldest id seen for the next page

		media, hasMedia := tmedia.GetMedia(m)
		if mediaOnly && !hasMedia {
			continue
		}

		info := &MessageInfo{
			ID:       m.ID,
			Date:     m.Date,
			Text:     m.Message,
			Views:    m.Views,
			Forwards: m.Forwards,
			Out:      m.Out,
			HasMedia: hasMedia,
		}
		if gid, ok := m.GetGroupedID(); ok {
			info.GroupedID = gid
		}
		if hasMedia {
			mm := &MessageMedia{Name: media.Name, Size: media.Size, DC: media.DC}
			mm.Width, mm.Height = mediaDimensions(m)
			mm.Mime = mediaMime(m)
			mm.Thumb = extractThumb(m)
			if st := tmedia.GetStrippedThumb(m); st != nil {
				mm.Stripped = &StrippedThumb{DataURI: st.DataURI, W: st.W, H: st.H}
			}
			info.Media = mm
		}
		out = append(out, info)

		if !fetchAll && len(out) >= limit {
			break
		}
	}

	if err := iter.Err(); err != nil {
		return nil, 0, err
	}

	// if we collected nothing and there is no error, we've reached the start
	if len(out) == 0 {
		nextCursor = 0
	}
	return out, nextCursor, nil
}

// mediaDimensions extracts the width/height of a message's media (video or
// photo) for aspect-ratio-aware rendering. Returns 0,0 when unknown.
func mediaDimensions(msg *tg.Message) (int, int) {
	md, ok := msg.GetMedia()
	if !ok {
		return 0, 0
	}
	switch mm := md.(type) {
	case *tg.MessageMediaDocument:
		if doc, ok := mm.Document.(*tg.Document); ok {
			for _, attr := range doc.Attributes {
				if v, ok := attr.(*tg.DocumentAttributeVideo); ok {
					return v.W, v.H
				}
				if v, ok := attr.(*tg.DocumentAttributeImageSize); ok {
					return v.W, v.H
				}
			}
		}
	case *tg.MessageMediaPhoto:
		if p, ok := mm.Photo.(*tg.Photo); ok {
			// largest size gives natural dimensions
			best, w, h := 0, 0, 0
			for _, sc := range p.Sizes {
				switch s := sc.(type) {
				case *tg.PhotoSize:
					if s.Size > best {
						best, w, h = s.Size, s.W, s.H
					}
				case *tg.PhotoSizeProgressive:
					if len(s.Sizes) > 0 && s.Sizes[len(s.Sizes)-1] > best {
						best, w, h = s.Sizes[len(s.Sizes)-1], s.W, s.H
					}
				}
			}
			return w, h
		}
	}
	return 0, 0
}

// mediaMime returns the document's mime type ("" for photos), so the browser
// can tell audio/video even when the file name lacks an extension (Telegram
// audio titles often have no .mp3 suffix).
func mediaMime(msg *tg.Message) string {
	md, ok := msg.GetMedia()
	if !ok {
		return ""
	}
	if mm, ok := md.(*tg.MessageMediaDocument); ok {
		if doc, ok := mm.Document.(*tg.Document); ok {
			return doc.MimeType
		}
	}
	return ""
}

// extractThumb pulls the small thumbnail location out of a message's media,
// so the server can pre-download it without re-fetching the message.
func extractThumb(msg *tg.Message) *ThumbInfo {
	md, ok := msg.GetMedia()
	if !ok {
		return nil
	}
	switch mm := md.(type) {
	case *tg.MessageMediaPhoto:
		mi, ok := tmedia.GetPhotoInfo(mm)
		if !ok {
			return nil
		}
		return &ThumbInfo{Loc: mi.InputFileLoc, Size: mi.Size, DC: mi.DC}
	case *tg.MessageMediaDocument:
		if doc, ok := mm.Document.(*tg.Document); ok {
			if th, ok := tmedia.GetDocumentThumb(doc); ok {
				return &ThumbInfo{Loc: th.InputFileLoc, Size: th.Size, DC: th.DC}
			}
		}
	}
	return nil
}

// applyEntities warms the peers manager cache from a batch's entities.
func applyEntities(ctx context.Context, manager *peers.Manager, ent peer.Entities) {
	users := make([]tg.UserClass, 0)
	for _, u := range ent.Users() {
		users = append(users, u)
	}
	chats := make([]tg.ChatClass, 0)
	for _, c := range ent.Chats() {
		chats = append(chats, c)
	}
	for _, c := range ent.Channels() {
		chats = append(chats, c)
	}
	if len(users)+len(chats) > 0 {
		_ = manager.Apply(ctx, users, chats)
	}
}
