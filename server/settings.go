package server

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/go-faster/errors"
)

// settings holds user-tunable service preferences, persisted to settings.json
// next to the executable.
type settings struct {
	mu sync.RWMutex

	// PreviewTakeout enables the takeout channel for large-file previews.
	// Takeout has much higher flood limits (faster streaming) but surfaces a
	// "data export" notification on the account's other devices.
	PreviewTakeout bool `json:"previewTakeout"`
	// TakeoutMinMB is the file size (MiB) above which previews use takeout when
	// PreviewTakeout is on.
	TakeoutMinMB int `json:"takeoutMinMB"`
	// CacheMaxMB caps the on-disk media/thumb cache (LRU-evicted when exceeded).
	CacheMaxMB int `json:"cacheMaxMB"`
	// ClearPreviewOnExit wipes the streaming preview cache (cache/preview, the
	// partial chunk files produced by on-demand video streaming) when the
	// service shuts down. Media/thumb caches are kept so re-opened files need
	// not re-download. Preview files are cheap to discard: they only save the
	// seek-ahead buffering of a video you were watching, which is stale next run.
	ClearPreviewOnExit bool `json:"clearPreviewOnExit"`

	path string
}

func defaultSettings(path string) *settings {
	return &settings{
		// default off: benchmarks showed the takeout channel is slower than the
		// normal channel on this DC for streaming, and it triggers a "data
		// export" notification. Users can opt in via the settings page.
		PreviewTakeout: false,
		TakeoutMinMB:   50,
		CacheMaxMB:     5120, // 5 GiB default cache
		// default on: preview chunks are streaming by-products that go stale on
		// exit; clearing them keeps the cache folder from growing unbounded.
		ClearPreviewOnExit: true,
		path:               path,
	}
}

func (st *settings) load() error {
	b, err := os.ReadFile(st.path)
	if err != nil {
		return nil // missing file -> defaults
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	var a struct {
		PreviewTakeout     bool  `json:"previewTakeout"`
		TakeoutMinMB       int   `json:"takeoutMinMB"`
		CacheMaxMB         int   `json:"cacheMaxMB"`
		ClearPreviewOnExit *bool `json:"clearPreviewOnExit"`
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return errors.Wrap(err, "parse settings")
	}
	st.PreviewTakeout = a.PreviewTakeout
	st.TakeoutMinMB = a.TakeoutMinMB
	st.CacheMaxMB = a.CacheMaxMB
	if a.ClearPreviewOnExit != nil {
		st.ClearPreviewOnExit = *a.ClearPreviewOnExit
	}
	if st.TakeoutMinMB <= 0 {
		st.TakeoutMinMB = 50
	}
	if st.CacheMaxMB <= 0 {
		st.CacheMaxMB = 5120
	}
	return nil
}

func (st *settings) save() error {
	st.mu.RLock()
	payload := map[string]any{
		"previewTakeout":     st.PreviewTakeout,
		"takeoutMinMB":       st.TakeoutMinMB,
		"cacheMaxMB":         st.CacheMaxMB,
		"clearPreviewOnExit": st.ClearPreviewOnExit,
	}
	st.mu.RUnlock()
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// useTakeoutFor reports whether a file of size bytes should use the takeout
// channel for preview/streaming, per current settings.
func (st *settings) useTakeoutFor(sizeBytes int64) bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if !st.PreviewTakeout {
		return false
	}
	return sizeBytes > int64(st.TakeoutMinMB)*1024*1024
}

// cacheMaxBytes returns the cache cap in bytes.
func (st *settings) cacheMaxBytes() int64 {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return int64(st.CacheMaxMB) * 1024 * 1024
}

// clearPreviewOnExit reports whether the preview cache should be wiped on
// shutdown.
func (st *settings) clearPreviewOnExit() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.ClearPreviewOnExit
}

// snapshot returns the settings as a JSON-friendly map.
func (st *settings) snapshot() map[string]any {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return map[string]any{
		"previewTakeout":     st.PreviewTakeout,
		"takeoutMinMB":       st.TakeoutMinMB,
		"cacheMaxMB":         st.CacheMaxMB,
		"clearPreviewOnExit": st.ClearPreviewOnExit,
	}
}

// update applies a partial update and persists.
func (st *settings) update(previewTakeout *bool, takeoutMinMB *int, cacheMaxMB *int, clearPreviewOnExit *bool) error {
	st.mu.Lock()
	if previewTakeout != nil {
		st.PreviewTakeout = *previewTakeout
	}
	if takeoutMinMB != nil && *takeoutMinMB > 0 {
		st.TakeoutMinMB = *takeoutMinMB
	}
	if cacheMaxMB != nil && *cacheMaxMB > 0 {
		st.CacheMaxMB = *cacheMaxMB
	}
	if clearPreviewOnExit != nil {
		st.ClearPreviewOnExit = *clearPreviewOnExit
	}
	st.mu.Unlock()
	return st.save()
}
