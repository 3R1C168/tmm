package dl

import (
	"io"
	"os"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/tmedia"
)

type iterElem struct {
	id         int // tracker id for progress tracking
	logicalPos int // logical position for resume/finished tracking
	key        MsgKey

	from    peers.Peer
	fromMsg *tg.Message
	file    *tmedia.Media

	to *os.File

	resume *downloader.State // non-nil when resuming a partial download

	opts Options
}

func (i *iterElem) File() downloader.File { return i }

func (i *iterElem) To() io.WriterAt { return i.to }

func (i *iterElem) AsTakeout() bool { return i.opts.Takeout.Enabled(i.file.Size) }

func (i *iterElem) Resume() *downloader.State { return i.resume }

func (i *iterElem) Location() tg.InputFileLocationClass { return i.file.InputFileLoc }

func (i *iterElem) Name() string { return i.file.Name }

func (i *iterElem) Size() int64 { return i.file.Size }

func (i *iterElem) DC() int { return i.file.DC }

// Exported accessors let non-CLI callers (e.g. the service backend) inspect a
// downloaded element when they replace the terminal progress UI.

// ID returns the tracker id assigned to this element.
func (i *iterElem) ID() int { return i.id }

// Key returns the order-independent (peerID, msgID) resume coordinate.
func (i *iterElem) Key() MsgKey { return i.key }

// FromID returns the numeric id of the source peer (dialog).
func (i *iterElem) FromID() int64 { return i.from.ID() }

// FromName returns a human-readable name of the source peer.
func (i *iterElem) FromName() string { return i.from.VisibleName() }

// MsgID returns the id of the source message.
func (i *iterElem) MsgID() int { return i.fromMsg.ID }

// ToPath returns the on-disk path of the (temporary) download target.
func (i *iterElem) ToPath() string { return i.to.Name() }

// FileDate returns the message date (unix seconds), used for chtimes.
func (i *iterElem) FileDate() int64 { return i.file.Date }
