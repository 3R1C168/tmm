//go:build !windows

package cmd

import "os/exec"

// openBrowser opens url in the default browser without blocking.
func openBrowser(url string) {
	for _, c := range [][]string{{"xdg-open", url}, {"open", url}} {
		if _, err := exec.LookPath(c[0]); err == nil {
			_ = exec.Command(c[0], c[1]).Start()
			return
		}
	}
}
