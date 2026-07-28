package runopts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalize_Defaults(t *testing.T) {
	o := Options{}.Normalize()
	require.Equal(t, DefaultThreads, o.Threads)
	require.Equal(t, DefaultLimit, o.Limit)
	require.Equal(t, DefaultPartSize, o.PartSize)
	// zero ReconnectTimeout is a valid explicit value (infinite), kept as-is
	require.Equal(t, time.Duration(0), o.ReconnectTimeout)
}

func TestNormalize_PreservesExplicit(t *testing.T) {
	o := Options{
		Threads:          16,
		Limit:            8,
		Delay:            time.Second,
		ReconnectTimeout: time.Minute,
	}.Normalize()
	require.Equal(t, 16, o.Threads)
	require.Equal(t, 8, o.Limit)
	require.Equal(t, time.Second, o.Delay)
	require.Equal(t, time.Minute, o.ReconnectTimeout)
}

// PoolSize and ReconnectTimeout legitimately accept 0 (unlimited/infinite), so
// Normalize must not clobber an explicit 0; only a negative value is treated as
// "unset" and defaulted.
func TestNormalize_ZeroPoolKept(t *testing.T) {
	o := Options{PoolSize: 0}.Normalize()
	require.Equal(t, 0, o.PoolSize)

	o = Options{PoolSize: -1}.Normalize()
	require.Equal(t, DefaultPoolSize, o.PoolSize)

	o = Options{ReconnectTimeout: -time.Second}.Normalize()
	require.Equal(t, DefaultReconnectTimeout, o.ReconnectTimeout)
}
