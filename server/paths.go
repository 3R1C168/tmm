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

// settingsPath locates settings.json inside the data directory (next to the
// exe's data/ folder), alongside the account session databases.
func settingsPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "settings.json"
	}
	return filepath.Join(filepath.Dir(exe), "data", "settings.json")
}

// legacySettingsPath is the pre-2026-08-03 location of settings.json (next to
// the exe, not in data/). Kept so the new location can migrate existing
// settings once on first run instead of losing them.
func legacySettingsPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "settings.json"
	}
	return filepath.Join(filepath.Dir(exe), "settings.json")
}
