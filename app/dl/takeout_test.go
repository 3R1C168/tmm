package dl

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTakeoutMode_Valid(t *testing.T) {
	require.True(t, TakeoutAuto.Valid())
	require.True(t, TakeoutAlways.Valid())
	require.True(t, TakeoutNever.Valid())
	require.False(t, TakeoutMode("sometimes").Valid())
	require.False(t, TakeoutMode("").Valid())
}

func TestTakeoutMode_Enabled(t *testing.T) {
	const (
		small = 10 * 1024 * 1024     // 10MB
		large = 100 * 1024 * 1024    // 100MB
		edge  = takeoutAutoThreshold // exactly 50MB
		over  = takeoutAutoThreshold + 1
	)

	// always: any size
	require.True(t, TakeoutAlways.Enabled(0))
	require.True(t, TakeoutAlways.Enabled(small))
	require.True(t, TakeoutAlways.Enabled(large))

	// never: no size
	require.False(t, TakeoutNever.Enabled(0))
	require.False(t, TakeoutNever.Enabled(large))

	// auto: only above threshold
	require.False(t, TakeoutAuto.Enabled(0))
	require.False(t, TakeoutAuto.Enabled(small))
	require.False(t, TakeoutAuto.Enabled(edge), "exactly at threshold is not above it")
	require.True(t, TakeoutAuto.Enabled(over))
	require.True(t, TakeoutAuto.Enabled(large))
}
