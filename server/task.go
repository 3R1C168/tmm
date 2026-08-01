package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
// it from the store. Downloaded files on disk are NOT touched.
func (m *taskManager) delete(id string) error {
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
	return m.srv.store.delete(id)
}

// resume re-enqueues a paused/failed/interrupted task. Partial downloads are
// continued from .tdlstate files and the KV resume record.
func (m *taskManager) resume(ctx context.Context, id string) error {	task, ok := m.srv.store.get(id)
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
	ctx, cancel := context.WithCancel(logctx.With(context.Background(),
		logctx.From(context.Background()).Named("task").With(
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

	end := time.Now()
	task.mu.Lock()
	task.EndedAt = &end
	switch {
	case err == nil:
		task.Status = StatusDone
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
