package server

import (
	"context"
	"fmt"
	"sort"
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

	// task 级速率滑动窗口（最近 N 次采样的瞬时 bytes/sec，取平均，避免抖动）
	speedMu       sync.Mutex
	speedSamples  []int64
	lastTotal     int64
	lastSpeedTime time.Time
}

func newProgressAdapter(srv *Server, task *Task, opts dl.Options) *progressAdapter {
	return &progressAdapter{
		srv:      srv,
		task:     task,
		opts:     opts,
		lastPush: make(map[string]time.Time),
	}
}

// hook captures the iterator so the adapter can mark messages finished.
func (p *progressAdapter) hook(it dl.Iter) {
	p.mu.Lock()
	p.iter = it
	p.mu.Unlock()

	p.task.mu.Lock()
	p.task.Fingerprint = it.Fingerprint()
	p.task.mu.Unlock()
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

	p.updateSpeed()
	p.srv.publishTask(p.task, "file")
}

// updateSpeed 用 task 总下载量的时间差算瞬时速率，滑动平均（最近 5 次），
// 存入 task.Speed 供 taskSnapshot 输出。比前端按 SSE 差值算更准（不受 SSE 节流影响）。
func (p *progressAdapter) updateSpeed() {
	p.speedMu.Lock()
	defer p.speedMu.Unlock()
	now := time.Now()
	total := p.task.TotalDownloaded()
	if !p.lastSpeedTime.IsZero() && now.After(p.lastSpeedTime) {
		dt := now.Sub(p.lastSpeedTime).Seconds()
		if dt > 0 {
			inst := int64(float64(total-p.lastTotal) / dt)
			if inst >= 0 {
				p.speedSamples = append(p.speedSamples, inst)
				if len(p.speedSamples) > 5 {
					p.speedSamples = p.speedSamples[1:]
				}
			}
		}
	}
	p.lastTotal = total
	p.lastSpeedTime = now
	// 平均
	var sum int64
	for _, s := range p.speedSamples {
		sum += s
	}
	if len(p.speedSamples) > 0 {
		p.task.Speed = sum / int64(len(p.speedSamples))
	}
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
				fe.Status = FilePending // paused; resumable
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

	if ferr != nil && !errors.Is(ferr, context.Canceled) {
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

// buildDlOptions translates a task into dl.Options for dl.Run, wiring the
// progress adapter and iter hook, and forcing non-interactive behaviour.
// Direct (chat,msgIDs) selections are resolved into tmessage.Dialog here.
func (srv *Server) buildDlOptions(ctx context.Context, a *account, task *Task, adapter *progressAdapter) (dl.Options, error) {
	o := task.Opts

	template := o.Template
	if template == "" {
		template = defaultTemplate
	}

	takeout := dl.TakeoutMode(o.Takeout)
	if !takeout.Valid() {
		takeout = dl.TakeoutAuto
	}

	dir := o.Dir
	if dir == "" {
		dir = srv.defaultDir
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
		Dir:        dir,
		RewriteExt: o.RewriteExt,
		SkipSame:   o.SkipSame,
		Template:   template,
		URLs:       task.URLs,
		Dialogs:    dialogs,
		Include:    o.Include,
		Exclude:    o.Exclude,
		Desc:       o.Desc,
		Takeout:    takeout,
		Group:      o.Group,

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
