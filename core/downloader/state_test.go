package downloader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const testPart = 4 // tiny part size to exercise many pieces

func TestState_MarkMissing_OutOfOrder(t *testing.T) {
	// 10 bytes / 4 per piece => pieces: [0:0-4] [1:4-8] [2:8-10]
	s := NewState(StatePath(filepath.Join(t.TempDir(), "f.tmp")), 10, testPart)

	require.False(t, s.Complete())
	require.Len(t, s.Missing(), 3)

	// write piece 1 (out of order), leaving a hole at piece 0
	s.Mark(4, 4)
	require.False(t, s.Complete())

	missing := s.Missing()
	require.Len(t, missing, 2)
	require.Equal(t, pieceRange{Off: 0, End: 4}, missing[0])
	require.Equal(t, pieceRange{Off: 8, End: 10}, missing[1]) // last piece is short

	// fill the holes
	s.Mark(0, 4)
	s.Mark(8, 2)
	require.True(t, s.Complete())
	require.Empty(t, s.Missing())
}

func TestState_Mark_Idempotent(t *testing.T) {
	s := NewState(StatePath(filepath.Join(t.TempDir(), "f.tmp")), 8, testPart)
	s.Mark(0, 4)
	s.Mark(0, 4) // duplicate write must not double-count
	require.Equal(t, 1, s.done)
	require.False(t, s.Complete())
}

func TestState_FlushAndLoad_RoundTrip(t *testing.T) {
	data := filepath.Join(t.TempDir(), "f.tmp")
	require.NoError(t, os.WriteFile(data, make([]byte, 10), 0o644))

	s := NewState(StatePath(data), 10, testPart)
	s.Mark(0, 4)
	s.Mark(8, 2)
	require.NoError(t, s.Flush())

	loaded, err := LoadState(data, 10, testPart)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	require.Equal(t, s.done, loaded.done)
	require.Equal(t, s.Missing(), loaded.Missing())
	require.False(t, loaded.Complete())
}

func TestLoadState_NoFile(t *testing.T) {
	data := filepath.Join(t.TempDir(), "f.tmp")
	s, err := LoadState(data, 10, testPart)
	require.NoError(t, err)
	require.Nil(t, s, "missing control file must yield nil state")
}

func TestLoadState_SizeMismatch(t *testing.T) {
	data := filepath.Join(t.TempDir(), "f.tmp")
	s := NewState(StatePath(data), 10, testPart)
	s.Mark(0, 4)
	require.NoError(t, s.Flush())

	// the media size changed (e.g. different file) => state is stale
	_, err := LoadState(data, 99, testPart)
	require.Error(t, err)
}

func TestLoadState_Corrupt(t *testing.T) {
	data := filepath.Join(t.TempDir(), "f.tmp")
	require.NoError(t, os.WriteFile(StatePath(data), []byte("{not json"), 0o644))

	_, err := LoadState(data, 10, testPart)
	require.Error(t, err)
}

func TestState_Remove(t *testing.T) {
	data := filepath.Join(t.TempDir(), "f.tmp")
	s := NewState(StatePath(data), 10, testPart)
	s.Mark(0, 4)
	require.NoError(t, s.Flush())

	require.NoError(t, s.Remove())
	_, err := os.Stat(StatePath(data))
	require.True(t, os.IsNotExist(err))

	// removing again is a no-op
	require.NoError(t, s.Remove())
}

func TestState_Mark_SubPiecePartial(t *testing.T) {
	// piece size 4: writing only half a piece must NOT complete it
	s := NewState(StatePath(filepath.Join(t.TempDir(), "f.tmp")), 8, testPart)

	s.Mark(0, 2) // first half of piece 0
	require.False(t, s.Complete())
	require.Len(t, s.Missing(), 2, "half-written piece must stay missing")

	s.Mark(2, 2) // second half completes piece 0
	missing := s.Missing()
	require.Len(t, missing, 1)
	require.Equal(t, pieceRange{Off: 4, End: 8}, missing[0])
}

func TestState_Mark_SubPieceOutOfOrder(t *testing.T) {
	s := NewState(StatePath(filepath.Join(t.TempDir(), "f.tmp")), 8, testPart)

	// write the second half first, then the first half
	s.Mark(2, 2)
	s.Mark(6, 2) // half of piece 1
	s.Mark(0, 2)
	s.Mark(4, 2)
	require.True(t, s.Complete())
	require.Empty(t, s.Missing())
}

func TestState_Mark_SpanningWrite(t *testing.T) {
	// one write covering a boundary between pieces
	s := NewState(StatePath(filepath.Join(t.TempDir(), "f.tmp")), 8, testPart)
	s.Mark(2, 4) // covers piece0[2-4) + piece1[0-2)
	require.Empty(t, s.Missing()[0:0])
	require.Len(t, s.Missing(), 2)

	s.Mark(0, 2)
	s.Mark(6, 2)
	require.True(t, s.Complete())
}

func TestState_FlushAtomic_NoLeftoverTmp(t *testing.T) {
	data := filepath.Join(t.TempDir(), "f.tmp")
	s := NewState(StatePath(data), 8, testPart)
	s.Mark(0, 4)
	require.NoError(t, s.Flush())

	_, err := os.Stat(StatePath(data) + ".tmp")
	require.True(t, os.IsNotExist(err), "temp file must be renamed away")
}
