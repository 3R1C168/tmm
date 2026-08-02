package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/go-faster/errors"
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
	// ?deleteFiles=true 同时删除磁盘文件（最终文件/.tmp/.tdlstate）
	deleteFiles := r.URL.Query().Get("deleteFiles") == "true"
	if err := s.tasks.delete(id, deleteFiles); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleRenameFile renames a downloaded file on disk and updates the store.
// Body: { key, name }. key = "peerID/msgID", name = new filename body (may
// include extension). If msgIdPrefix setting is on, "#msgId " is prepended
// server-side (not included in the name field).
func (s *Server) handleRenameFile(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Key == "" || req.Name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("key and name are required"))
		return
	}

	task, ok := s.store.get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("task not found"))
		return
	}

	// 应用文件名截断设置
	name := req.Name
	if maxLen := s.settings.filenameMaxLen(); maxLen > 0 && len(name) > maxLen {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if len(base) > maxLen {
			base = base[:maxLen]
		}
		name = base + ext
	}

	task.mu.Lock()
	var fe *FileEntry
	for _, f := range task.Files {
		if f.Key == req.Key {
			fe = f
			break
		}
	}
	if fe == nil {
		task.mu.Unlock()
		writeErr(w, http.StatusNotFound, errors.New("file not found in task"))
		return
	}

	// 磁盘文件：可能是最终名（done）或 .tmp（进行中）
	dir := task.Opts.Dir
	if dir == "" {
		dir = s.defaultDir
	}
	oldPath := fe.Path
	if oldPath == "" {
		oldPath = filepath.Join(dir, fe.Name)
	}
	if !filepath.IsAbs(oldPath) {
		oldPath = filepath.Join(dir, oldPath)
	}

	// 如果用户输入的新名没有扩展名，从磁盘文件补上扩展名
	if filepath.Ext(name) == "" {
		diskName := filepath.Base(oldPath)
		diskName = strings.TrimSuffix(diskName, ".tmp")
		oldExt := filepath.Ext(diskName)
		if oldExt == "" {
			// 磁盘文件名也没扩展名（之前被误删），用 mimetype 读文件头检测
			if mime, err := mimetype.DetectFile(oldPath); err == nil {
				oldExt = mime.Extension()
			}
		}
		if oldExt == "" {
			oldExt = filepath.Ext(fe.Name)
		}
		if oldExt != "" {
			name += oldExt
		}
	}

	// 新文件名：如果开了 msgIdPrefix，前面加 #msgId
	newName := name
	if s.settings.msgIdPrefix() {
		newName = fmt.Sprintf("#%d %s", fe.MsgID, name)
	}
	oldBase := filepath.Base(oldPath)
	newPath := filepath.Join(filepath.Dir(oldPath), newName)

	// 如果旧文件是 .tmp，新文件也保留 .tmp
	if strings.HasSuffix(oldBase, ".tmp") && !strings.HasSuffix(newName, ".tmp") {
		newPath += ".tmp"
		newName += ".tmp"
	}

	// 重命名磁盘文件
	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Rename(oldPath, newPath); err != nil {
			task.mu.Unlock()
			writeErr(w, http.StatusInternalServerError, errors.Wrap(err, "rename file"))
			return
		}
	}
	// 同步重命名 .tdlstate 控制文件
	oldState := oldPath + ".tdlstate"
	if _, err := os.Stat(oldState); err == nil {
		_ = os.Rename(oldState, newPath+".tdlstate")
	}

	// 更新 store
	fe.Name = newName
	fe.Path = newPath
	task.mu.Unlock()
	_ = s.store.upsert(task)
	s.publishTask(task, "task")

	s.log().Info("file renamed",
		zap.String("task", id), zap.String("file", req.Key),
		zap.String("old", oldBase), zap.String("new", newName))
	writeJSON(w, http.StatusOK, map[string]string{"status": "renamed", "name": newName})
}

type batchRequest struct {
	IDs         []string `json:"ids"`
	Action      string   `json:"action"`       // pause | resume | delete
	DeleteFiles bool     `json:"deleteFiles"`  // only affects delete action
}

func (s *Server) handleBatchTasks(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(req.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("ids must not be empty"))
		return
	}
	switch req.Action {
	case "pause", "resume", "delete":
	default:
		writeErr(w, http.StatusBadRequest, errors.Errorf("invalid action: %s", req.Action))
		return
	}
	results := s.tasks.Batch(r.Context(), req.IDs, req.Action, req.DeleteFiles)
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
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
	PreviewTakeout     *bool `json:"previewTakeout"`
	TakeoutMinMB       *int  `json:"takeoutMinMB"`
	CacheMaxMB         *int  `json:"cacheMaxMB"`
	ClearPreviewOnExit *bool `json:"clearPreviewOnExit"`
	MsgIdPrefix        *bool `json:"msgIdPrefix"`
	FilenameMaxLen     *int  `json:"filenameMaxLen"`
}

// handleUpdateSettings applies a partial settings update.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req updateSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.settings.update(req.PreviewTakeout, req.TakeoutMinMB, req.CacheMaxMB, req.ClearPreviewOnExit, req.MsgIdPrefix, req.FilenameMaxLen); err != nil {
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
