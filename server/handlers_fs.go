package server

import (
	"net/http"
	"path/filepath"
	"runtime"

	"github.com/sqweek/dialog"

	"github.com/iyear/tdl/core/util/fsutil"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/tpath"
)

// handlePickFolder opens a native directory picker on the server machine and
// returns the chosen path. Blocks until the user picks or cancels.
func (s *Server) handlePickFolder(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "windows" {
		writeErr(w, http.StatusNotImplemented, errString("当前平台不支持原生目录选择框"))
		return
	}

	dir, err := dialog.Directory().Title("选择 Telegram Desktop 数据目录（tdata 所在目录）").Browse()
	if err == dialog.ErrCancelled {
		writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": false, "path": dir})
}

// handleDetectTdata returns candidate Telegram Desktop data dirs that exist on
// this machine, so the login page can pre-fill without manual browsing.
func (s *Server) handleDetectTdata(w http.ResponseWriter, r *http.Request) {
	type candidate struct {
		Path   string `json:"path"`
		Exists bool   `json:"exists"`
	}
	out := make([]candidate, 0)
	var first string
	for _, p := range tpath.Desktop.AppData(consts.HomeDir) {
		full := p
		if filepath.Base(full) != "tdata" {
			full = filepath.Join(full, "tdata")
		}
		exists := fsutil.PathExists(full)
		out = append(out, candidate{Path: full, Exists: exists})
		if exists && first == "" {
			first = full
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": out,
		"detected":   first, // first existing candidate, "" if none
	})
}
