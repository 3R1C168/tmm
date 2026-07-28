package dl

// TakeoutMode controls when a takeout session is used for downloading. Takeout
// sessions have much lower flood-wait limits, so they greatly reduce
// interruptions on large files, but they surface a "data export" notification
// on the account's other devices.
type TakeoutMode string

const (
	// TakeoutAuto uses takeout only for files larger than takeoutAutoThreshold.
	// This is the default: large files avoid flood-wait interruptions while
	// small files (which would not hit flood limits anyway) stay on the normal
	// path and do not trigger the export notification.
	TakeoutAuto TakeoutMode = "auto"
	// TakeoutAlways forces takeout for every file (e.g. restricted content).
	TakeoutAlways TakeoutMode = "always"
	// TakeoutNever disables takeout entirely.
	TakeoutNever TakeoutMode = "never"
)

// takeoutAutoThreshold is the file size above which auto mode uses takeout.
// Flood limits throttle sustained per-file request rates, so the threshold is
// size-based rather than count-based.
const takeoutAutoThreshold = 50 * 1024 * 1024 // 50MB

// Valid reports whether m is a recognized takeout mode.
func (m TakeoutMode) Valid() bool {
	switch m {
	case TakeoutAuto, TakeoutAlways, TakeoutNever:
		return true
	}
	return false
}

// Enabled reports whether takeout should be used for a file of the given size.
func (m TakeoutMode) Enabled(size int64) bool {
	switch m {
	case TakeoutAlways:
		return true
	case TakeoutAuto:
		return size > takeoutAutoThreshold
	default: // TakeoutNever
		return false
	}
}
