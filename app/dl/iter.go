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
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/flytam/filenamify"
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

	// peers maps peerID -> InputPeer for the requeue index (independent of the
	// main dialogIndex/messageIndex state machine).
	peers map[int64]tg.InputPeerClass

	mu       *sync.Mutex
	finished map[MsgKey]struct{} // order-independent set of downloaded messages
	// paused holds keys the user manually paused; processSingle skips them.
	// It is only read while holding i.mu (see isPausedLocked).
	paused map[MsgKey]struct{}
	// requeue is the resume queue: Next re-produces these keys first, so a
	// running task resumes a file immediately after Unpause.
	requeue []MsgKey
	// produced tracks keys already emitted via the requeue path (produceFromKey).
	// When the main loop later reaches such a message it must NOT produce it
	// again — otherwise two goroutines would download the same .tmp/.tdlstate
	// concurrently. Unpause removes the key (so a later pause+unpause may
	// re-produce); ClearPaused empties it.
	produced map[MsgKey]struct{}
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

		peers: buildPeerIndex(dialogs),

		mu:              &sync.Mutex{},
		finished:        make(map[MsgKey]struct{}),
		paused:          make(map[MsgKey]struct{}),
		requeue:         make([]MsgKey, 0),
		produced:        make(map[MsgKey]struct{}),
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

	// 恢复队列优先：用户恢复了暂停的文件，立即重新产出。
	// nextRequeue 拿锁弹队列后释放锁，produceFromKey 内部再自己拿锁调
	// processSingle —— Next 本身不重入 i.mu，避免死锁。
	if key, ok := i.nextRequeue(); ok {
		if err := i.produceFromKey(ctx, key); err != nil {
			i.err = err
			return false
		}
		// produceFromKey 可能因文件被再次跳过（重新暂停/无媒体/filter 命中/
		// SkipSame）而未产出 elem：此时不能 return true（Value() 会阻塞在空
		// channel 上死锁），落到主循环继续即可。requeue 条目已被弹出。
		if len(i.elem) > 0 {
			return true
		}
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

	// 防止双下载：requeue 路径已产出该消息（用户 early unpause 抢先下载，
	// 下载还在进行中），主循环到达时跳过，避免两个 goroutine 并发写同一
	// .tmp/.tdlstate。
	if _, ok := i.produced[keyFromMsg(peer, message)]; ok {
		i.logicalPos++
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

// channelSubdir builds the per-channel subdirectory name: "#<id> <cleaned name>".
// Special chars in the channel name are sanitized via filenamify; empty name
// (or empty after sanitize) falls back to "#<id>".
func channelSubdir(id int64, name string) string {
	s := name
	if s != "" {
		if cleaned, err := filenamify.FilenamifyV2(s, func(o *filenamify.Options) {}); err == nil && cleaned != "" {
			s = cleaned
		}
	}
	if s == "" {
		return fmt.Sprintf("#%d", id)
	}
	return fmt.Sprintf("#%d %s", id, s)
}

func (i *iter) processSingle(ctx context.Context, message *tg.Message, from peers.Peer, logicalPos int) (bool, bool) {
	// 文件级暂停：用户在 UI 里暂停了该文件，跳过不下载（恢复后 requeue 重新产出）。
	// isPausedLocked：processSingle 总在持锁路径被调用（process 或 produceFromKey 持锁），
	// 直接读 i.paused，不加锁（避免 process 持锁下重入死锁）。
	if i.isPausedLocked(keyFromMsg(from.InputPeer(), message)) {
		logctx.From(ctx).Info("message skipped: user paused",
			zap.Int64("dialog_id", from.ID()),
			zap.Int("message_id", message.ID))
		return false, true
	}

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
		logctx.From(ctx).Info("message skipped: not in include",
			zap.Int("msg_id", message.ID), zap.String("ext", ext))
		return false, true
	}
	if _, ok = i.exclude[ext]; len(i.exclude) > 0 && ok {
		logctx.From(ctx).Info("message skipped: in exclude",
			zap.Int("msg_id", message.ID), zap.String("ext", ext))
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

	// 目标目录：channelDir 开时按频道（#<id> <name>）存子目录。
	// 目录拼接必须先于 SkipSame 检查——否则 SkipSame 会在旧的顶层目录 stat，
	// channelDir 开时永远找不到已下载文件，导致每次都重下。
	dir := i.opts.Dir
	if i.opts.ChannelDir {
		dir = filepath.Join(dir, channelSubdir(from.ID(), from.VisibleName()))
	}

	if i.opts.SkipSame {
		if stat, err := os.Stat(filepath.Join(dir, toName.String())); err == nil {
			if fsutil.GetNameWithoutExt(toName.String()) == fsutil.GetNameWithoutExt(stat.Name()) &&
				stat.Size() == item.Size {
				logctx.From(ctx).Info("message skipped: SkipSame (disk has same name+size)",
					zap.Int("msg_id", message.ID), zap.String("name", toName.String()), zap.Int64("size", item.Size))
				return false, true
			}
		}
	}

	// 截断文件名（不含扩展名）到 FilenameMaxLen 字节，防止超过文件系统名称限制。
	// 保留扩展名，只截断主体部分。
	rendered := toName.String()
	if i.opts.FilenameMaxLen > 0 {
		ext := filepath.Ext(rendered)
		base := strings.TrimSuffix(rendered, ext)
		if len(base) > i.opts.FilenameMaxLen {
			base = base[:i.opts.FilenameMaxLen]
			rendered = base + ext
			logctx.From(ctx).Info("filename truncated",
				zap.Int("msg_id", message.ID),
				zap.Int("maxLen", i.opts.FilenameMaxLen),
				zap.String("result", rendered))
		}
	}

	filename := fmt.Sprintf("%s%s", rendered, tempExt)
	path := filepath.Join(dir, filename)

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

	// 校验磁盘实际大小：state.Complete() 可能误报（bitmap 全满但实际缺块），
	// 若磁盘 < 声明 size，强制把尾部 piece 标记为未完成，走 resume 补齐，不 truncate。
	diskComplete := false
	if resume != nil && resume.Complete() {
		if fi, serr := os.Stat(path); serr == nil && fi.Size() == item.Size {
			diskComplete = true
		}
	}

	if resume == nil || (resume.Complete() && diskComplete) {
		// 真正的全新下载，或磁盘确实完整（下完的残留 state）：truncate 重来
		logctx.From(ctx).Info("fresh download (truncating)",
			zap.String("path", path),
			zap.Bool("resumeNil", resume == nil),
			zap.Bool("diskComplete", diskComplete))
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
	} else if resume != nil && resume.Complete() && !diskComplete {
		// state 误报完成但磁盘不全：标记缺块，走 resume 补齐
		if fi, _ := os.Stat(path); fi != nil {
			resume.MarkMissingFrom(fi.Size())
		}
		missing := resume.Missing()
		logctx.From(ctx).Info("resume after false-complete",
			zap.String("path", path),
			zap.Int("missingPieces", len(missing)))
	} else {
		missing := resume.Missing()
		logctx.From(ctx).Info("resume partial download",
			zap.String("path", path),
			zap.Int("missingPieces", len(missing)))
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
			logctx.From(ctx).Info("grouped message skipped: already finished",
				zap.Int("msg_id", msg.ID))
			continue
		}

		ret, skip := i.processSingle(ctx, msg, from, logicalPos)
		if !ret && skip {
			logctx.From(ctx).Info("grouped message skipped by processSingle",
				zap.Int("msg_id", msg.ID))
		}

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

// PauseKeys marks messages as user-paused so they are skipped.
func (i *iter) PauseKeys(keys []MsgKey) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, k := range keys {
		i.paused[k] = struct{}{}
	}
}

// IsPaused reports whether a key is paused.
func (i *iter) IsPaused(k MsgKey) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	_, ok := i.paused[k]
	return ok
}

// Unpause removes a key from paused and re-queues it for immediate download.
//
// 无需唤醒下载循环：Next 是同步返回的（requeue 分支在 Next 内同步 produceFromKey
// 并写入 i.elem），驱动循环 for it.Next(wgctx){ it.Value() } 取到 elem 即续传。
func (i *iter) Unpause(key MsgKey) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.paused, key)
	// 允许该 key 后续被 requeue 路径再次产出（下一次暂停再恢复时重新入队下载）
	delete(i.produced, key)
	// 去重：已在 requeue 里则不重复加
	for _, k := range i.requeue {
		if k == key {
			return
		}
	}
	i.requeue = append(i.requeue, key)
}

// ClearPaused empties the paused set and requeue (task restart).
func (i *iter) ClearPaused() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.paused = make(map[MsgKey]struct{})
	i.requeue = i.requeue[:0]
	i.produced = make(map[MsgKey]struct{})
}

// isPausedLocked reports whether a key is paused. Caller must hold i.mu.
func (i *iter) isPausedLocked(k MsgKey) bool {
	_, ok := i.paused[k]
	return ok
}

// nextRequeue pops the head of the requeue queue.
func (i *iter) nextRequeue() (MsgKey, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.requeue) == 0 {
		return MsgKey{}, false
	}
	key := i.requeue[0]
	i.requeue = i.requeue[1:]
	return key, true
}

// peerByID returns the InputPeerClass for a peer id, and whether it exists.
func (i *iter) peerByID(id int64) (tg.InputPeerClass, bool) {
	p, ok := i.peers[id]
	return p, ok
}

// produceFromKey fetches the message for a requeued key and produces its elem
// through the same path as the main loop. It does NOT touch dialogIndex /
// messageIndex / logicalPos (main state machine is fully isolated).
//
// 锁语义：本方法自身持锁调 processSingle（processSingle 假设调用方持锁，
// 直接读 i.paused/finished 等，不加锁）；Next 在调用本方法前已释放锁。
func (i *iter) produceFromKey(ctx context.Context, key MsgKey) error {
	peer, ok := i.peerByID(key.PeerID)
	if !ok {
		return errors.Errorf("requeue key %s: peer not in download set", key.String())
	}
	// 从独立索引取 (peer, msgID)
	from, err := i.manager.FromInputPeer(ctx, peer)
	if err != nil {
		return errors.Wrap(err, "requeue: resolve from input peer")
	}
	message, err := i.prefetcher.get(ctx, peer, key.MsgID)
	if err != nil {
		// 消息已删：静默跳过（与主循环 process 的 ErrMessageDeleted 处理一致，
		// 不把 requeue 解析失败当成整任务致命错误）
		if errors.Is(err, tutil.ErrMessageDeleted) {
			return nil
		}
		return errors.Wrap(err, "requeue: resolve message")
	}

	i.mu.Lock()
	// 已完成的文件（主循环在到达时已 Finish）：requeue 恢复它只会重复下载，
	// 静默跳过。isFinished 不加锁（调用方持锁），与 process 路径一致。
	if i.isFinished(key) {
		i.mu.Unlock()
		return nil
	}
	// 复用 processSingle 产出 elem（含 template 渲染、SkipSame、resume state、i.elem <-）。
	// 此处必须持锁：processSingle 不加锁（它假设调用方持锁），而 Next 已在调用前释放锁，
	// 不加锁会与 process 持锁路径数据竞争（i.elem/i.err/i.tpl 等）。
	ret, skip := i.processSingle(ctx, message, from, i.logicalPos)
	if ret {
		// 记录「已产出」：主循环稍后到达该消息时跳过，防止 requeue 早产 +
		// 主循环重复产出导致两个 goroutine 并发写同一 .tmp/.tdlstate。
		i.produced[key] = struct{}{}
	}
	i.mu.Unlock()

	if skip {
		// 文件被再次跳过（重新暂停 / 无媒体 / include-exclude / SkipSame）：静默忽略。
		// Next 通过 len(i.elem)>0 判断未产出，落到主循环继续，不会空转死锁。
		return nil
	}
	if !ret {
		// 致命错误（i.err 已设置）。i.err 在锁外读取有极小竞态窗口，
		// 但只影响错误路径的保守返回，可接受。
		if i.err != nil {
			return i.err
		}
		return errors.New("requeue: processSingle produced no element")
	}
	return nil
}

// buildPeerIndex builds a peerID -> InputPeer map for the requeue index. It is
// independent of the main dialogIndex/messageIndex state machine.
func buildPeerIndex(dialogs []*tmessage.Dialog) map[int64]tg.InputPeerClass {
	m := make(map[int64]tg.InputPeerClass, len(dialogs))
	for _, d := range dialogs {
		m[tutil.GetInputPeerID(d.Peer)] = d.Peer
	}
	return m
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
