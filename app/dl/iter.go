package dl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"text/template"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"go.uber.org/atomic"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/fsutil"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/filterMap"
	"github.com/iyear/tdl/pkg/tmessage"
	"github.com/iyear/tdl/pkg/tplfunc"
	"github.com/iyear/tdl/pkg/utils"
)

const tempExt = ".tmp"

type fileTemplate struct {
	DialogID     int64
	MessageID    int
	MessageDate  int64
	FileName     string
	FileCaption  string
	FileSize     string
	DownloadDate int64
}

// MsgKey identifies a downloaded message independent of iteration order, so
// resume progress survives changes to --desc, grouping and message deletion.
type MsgKey struct {
	PeerID int64
	MsgID  int
}

// String returns the order-independent "peerID/msgID" resume coordinate.
func (k MsgKey) String() string { return fmt.Sprintf("%d/%d", k.PeerID, k.MsgID) }

func keyOf(peer tg.InputPeerClass, msgID int) MsgKey {
	return MsgKey{PeerID: tutil.GetInputPeerID(peer), MsgID: msgID}
}

func keyFromMsg(peer tg.InputPeerClass, m *tg.Message) MsgKey {
	return keyOf(peer, m.ID)
}

// groupKey identifies an album (grouped messages) within a peer.
type groupKey struct {
	PeerID    int64
	GroupedID int64
}

func groupKeyOf(peer tg.InputPeerClass, groupedID int64) groupKey {
	return groupKey{PeerID: tutil.GetInputPeerID(peer), GroupedID: groupedID}
}

type iter struct {
	pool    dcpool.Pool
	manager *peers.Manager
	dialogs []*tmessage.Dialog
	tpl     *template.Template
	include map[string]struct{}
	exclude map[string]struct{}
	opts    Options
	delay   time.Duration

	prefetcher *prefetcher

	mu       *sync.Mutex
	finished map[MsgKey]struct{} // order-independent set of downloaded messages
	// processedGroups records grouped IDs whose whole album was already
	// expanded, so when the iterator later reaches another message of the same
	// album it is skipped instead of re-fetching the group.
	processedGroups map[groupKey]struct{}
	fingerprint     string
	// This param is kept for potential future use but is currently unused.
	// preSum       []int
	logicalPos   int // logical position for finished tracking
	dialogIndex  int // physical position: current dialog in dialogs array
	messageIndex int // physical position: current message in dialog.Messages array

	// TODO(Hexa): counter is de facto not be used in the codebase, but I perfer to reserve it. The key point is whether it still needs to be atomic or not.
	counter        *atomic.Int64
	skippedDeleted *atomic.Int64 // count of skipped deleted messages
	deletedIDs     []string      // IDs of deleted messages (format: "dialogID/messageID")
	elem           chan downloader.Elem
	err            error
}

func newIter(pool dcpool.Pool, manager *peers.Manager, dialog [][]*tmessage.Dialog,
	opts Options, delay time.Duration,
) (*iter, error) {
	tpl, err := template.New("dl").
		Funcs(tplfunc.FuncMap(tplfunc.All...)).
		Parse(opts.Template)
	if err != nil {
		return nil, errors.Wrap(err, "parse template")
	}

	dialogs := flatDialogs(dialog)
	// if msgs is empty, return error to avoid range out of index
	if len(dialogs) == 0 {
		return nil, errors.Errorf("you must specify at least one message")
	}

	// include and exclude
	includeMap := filterMap.New(opts.Include, fsutil.AddPrefixDot)
	excludeMap := filterMap.New(opts.Exclude, fsutil.AddPrefixDot)

	// to keep fingerprint stable
	sortDialogs(dialogs, opts.Desc)

	it := &iter{
		pool:    pool,
		manager: manager,
		dialogs: dialogs,
		opts:    opts,
		include: includeMap,
		exclude: excludeMap,
		tpl:     tpl,
		delay:   delay,

		prefetcher: newPrefetcher(pool, dialogs),

		mu:              &sync.Mutex{},
		finished:        make(map[MsgKey]struct{}),
		processedGroups: make(map[groupKey]struct{}),
		fingerprint:     fingerprint(dialogs),
		// This param is kept for potential future use but is currently unused.
		// preSum:       preSum(dialogs),
		logicalPos:     0,
		dialogIndex:    0,
		messageIndex:   0,
		counter:        atomic.NewInt64(-1),
		skippedDeleted: atomic.NewInt64(0),
		deletedIDs:     make([]string, 0),
		elem:           make(chan downloader.Elem, 10), // grouped message buffer
		err:            nil,
	}

	return it, nil
}

// startPrefetch launches the background message-resolution pipeline. It must be
// called once the caller's context is available (iter is built before Run's ctx
// is fully wired in some paths, so this is separate from newIter).
func (i *iter) startPrefetch(ctx context.Context) {
	i.prefetcher.prefetch(ctx)
}

func (i *iter) Next(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		i.err = ctx.Err()
		return false
	default:
	}

	// if delay is set, sleep for a while for each iteration
	if i.delay > 0 && (i.dialogIndex+i.messageIndex) > 0 { // skip first delay
		time.Sleep(i.delay)
	}

	if len(i.elem) > 0 { // there are messages(grouped) in channel that not processed
		return true
	}

	for {
		ok, skip := i.process(ctx)
		if skip {
			continue
		}

		return ok
	}
}

func (i *iter) process(ctx context.Context) (ret bool, skip bool) {
	i.mu.Lock()
	defer i.mu.Unlock()

	// end of iteration or error occurred
	if i.dialogIndex >= len(i.dialogs) || i.messageIndex >= len(i.dialogs[i.dialogIndex].Messages) || i.err != nil {
		return false, false
	}

	peer, msg := i.dialogs[i.dialogIndex].Peer, i.dialogs[i.dialogIndex].Messages[i.messageIndex]

	// Record current logical position before processing
	startLogicalPos := i.logicalPos

	// Defer physical position increment
	defer func() {
		if i.messageIndex++; i.dialogIndex < len(i.dialogs) && i.messageIndex >= len(i.dialogs[i.dialogIndex].Messages) {
			i.dialogIndex++
			i.messageIndex = 0
		}
	}()

	from, err := i.manager.FromInputPeer(ctx, peer)
	if err != nil {
		i.err = errors.Wrap(err, "resolve from input peer")
		return false, false
	}
	// message bodies are prefetched in the background in batches, so resolving
	// one here is normally a cache hit and does not block the download workers
	message, err := i.prefetcher.get(ctx, peer, msg)
	if err != nil {
		// Check if the error is due to a deleted message
		if errors.Is(err, tutil.ErrMessageDeleted) {
			logctx.From(ctx).Info("Message may be deleted, skipping",
				zap.Int64("dialog_id", tutil.GetInputPeerID(peer)),
				zap.Int("message_id", msg),
			)
			i.skippedDeleted.Inc()                                                                     // increment skipped deleted counter
			i.deletedIDs = append(i.deletedIDs, fmt.Sprintf("%d/%d", tutil.GetInputPeerID(peer), msg)) // track deleted message ID
			i.logicalPos++                                                                             // increment logical position for skipped message
			return false, true
		}
		i.err = errors.Wrap(err, "resolve message")
		return false, false
	}

	if gid, ok := message.GetGroupedID(); ok && i.opts.Group {
		// skip if this album was already expanded via another message in it
		if i.isGroupProcessed(groupKeyOf(peer, gid)) {
			i.logicalPos++
			return false, true
		}
		return i.processGrouped(ctx, message, from, startLogicalPos)
	}

	// check if finished (order-independent: survives --desc/grouping changes)
	if i.isFinished(keyFromMsg(peer, message)) {
		i.logicalPos++ // increment logical position even if skipped
		return false, true
	}

	ret, skip = i.processSingle(ctx, message, from, startLogicalPos)
	i.logicalPos++ // increment logical position after processing
	return ret, skip
}

// isFinished reports whether a message has already been downloaded. It checks
// the order-independent key set, and falls back to the legacy logical-position
// set when an old-format progress record was loaded.
func (i *iter) isFinished(k MsgKey) bool {
	if _, ok := i.finished[k]; ok {
		return true
	}
	return false
}

// isGroupProcessed reports whether an album was already expanded.
func (i *iter) isGroupProcessed(k groupKey) bool {
	_, ok := i.processedGroups[k]
	return ok
}

// markGroupProcessed records an album as expanded so it is not re-fetched.
func (i *iter) markGroupProcessed(k groupKey) {
	i.processedGroups[k] = struct{}{}
}

func (i *iter) processSingle(ctx context.Context, message *tg.Message, from peers.Peer, logicalPos int) (bool, bool) {
	item, ok := tmedia.GetMedia(message)
	if !ok {
		logctx.From(ctx).Warn("Message has no media",
			zap.Int64("dialog_id", from.ID()),
			zap.Int("message_id", message.ID),
		)

		return false, true
	}

	// process include and exclude
	ext := filepath.Ext(item.Name)
	if _, ok = i.include[ext]; len(i.include) > 0 && !ok {
		return false, true
	}
	if _, ok = i.exclude[ext]; len(i.exclude) > 0 && ok {
		return false, true
	}

	toName := bytes.Buffer{}
	err := i.tpl.Execute(&toName, &fileTemplate{
		DialogID:     from.ID(),
		MessageID:    message.ID,
		MessageDate:  int64(message.Date),
		FileName:     item.Name,
		FileCaption:  message.Message,
		FileSize:     utils.Byte.FormatBinaryBytes(item.Size),
		DownloadDate: time.Now().Unix(),
	})
	if err != nil {
		i.err = errors.Wrap(err, "execute template")
		return false, false
	}

	if i.opts.SkipSame {
		if stat, err := os.Stat(filepath.Join(i.opts.Dir, toName.String())); err == nil {
			if fsutil.GetNameWithoutExt(toName.String()) == fsutil.GetNameWithoutExt(stat.Name()) &&
				stat.Size() == item.Size {
				return false, true
			}
		}
	}

	filename := fmt.Sprintf("%s%s", toName.String(), tempExt)
	path := filepath.Join(i.opts.Dir, filename)

	// #113. If path contains dirs, create it. So now we support nested dirs.
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		i.err = errors.Wrap(err, "create dir")
		return false, false
	}

	// Open without truncating so we can resume a partial download. If a control
	// file exists and matches the media, only the missing pieces are fetched;
	// otherwise the file is truncated and downloaded from scratch.
	to, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		i.err = errors.Wrap(err, "create file")
		return false, false
	}

	resume, err := downloader.LoadState(path, item.Size, downloader.MaxPartSize)
	if err != nil {
		// state exists but is corrupt or stale: fall back to a fresh download
		logctx.From(ctx).Warn("Ignore invalid resume state, re-download",
			zap.String("path", path), zap.Error(err))
		resume = nil
	}

	if resume == nil || resume.Complete() {
		// fresh download, or a leftover complete state: truncate and start over
		if err = to.Truncate(0); err != nil {
			_ = to.Close()
			i.err = errors.Wrap(err, "truncate file")
			return false, false
		}
		if _, err = to.Seek(0, io.SeekStart); err != nil {
			_ = to.Close()
			i.err = errors.Wrap(err, "seek file")
			return false, false
		}
		resume = downloader.NewState(downloader.StatePath(path), item.Size, downloader.MaxPartSize)
	}

	i.elem <- &iterElem{
		id:         int(i.counter.Inc()),
		logicalPos: logicalPos,
		key:        keyFromMsg(from.InputPeer(), message),

		from:    from,
		fromMsg: message,
		file:    item,

		to:     to,
		resume: resume,

		opts: i.opts,
	}

	return true, false
}

func (i *iter) processGrouped(ctx context.Context, message *tg.Message, from peers.Peer, startLogicalPos int) (bool, bool) {
	grouped, err := tutil.GetGroupedMessages(ctx, i.pool.Default(ctx), from.InputPeer(), message)
	if err != nil {
		i.err = errors.Wrapf(err, "resolve grouped message %d/%d", from.ID(), message.ID)
		return false, false
	}

	logctx.From(ctx).Info("album expanded",
		zap.Int64("dialog_id", from.ID()),
		zap.Int("anchor_id", message.ID),
		zap.Int("found", len(grouped)))

	// mark the album as expanded so the iterator skips its other messages
	if gid, ok := message.GetGroupedID(); ok {
		i.markGroupProcessed(groupKeyOf(from.InputPeer(), gid))
	}

	hasValid := false

	for idx, msg := range grouped {
		logicalPos := startLogicalPos + idx

		// check if this grouped message is already finished (order-independent)
		if i.isFinished(keyFromMsg(from.InputPeer(), msg)) {
			continue
		}

		ret, skip := i.processSingle(ctx, msg, from, logicalPos)

		// if processSingle encounters a fatal error (not just skip), propagate it
		if !ret && !skip {
			// i.err should already be set by processSingle
			return false, false
		}

		if ret {
			hasValid = true
		}
	}

	// increment logical position by the number of messages in the group
	i.logicalPos += len(grouped)

	return hasValid, !hasValid
}

func (i *iter) Value() downloader.Elem {
	return <-i.elem
}

func (i *iter) Err() error {
	return i.err
}

func (i *iter) SetFinished(finished map[MsgKey]struct{}) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.finished = finished
}

func (i *iter) Finished() map[MsgKey]struct{} {
	i.mu.Lock()
	defer i.mu.Unlock()

	// return a copy so callers can iterate without racing concurrent Finish
	out := make(map[MsgKey]struct{}, len(i.finished))
	for k := range i.finished {
		out[k] = struct{}{}
	}
	return out
}

func (i *iter) Fingerprint() string {
	return i.fingerprint
}

func (i *iter) Finish(k MsgKey) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.finished[k] = struct{}{}
}

func (i *iter) Total() int {
	i.mu.Lock()
	defer i.mu.Unlock()

	total := 0
	for _, m := range i.dialogs {
		total += len(m.Messages)
	}
	return total
}

func (i *iter) SkippedDeleted() int64 {
	return i.skippedDeleted.Load()
}

func (i *iter) DeletedIDs() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.deletedIDs
}

// positionToLogicalIndex converts physical position (dialogIndex, messageIndex) to logical index
// This method is kept for potential future use but is currently unused.
// func (i *iter) positionToLogicalIndex(dialogIdx, messageIdx int) int {
// 	return i.preSum[dialogIdx] + messageIdx
// }

func flatDialogs(dialogs [][]*tmessage.Dialog) []*tmessage.Dialog {
	res := make([]*tmessage.Dialog, 0)
	for _, d := range dialogs {
		if len(d) == 0 {
			continue
		}
		res = append(res, d...)
	}
	return res
}

func sortDialogs(dialogs []*tmessage.Dialog, desc bool) {
	// stable sort so equal peers keep a deterministic relative order
	sort.SliceStable(dialogs, func(i, j int) bool {
		return tutil.GetInputPeerID(dialogs[i].Peer) <
			tutil.GetInputPeerID(dialogs[j].Peer) // increasing order
	})

	for _, m := range dialogs {
		sort.SliceStable(m.Messages, func(i, j int) bool {
			if desc {
				return m.Messages[i] > m.Messages[j]
			}
			return m.Messages[i] < m.Messages[j]
		})
	}
}

// preSum of dialogs
// This method is kept for potential future use but is currently unused.
// func preSum(dialogs []*tmessage.Dialog) []int {
// 	sum := make([]int, len(dialogs)+1)
// 	for i, m := range dialogs {
// 		sum[i+1] = sum[i] + len(m.Messages)
// 	}
// 	return sum
// }

// fingerprint identifies the set of (peer, message) pairs to download. It is
// normalized (peers sorted ascending, message IDs within a peer sorted
// ascending and de-duplicated) so it does not depend on --desc or input order,
// keeping the resume key stable across runs with the same content.
func fingerprint(dialogs []*tmessage.Dialog) string {
	type peerMsgs struct {
		peerID int64
		msgs   []int
	}

	byPeer := make(map[int64]map[int]struct{})
	for _, m := range dialogs {
		pid := tutil.GetInputPeerID(m.Peer)
		if byPeer[pid] == nil {
			byPeer[pid] = make(map[int]struct{})
		}
		for _, msg := range m.Messages {
			byPeer[pid][msg] = struct{}{}
		}
	}

	peersList := make([]peerMsgs, 0, len(byPeer))
	for pid, set := range byPeer {
		msgs := make([]int, 0, len(set))
		for id := range set {
			msgs = append(msgs, id)
		}
		sort.Ints(msgs)
		peersList = append(peersList, peerMsgs{peerID: pid, msgs: msgs})
	}
	sort.Slice(peersList, func(i, j int) bool { return peersList[i].peerID < peersList[j].peerID })

	endian := binary.BigEndian
	buf, b := &bytes.Buffer{}, make([]byte, 8)
	for _, pm := range peersList {
		endian.PutUint64(b, uint64(pm.peerID))
		buf.Write(b)
		for _, msg := range pm.msgs {
			endian.PutUint64(b, uint64(msg))
			buf.Write(b)
		}
	}

	return fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
}
