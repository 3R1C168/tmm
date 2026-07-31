package server

import (
	"os"
	"path/filepath"
)

// defaultWebRoot locates the built frontend relative to the executable so the
// service works when double-clicked (cwd is unreliable on Windows).
func defaultWebRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return "web"
	}
	return filepath.Join(filepath.Dir(exe), "web")
}

// defaultDownloadDir places downloads in a "downloads" folder next to the exe.
func defaultDownloadDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "downloads"
	}
	return filepath.Join(filepath.Dir(exe), "downloads")
}

// cacheRoot places the cache in a "cache" folder next to the exe.
func cacheRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return "cache"
	}
	return filepath.Join(filepath.Dir(exe), "cache")
}

// settingsPath locates settings.json next to the exe.
func settingsPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "settings.json"
	}
	return filepath.Join(filepath.Dir(exe), "settings.json")
}
