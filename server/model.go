package server

import (
	"sync"
	"time"
)

// TaskStatus is the lifecycle state of a download task.
type TaskStatus string

const (
	StatusPending     TaskStatus = "pending"     // queued, not yet started
	StatusRunning     TaskStatus = "running"     // downloading
	StatusPaused      TaskStatus = "paused"      // cancelled by user, resumable
	StatusDone        TaskStatus = "done"        // all files finished
	StatusFailed      TaskStatus = "failed"      // fatal error
	StatusInterrupted TaskStatus = "interrupted" // was running when the process stopped
)

// FileStatus is the per-file lifecycle state.
type FileStatus string

const (
	FilePending FileStatus = "pending" // 预填排队中：任务创建时列入，尚未轮到下载
	FileRunning FileStatus = "running"
	FileDone    FileStatus = "done"
	FileFailed  FileStatus = "failed"
	FilePaused  FileStatus = "paused" // 已开始下载后被打断（暂停），可续传
)

// FileEntry tracks one downloaded file within a task.
type FileEntry struct {
	Key        string     `json:"key"` // "peerID/msgID" resume coordinate
	Name       string     `json:"name"`
	Path       string     `json:"path"` // final path once done, temp path while running
	FromName   string     `json:"fromName"`
	FromID     int64      `json:"fromId"`
	MsgID      int        `json:"msgId"`
	Caption    string     `json:"caption"` // source message text (first line shown in UI)
	Size       int64      `json:"size"`
	Downloaded int64      `json:"downloaded"`
	Status     FileStatus `json:"status"`
	UserPaused bool       `json:"userPaused"` // 文件级手动暂停标记；任务级暂停不置此位
	Error      string     `json:"error,omitempty"`
}

// TaskOptions are the user-supplied download settings for a task.
type TaskOptions struct {
	Dir        string   `json:"dir"`
	Template   string   `json:"template"`
	Takeout    string   `json:"takeout"` // auto | always | never
	Desc       bool     `json:"desc"`
	Group      bool     `json:"group"`
	RewriteExt bool     `json:"rewriteExt"`
	SkipSame   bool     `json:"skipSame"`
	Include    []string `json:"include"`
	Exclude    []string `json:"exclude"`
	Restart    bool     `json:"restart"` // ignore resume record and re-download

	// runtime tunables (0 = default)
	PoolSize int `json:"poolSize"`
	Threads  int `json:"threads"`
	Limit    int `json:"limit"`
	DelayMs  int `json:"delayMs"`
}

// SelectedMessages identifies a set of messages in one chat to download,
// supplied directly (bypasses URL/file parsing).
type SelectedMessages struct {
	ChatID string `json:"chatId"` // numeric id or username
	MsgIDs []int  `json:"msgIds"`
}

// Task is a single download job.
type Task struct {
	ID        string             `json:"id"`
	Account   string             `json:"account"` // namespace
	URLs      []string           `json:"urls"`
	Selected  []SelectedMessages `json:"selected,omitempty"` // direct (chat,msgIDs) selection
	Opts      TaskOptions        `json:"opts"`
	Status    TaskStatus         `json:"status"`
	Error     string             `json:"error,omitempty"`
	Files     []*FileEntry       `json:"files"`
	CreatedAt time.Time          `json:"createdAt"`
	StartedAt *time.Time         `json:"startedAt,omitempty"`
	EndedAt   *time.Time         `json:"endedAt,omitempty"`

	// runtime bookkeeping (not persisted meaningfully; rebuilt on load)
	Fingerprint string `json:"fingerprint,omitempty"`
	// Speed is the current aggregate download speed (bytes/sec) across all
	// files, maintained by progressAdapter for live UI display. Not persisted.
	Speed int64 `json:"-"`

	// mu guards mutable fields (Status, Files, Error, timestamps). It is not
	// serialized.
	mu sync.Mutex `json:"-"`
}

// TotalSize sums the size of all known files.
func (t *Task) TotalSize() int64 {
	var s int64
	for _, f := range t.Files {
		s += f.Size
	}
	return s
}

// TotalDownloaded sums downloaded bytes across files.
func (t *Task) TotalDownloaded() int64 {
	var s int64
	for _, f := range t.Files {
		s += f.Downloaded
	}
	return s
}
