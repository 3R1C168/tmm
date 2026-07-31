package server

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/dl"
	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/logctx"
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
	fe := &FileEntry{
		Key:      e.Key().String(),
		Name:     e.Name(),
		Path:     e.ToPath(),
		FromName: e.FromName(),
		FromID:   e.FromID(),
		MsgID:    e.MsgID(),
		Size:     e.File().Size(),
		Status:   FileRunning,
	}
	p.task.mu.Lock()
	p.task.Files = append(p.task.Files, fe)
	p.task.mu.Unlock()

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

	p.srv.publishTask(p.task, "file")
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
		logctx.From(context.Background()).Warn("file finalize failed",
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
	for _, sel := range task.Selected {
		if len(sel.MsgIDs) == 0 {
			continue
		}
		peer, err := tutil.GetInputPeer(ctx, a.peers, sel.ChatID)
		if err != nil {
			return dl.Options{}, errors.Wrapf(err, "解析会话 %s 失败", sel.ChatID)
		}
		ids := append([]int(nil), sel.MsgIDs...)
		sort.Ints(ids)
		dialogs = append(dialogs, &tmessage.Dialog{
			Peer:     peer.InputPeer(),
			Messages: ids,
		})
	}

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

		Runtime: runopts.Options{
			PoolSize:          o.PoolSize,
			Threads:           o.Threads,
			Limit:             o.Limit,
			Delay:             time.Duration(o.DelayMs) * time.Millisecond,
			ReconnectTimeout:  runopts.DefaultReconnectTimeout,
			DisableProgressPS: true,
		}.Normalize(),

		Progress: adapter,
		IterHook: adapter.hook,
	}
	return opts, nil
}
