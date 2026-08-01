package dl

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/go-faster/errors"

	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/util/fsutil"
)

// Iter is the subset of the download iterator that a non-CLI caller (e.g. the
// service backend) needs to observe and control resume/finished state. It is
// handed to Options.IterHook right before the download starts.
type Iter interface {
	// Fingerprint identifies the (peer, message) set being downloaded; it keys
	// the resume record in KV.
	Fingerprint() string
	// Finish marks a message as downloaded (order-independent coordinate).
	Finish(k MsgKey)
	// Finished returns a copy of the finished set.
	Finished() map[MsgKey]struct{}
	// Total is the number of messages requested (before skipping).
	Total() int
	// SkippedDeleted is how many messages were skipped because they were deleted.
	SkippedDeleted() int64
	// DeletedIDs lists "dialogID/messageID" strings for deleted messages.
	DeletedIDs() []string
}

// Elem is the exported view of a single download element. downloader.Elem
// values passed to Progress callbacks can be type-asserted to this interface
// to expose file/source metadata for UI display and finalization.
type Elem interface {
	downloader.Elem
	ID() int
	Key() MsgKey
	FromID() int64
	FromName() string
	MsgID() int
	// Caption returns the source message text, empty when none.
	Caption() string
	ToPath() string
	FileDate() int64
	// Name is the original media file name.
	Name() string
}

// FinalizeElem performs the post-download work for one element. It always
// closes the underlying file. When derr is non-nil the partial file and its
// resume control file are kept (flushed) so the download can resume later.
// When derr is nil the element is marked finished, the control file removed,
// and the temporary file renamed to its final name (honoring RewriteExt and
// message-date chtimes).
//
// The terminal progress UI calls this from its OnDone; a service backend that
// supplies its own downloader.Progress must call it too (or the temp files are
// never finalized). It returns the final file path on success.
func FinalizeElem(elem Elem, opts Options, it Iter, derr error) (finalPath string, err error) {
	if cerr := elem.To().(interface{ Close() error }).Close(); cerr != nil {
		return "", errors.Wrap(cerr, "close file")
	}

	if derr != nil {
		// keep the partial file and its control file so the download can be
		// resumed later; flush whatever pieces were written
		if st := elem.Resume(); st != nil {
			_ = st.Flush()
		}
		return "", derr
	}

	if it != nil {
		it.Finish(elem.Key())
	}

	// download finished: drop the control file before renaming the data file
	if st := elem.Resume(); st != nil {
		_ = st.Remove()
	}

	finalPath, err = donePost(elem, opts)
	if err != nil {
		return "", errors.Wrap(err, "post file")
	}
	return finalPath, nil
}

// donePost renames the finished temp file to its final name, optionally
// rewriting the extension from the detected mime type and setting the file
// mtime to the message date.
func donePost(elem Elem, opts Options) (string, error) {
	tmpPath := elem.ToPath()
	newfile := strings.TrimSuffix(filepath.Base(tmpPath), tempExt)

	if opts.RewriteExt {
		mime, err := mimetype.DetectFile(tmpPath)
		if err != nil {
			return "", errors.Wrap(err, "detect mime")
		}
		ext := mime.Extension()
		if ext != "" && (filepath.Ext(newfile) != ext) {
			newfile = fsutil.GetNameWithoutExt(newfile) + ext
		}
	}

	newpath := filepath.Join(filepath.Dir(tmpPath), newfile)
	if err := os.Rename(tmpPath, newpath); err != nil {
		return "", errors.Wrap(err, "rename file")
	}

	// Set file modification time to message date if available
	if d := elem.FileDate(); d > 0 {
		fileTime := time.Unix(d, 0)
		if err := os.Chtimes(newpath, fileTime, fileTime); err != nil {
			return "", errors.Wrap(err, "set file time")
		}
	}

	return newpath, nil
}

// compile-time assertion that the concrete iterator satisfies the exported
// view handed to IterHook.
var _ Iter = (*iter)(nil)
