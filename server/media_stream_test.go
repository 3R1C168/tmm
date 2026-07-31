package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRange_NoHeader(t *testing.T) {
	start, end, partial, ok := parseRange("", 1000)
	require.True(t, ok)
	require.False(t, partial, "no Range header is a full 200, not 206")
	require.Equal(t, int64(0), start)
	require.Equal(t, int64(999), end, "no header serves the FULL file (streamed)")
}

func TestParseRange_OpenEnded(t *testing.T) {
	// Chrome's typical first probe
	start, end, partial, ok := parseRange("bytes=0-", 1000)
	require.True(t, ok)
	require.True(t, partial)
	require.Equal(t, int64(0), start)
	require.Equal(t, int64(999), end)
}

func TestParseRange_Explicit(t *testing.T) {
	start, end, partial, ok := parseRange("bytes=100-199", 1000)
	require.True(t, ok)
	require.True(t, partial)
	require.Equal(t, int64(100), start)
	require.Equal(t, int64(199), end)
}

func TestParseRange_EndClamped(t *testing.T) {
	start, end, _, ok := parseRange("bytes=900-9999", 1000)
	require.True(t, ok)
	require.Equal(t, int64(900), start)
	require.Equal(t, int64(999), end, "end clamps to size-1")
}

func TestParseRange_Suffix(t *testing.T) {
	// how a browser fetches moov-at-tail: last 200 bytes
	start, end, partial, ok := parseRange("bytes=-200", 1000)
	require.True(t, ok)
	require.True(t, partial)
	require.Equal(t, int64(800), start)
	require.Equal(t, int64(999), end)
}

func TestParseRange_SuffixLargerThanFile(t *testing.T) {
	start, end, _, ok := parseRange("bytes=-5000", 1000)
	require.True(t, ok)
	require.Equal(t, int64(0), start, "suffix larger than the file means the whole file")
	require.Equal(t, int64(999), end)
}

func TestParseRange_StartBeyondSize(t *testing.T) {
	_, _, _, ok := parseRange("bytes=1000-", 1000)
	require.False(t, ok, "start >= size is unsatisfiable -> 416")
}

func TestParseRange_StartAfterEnd(t *testing.T) {
	_, _, _, ok := parseRange("bytes=200-100", 1000)
	require.False(t, ok, "start > end is unsatisfiable -> 416")
}

func TestParseRange_MultiRangeRejected(t *testing.T) {
	_, _, _, ok := parseRange("bytes=0-100,200-300", 1000)
	require.False(t, ok, "multi-range is not supported")
}

func TestParseRange_Garbage(t *testing.T) {
	for _, h := range []string{"items=0-1", "bytes=", "bytes=abc-def", "bytes=5", "bytes=-0"} {
		_, _, _, ok := parseRange(h, 1000)
		require.False(t, ok, "header %q must be rejected", h)
	}
}

func TestParseRange_ZeroSize(t *testing.T) {
	_, _, _, ok := parseRange("bytes=0-", 0)
	require.False(t, ok, "empty resource is unsatisfiable")
}
