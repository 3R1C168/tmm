package consts

import (
	"os"
	"path/filepath"
)

// Portable layout: everything the app needs at runtime lives next to the
// executable, so the whole folder can be copied anywhere and just work.
//
//	<exe dir>/
//	  tmm.exe
//	  data/           ← DataDir (account session, peers, tasks, logs)
//	    data.kv       ← login session / auth keys
//	    data/         ← peers cache
//	    log/
//	    extensions/
//	  cache/          ← media/thumb/preview cache (safe to wipe)
//	  web/
//
// TDL_DATA_DIR env var overrides the data dir (useful for the CLI when you
// really want the old ~/.tdl location). Falls back to ~/.tdl only if the
// executable's directory can't be determined.
func init() {
	dir := portableDataDir()

	HomeDir, _ = os.UserHomeDir()
	DataDir = dir
	LogPath = filepath.Join(DataDir, "log")
	ExtensionsPath = filepath.Join(DataDir, "extensions")
	ExtensionsDataPath = filepath.Join(ExtensionsPath, "data")

	for _, p := range []string{DataDir, ExtensionsPath, ExtensionsDataPath} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			panic(err)
		}
	}
}

// portableDataDir resolves the runtime data directory.
func portableDataDir() string {
	// explicit override wins
	if env := os.Getenv("TDL_DATA_DIR"); env != "" {
		return env
	}
	// portable: <exe dir>/data
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "data")
	}
	// last resort: legacy home location
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".tdl")
	}
	return ".tdl"
}
