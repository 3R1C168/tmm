package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-faster/errors"

	"github.com/iyear/tdl/core/logctx"
)

// store persists the task list to a JSON file so history survives restarts and
// interrupted tasks can be resumed. Writes are atomic (tmp + rename).
type store struct {
	path string

	mu    sync.Mutex
	tasks map[string]*Task
	order []string // task ids in insertion order
}

type storeFile struct {
	Tasks []*Task `json:"tasks"`
}

func newStore(path string) *store {
	return &store{
		path:  path,
		tasks: make(map[string]*Task),
		order: make([]string, 0),
	}
}

// load reads the task file. A missing file is not an error. Tasks that were
// pending/running when the process stopped are marked interrupted.
func (s *store) load(ctx context.Context) error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrap(err, "read task store")
	}

	var f storeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return errors.Wrap(err, "parse task store")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range f.Tasks {
		if t.Status == StatusPending || t.Status == StatusRunning {
			t.Status = StatusInterrupted
		}
		s.tasks[t.ID] = t
		s.order = append(s.order, t.ID)
	}
	logctx.From(ctx).Sugar().Infof("Loaded %d tasks from store", len(f.Tasks))
	return nil
}

// save writes the current task list atomically. Caller must hold no lock.
func (s *store) save() error {
	s.mu.Lock()
	f := storeFile{Tasks: make([]*Task, 0, len(s.order))}
	for _, id := range s.order {
		f.Tasks = append(f.Tasks, s.tasks[id])
	}
	s.mu.Unlock()

	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshal task store")
	}

	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errors.Wrap(err, "create store dir")
		}
	}

	// tmp 用唯一后缀：worker 的 run() 与 handler（fileBatch/resume/pause）可能并发
	// upsert，若共用固定 .tmp 名会互相覆盖/撞 rename（Windows 上句柄未释放时报错）。
	// 每次写用新 tmp 文件，rename 原子替换，天然容忍并发。
	tmp := fmt.Sprintf("%s.tmp.%d", s.path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return errors.Wrap(err, "write task store")
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return errors.Wrap(err, "rename task store")
	}
	return nil
}

// upsert adds or replaces a task and persists.
func (s *store) upsert(t *Task) error {
	s.mu.Lock()
	if _, ok := s.tasks[t.ID]; !ok {
		s.order = append(s.order, t.ID)
	}
	s.tasks[t.ID] = t
	s.mu.Unlock()
	return s.save()
}

// get returns a task by id.
func (s *store) get(id string) (*Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	return t, ok
}

// list returns tasks in insertion order.
func (s *store) list() []*Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Task, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.tasks[id])
	}
	return out
}

// delete removes a task from the store and persists.
func (s *store) delete(id string) error {
	s.mu.Lock()
	if _, ok := s.tasks[id]; !ok {
		s.mu.Unlock()
		return errors.New("task not found")
	}
	delete(s.tasks, id)
	for i, qid := range s.order {
		if qid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return s.save()
}
