package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/dl"
	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/runopts"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/tmessage"
)

const defaultTemplate = `{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}`

// errFilePaused is the sentinel the PauseCheck returns when the user manually
// pauses a single file. It is deliberately NOT context.Canceled, so the
// downloader treats it as a single-file abort (reported to OnDone, task keeps
// running) instead of a whole-task cancellation.
var errFilePaused = errors.New("file paused by user")

// sseThrottle is how often per-file byte progress is pushed to the UI.
const sseThrottle = 200 * time.Millisecond

// progressAdapter implements downloader.Progress for a task, updating file
// state and streaming events over SSE. It reuses dl.FinalizeElem so temp files
// are closed/renamed exactly as the CLI does.
type progressAdapter struct {
	srv  *Server
	task *Task
	opts dl.Options

	mu   sync.Mutex
	iter dl.Iter
	// lastPush tracks the last SSE emit per file key for throttling.
	lastPush map[string]time.Time

	// task 级速率：独立 goroutine 每秒采样 total 差值算瞬时速率，EWMA 平滑。
	// 之前在 OnDownload 里按 SSE push 触发计算，分子(全局 total 增量)与
	// 分母(触发时局部 dt)时间窗错配，多文件并发时瞬时值会飙高数倍。
	speedMu       sync.Mutex
	speedEwma     int64 // EWMA 平滑后的速率 bytes/sec
	lastSample    int64 // 上次采样的 total，用于算 1 秒增量
	sampleStarted bool  // sampler goroutine 是否已启动（防重复）
	stopSampler   chan struct{}
}

func newProgressAdapter(srv *Server, task *Task, opts dl.Options) *progressAdapter {
	return &progressAdapter{
		srv:         srv,
		task:        task,
		opts:        opts,
		lastPush:    make(map[string]time.Time),
		stopSampler: make(chan struct{}),
	}
}

// hook captures the iterator so the adapter can mark messages finished.
func (p *progressAdapter) hook(it dl.Iter) {
	p.mu.Lock()
	p.iter = it
	p.mu.Unlock()

	p.task.mu.Lock()
	p.task.Fingerprint = it.Fingerprint()
	// 任务 resume 重跑时，用户手动暂停的文件（UserPaused）保持跳过，不自动续传
	var pausedKeys []dl.MsgKey
	for _, f := range p.task.Files {
		if f != nil && f.UserPaused {
			if mk, ok := parseMsgKey(f.Key); ok {
				pausedKeys = append(pausedKeys, mk)
			}
		}
	}
	p.task.mu.Unlock()

	if len(pausedKeys) > 0 {
		it.PauseKeys(pausedKeys)
	}

	// 暴露 iter 给 taskManager（文件级暂停/恢复即时生效）
	p.srv.tasks.setIter(p.task.ID, it)
}

func (p *progressAdapter) OnAdd(elem downloader.Elem) {
	e, ok := elem.(dl.Elem)
	if !ok {
		return
	}
	key := e.Key().String()

	p.task.mu.Lock()
	// 去重：同一文件（按 Key）只保留一个 FileEntry。
	// resume 会重新跑 dl.Run，iter 不跳过未 Finish 的文件，会再次 OnAdd；
	// 这里命中已有条目就更新它（重置为 Running），避免 Files 列表无限累加。
	var action string
	if existing := p.findFileLocked(key); existing != nil {
		existing.Status = FileRunning
		existing.Error = ""
		// 刷新可能变化的元数据（路径在 resume 时可能变；FromName/FromID 预填时没有）
		existing.Name = e.Name()
		existing.Path = e.ToPath()
		existing.FromName = e.FromName()
		existing.FromID = e.FromID()
		existing.Caption = e.Caption()
		existing.Size = e.File().Size()
		action = "updated"
	} else {
		p.task.Files = append(p.task.Files, &FileEntry{
			Key:      key,
			Name:     e.Name(),
			Path:     e.ToPath(),
			FromName: e.FromName(),
			FromID:   e.FromID(),
			MsgID:    e.MsgID(),
			Caption:  e.Caption(),
			Size:     e.File().Size(),
			Status:   FileRunning,
		})
		action = "added"
	}
	fileCount := len(p.task.Files)
	p.task.mu.Unlock()

	// 用 srv.log() 而非 logctx.From(context.Background())：后者拿到的是 Nop logger
	// （context.Background() 没 attach logger），日志写不出来。
	p.srv.log().Info("task file "+action,
		zap.String("task", p.task.ID),
		zap.String("file", key),
		zap.Int("msgId", e.MsgID()),
		zap.Int("totalFiles", fileCount))

	p.srv.publishTask(p.task, "file")
}

func (p *progressAdapter) OnDownload(elem downloader.Elem, state downloader.ProgressState) {
	e, ok := elem.(dl.Elem)
	if !ok {
		return
	}
	key := e.Key().String()

	p.mu.Lock()
	last, okp := p.lastPush[key]
	now := time.Now()
	// always record the byte count, but throttle the SSE emit
	p.task.mu.Lock()
	if fe := p.findFileLocked(key); fe != nil {
		fe.Downloaded = state.Downloaded
		if state.Total > 0 {
			fe.Size = state.Total
		}
	}
	p.task.mu.Unlock()
	if okp && now.Sub(last) < sseThrottle {
		p.mu.Unlock()
		return
	}
	p.lastPush[key] = now
	p.mu.Unlock()

	// 首次 OnDownload 时启动速率采样 goroutine（每秒固定采样，独立于 SSE 频率）
	p.ensureSpeedSampler()
	p.srv.publishTask(p.task, "file")
}

// ensureSpeedSampler 启动一个每秒采样 total 的 goroutine（仅启动一次）。
// 独立于 SSE push 频率，用固定 1 秒窗口算瞬时速率，EWMA(α=0.4) 平滑，
// 避免「全局 total 增量 / 触发时局部 dt」时间窗错配导致的瞬时飙高。
func (p *progressAdapter) ensureSpeedSampler() {
	p.speedMu.Lock()
	defer p.speedMu.Unlock()
	if p.sampleStarted {
		return
	}
	p.sampleStarted = true
	p.lastSample = p.task.TotalDownloaded()
	go p.runSpeedSampler()
}

func (p *progressAdapter) runSpeedSampler() {
	const interval = time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopSampler:
			return
		case <-ticker.C:
			total := p.task.TotalDownloaded()
			p.speedMu.Lock()
			inst := total - p.lastSample
			p.lastSample = total
			if inst < 0 {
				inst = 0 // 防御：total 不应回退，但 resume 重置等场景可能触发
			}
			// EWMA：新样本权重 0.4，平滑历史，抑制抖动又不至于太迟钝
			if p.speedEwma == 0 {
				p.speedEwma = inst
			} else {
				p.speedEwma = int64(float64(p.speedEwma)*0.6 + float64(inst)*0.4)
			}
			p.task.Speed = p.speedEwma
			p.speedMu.Unlock()
		}
	}
}

// stopSpeedSampler 停止采样 goroutine，置 Speed 为 0（任务结束）。
func (p *progressAdapter) stopSpeedSampler() {
	p.speedMu.Lock()
	chanOpen := false
	// 用 select-default 探测 channel 是否已关闭，避免重复 close panic
	select {
	case <-p.stopSampler:
	default:
		chanOpen = true
	}
	if chanOpen {
		close(p.stopSampler)
	}
	p.speedMu.Unlock()
	p.task.Speed = 0
}

func (p *progressAdapter) OnDone(elem downloader.Elem, derr error) {
	e, ok := elem.(dl.Elem)
	if !ok {
		return
	}
	key := e.Key().String()

	p.mu.Lock()
	it := p.iter
	p.mu.Unlock()

	finalPath, ferr := dl.FinalizeElem(e, p.opts, it, derr)

	p.task.mu.Lock()
	fe := p.findFileLocked(key)
	if fe != nil {
		if ferr != nil {
			if errors.Is(ferr, context.Canceled) {
				fe.Status = FilePaused // 任务级取消：已开始下载后被中断，可续传（区别于预填排队中的 FilePending）
			} else if errors.Is(ferr, errFilePaused) {
				fe.Status = FilePaused // 文件级手动暂停：区别于任务级取消
				fe.UserPaused = true
			} else {
				fe.Status = FileFailed
				fe.Error = ferr.Error()
			}
		} else {
			fe.Status = FileDone
			fe.Downloaded = fe.Size
			fe.Path = finalPath
		}
	}
	p.task.mu.Unlock()

	if ferr != nil && !errors.Is(ferr, context.Canceled) && !errors.Is(ferr, errFilePaused) {
		p.srv.log().Warn("file finalize failed",
			zap.String("task", p.task.ID), zap.String("file", key), zap.Error(ferr))
	}

	p.srv.publishTask(p.task, "file")
}

// findFileLocked locates a file entry by key. Caller must hold task.mu.
func (p *progressAdapter) findFileLocked(key string) *FileEntry {
	for _, f := range p.task.Files {
		if f.Key == key {
			return f
		}
	}
	return nil
}

// buildFilenamePrefix assembles the filename prefix template parts from the
// two independent settings. Channel prefix goes first, then message prefix.
func buildFilenamePrefix(channel, msg bool) string {
	var sb strings.Builder
	if channel {
		sb.WriteString("#{{ .DialogID }} ")
	}
	if msg {
		sb.WriteString("#{{ .MessageID }} ")
	}
	return sb.String()
}

// buildDlOptions translates a task into dl.Options for dl.Run, wiring the
// progress adapter and iter hook, and forcing non-interactive behaviour.
// Direct (chat,msgIDs) selections are resolved into tmessage.Dialog here.
func (srv *Server) buildDlOptions(ctx context.Context, a *account, task *Task, adapter *progressAdapter) (dl.Options, error) {
	o := task.Opts

	template := o.Template
	if template == "" {
		template = defaultTemplate
	}
	// 全局设置：文件名前缀 #频道ID / #msgId（开关各自独立，频道在前、消息在后）
	template = buildFilenamePrefix(srv.settings.channelIdPrefix(), srv.settings.msgIdPrefix()) + template

	takeout := dl.TakeoutMode(o.Takeout)
	if !takeout.Valid() {
		takeout = dl.TakeoutAuto
	}

	dir := o.Dir
	if dir == "" {
		dir = srv.downloadDirEffective()
	}

	// resolve direct message selections into dialogs
	var dialogs []*tmessage.Dialog
	// 预填：任务一开始就把所有选中的 msgId 列入 FileEntry（pending/排队中），
	// 让用户立即看到完整文件清单。OnAdd 触发时 Key 命中已存在条目 → 更新为 running。
	// 去重：resume 会再跑 buildDlOptions，已有的 Key 不重复预填。
	task.mu.Lock()
	existingKeys := make(map[string]bool, len(task.Files))
	for _, f := range task.Files {
		existingKeys[f.Key] = true
	}
	for _, sel := range task.Selected {
		if len(sel.MsgIDs) == 0 {
			continue
		}
		peer, err := tutil.GetInputPeer(ctx, a.peers, sel.ChatID)
		if err != nil {
			task.mu.Unlock()
			return dl.Options{}, errors.Wrapf(err, "解析会话 %s 失败", sel.ChatID)
		}
		peerID := tutil.GetInputPeerID(peer.InputPeer())
		ids := append([]int(nil), sel.MsgIDs...)
		sort.Ints(ids)
		for _, mid := range ids {
			key := fmt.Sprintf("%d/%d", peerID, mid)
			if !existingKeys[key] {
				task.Files = append(task.Files, &FileEntry{
					Key:    key,
					MsgID:  mid,
					Status: FilePending, // 排队中，等 OnAdd 标 running
				})
				existingKeys[key] = true
			}
		}
		dialogs = append(dialogs, &tmessage.Dialog{
			Peer:     peer.InputPeer(),
			Messages: ids,
		})
	}
	task.mu.Unlock()

	opts := dl.Options{
		Dir:            dir,
		RewriteExt:     o.RewriteExt,
		SkipSame:       o.SkipSame,
		Template:       template,
		URLs:           task.URLs,
		Dialogs:        dialogs,
		Include:        o.Include,
		Exclude:        o.Exclude,
		Desc:           o.Desc,
		Takeout:        takeout,
		Group:          o.Group,
		FilenameMaxLen: srv.settings.filenameMaxLen(),
		ChannelDir:     srv.settings.channelDir(),

		// non-interactive: never prompt; always continue partial downloads
		Continue: true,
		Restart:  o.Restart,

		// 并发 clamp：防止无界连接池（PoolSize=0 在 gotd 里表示无限连接），
		// 并把文件级/piece 级并发限制在不触发 Telegram 限流的安全范围。
		// 用户传 0 时用推荐值；传非零值也夹在安全区间内。
		// 依据：单账号单 DC 约 30 并发 chunk 持续数秒才触发 FLOOD_WAIT，
		// Limit(3)×Threads(8)=24 留有缓冲，且 floodwait 中间件会自动重试。
		Runtime: runopts.Options{
			PoolSize:          clampPoolSize(o.PoolSize),
			Threads:           clampInt(o.Threads, 8, 1, 8),
			Limit:             clampInt(o.Limit, 3, 1, 4),
			Delay:             time.Duration(o.DelayMs) * time.Millisecond,
			ReconnectTimeout:  runopts.DefaultReconnectTimeout,
			DisableProgressPS: true,
		}.Normalize(),

		Progress: adapter,
		IterHook: adapter.hook,
		// 文件级暂停：writeAt 每次写入前检查该文件是否被用户手动暂停（UserPaused）。
		// 命中返回 errFilePaused 哨兵（非 context.Canceled）→ downloader 只中止该文件、
		// 不取消整任务；OnDone 判定为 FilePaused + UserPaused。
		PauseCheck: func(elem downloader.Elem) error {
			// downloader.Elem 无 Key()，需断言为 dl.Elem（progressAdapter.OnAdd 同款用法）
			e, ok := elem.(dl.Elem)
			if !ok {
				return nil
			}
			key := e.Key().String()
			task.mu.Lock()
			defer task.mu.Unlock()
			if fe := adapter.findFileLocked(key); fe != nil && fe.UserPaused {
				return errFilePaused
			}
			return nil
		},
	}
	return opts, nil
}

// clampInt returns def when v==0, otherwise clamps v into [lo, hi].
func clampInt(v, def, lo, hi int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampPoolSize returns a safe pool size when v==0 (gotd: 0 means infinity,
// which lets connections grow unbounded); otherwise at least 16.
func clampPoolSize(v int) int {
	if v <= 0 {
		return 24
	}
	if v < 16 {
		return 16
	}
	return v
}
