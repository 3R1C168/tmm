package server

import (
	"net/http"

	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// --- task handlers ---

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks := s.store.list()
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		t.mu.Lock()
		out = append(out, taskSnapshot(t))
		t.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

type createTaskRequest struct {
	Account  string             `json:"account"`
	URLs     []string           `json:"urls"`
	Selected []SelectedMessages `json:"selected"`
	Opts     TaskOptions        `json:"opts"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	task, err := s.tasks.submit(r.Context(), req.Account, req.URLs, req.Selected, req.Opts)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	task.mu.Lock()
	snap := taskSnapshot(task)
	task.mu.Unlock()
	writeJSON(w, http.StatusCreated, snap)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	task, ok := s.store.get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, errNotFound("task"))
		return
	}
	task.mu.Lock()
	snap := taskSnapshot(task)
	task.mu.Unlock()
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handlePauseTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := s.tasks.pause(id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "pausing"})
}

func (s *Server) handleResumeTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := s.tasks.resume(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := s.tasks.delete(id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleTaskEvents streams this task's events. It filters the global stream by
// task id and first replays the current snapshot.
func (s *Server) handleTaskEvents(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if _, ok := s.store.get(id); !ok {
		writeErr(w, http.StatusNotFound, errNotFound("task"))
		return
	}

	// 先订阅再 publish 快照：原顺序是 publish 后 subscribe，新订阅者收不到
	// 这次快照重放。先 subscribe 保证后续 publish 能投递到该 channel。
	ch := s.broker.subscribe()
	filtered := filterByTask(ch, id)
	defer s.broker.unsubscribe(ch)

	// replay current snapshot immediately so late joiners see state
	if task, ok := s.store.get(id); ok {
		task.mu.Lock()
		snap := taskSnapshot(task)
		task.mu.Unlock()
		s.broker.publish("task", snap) // n.b. broadcast; filtered below by id
	}

	serveSSE(w, r, filtered)
}

// handleGlobalEvents streams all task/auth events.
func (s *Server) handleGlobalEvents(w http.ResponseWriter, r *http.Request) {
	ch := s.broker.subscribe()
	defer s.broker.unsubscribe(ch)
	serveSSE(w, r, ch)
}

// filterByTask forwards only events whose data carries the matching task id.
func filterByTask(in chan event, id string) chan event {
	out := make(chan event, 64)
	go func() {
		defer close(out)
		for ev := range in {
			if m, ok := ev.Data.(map[string]any); ok {
				if tid, _ := m["id"].(string); tid == id {
					out <- ev
				}
			}
		}
	}()
	return out
}

// --- account handlers ---

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"accounts": s.accounts.list()})
}

// --- system handlers ---

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"downloadDir": s.defaultDir,
		"webRoot":     s.opts.WebRoot,
	})
}

// handleGetSettings returns current user settings.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settings.snapshot())
}

type updateSettingsRequest struct {
	PreviewTakeout *bool `json:"previewTakeout"`
	TakeoutMinMB   *int  `json:"takeoutMinMB"`
	CacheMaxMB     *int  `json:"cacheMaxMB"`
}

// handleUpdateSettings applies a partial settings update.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req updateSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.settings.update(req.PreviewTakeout, req.TakeoutMinMB, req.CacheMaxMB); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log().Info("settings updated", zap.Any("settings", s.settings.snapshot()))
	writeJSON(w, http.StatusOK, s.settings.snapshot())
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting down"})
	// shutdown after the response is flushed
	go func() {
		s.Shutdown()
	}()
}

type errString string

func (e errString) Error() string { return string(e) }

func errNotFound(what string) error { return errString(what + " not found") }
