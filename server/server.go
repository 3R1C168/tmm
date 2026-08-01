package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gorilla/mux"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/pkg/kv"
)

// Options configures the service.
type Options struct {
	Addr       string // listen address, e.g. 127.0.0.1:8080
	WebRoot    string // directory holding the built frontend; "" = <exe dir>/web
	DefaultDir string // default download directory for tasks that don't set one
	DataDir    string // where the task store JSON is written; "" = <exe dir>
	Proxy      string
	NTP        string
}

// Server is the tdl download service.
type Server struct {
	opts Options

	stg      kv.Storage
	accounts *accountManager
	tasks    *taskManager
	store    *store
	broker   *broker
	qr       *qrManager
	code     *codeManager
	cache    *cache

	defaultDir string
	proxy      string
	ntp        string

	http     *http.Server
	logger   *zap.Logger
	settings *settings

	evictMu   sync.Mutex
	lastEvict time.Time

	// streamers shares one streamSession (and its in-memory bitmap) per
	// preview file across concurrent range requests. Idle entries are
	// evicted after 60 s.
	streamerMu sync.Mutex
	streamers  map[string]*ssEntry

	// mediaTargets caches resolveMedia results (chat:msg -> location/size) so
	// a video's many range requests don't each re-fetch the message via RPC.
	// Keyed per account; cleared when the active account changes.
	mediaMu       sync.Mutex
	mediaTargets  map[string]*mediaTarget
	mediaTargetNS string

	// thumbInflight deduplicates concurrent thumbnail downloads for the same
	// (peerID, msgID). Only one goroutine downloads; others wait and then
	// serve the cached file.
	thumbMu       sync.Mutex
	thumbInflight map[string]chan struct{}

	// noThumb caches (ns, chat, msg) triples with no server-side thumbnail so
	// repeat probes don't each pay a resolveMedia RPC. TTL-bounded, see
	// noThumbCacheTTL.
	noThumbMu sync.Mutex
	noThumb   map[string]time.Time

	// warmupSession holds the cancellable context for a chat's thumbnail
	// warmup. First page creates it; subsequent pages of the same chat reuse
	// the ctx (so loadMore thumbs join the same warmup lifecycle); switching
	// to another chat cancels it.
	warmupSession struct {
		ctx    context.Context
		cancel context.CancelFunc
		chatID string
	}

	// warmupMu guards warmupSession.
	warmupMu sync.Mutex

	// msgsInflight deduplicates concurrent FetchMessages RPCs for the same
	// cacheKey. Crucially the RPC runs on a background context detached from
	// the HTTP request: if the user navigates away mid-fetch (aborting the
	// request), the RPC still completes and its result is cached, so the next
	// visit hits cache instead of re-fetching.
	msgsMu       sync.Mutex
	msgsInflight map[string]*msgsCall

	shutdownOnce sync.Once
	shutdownDone chan struct{}
}

// New builds the service. The storage engine is shared by all accounts (bbolt
// allows a single writer process).
func New(opts Options, stg kv.Storage) *Server {
	if opts.WebRoot == "" {
		opts.WebRoot = defaultWebRoot()
	}
	defaultDir := opts.DefaultDir
	if defaultDir == "" {
		defaultDir = defaultDownloadDir()
	}
	dataDir := opts.DataDir
	if dataDir == "" {
		if exe, err := os.Executable(); err == nil {
			dataDir = filepath.Dir(exe)
		} else {
			dataDir = "."
		}
	}

	s := &Server{
		opts:       opts,
		stg:        stg,
		broker:     newBroker(),
		qr:         newQRManager(),
		code:       newCodeManager(),
		defaultDir: defaultDir,
		proxy:      opts.Proxy,
		ntp:        opts.NTP,
		// store lives alongside the data so history survives restarts
		store:         newStore(storePath(dataDir)),
		cache:         newCache(cacheRoot()),
		thumbInflight: make(map[string]chan struct{}),
		noThumb:       make(map[string]time.Time),
		msgsInflight:  make(map[string]*msgsCall),
		shutdownDone:  make(chan struct{}),
	}
	s.accounts = newAccountManager(stg, opts.Proxy, opts.NTP)
	s.tasks = newTaskManager(s)
	s.initLogger(logctx.From(context.Background()))
	s.settings = defaultSettings(settingsPath())
	_ = s.settings.load()
	return s
}

// storePath names the task-history JSON file inside dataDir.
func storePath(dataDir string) string {
	return filepath.Join(dataDir, "server-tasks.json")
}

// Run starts the service and blocks until the context is cancelled or
// Shutdown is called (via /api/shutdown).
func (s *Server) Run(ctx context.Context) error {
	if err := s.store.load(ctx); err != nil {
		logctx.From(ctx).Warn("load task store", zap.Error(err))
	}
	s.accounts.init(ctx)
	s.tasks.start()

	router := s.router()
	s.http = &http.Server{
		Addr:              s.opts.Addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logctx.From(ctx).Sugar().Infof("Serving web UI at http://%s", s.opts.Addr)
		logctx.From(ctx).Sugar().Infof("Web root: %s", s.opts.WebRoot)
		logctx.From(ctx).Sugar().Infof("Download dir: %s", s.defaultDir)
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
	case <-s.shutdownDone:
	case err := <-errCh:
		return err
	}

	return s.gracefulStop()
}

// gracefulStop cancels tasks, closes accounts, and shuts the HTTP server down.
func (s *Server) gracefulStop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s.tasks.shutdown()
	s.qr.closeAll()
	s.code.closeAll()
	s.accounts.close()
	if s.http != nil {
		_ = s.http.Shutdown(ctx)
	}
	return s.stg.Close()
}

// Shutdown requests a graceful stop (idempotent via sync.Once). Used by /api/shutdown.
func (s *Server) Shutdown() {
	s.shutdownOnce.Do(func() { close(s.shutdownDone) })
}

// publishTask broadcasts a task snapshot to all SSE subscribers.
func (s *Server) publishTask(t *Task, typ string) {
	t.mu.Lock()
	snap := taskSnapshot(t)
	t.mu.Unlock()
	s.broker.publish(typ, snap)
}

func (s *Server) router() *mux.Router {
	r := mux.NewRouter()

	api := r.PathPrefix("/api").Subrouter()

	// tasks
	api.HandleFunc("/tasks", s.handleListTasks).Methods(http.MethodGet)
	api.HandleFunc("/tasks", s.handleCreateTask).Methods(http.MethodPost)
	api.HandleFunc("/tasks/{id}", s.handleGetTask).Methods(http.MethodGet)
	api.HandleFunc("/tasks/{id}/pause", s.handlePauseTask).Methods(http.MethodPost)
	api.HandleFunc("/tasks/{id}/resume", s.handleResumeTask).Methods(http.MethodPost)
	api.HandleFunc("/tasks/{id}", s.handleDeleteTask).Methods(http.MethodDelete)
	api.HandleFunc("/tasks/{id}/events", s.handleTaskEvents).Methods(http.MethodGet)
	api.HandleFunc("/events", s.handleGlobalEvents).Methods(http.MethodGet)

	// accounts
	api.HandleFunc("/accounts", s.handleListAccounts).Methods(http.MethodGet)

	// chats & messages (message browser)
	api.HandleFunc("/chats", s.handleListChats).Methods(http.MethodGet)
	api.HandleFunc("/chats/{id}/messages", s.handleListMessages).Methods(http.MethodGet)

	// media preview / streaming
	api.HandleFunc("/media/{id}/{msg}/info", s.handleMediaInfo).Methods(http.MethodGet)
	api.HandleFunc("/media/{id}/{msg}/thumb", s.handleMediaThumb).Methods(http.MethodGet)
	api.HandleFunc("/media/{id}/{msg}/file", s.handleMediaFile).Methods(http.MethodGet)
	api.HandleFunc("/media/{id}/{msg}/stream", s.handleMediaStream).Methods(http.MethodGet)

	// filesystem helpers (native dir picker + tdata detection)
	api.HandleFunc("/fs/pick-folder", s.handlePickFolder).Methods(http.MethodPost)
	api.HandleFunc("/fs/detect-tdata", s.handleDetectTdata).Methods(http.MethodGet)

	// auth
	api.HandleFunc("/auth/qr/start", s.handleQRStart).Methods(http.MethodPost)
	api.HandleFunc("/auth/qr/password", s.handleQRPassword).Methods(http.MethodPost)
	api.HandleFunc("/auth/qr/events", s.handleGlobalEvents).Methods(http.MethodGet)
	api.HandleFunc("/auth/code/start", s.handleCodeStart).Methods(http.MethodPost)
	api.HandleFunc("/auth/code/input", s.handleCodeInput).Methods(http.MethodPost)
	api.HandleFunc("/auth/code/events", s.handleGlobalEvents).Methods(http.MethodGet)
	api.HandleFunc("/auth/desktop/import", s.handleDesktopImport).Methods(http.MethodPost)
	api.HandleFunc("/auth/desktop/confirm", s.handleDesktopConfirm).Methods(http.MethodPost)

	// system
	api.HandleFunc("/shutdown", s.handleShutdown).Methods(http.MethodPost)
	api.HandleFunc("/config", s.handleConfig).Methods(http.MethodGet)
	api.HandleFunc("/settings", s.handleGetSettings).Methods(http.MethodGet)
	api.HandleFunc("/settings", s.handleUpdateSettings).Methods(http.MethodPost)

	// static frontend + SPA fallback
	r.PathPrefix("/").Handler(s.spaHandler())

	return r
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return errors.Wrap(err, "invalid JSON body")
	}
	return nil
}

// taskSnapshot returns a JSON-friendly copy of a task (caller holds task.mu).
func taskSnapshot(t *Task) map[string]any {
	files := make([]*FileEntry, len(t.Files))
	copy(files, t.Files)
	return map[string]any{
		"id":          t.ID,
		"account":     t.Account,
		"urls":        t.URLs,
		"opts":        t.Opts,
		"status":      t.Status,
		"error":       t.Error,
		"files":       files,
		"createdAt":   t.CreatedAt,
		"startedAt":   t.StartedAt,
		"endedAt":     t.EndedAt,
		"fingerprint": t.Fingerprint,
		"totalSize":   t.TotalSize(),
		"downloaded":  t.TotalDownloaded(),
	}
}

// --- static / SPA ---

// spaHandler serves the built frontend, falling back to index.html for
// client-side routes. index.html is served with no-cache.
func (s *Server) spaHandler() http.Handler {
	root := s.opts.WebRoot
	fs := http.FileServer(http.Dir(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// clean path and check existence
		p := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			if filepath.Base(p) == "index.html" {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fs.ServeHTTP(w, r)
			return
		}

		// SPA fallback
		index := filepath.Join(root, "index.html")
		if _, err := os.Stat(index); err != nil {
			http.Error(w, "web frontend not found (web root: "+root+")", http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}
