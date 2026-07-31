package server

import (
	"context"
	"sync"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/pkg/key"
	pkgclient "github.com/iyear/tdl/pkg/tclient"
)

// noSignUp prevents account registration through the service.
type noSignUp struct{}

func (noSignUp) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("don't support sign up Telegram account")
}

func (noSignUp) AcceptTermsOfService(_ context.Context, tos tg.HelpTermsOfService) error {
	return &auth.SignUpRequired{TermsOfService: tos}
}

// codeSession is an in-flight phone/code login. The webAuth implementation
// blocks on channels that HTTP handlers feed.
type codeSession struct {
	id string
	ns string

	mu       sync.Mutex
	state    string // "needPhone" | "needCode" | "needPassword" | "success" | "error"
	err      string
	username string
	userID   int64

	cancel context.CancelFunc
	done   chan struct{}

	phoneCh    chan string
	codeCh     chan string
	passwordCh chan string
}

type codeManager struct {
	mu       sync.Mutex
	sessions map[string]*codeSession
}

func newCodeManager() *codeManager {
	return &codeManager{sessions: make(map[string]*codeSession)}
}

func (m *codeManager) get(id string) (*codeSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *codeManager) add(s *codeSession) {
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
}

func (m *codeManager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (s *codeSession) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{
		"session": s.id,
		"ns":      s.ns,
		"state":   s.state,
	}
	if s.err != "" {
		out["error"] = s.err
	}
	if s.state == "success" {
		out["userId"] = s.userID
		out["username"] = s.username
	}
	return out
}

func (s *codeSession) set(state string, err error) {
	s.mu.Lock()
	s.state = state
	if err != nil {
		s.err = err.Error()
	}
	s.mu.Unlock()
}

// webAuth implements auth.UserAuthenticator by blocking until the web frontend
// supplies each value over HTTP.
type webAuth struct {
	noSignUp
	sess *codeSession
	srv  *Server
}

func (a webAuth) Phone(ctx context.Context) (string, error) {
	a.sess.set("needPhone", nil)
	a.srv.broker.publish("code", a.sess.snapshot())
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case p := <-a.sess.phoneCh:
		return p, nil
	}
}

func (a webAuth) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	a.sess.set("needCode", nil)
	a.srv.broker.publish("code", a.sess.snapshot())
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case c := <-a.sess.codeCh:
		return c, nil
	}
}

func (a webAuth) Password(ctx context.Context) (string, error) {
	a.sess.set("needPassword", nil)
	a.srv.broker.publish("code", a.sess.snapshot())
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case p := <-a.sess.passwordCh:
		return p, nil
	}
}

// startCodeLogin launches a phone/code login flow for ns.
func (srv *Server) startCodeLogin(ctx context.Context, ns string) (*codeSession, error) {
	if ns == "" {
		ns = "default"
	}

	kvd, err := srv.stg.Open(ns)
	if err != nil {
		return nil, errors.Wrap(err, "open kv")
	}
	if err := kvd.Set(ctx, key.App(), []byte(pkgclient.AppDesktop)); err != nil {
		return nil, errors.Wrap(err, "set app")
	}

	client, err := pkgclient.New(ctx, pkgclient.Options{
		KV:    kvd,
		Proxy: srv.proxy,
		NTP:   srv.ntp,
	}, true)
	if err != nil {
		return nil, errors.Wrap(err, "create client")
	}

	sess := &codeSession{
		id:         newID(),
		ns:         ns,
		state:      "needPhone",
		done:       make(chan struct{}),
		phoneCh:    make(chan string, 1),
		codeCh:     make(chan string, 1),
		passwordCh: make(chan string, 1),
	}
	srv.code.add(sess)

	runCtx, cancel := context.WithCancel(logctx.With(context.Background(),
		logctx.From(ctx).Named("codelogin").With(zap.String("ns", ns))))
	sess.cancel = cancel

	go func() {
		defer close(sess.done)
		defer srv.code.remove(sess.id)

		err := client.Run(runCtx, func(ctx context.Context) error {
			if err := client.Ping(ctx); err != nil {
				return err
			}
			flow := auth.NewFlow(webAuth{sess: sess, srv: srv}, auth.SendCodeOptions{})
			if err := client.Auth().IfNecessary(ctx, flow); err != nil {
				return err
			}

			user, err := client.Self(ctx)
			if err != nil {
				return errors.Wrap(err, "get self")
			}
			sess.mu.Lock()
			sess.state = "success"
			sess.userID = user.ID
			sess.username = user.Username
			sess.mu.Unlock()
			srv.broker.publish("code", sess.snapshot())
			return nil
		})

		if err != nil && sess.state != "success" {
			sess.set("error", err)
			srv.broker.publish("code", sess.snapshot())
			logctx.From(runCtx).Warn("code login failed", zap.Error(err))
		}

		if sess.state == "success" {
			if _, err := srv.accounts.ensure(context.Background(), ns); err != nil {
				logctx.From(runCtx).Warn("register account after login", zap.Error(err))
			}
		}
	}()

	return sess, nil
}

// codeInput feeds one of phone/code/password into an in-flight session. The
// field is chosen by the session's current state.
func (srv *Server) codeInput(id, value string) error {
	sess, ok := srv.code.get(id)
	if !ok {
		return errors.New("code session not found or expired")
	}

	sess.mu.Lock()
	state := sess.state
	sess.mu.Unlock()

	var ch chan string
	switch state {
	case "needPhone":
		ch = sess.phoneCh
	case "needCode":
		ch = sess.codeCh
	case "needPassword":
		ch = sess.passwordCh
	default:
		return errors.Errorf("session is not waiting for input (state=%s)", state)
	}

	select {
	case ch <- value:
		return nil
	default:
		return errors.New("input already submitted")
	}
}
