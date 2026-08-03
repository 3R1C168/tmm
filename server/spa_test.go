package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestServer builds a Server with a temp web root and no Telegram storage.
func newTestServer(t *testing.T, webRoot string) *Server {
	t.Helper()
	return &Server{
		opts:   Options{WebRoot: webRoot},
		broker: newBroker(),
		qr:     newQRManager(),
		code:   newCodeManager(),
		store:  newStore(filepath.Join(t.TempDir(), "tasks.json")),
		// 空的 account manager：activeAccount() 无账号返回 nil（不连 telegram）
		accounts: newAccountManager(nil, "", ""),
	}
}

func TestSPAFallback(t *testing.T) {
	web := t.TempDir()
	index := []byte("<html><body>app</body></html>")
	require.NoError(t, os.WriteFile(filepath.Join(web, "index.html"), index, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(web, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(web, "assets", "app.js"), []byte("console.log(1)"), 0o644))

	s := newTestServer(t, web)
	h := s.spaHandler()

	// existing static file is served directly
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "console.log(1)", rec.Body.String())

	// unknown client-side route falls back to index.html with no-cache
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/tasks/123", nil)
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, string(index), rec.Body.String())
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))

	// missing asset (e.g. stale hash from an old index.html) must 404, NOT
	// fall back to index.html — otherwise the browser parses HTML as JS/CSS
	// and shows a silent blank page.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/assets/stale-OLD.js", nil)
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.NotEqual(t, string(index), rec.Body.String(), "missing asset must not serve index.html")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/assets/stale-OLD.css", nil)
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// index.html itself is no-cache
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/index.html", nil)
	h.ServeHTTP(rec, req)
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
}

func TestSPAMissingWebRoot(t *testing.T) {
	s := newTestServer(t, filepath.Join(t.TempDir(), "nonexistent"))
	h := s.spaHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
