package dcpool

import (
	"context"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/middlewares/takeout"
)

var testMode = false

// EnableTestMode enables test mode, which disables takeout and pooling and directly returns original client.
func EnableTestMode() {
	testMode = true
}

// closeTimeout bounds how long Close waits for the takeout session to finish,
// so a stuck network cannot block process shutdown.
const closeTimeout = 10 * time.Second

type Pool interface {
	Client(ctx context.Context, dc int) *tg.Client
	Takeout(ctx context.Context, dc int) *tg.Client
	Default(ctx context.Context) *tg.Client
	Close() error
}

type pool struct {
	api         *telegram.Client
	size        int64
	middlewares []telegram.Middleware

	// mu guards invokers, closes, dcMu, takeout and closed. It is only held for
	// short map/field access; network dialing happens under per-DC locks.
	mu       sync.Mutex
	invokers map[int]tg.Invoker
	closes   map[int]func() error
	// dcMu serializes lazy invoker creation per DC, so a slow dial for one DC
	// does not block clients of other DCs, and one DC is only dialed once.
	dcMu    map[int]*sync.Mutex
	takeout int64
	closed  bool
}

func NewPool(c *telegram.Client, size int64, middlewares ...telegram.Middleware) Pool {
	return &pool{
		api:         c,
		size:        size,
		middlewares: middlewares,
		invokers:    make(map[int]tg.Invoker),
		closes:      make(map[int]func() error),
		dcMu:        make(map[int]*sync.Mutex),
		takeout:     0,
	}
}

func (p *pool) current() int {
	return p.api.Config().ThisDC
}

func (p *pool) Client(ctx context.Context, dc int) *tg.Client {
	return tg.NewClient(p.invoker(ctx, dc))
}

func (p *pool) invoker(ctx context.Context, dc int) tg.Invoker {
	// self-hosted Telegram server can't properly handle pooling connections,
	// so directly return original client
	if testMode {
		return p.api
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return p.api
	}
	if i, ok := p.invokers[dc]; ok {
		p.mu.Unlock()
		return i
	}
	// register a per-DC lock so creation is serialized for this DC only
	dm, ok := p.dcMu[dc]
	if !ok {
		dm = &sync.Mutex{}
		p.dcMu[dc] = dm
	}
	p.mu.Unlock()

	dm.Lock()
	defer dm.Unlock()

	// re-check after acquiring the per-DC lock (another goroutine may have
	// created the invoker while we were waiting)
	p.mu.Lock()
	if i, ok := p.invokers[dc]; ok {
		p.mu.Unlock()
		return i
	}
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return p.api
	}

	// lazy init; network dialing happens here, outside the global lock
	var (
		invoker telegram.CloseInvoker
		err     error
	)
	if dc == p.current() { // can't transfer dc to current dc
		invoker, err = p.api.Pool(p.size)
	} else {
		invoker, err = p.api.DC(ctx, dc, p.size)
	}

	if err != nil {
		logctx.From(ctx).Error("create invoker", zap.Error(err))
		return p.api // degraded
	}

	p.mu.Lock()
	if p.closed {
		// pool was closed while we were dialing; close the fresh invoker and
		// degrade to the original client
		p.mu.Unlock()
		_ = invoker.Close()
		return p.api
	}
	p.closes[dc] = invoker.Close
	p.invokers[dc] = chainMiddlewares(invoker, p.middlewares...)
	i := p.invokers[dc]
	p.mu.Unlock()

	return i
}

func (p *pool) Default(ctx context.Context) *tg.Client {
	return p.Client(ctx, p.current())
}

func (p *pool) Close() (err error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	takeoutID := p.takeout
	closes := make([]func() error, 0, len(p.closes))
	for _, c := range p.closes {
		closes = append(closes, c)
	}
	// reset so a second Close is a no-op and late invoker creations degrade
	p.closes = make(map[int]func() error)
	p.invokers = make(map[int]tg.Invoker)
	p.mu.Unlock()

	if takeoutID != 0 {
		// use a bounded context instead of context.TODO() so shutdown cannot hang
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		err = multierr.Append(err, takeout.UnTakeout(ctx, chainMiddlewares(p.api, takeout.Middleware(takeoutID))))
		cancel()
	}

	for _, c := range closes {
		err = multierr.Append(err, c())
	}

	return err
}

func (p *pool) Takeout(ctx context.Context, dc int) *tg.Client {
	p.mu.Lock()
	takeoutID := p.takeout
	closed := p.closed
	p.mu.Unlock()

	if closed {
		return p.Client(ctx, dc)
	}

	// lazy init
	if takeoutID == 0 {
		p.mu.Lock()
		// double-check after acquiring the lock
		if p.takeout == 0 {
			sid, err := takeout.Takeout(ctx, p.api)
			if err != nil {
				p.mu.Unlock()
				logctx.From(ctx).Warn("takeout error", zap.Error(err))
				// ignore init delay error and return non-takeout client
				return p.Client(ctx, dc)
			}
			p.takeout = sid
			logctx.From(ctx).Info("get takeout id", zap.Int64("id", sid))
		}
		takeoutID = p.takeout
		p.mu.Unlock()
	}

	return tg.NewClient(chainMiddlewares(p.invoker(ctx, dc), takeout.Middleware(takeoutID)))
}
