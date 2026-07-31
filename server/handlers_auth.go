package server

import (
	"net/http"
)

// --- QR login ---

type qrStartRequest struct {
	NS string `json:"ns"`
}

func (s *Server) handleQRStart(w http.ResponseWriter, r *http.Request) {
	var req qrStartRequest
	_ = decodeJSON(r, &req) // empty body is fine

	sess, err := s.startQRLogin(r.Context(), req.NS)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sess.snapshot())
}

type qrPasswordRequest struct {
	Session  string `json:"session"`
	Password string `json:"password"`
}

func (s *Server) handleQRPassword(w http.ResponseWriter, r *http.Request) {
	var req qrPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Password == "" {
		writeErr(w, http.StatusBadRequest, errString("password is required"))
		return
	}
	if err := s.submitQRPassword(req.Session, req.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- code login ---

type codeStartRequest struct {
	NS string `json:"ns"`
}

func (s *Server) handleCodeStart(w http.ResponseWriter, r *http.Request) {
	var req codeStartRequest
	_ = decodeJSON(r, &req)

	sess, err := s.startCodeLogin(r.Context(), req.NS)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sess.snapshot())
}

type codeInputRequest struct {
	Session string `json:"session"`
	Value   string `json:"value"`
}

func (s *Server) handleCodeInput(w http.ResponseWriter, r *http.Request) {
	var req codeInputRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Value == "" {
		writeErr(w, http.StatusBadRequest, errString("value is required"))
		return
	}
	if err := s.codeInput(req.Session, req.Value); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- desktop import ---

type desktopImportRequest struct {
	Path     string `json:"path"`     // tdata dir or Telegram Desktop dir; "" = auto detect
	Passcode string `json:"passcode"` // local passcode, may be ""
}

func (s *Server) handleDesktopImport(w http.ResponseWriter, r *http.Request) {
	var req desktopImportRequest
	_ = decodeJSON(r, &req)

	probe, err := probeDesktop(req.Path, req.Passcode)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     probe.path,
		"accounts": probe.accounts,
	})
}

type desktopConfirmRequest struct {
	NS       string `json:"ns"`
	Path     string `json:"path"`
	Passcode string `json:"passcode"`
	UserID   uint64 `json:"userId"`
	Logout   bool   `json:"logout"`
}

func (s *Server) handleDesktopConfirm(w http.ResponseWriter, r *http.Request) {
	var req desktopConfirmRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.UserID == 0 {
		writeErr(w, http.StatusBadRequest, errString("userId is required"))
		return
	}
	if err := s.confirmDesktopImport(r.Context(), req.NS, req.Path, req.Passcode, req.UserID, req.Logout); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "imported"})
}
