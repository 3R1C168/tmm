package server

import (
	"context"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/pkg/key"
	pkgclient "github.com/iyear/tdl/pkg/tclient"
)

// qrSession is an in-flight QR login. State changes are pushed to subscribers
// via the server broker as "qr" events.
type qrSession struct {
	id string
	ns string

	mu       sync.Mutex
	state    string // "qr" | "needPassword" | "success" | "error"
	url      string
	expires  time.Time
	err      string
	username string
	userID   int64

	cancel context.CancelFunc
	done   chan struct{}

	// passwordCh carries the 2FA password from the HTTP handler to the flow.
	passwordCh chan string
}

// qrManager tracks in-flight QR sessions by id.
type qrManager struct {
	mu       sync.Mutex
	sessions map[string]*qrSession
}

func newQRManager() *qrManager {
	return &qrManager{sessions: make(map[string]*qrSession)}
}

func (m *qrManager) get(id string) (*qrSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *qrManager) add(s *qrSession) {
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
}

func (m *qrManager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// closeAll cancels all in-flight sessions (used on shutdown). Sessions whose
// login flow is still blocked waiting for user input would otherwise leak the
// underlying client.Run goroutine and MTProto connection forever.
func (m *qrManager) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.cancel != nil {
			s.cancel()
		}
	}
}

func (s *qrSession) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{
		"session": s.id,
		"ns":      s.ns,
		"state":   s.state,
	}
	if s.url != "" {
		out["url"] = s.url
		out["expires"] = s.expires.Unix()
	}
	if s.err != "" {
		out["error"] = s.err
	}
	if s.state == stateSuccess {
		out["userId"] = s.userID
		out["username"] = s.username
	}
	return out
}

func (s *qrSession) set(state, url string, expires time.Time, err error) {
	s.mu.Lock()
	s.state = state
	s.url = url
	s.expires = expires
	if err != nil {
		s.err = err.Error()
	}
	s.mu.Unlock()
}

// startQRLogin launches a QR login flow for ns in the background.
func (srv *Server) startQRLogin(ctx context.Context, ns string) (*qrSession, error) {
	if ns == "" {
		ns = defaultAccount
	}

	kvd, err := srv.stg.Open(ns)
	if err != nil {
		return nil, errors.Wrap(err, "open kv")
	}
	if err := kvd.Set(ctx, key.App(), []byte(pkgclient.AppDesktop)); err != nil {
		return nil, errors.Wrap(err, "set app")
	}

	d := tg.NewUpdateDispatcher()
	client, err := pkgclient.New(ctx, pkgclient.Options{
		KV:            kvd,
		Proxy:         srv.proxy,
		NTP:           srv.ntp,
		UpdateHandler: d,
	}, true)
	if err != nil {
		return nil, errors.Wrap(err, "create client")
	}

	sess := &qrSession{
		id:         newID(),
		ns:         ns,
		state:      "qr",
		done:       make(chan struct{}),
		passwordCh: make(chan string, 1),
	}
	srv.qr.add(sess)

	runCtx, cancel := context.WithCancel(logctx.With(context.Background(),
		logctx.From(ctx).Named("qrlogin").With(zap.String("ns", ns))))
	sess.cancel = cancel

	// TTL: 用户发起登录后若 5 分钟未完成（关闭浏览器/忘记输入），自动取消，
	// 避免 client.Run goroutine 和 MTProto 连接永久泄漏。
	go func() {
		t := time.NewTimer(5 * time.Minute)
		defer t.Stop()
		select {
		case <-sess.done:
		case <-t.C:
			cancel()
		}
	}()

	go func() {
		defer close(sess.done)
		defer srv.qr.remove(sess.id)

		err := client.Run(runCtx, func(ctx context.Context) error {
			_, err := client.QR().Auth(ctx, qrlogin.OnLoginToken(d),
				func(ctx context.Context, token qrlogin.Token) error {
					sess.set("qr", token.URL(), token.Expires(), nil)
					srv.broker.publish("qr", sess.snapshot())
					return nil
				})
			if err != nil {
				if !tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
					return errors.Wrap(err, "qr auth")
				}
				// 2FA required
				sess.set("needPassword", "", time.Time{}, nil)
				srv.broker.publish("qr", sess.snapshot())

				select {
				case <-ctx.Done():
					return ctx.Err()
				case pwd := <-sess.passwordCh:
					if _, err := client.Auth().Password(ctx, pwd); err != nil {
						return errors.Wrap(err, "2fa password")
					}
				}
			}

			user, err := client.Self(ctx)
			if err != nil {
				return errors.Wrap(err, "get self")
			}

			sess.mu.Lock()
			sess.state = stateSuccess
			sess.userID = user.ID
			sess.username = user.Username
			sess.mu.Unlock()
			srv.broker.publish("qr", sess.snapshot())

			// keep the client alive briefly so session is fully persisted
			return nil
		})

		if err != nil && sess.state != stateSuccess {
			sess.set("error", "", time.Time{}, err)
			srv.broker.publish("qr", sess.snapshot())
			logctx.From(runCtx).Warn("qr login failed", zap.Error(err))
		}

		// on success, register the namespace as a live account
		if sess.state == stateSuccess {
			if _, err := srv.accounts.ensure(context.Background(), ns); err != nil {
				logctx.From(runCtx).Warn("register account after login", zap.Error(err))
			}
		}
	}()

	return sess, nil
}

// submitQRPassword feeds a 2FA password into an in-flight QR session.
func (srv *Server) submitQRPassword(id, password string) error {
	sess, ok := srv.qr.get(id)
	if !ok {
		return errors.New("qr session not found or expired")
	}
	select {
	case sess.passwordCh <- password:
		return nil
	default:
		return errors.New("password already submitted")
	}
}
