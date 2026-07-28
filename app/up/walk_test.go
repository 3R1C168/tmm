package up

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWalk_ThumbNameEndsWithExtChar is a regression test: the thumbnail path
// was computed with strings.TrimRight, which treats the extension as a cutset
// and over-trims file names whose base ends with an extension character
// (e.g. "report.txt" -> "repor.thumb" instead of "report.thumb").
func TestWalk_ThumbNameEndsWithExtChar(t *testing.T) {
	dir := t.TempDir()

	video := filepath.Join(dir, "report.txt")
	require.NoError(t, os.WriteFile(video, []byte("data"), 0o644))

	thumb := filepath.Join(dir, "report.thumb")
	require.NoError(t, os.WriteFile(thumb, []byte("thumb"), 0o644))

	files, err := walk([]string{dir}, nil, nil)
	require.NoError(t, err)
	require.Len(t, files, 1)

	require.Equal(t, video, files[0].File)
	require.Equal(t, thumb, files[0].Thumb, "thumbnail must be detected for names ending with ext chars")
}
