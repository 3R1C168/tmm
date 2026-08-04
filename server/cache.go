package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"
)

// cacheDir is the on-disk cache layout under the executable directory:
//
//	cache/
//	  meta/     chat & message list JSON (TTL-refreshed)
//	  media/    fully-downloaded preview images / small files
//	  preview/  partial video/file bytes + .tdlstate for resume
type cache struct {
	root string
}

const (
	chatsCacheTTL = 30 * time.Minute
	msgsCacheTTL  = 2 * time.Hour // messages change slowly; long TTL makes re-entry instant
)

func newCache(root string) *cache {
	return &cache{root: root}
}

func (c *cache) metaDir() string    { return filepath.Join(c.root, "meta") }
func (c *cache) mediaDir() string   { return filepath.Join(c.root, "media") }
func (c *cache) previewDir() string { return filepath.Join(c.root, "preview") }

func (c *cache) ensure() error {
	for _, d := range []string{c.metaDir(), c.mediaDir(), c.previewDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return errors.Wrap(err, "create cache dir")
		}
	}
	return nil
}

// clearPreview removes every file under the preview directory (the partial
// streaming chunks produced by on-demand video playback). Media/thumb caches
// are left untouched so already-downloaded files do not need re-fetching. The
// directory itself is recreated so the next stream session can write to it.
// Returns the number of files removed. Used by the "clear preview on exit"
// setting at shutdown.
func (c *cache) clearPreview() (int, error) {
	dir := c.previewDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // nothing to clear
		}
		return 0, errors.Wrap(err, "read preview dir")
	}
	n := 0
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err == nil {
			n++
		}
	}
	return n, nil
}

// --- meta (JSON with timestamp) ---

type metaFile struct {
	FetchedAt time.Time       `json:"fetchedAt"`
	Data      json.RawMessage `json:"data"`
}

func (c *cache) metaPath(name string) string {
	return filepath.Join(c.metaDir(), name+".json")
}

// readMeta loads a cached JSON payload into out. fresh reports whether it is
// within ttl. A missing/corrupt cache is treated as a miss (fresh=false, nil error).
func (c *cache) readMeta(name string, ttl time.Duration, out any) (fresh bool, err error) {
	b, err := os.ReadFile(c.metaPath(name))
	if err != nil {
		return false, nil
	}
	var mf metaFile
	if err := json.Unmarshal(b, &mf); err != nil {
		return false, nil
	}
	if err := json.Unmarshal(mf.Data, out); err != nil {
		return false, nil
	}
	return time.Since(mf.FetchedAt) < ttl, nil
}

func (c *cache) writeMeta(name string, data any) error {
	if err := c.ensure(); err != nil {
		return err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return errors.Wrap(err, "marshal meta data")
	}
	mf := metaFile{FetchedAt: time.Now(), Data: raw}
	b, err := json.Marshal(mf)
	if err != nil {
		return errors.Wrap(err, "marshal meta")
	}
	tmp := c.metaPath(name) + tmpSuffix
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return errors.Wrap(err, "write meta")
	}
	return os.Rename(tmp, c.metaPath(name))
}

// --- media (full preview files) ---

// mediaPath returns the cache path for a fully-downloaded preview file.
func (c *cache) mediaPath(peerID int64, msgID int, ext string) string {
	if ext == "" {
		ext = ".bin"
	}
	name := strconv.FormatInt(peerID, 10) + "_" + strconv.Itoa(msgID) + ext
	return filepath.Join(c.mediaDir(), name)
}

// mediaExists returns the path if the preview file is already cached.
func (c *cache) mediaExists(peerID int64, msgID int, ext string) (string, bool) {
	p := c.mediaPath(peerID, msgID, ext)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p, true
	}
	return "", false
}

// thumbPath returns the cache path for a message's small thumbnail.
func (c *cache) thumbPath(peerID int64, msgID int) string {
	name := strconv.FormatInt(peerID, 10) + "_" + strconv.Itoa(msgID) + ".thumb.jpg"
	return filepath.Join(c.mediaDir(), name)
}

// thumbExists returns the path if the thumbnail is already cached.
func (c *cache) thumbExists(peerID int64, msgID int) (string, bool) {
	p := c.thumbPath(peerID, msgID)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p, true
	}
	return "", false
}

// evictLRU trims the cache (media + preview) to maxBytes by deleting the
// oldest files (by modification time) first. Preview partials (.tmp) and their
// control files (.tdlstate) count toward the cap too — a half-watched 2GB
// video is exactly what blows the cache up. Deleting a .tmp also deletes its
// .tdlstate (and vice versa) so a file never outlives its own progress record.
// It runs best-effort and logs via logf.
func (c *cache) evictLRU(maxBytes int64, logf func(msg string, args ...any)) {
	if maxBytes <= 0 {
		return
	}
	type fi struct {
		path string
		size int64
		mod  int64
	}
	var files []fi
	var total int64
	for _, dir := range []string{c.mediaDir(), c.previewDir()} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			files = append(files, fi{filepath.Join(dir, e.Name()), info.Size(), info.ModTime().Unix()})
			total += info.Size()
		}
	}
	if total <= maxBytes {
		return
	}
	// sort oldest first
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	removed := int64(0)
	count := 0
	for _, f := range files {
		if total <= maxBytes {
			break
		}
		if err := os.Remove(f.path); err == nil {
			total -= f.size
			removed += f.size
			count++
			// keep the partial and its control file in lockstep
			var sibling string
			if strings.HasSuffix(f.path, tmpSuffix) {
				sibling = f.path + ".tdlstate"
			} else if strings.HasSuffix(f.path, ".tdlstate") {
				sibling = strings.TrimSuffix(f.path, ".tdlstate")
			}
			if sibling != "" {
				if si, err := os.Stat(sibling); err == nil {
					if os.Remove(sibling) == nil {
						total -= si.Size()
						removed += si.Size()
					}
				}
			}
		}
	}
	if count > 0 && logf != nil {
		logf("cache evicted %d files, freed %d bytes", count, removed)
	}
}

// --- preview (partial video/file with .tdlstate) ---

// previewDataPath returns the partial-data path used for streaming a file so
// that a later download resumes from the bytes already fetched. The matching
// control file is downloader.StatePath(previewDataPath).
func (c *cache) previewDataPath(peerID int64, msgID int) string {
	name := strconv.FormatInt(peerID, 10) + "_" + strconv.Itoa(msgID) + tmpSuffix
	return filepath.Join(c.previewDir(), name)
}
