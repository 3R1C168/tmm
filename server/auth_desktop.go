package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-faster/errors"
	"github.com/gotd/td/session"
	tdtdesktop "github.com/gotd/td/session/tdesktop"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/util/fsutil"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/key"
	pkgclient "github.com/iyear/tdl/pkg/tclient"
	"github.com/iyear/tdl/pkg/tdesktop"
	"github.com/iyear/tdl/pkg/tpath"
)

const tdataDir = "tdata"

// desktopAccount is one account discovered in a Telegram Desktop tdata dir.
type desktopAccount struct {
	UserID uint64 `json:"userId"`
	Idx    uint32 `json:"-"`
}

// desktopProbe reads a tdata directory and returns the accounts found. The
// session bytes never leave the backend; confirmDesktopImport re-reads from
// disk when the user picks an account.
type desktopProbe struct {
	path     string
	passcode string
	accounts []desktopAccount
}

// probeDesktop locates and reads a Telegram Desktop tdata dir. If path is
// empty the standard locations are auto-detected.
func probeDesktop(path, passcode string) (*desktopProbe, error) {
	desktop, err := findDesktopDir(path)
	if err != nil {
		return nil, err
	}

	accounts, err := tdtdesktop.Read(appendTDataDir(desktop), []byte(passcode))
	if err != nil {
		return nil, errors.Wrap(err, "read tdata (wrong passcode or not a Telegram Desktop data dir)")
	}
	if len(accounts) == 0 {
		return nil, errors.New("no accounts found in tdata")
	}

	p := &desktopProbe{path: desktop, passcode: passcode}
	for _, acc := range accounts {
		p.accounts = append(p.accounts, desktopAccount{
			UserID: acc.Authorization.UserID,
			Idx:    acc.IDx,
		})
	}
	return p, nil
}

// confirmDesktopImport imports the chosen account's session into ns.
func (srv *Server) confirmDesktopImport(ctx context.Context, ns, path, passcode string, userID uint64, logout bool) error {
	if ns == "" {
		ns = "default"
	}

	desktop, err := findDesktopDir(path)
	if err != nil {
		return err
	}

	accounts, err := tdtdesktop.Read(appendTDataDir(desktop), []byte(passcode))
	if err != nil {
		return errors.Wrap(err, "read tdata")
	}

	var chosen *tdtdesktop.Account
	for i := range accounts {
		if accounts[i].Authorization.UserID == userID {
			chosen = &accounts[i]
			break
		}
	}
	if chosen == nil {
		return errors.Errorf("account %d not found in tdata", userID)
	}

	kvd, err := srv.stg.Open(ns)
	if err != nil {
		return errors.Wrap(err, "open kv")
	}

	data, err := session.TDesktopSession(*chosen)
	if err != nil {
		return errors.Wrap(err, "convert session")
	}

	loader := &session.Loader{Storage: storage.NewSession(kvd, true)}
	if err := loader.Save(ctx, data); err != nil {
		return errors.Wrap(err, "save session")
	}

	if err := kvd.Set(ctx, key.App(), []byte(pkgclient.AppDesktop)); err != nil {
		return errors.Wrap(err, "set app")
	}

	if logout {
		if err := forceLogoutDesktop(chosen.IDx, desktop); err != nil {
			return errors.Wrap(err, "logout desktop session")
		}
	}

	// register the namespace as a live account
	if _, err := srv.accounts.ensure(ctx, ns); err != nil {
		return errors.Wrap(err, "register account")
	}
	return nil
}

func findDesktopDir(desktop string) (string, error) {
	if desktop == "" { // auto detect
		if desktop = detectAppDataDir(); desktop == "" {
			return "", fmt.Errorf("no Telegram Desktop data found in standard paths; please provide the tdata path")
		}
		return desktop, nil
	}

	stat, err := os.Stat(desktop)
	if err != nil {
		return "", err
	}
	if !stat.IsDir() { // a path to the executable; use its directory
		desktop = filepath.Dir(desktop)
	}
	return desktop, nil
}

func detectAppDataDir() string {
	for _, p := range tpath.Desktop.AppData(consts.HomeDir) {
		if path := appendTDataDir(p); fsutil.PathExists(path) {
			return path
		}
	}
	return ""
}

func appendTDataDir(path string) string {
	if filepath.Base(path) != tdataDir {
		path = filepath.Join(path, tdataDir)
	}
	return path
}

// forceLogoutDesktop removes the local session key for the account so the
// desktop client must re-login, preventing session conflict.
func forceLogoutDesktop(idx uint32, desktop string) error {
	dir := "data"
	if idx > 0 {
		dir = fmt.Sprintf("data#%d", idx+1)
	}
	return os.RemoveAll(filepath.Join(appendTDataDir(desktop), tdesktop.FileKey(dir)))
}
