package server

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tclient"
	"github.com/iyear/tdl/pkg/kv"
	pkgclient "github.com/iyear/tdl/pkg/tclient"
)

// account holds the long-lived per-namespace Telegram client state. The client
// runs in a background goroutine for the lifetime of the process; the dcpool
// and peers manager are shared across all tasks for this account.
type account struct {
	ns  string
	kvd storage.Storage

	client *telegram.Client
	pool   dcpool.Pool
	peers  *peers.Manager

	cancel context.CancelFunc
	done   chan struct{}

	mu         sync.RWMutex
	authorized bool
	self       *tg.User
	runErr     error
}

// accountManager owns all accounts keyed by namespace and the single KV engine
// (bbolt allows one writer process, so the whole service shares one engine).
type accountManager struct {
	stg   kv.Storage
	proxy string
	ntp   string

	mu       sync.Mutex
	accounts map[string]*account
}

func newAccountManager(stg kv.Storage, proxy, ntp string) *accountManager {
	return &accountManager{
		stg:      stg,
		proxy:    proxy,
		ntp:      ntp,
		accounts: make(map[string]*account),
	}
}

// init scans existing namespaces and starts clients for authorized ones.
// Namespaces that fail the auth check are left for the login flow.
func (m *accountManager) init(ctx context.Context) {
	nss, err := m.stg.Namespaces()
	if err != nil {
		logctx.From(ctx).Warn("list namespaces", zap.Error(err))
		return
	}
	for _, ns := range nss {
		if _, err := m.ensure(ctx, ns); err != nil {
			logctx.From(ctx).Warn("init account", zap.String("ns", ns), zap.Error(err))
		}
	}
}

// ensure returns the account for ns, creating and starting its client if
// needed. Creating an account does not require authorization; the auth status
// is probed asynchronously.
func (m *accountManager) ensure(ctx context.Context, ns string) (*account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if a, ok := m.accounts[ns]; ok {
		return a, nil
	}

	kvd, err := m.stg.Open(ns)
	if err != nil {
		return nil, errors.Wrap(err, "open kv for namespace")
	}

	client, err := pkgclient.New(ctx, pkgclient.Options{
		KV:    kvd,
		Proxy: m.proxy,
		NTP:   m.ntp,
	}, false)
	if err != nil {
		return nil, errors.Wrap(err, "create client")
	}

	a := &account{
		ns:     ns,
		kvd:    kvd,
		client: client,
		done:   make(chan struct{}),
	}
	m.accounts[ns] = a

	runCtx, cancel := context.WithCancel(logctx.With(context.Background(), logctx.From(ctx).Named("account").With(zap.String("ns", ns))))
	a.cancel = cancel
	go a.run(runCtx)

	return a, nil
}

// run keeps the client connected for the account's lifetime and builds the
// shared pool/peers manager once authorized.
func (a *account) run(ctx context.Context) {
	defer close(a.done)

	err := a.client.Run(ctx, func(ctx context.Context) error {
		status, err := a.client.Auth().Status(ctx)
		if err != nil {
			return errors.Wrap(err, "auth status")
		}
		if !status.Authorized {
			logctx.From(ctx).Info("account not authorized, waiting for login")
			// keep the connection alive so a login flow can complete; block
			// until cancelled
			<-ctx.Done()
			return ctx.Err()
		}

		a.mu.Lock()
		a.authorized = true
		a.mu.Unlock()

		if u, err := a.client.Self(ctx); err == nil {
			a.mu.Lock()
			a.self = u
			a.mu.Unlock()
		}

		// pool size 32: a single Telegram connection is throughput-capped
		// (~100-150KB/s observed), so 8 connections top out under 1MB/s and
		// streaming starves. Telegram tolerates many connections per account
		// (the desktop client opens dozens); 32 lets streaming reach several
		// MB/s like the official client.
		a.pool = dcpool.NewPool(a.client, 32, tclient.NewDefaultMiddlewares(ctx, 0)...)
		a.peers = peers.Options{Storage: storage.NewPeers(a.kvd)}.Build(a.pool.Default(ctx))

		logctx.From(ctx).Info("account ready", zap.String("ns", a.ns))

		// Pre-dial the common media DCs in the background so the first chat's
		// thumbnail warmup doesn't pay the per-DC MTProto handshake (~seconds,
		// serialized per DC) on the user's critical path. DCs 1-5 cover the
		// vast majority of Telegram file storage. Failures are non-fatal — the
		// lazy path in pool.Client will dial on demand as before.
		go func() {
			dialCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			for dc := 1; dc <= 5; dc++ {
				_ = a.pool.Client(dialCtx, dc) // triggers lazy dial + caches invoker
			}
			logctx.From(dialCtx).Debug("pre-dialed media DCs", zap.String("ns", a.ns))
		}()

		<-ctx.Done()
		return ctx.Err()
	})

	a.mu.Lock()
	a.runErr = err
	a.mu.Unlock()
	if err != nil && !errors.Is(err, context.Canceled) {
		logctx.From(ctx).Warn("account client stopped", zap.String("ns", a.ns), zap.Error(err))
	}
}

// ready reports whether the account is authorized and its pool is available.
func (a *account) ready() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.authorized && a.pool != nil
}

// info returns a summary for the API.
func (a *account) info() accountInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	info := accountInfo{NS: a.ns, Authorized: a.authorized}
	if a.self != nil {
		info.UserID = a.self.ID
		info.Username = a.self.Username
		info.FirstName = a.self.FirstName
	}
	return info
}

func (m *accountManager) get(ns string) (*account, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[ns]
	return a, ok
}

// accountsSnapshot returns a snapshot of all accounts.
func (m *accountManager) accountsSnapshot() []*account {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*account, 0, len(m.accounts))
	for _, a := range m.accounts {
		out = append(out, a)
	}
	// 按 ns 排序，保证 activeAccount()/list() 返回顺序稳定，
	// 避免 map 迭代随机导致"当前账号"在多账号间跳动。
	sort.Slice(out, func(i, j int) bool { return out[i].ns < out[j].ns })
	return out
}

// list returns info for authorized (logged-in) accounts only, marking the
// active one. The service uses a single active account at a time.
func (m *accountManager) list() []accountInfo {
	snap := m.accountsSnapshot()
	out := make([]accountInfo, 0, len(snap))
	activeMarked := false
	for _, a := range snap {
		info := a.info()
		if !info.Authorized {
			continue // only show logged-in accounts
		}
		if !activeMarked {
			info.Active = true
			activeMarked = true
		}
		out = append(out, info)
	}
	return out
}

// close stops all account clients.
func (m *accountManager) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.cancel != nil {
			a.cancel()
		}
	}
	for _, a := range m.accounts {
		<-a.done
		if a.pool != nil {
			_ = a.pool.Close()
		}
	}
}

type accountInfo struct {
	NS         string `json:"ns"`
	Authorized bool   `json:"authorized"`
	Active     bool   `json:"active"`
	UserID     int64  `json:"userId,omitempty"`
	Username   string `json:"username,omitempty"`
	FirstName  string `json:"firstName,omitempty"`
}
