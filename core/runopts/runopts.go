// Package runopts defines the runtime tunables shared by the download, upload
// and forward commands. Moving them out of viper global state lets callers run
// multiple tasks concurrently with different settings, which is a prerequisite
// for embedding tdl in a service.
package runopts

import "time"

// Options are the per-run tunables. The zero value is valid and means "use
// defaults"; see Normalize.
type Options struct {
	// PoolSize is the connection pool size per DC. 0 means unlimited.
	PoolSize int
	// ReconnectTimeout bounds reconnection backoff.
	ReconnectTimeout time.Duration
	// Threads is the per-file download/upload thread count.
	Threads int
	// Limit is the max number of concurrent files.
	Limit int
	// Delay is an optional pause between messages.
	Delay time.Duration
	// PartSize is the chunk size for streaming (serve). 0 uses the default.
	PartSize int
	// DisableProgressPS disables the system-stats line in the progress UI.
	DisableProgressPS bool
}

// Default values matching the CLI flag defaults (see cmd).
const (
	DefaultThreads          = 4
	DefaultLimit            = 2
	DefaultPoolSize         = 8
	DefaultReconnectTimeout = 5 * time.Minute
	DefaultPartSize         = 512 * 1024
)

// Normalize fills unset fields with defaults. Note that PoolSize and
// ReconnectTimeout legitimately accept 0 (meaning "unlimited"/"infinite"), so
// they are only defaulted when negative; use the CLI layer to supply explicit
// values when overriding.
func (o Options) Normalize() Options {
	if o.Threads <= 0 {
		o.Threads = DefaultThreads
	}
	if o.Limit <= 0 {
		o.Limit = DefaultLimit
	}
	if o.PoolSize < 0 {
		o.PoolSize = DefaultPoolSize
	}
	if o.ReconnectTimeout < 0 {
		o.ReconnectTimeout = DefaultReconnectTimeout
	}
	if o.PartSize <= 0 {
		o.PartSize = DefaultPartSize
	}
	return o
}
