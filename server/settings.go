package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-faster/errors"
)

// settings holds user-tunable service preferences, persisted to settings.json
// next to the executable. JSON 序列化直接走 struct tag，不再手写字段映射。
type settings struct {
	mu sync.RWMutex `json:"-"`

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

	// MsgIdPrefix enables prefixing download filenames with #<MessageID> ,
	// e.g. "#22713 video.mp4". Helps identify files by their source message.
	MsgIdPrefix bool `json:"msgIdPrefix"`
	// FilenameMaxLen truncates the final filename (without extension) to this
	// many bytes, to respect filesystem name limits. 0 = no truncation.
	// Windows=255, macOS=255 (HFS+/APFS), Linux=255 bytes.
	FilenameMaxLen int `json:"filenameMaxLen"`

	// ChannelIdPrefix prefixes download filenames with #<DialogID> to identify
	// the source channel.
	ChannelIdPrefix bool `json:"channelIdPrefix"`
	// ChannelDir stores downloads in per-channel subdirectories.
	ChannelDir bool `json:"channelDir"`
	// DownloadDir is the configurable download directory ("" = server default).
	DownloadDir string `json:"downloadDir"`

	path string `json:"-"`

	// legacyPath is the pre-2026-08-03 settings location (next to the exe).
	// load() migrates an existing file from here to path once.
	legacyPath string `json:"-"`
}

// settingsJSON 是 load 时的辅助结构：可空字段用指针，区分"未设置=用默认"
// 和"显式 false/0"。只有 load 用，save/snapshot 走 json.Marshal(settings)。
type settingsJSON struct {
	PreviewTakeout     bool    `json:"previewTakeout"`
	TakeoutMinMB       int     `json:"takeoutMinMB"`
	CacheMaxMB         int     `json:"cacheMaxMB"`
	ClearPreviewOnExit *bool   `json:"clearPreviewOnExit"`
	MsgIdPrefix        *bool   `json:"msgIdPrefix"`
	FilenameMaxLen     *int    `json:"filenameMaxLen"`
	ChannelIdPrefix    *bool   `json:"channelIdPrefix"`
	ChannelDir         *bool   `json:"channelDir"`
	DownloadDir        *string `json:"downloadDir"`
}

func defaultSettings(path string) *settings {
	return &settings{
		PreviewTakeout:     false,
		TakeoutMinMB:       50,
		CacheMaxMB:         5120,
		ClearPreviewOnExit: true,
		MsgIdPrefix:        false,
		FilenameMaxLen:     200, // 留余量给 .tmp/.tdlstate 后缀和扩展名
		ChannelIdPrefix:    false,
		ChannelDir:         false,
		DownloadDir:        "",
		path:               path,
		legacyPath:         legacySettingsPath(),
	}
}

func (st *settings) load() error {
	// 迁移旧位置（exe 旁的 settings.json）到 data/settings.json：新路径不存在
	// 且旧路径存在时移动一次，保留用户已有设置。data/ 目录由 consts 保证存在。
	if !fileExists(st.path) && fileExists(st.legacyPath) {
		if err := os.Rename(st.legacyPath, st.path); err == nil {
			// 移动成功即可；失败则走下面的默认值逻辑，不阻塞启动
		}
	}
	b, err := os.ReadFile(st.path)
	if err != nil {
		return nil // missing file -> defaults
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	var a settingsJSON
	if err := json.Unmarshal(b, &a); err != nil {
		return errors.Wrap(err, "parse settings")
	}
	st.PreviewTakeout = a.PreviewTakeout
	st.TakeoutMinMB = a.TakeoutMinMB
	st.CacheMaxMB = a.CacheMaxMB
	if a.ClearPreviewOnExit != nil {
		st.ClearPreviewOnExit = *a.ClearPreviewOnExit
	}
	if a.MsgIdPrefix != nil {
		st.MsgIdPrefix = *a.MsgIdPrefix
	}
	if a.FilenameMaxLen != nil {
		st.FilenameMaxLen = *a.FilenameMaxLen
	}
	if a.ChannelIdPrefix != nil {
		st.ChannelIdPrefix = *a.ChannelIdPrefix
	}
	if a.ChannelDir != nil {
		st.ChannelDir = *a.ChannelDir
	}
	if a.DownloadDir != nil {
		st.DownloadDir = *a.DownloadDir
	}
	if st.TakeoutMinMB <= 0 {
		st.TakeoutMinMB = 50
	}
	if st.CacheMaxMB <= 0 {
		st.CacheMaxMB = 5120
	}
	return nil
}

// save 直接序列化 settings 的导出字段，无需手写字段映射。
func (st *settings) save() error {
	st.mu.RLock()
	b, err := json.MarshalIndent(st, "", "  ")
	st.mu.RUnlock()
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

// msgIdPrefix reports whether download filenames should be prefixed with #msgId.
func (st *settings) msgIdPrefix() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.MsgIdPrefix
}

// channelIdPrefix reports whether download filenames should be prefixed with #DialogID.
func (st *settings) channelIdPrefix() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.ChannelIdPrefix
}

// channelDir reports whether downloads should be stored in per-channel subdirs.
func (st *settings) channelDir() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.ChannelDir
}

// downloadDir returns the configured download directory ("" = use default).
func (st *settings) downloadDir() string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.DownloadDir
}

// filenameMaxLen reports the max filename length (0 = unlimited).
func (st *settings) filenameMaxLen() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.FilenameMaxLen
}

// snapshot returns the settings as a JSON-friendly map（直接 marshal，无字段映射）。
func (st *settings) snapshot() map[string]any {
	st.mu.RLock()
	b, _ := json.Marshal(st)
	st.mu.RUnlock()
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// update applies a partial update and persists。接收 updateSettingsRequest（全指针字段，
// 区分"未发=不改" vs "显式 false/0"），不再逐字段透传六个参数。
func (st *settings) update(a *updateSettingsRequest) error {
	st.mu.Lock()
	if a.PreviewTakeout != nil {
		st.PreviewTakeout = *a.PreviewTakeout
	}
	if a.TakeoutMinMB != nil && *a.TakeoutMinMB > 0 {
		st.TakeoutMinMB = *a.TakeoutMinMB
	}
	if a.CacheMaxMB != nil && *a.CacheMaxMB > 0 {
		st.CacheMaxMB = *a.CacheMaxMB
	}
	if a.ClearPreviewOnExit != nil {
		st.ClearPreviewOnExit = *a.ClearPreviewOnExit
	}
	if a.MsgIdPrefix != nil {
		st.MsgIdPrefix = *a.MsgIdPrefix
	}
	if a.FilenameMaxLen != nil {
		st.FilenameMaxLen = *a.FilenameMaxLen
	}
	if a.ChannelIdPrefix != nil {
		st.ChannelIdPrefix = *a.ChannelIdPrefix
	}
	if a.ChannelDir != nil {
		st.ChannelDir = *a.ChannelDir
	}
	if a.DownloadDir != nil {
		if *a.DownloadDir == "" {
			st.DownloadDir = ""
		} else {
			// 相对路径基于 exe 目录绝对化，避免工作目录依赖
			d := *a.DownloadDir
			if !filepath.IsAbs(d) {
				if exe, err := os.Executable(); err == nil {
					d = filepath.Join(filepath.Dir(exe), d)
				} else {
					d, _ = filepath.Abs(d)
				}
			}
			st.DownloadDir = d
		}
	}
	st.mu.Unlock()
	return st.save()
}

// downloadDirEffective returns the effective download dir: settings value if
// set, else the server default.
func (s *Server) downloadDirEffective() string {
	if d := s.settings.downloadDir(); d != "" {
		return d
	}
	return s.defaultDir
}

// fileExists reports whether path exists and is not a directory.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
