package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/dl"
	"github.com/iyear/tdl/core/logctx"
)

// newID returns a short random hex identifier.
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("20060102150405.000000")
	}
	return hex.EncodeToString(b)
}

// taskManager owns the task queue and executes one task at a time (the
// per-file concurrency is controlled by runopts Limit inside dl.Run).
type taskManager struct {
	srv *Server

	mu      sync.Mutex
	queue   []string // pending task ids, FIFO
	cancels map[string]context.CancelFunc
	running string // currently-running task id, "" if idle
	iter    dl.Iter // 当前运行任务的迭代器（文件级暂停/恢复用），非 running 时为 nil

	wake chan struct{}
	stop chan struct{}
	wg   sync.WaitGroup
}

func newTaskManager(srv *Server) *taskManager {
	return &taskManager{
		srv:     srv,
		cancels: make(map[string]context.CancelFunc),
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
}

// setIter 记录当前运行任务的 iter（文件级操作即时生效用）。
func (m *taskManager) setIter(taskID string, it dl.Iter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == taskID {
		m.iter = it
	}
}

// clearIter 清空 iter（任务结束时）。
func (m *taskManager) clearIter(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == taskID {
		m.iter = nil
	}
}

// iterForTask 返回当前运行任务的 iter；任务非 running 或 iter 未就绪返回 nil。
func (m *taskManager) iterForTask(taskID string) dl.Iter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running != taskID {
		return nil
	}
	return m.iter
}

// keysToMsgKeys 把 "peerID/msgID" 字符串转成 dl.MsgKey 切片；解析失败的跳过。
func keysToMsgKeys(keys []string) []dl.MsgKey {
	out := make([]dl.MsgKey, 0, len(keys))
	for _, k := range keys {
		if mk, ok := parseMsgKey(k); ok {
			out = append(out, mk)
		}
	}
	return out
}

// parseMsgKey 解析 "peerID/msgID"。非法返回 ok=false。
func parseMsgKey(k string) (dl.MsgKey, bool) {
	parts := strings.SplitN(k, "/", 2)
	if len(parts) != 2 {
		return dl.MsgKey{}, false
	}
	peer, err1 := strconv.ParseInt(parts[0], 10, 64)
	msg, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return dl.MsgKey{}, false
	}
	return dl.MsgKey{PeerID: peer, MsgID: msg}, true
}

// submit validates and enqueues a new task.
func (m *taskManager) submit(ctx context.Context, account string, urls []string, selected []SelectedMessages, opts TaskOptions) (*Task, error) {
	if len(urls) == 0 && len(selected) == 0 {
		return nil, errors.New("请提供消息链接或选择消息")
	}
	if account == "" {
		account = "default"
	}

	a, err := m.srv.accounts.ensure(ctx, account)
	if err != nil {
		return nil, errors.Wrap(err, "open account")
	}
	if !a.ready() {
		return nil, errors.Errorf("account '%s' is not logged in", account)
	}

	task := &Task{
		ID:        newID(),
		Account:   account,
		URLs:      urls,
		Selected:  selected,
		Opts:      opts,
		Status:    StatusPending,
		Files:     make([]*FileEntry, 0),
		CreatedAt: time.Now(),
	}
	if err := m.srv.store.upsert(task); err != nil {
		return nil, err
	}

	m.srv.log().Info("task submitted",
		zap.String("task", task.ID),
		zap.String("account", account),
		zap.Strings("urls", urls),
		zap.Any("selected", selected),
		zap.String("dir", opts.Dir))

	m.mu.Lock()
	m.queue = append(m.queue, task.ID)
	m.mu.Unlock()
	m.notify()

	m.srv.publishTask(task, "task")
	return task, nil
}

// pause cancels a running task; a pending one is dequeued and marked paused.
func (m *taskManager) pause(id string) error {
	m.mu.Lock()
	// remove from pending queue if present
	for i, qid := range m.queue {
		if qid == id {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			break
		}
	}
	cancel, isRunning := m.cancels[id]
	m.mu.Unlock()

	task, ok := m.srv.store.get(id)
	if !ok {
		return errors.New("task not found")
	}

	if isRunning {
		cancel() // dl.Run observes ctx cancellation; run() sets StatusPaused
		return nil
	}

	task.mu.Lock()
	if task.Status == StatusPending {
		task.Status = StatusPaused
	}
	task.mu.Unlock()
	m.srv.publishTask(task, "task")
	return m.srv.store.upsert(task)
}

// delete removes a task: cancels if running, dequeues if pending, then erases
// it from the store. When deleteFiles is true, also removes the downloaded
// files on disk (final files, .tmp partials, and .tdlstate control files).
func (m *taskManager) delete(id string, deleteFiles bool) error {
	m.mu.Lock()
	for i, qid := range m.queue {
		if qid == id {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			break
		}
	}
	cancel, isRunning := m.cancels[id]
	m.mu.Unlock()

	if isRunning {
		cancel() // stop the in-flight download; run() will observe cancellation
	}

	// 先取 task 快照（store.delete 之后就没了），按需删磁盘文件
	if deleteFiles {
		if task, ok := m.srv.store.get(id); ok {
			m.removeTaskFiles(task)
		} else {
			m.srv.log().Warn("deleteFiles: task not found in store before cleanup",
				zap.String("task", id))
		}
	}
	return m.srv.store.delete(id)
}

// removeTaskFiles deletes every file associated with a task from disk.
//
// 历史问题：FileEntry 在某些路径下（SkipSame 跳过 + resume 重跑）内存中变 nil，
// Path 也常为空，不可靠。因此删除完全基于 task.Selected（创建时的原始数据，
// 稳定不变）：对每个 (ChatID, msgID)，到 task 目录 glob `{chatID}_{msgID}_*`。
// 文件名模板 {DialogID}_{MessageID}_{FileName}，ChatID 即文件名里的 DialogID。
// 同时清理 .tmp 半成品和 .tdlstate 控制文件。Best-effort：缺失静默忽略。
func (m *taskManager) removeTaskFiles(task *Task) {
	task.mu.Lock()
	dir := task.Opts.Dir
	if dir == "" {
		// 与 buildDlOptions 一致：Opts.Dir 为空时落到生效下载目录（含配置的 downloadDir），
		// 而不是 srv.defaultDir——否则配置了自定义下载目录时文件删不到。
		dir = m.srv.downloadDirEffective()
	}
	// 收集 (peerID, msgID) 对：优先 FileEntry.Key（若有），兜底 Selected
	type pm struct{ peer, msg string }
	var targets []pm
	seen := map[string]bool{}
	for _, f := range task.Files {
		if f == nil || f.Key == "" {
			continue
		}
		if seen[f.Key] {
			continue
		}
		seen[f.Key] = true
		parts := strings.SplitN(f.Key, "/", 2)
		if len(parts) == 2 {
			targets = append(targets, pm{parts[0], parts[1]})
		}
	}
	for _, sel := range task.Selected {
		for _, mid := range sel.MsgIDs {
			k := sel.ChatID + "/" + strconv.Itoa(mid)
			if seen[k] {
				continue
			}
			seen[k] = true
			targets = append(targets, pm{sel.ChatID, strconv.Itoa(mid)})
		}
	}
	task.mu.Unlock()

	m.srv.log().Info("removing task files",
		zap.String("task", task.ID), zap.Int("targets", len(targets)), zap.String("dir", dir))
	removed := 0
	for _, t := range targets {
		matches := globTaskFiles(dir, t.peer, t.msg)
		for _, p := range matches {
			if err := os.Remove(p); err != nil {
				if !os.IsNotExist(err) {
					m.srv.log().Warn("remove task file failed",
						zap.String("task", task.ID), zap.String("path", p), zap.Error(err))
				}
				continue
			}
			removed++
		}
	}
	m.srv.log().Info("task files removed",
		zap.String("task", task.ID), zap.Int("removed", removed))
}

// globTaskFiles recursively finds files matching <peer>_<msg>_* under dir,
// covering the per-channel subdir layout (channelDir on) and flat layout.
// 兼容文件名前缀（channelIdPrefix/msgIdPrefix）：前缀开启时磁盘文件形如
// "#<频道ID> #<msgID> <peer>_<msg>_<原名>"，<peer>_<msg>_ 主名部分前有空格。
// 因此除「开头匹配」外，还接受「空格后匹配」，且比纯 Contains 少误配
// （other_<peer>_<msg>_x.mp4 这类文件名不会命中）。
func globTaskFiles(dir, peer, msg string) []string {
	prefix := peer + "_" + msg + "_"
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(p)
		if strings.HasPrefix(base, prefix) || strings.Contains(base, " "+prefix) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// BatchResult reports the outcome of one id in a batch operation.
type BatchResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Batch applies the same action (pause/resume/delete) to many task ids,
// returning a per-id result. It is NOT transactional: a failure on one id
// does not roll back the others. deleteFiles only affects the delete action.
func (m *taskManager) Batch(ctx context.Context, ids []string, action string, deleteFiles bool) []BatchResult {
	results := make([]BatchResult, 0, len(ids))
	for _, id := range ids {
		var err error
		switch action {
		case "pause":
			err = m.pause(id)
		case "resume":
			err = m.resume(ctx, id)
		case "delete":
			err = m.delete(id, deleteFiles)
		default:
			err = errors.Errorf("unknown action: %s", action)
		}
		r := BatchResult{ID: id}
		if err != nil {
			r.Error = err.Error()
		} else {
			r.OK = true
		}
		results = append(results, r)
	}
	return results
}

// FileBatchResult reports the outcome of one file key in a file-level batch op.
type FileBatchResult struct {
	Key   string `json:"key"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// fileBatch applies a file-level pause/resume to the given keys of one task.
// action: "pause" | "resume". Per-key results, not transactional.
//
// 暂停：仅 FileRunning/FilePending → FilePaused + UserPaused=true（文件级手动暂停标记）。
//        FileDone/FileFailed 不动（返回 ok=false）。
// 恢复：仅 FilePaused → FilePending + UserPaused=false（任务级暂停产生的 paused 也允许恢复）。
//        FileDone/FileFailed 不动。
// 任务 running 时文件级暂停/恢复即时生效：
//   暂停→iter.PauseKeys（已产出的正在下载文件由 PauseCheck 钩子即刻中断）；
//   恢复→iter.Unpause（requeue 即时续传）。非 running 时只改状态，下次任务 resume 生效。
func (m *taskManager) fileBatch(taskID string, action string, keys []string) ([]FileBatchResult, error) {
	if action != "pause" && action != "resume" {
		return nil, errors.Errorf("unknown action: %s", action)
	}
	if len(keys) == 0 {
		return nil, errors.New("keys must not be empty")
	}
	task, ok := m.srv.store.get(taskID)
	if !ok {
		return nil, errors.New("task not found")
	}

	results := make([]FileBatchResult, 0, len(keys))
	// 收集受影响 key 的集合，供 iter 调用
	affected := make([]string, 0, len(keys))

	task.mu.Lock()
	byKey := make(map[string]*FileEntry, len(task.Files))
	for _, f := range task.Files {
		if f != nil {
			byKey[f.Key] = f
		}
	}
	for _, key := range keys {
		fe := byKey[key]
		r := FileBatchResult{Key: key}
		switch action {
		case "pause":
			if fe == nil {
				r.Error = "file not found in task"
			} else if fe.Status == FileDone || fe.Status == FileFailed {
				r.Error = "file already done/failed"
			} else {
				fe.Status = FilePaused
				fe.UserPaused = true
				affected = append(affected, key)
				r.OK = true
			}
		case "resume":
			if fe == nil {
				r.Error = "file not found in task"
			} else if fe.Status == FilePaused {
				fe.Status = FilePending
				fe.UserPaused = false
				affected = append(affected, key)
				r.OK = true
			} else {
				r.Error = "file not paused"
			}
		}
		results = append(results, r)
	}
	// 若任务当前在跑，让 iter 立即响应（iterForTask 在 Task 4+5 6c 实现）
	iter := m.iterForTask(taskID)
	task.mu.Unlock()

	if iter != nil {
		switch action {
		case "pause":
			iter.PauseKeys(keysToMsgKeys(affected))
		case "resume":
			for _, k := range affected {
				if mk, ok := parseMsgKey(k); ok {
					iter.Unpause(mk)
				}
			}
		}
	}

	if err := m.srv.store.upsert(task); err != nil {
		return nil, err
	}
	m.srv.publishTask(task, "file")
	return results, nil
}

// resume re-enqueues a paused/failed/interrupted task. Partial downloads are
// continued from .tdlstate files and the KV resume record.
func (m *taskManager) resume(ctx context.Context, id string) error {
	task, ok := m.srv.store.get(id)
	if !ok {
		return errors.New("task not found")
	}

	task.mu.Lock()
	switch task.Status {
	case StatusPaused, StatusFailed, StatusInterrupted, StatusDone:
		// allowed; Done re-runs to pick up anything new
	default:
		task.mu.Unlock()
		return errors.Errorf("task is %s, cannot resume", task.Status)
	}
	task.Status = StatusPending
	task.Error = ""
	task.mu.Unlock()

	a, err := m.srv.accounts.ensure(ctx, task.Account)
	if err != nil {
		return errors.Wrap(err, "open account")
	}
	if !a.ready() {
		return errors.Errorf("account '%s' is not logged in", task.Account)
	}

	if err := m.srv.store.upsert(task); err != nil {
		return err
	}

	m.mu.Lock()
	m.queue = append(m.queue, task.ID)
	m.mu.Unlock()
	m.notify()

	m.srv.publishTask(task, "task")
	return nil
}

func (m *taskManager) notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// start launches the worker loop.
func (m *taskManager) start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			select {
			case <-m.stop:
				return
			case <-m.wake:
				m.drain()
			}
		}
	}()
}

// drain runs queued tasks one at a time until the queue is empty.
func (m *taskManager) drain() {
	for {
		m.mu.Lock()
		if m.running != "" || len(m.queue) == 0 {
			m.mu.Unlock()
			return
		}
		id := m.queue[0]
		m.queue = m.queue[1:]
		m.running = id
		m.mu.Unlock()

		m.run(id)

		m.mu.Lock()
		m.running = ""
		m.mu.Unlock()
	}
}

// run executes a single task to completion (or pause/failure).
func (m *taskManager) run(id string) {
	task, ok := m.srv.store.get(id)
	if !ok {
		return
	}

	// 立即注册 cancel，避免 pause/delete 在 cancel 注册窗口内找不到 task
	// （原代码在 dl.Run 启动后才注册，用户在此窗口点 pause 会误判未运行）。
	//
	// logger 来源必须是 m.srv.log()：之前用 logctx.From(context.Background())
	// 取到的是 Nop（Background 没 attach logger），导致整条 ctx 的 logger 是 Nop，
	// 下游 iter/downloader 里 10+ 处 logctx.From(ctx) 全部静默，断点续传的
	// 诊断日志（fresh download / resume partial / resume after false-complete）
	// 写不出来，无法判断暂停后是真续传还是从头重下。
	ctx, cancel := context.WithCancel(logctx.With(context.Background(),
		m.srv.log().Named("task").With(
			zap.String("task", id), zap.String("ns", task.Account))))
	m.mu.Lock()
	if existing, ok := m.cancels[id]; ok {
		// 上一次运行的 cancel 还没清理（异常退出），先取消旧的
		existing()
	}
	m.cancels[id] = cancel
	m.running = id
	m.mu.Unlock()
	defer func() {
		cancel()
		// 必须先清 iter 再置 running=""：clearIter 内部校验 m.running==id，
		// 若先清 running 则永远清不掉（iter 引用泄漏）。drain() 同步调 run()，
		// 整个 run 期间 running==id，无并发任务，这里先清是安全的。
		m.clearIter(id)
		m.mu.Lock()
		delete(m.cancels, id)
		m.running = ""
		m.mu.Unlock()
	}()

	a, ok := m.srv.accounts.get(task.Account)
	if !ok || !a.ready() {
		m.fail(task, errors.Errorf("account '%s' is not available", task.Account))
		return
	}

	now := time.Now()
	task.mu.Lock()
	task.Status = StatusRunning
	task.StartedAt = &now
	task.Error = ""
	task.mu.Unlock()
	m.srv.publishTask(task, "task")
	_ = m.srv.store.upsert(task)

	adapter := newProgressAdapter(m.srv, task, dl.Options{})
	opts, oerr := m.srv.buildDlOptions(ctx, a, task, adapter)
	if oerr != nil {
		m.fail(task, oerr)
		return
	}
	adapter.opts = opts

	err := dl.Run(ctx, a.client, a.kvd, opts)

	// 任务结束（完成/暂停/失败）一律停止速率采样并清零 Speed，
	// 避免 goroutine 泄漏，也让非 running 态不显示陈旧速度。
	adapter.stopSpeedSampler()

	end := time.Now()
	task.mu.Lock()
	task.EndedAt = &end
	switch {
	case err == nil:
		task.Status = StatusDone
		// dl.Run 成功完成 = iter 把所有消息都处理了。但 SkipSame 命中的文件
		// （磁盘已有同名同大小）会被 iter 跳过、不触发 OnAdd，预填的 pending
		// FileEntry 残留。把这些标为 done，语义正确（文件确实在磁盘上），
		// 也避免「task done 但有 pending 文件」的矛盾显示。
		//
		// 注意：FilePaused 不在此列——文件级手动暂停（UserPaused=true）的文件
		// 保持 paused 留待恢复，不能被强制标 done（否则失去恢复能力）。
		for _, f := range task.Files {
			if f.Status == FilePending {
				f.Status = FileDone
			}
		}
	case errors.Is(err, context.Canceled):
		task.Status = StatusPaused
	default:
		task.Status = StatusFailed
		task.Error = err.Error()
	}
	finalStatus := task.Status
	finalErr := task.Error
	task.mu.Unlock()

	m.srv.log().Info("task finished",
		zap.String("task", id),
		zap.String("status", string(finalStatus)),
		zap.String("error", finalErr),
		zap.Duration("cost", end.Sub(now)))

	m.srv.publishTask(task, "task")
	_ = m.srv.store.upsert(task)
}

func (m *taskManager) fail(task *Task, err error) {
	end := time.Now()
	task.mu.Lock()
	task.Status = StatusFailed
	task.Error = err.Error()
	task.EndedAt = &end
	task.mu.Unlock()
	m.srv.publishTask(task, "task")
	_ = m.srv.store.upsert(task)
}

// shutdown cancels the running task and stops the worker.
func (m *taskManager) shutdown() {
	close(m.stop)
	m.mu.Lock()
	for _, cancel := range m.cancels {
		cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}
