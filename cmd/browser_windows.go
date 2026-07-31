//go:build windows

package cmd

import (
	"os/exec"
	"syscall"
)

// openBrowser opens url in the default browser without blocking.
func openBrowser(url string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}
