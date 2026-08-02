# 文件名频道ID前缀 + 按频道存放 + 下载目录可配置 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增三个设置项——「文件名添加频道ID前缀」（`channelIdPrefix`）、「按频道存放下载文件」（`channelDir`）、「下载目录可配置」（`downloadDir`），全部默认关闭/空，兼容现有行为。

**Architecture:** 复用现有 `msgIdPrefix` 的 settings → buildDlOptions → iter 模板链路。文件名前缀在 `server/dl.go` 模板层 prepend（`.DialogID`/`.MessageID` 模板字段已支持）；按频道存放的目录拼接在 `app/dl/iter.go` 的 `processSingle`（`from.VisibleName()` 已可用，`MkdirAll` 已支持嵌套）；下载目录替换 `buildDlOptions` 的 `dir` 兜底。连带适配 `removeTaskFiles` 的递归查找。

**Tech Stack:** Go 1.22+ / gorilla/mux / Vue 3 + Element Plus / vitest（前端可无单测，手动验收为主）

## Global Constraints

- 新开关默认 `false`/`""`，存量行为不变（`msgIdPrefix` 逻辑不删）。
- 目录格式：`#频道ID 频道名`，频道名用 `filenamify` 清理，空名回退纯 `#频道ID`。
- 下载目录相对路径按 exe 目录绝对化后存。
- 批量重命名：msgId 强制 + 频道ID 跟随 `channelIdPrefix` 开关。
- `channelDir` 作为基准目录的子目录：`<downloadDir>/#频道ID 频道名/`。
- 构建：`cd tdl && ./build-tmm.sh web`（后端+前端），产物拷到 `tmm.v11/`。
- 交付目录：`tmm.v11/`；spec 同步 `tmm.v11/docs/`。

---

### Task 1: 后端设置项（settings.go + handlers.go）

**Files:**
- Modify: `server/settings.go`（struct 字段、defaultSettings、settingsJSON、load、update、新 getter）
- Modify: `server/handlers.go`（updateSettingsRequest 字段、handleConfig）
- Test: `server/settings_test.go`（新建）

**Interfaces:**
- Consumes: 现有 `settings` struct 模式（`json` tag、`mu sync.RWMutex`、指针辅助结构 `settingsJSON`）。
- Produces:
  - `settings.ChannelIdPrefix bool`（`json:"channelIdPrefix"`）
  - `settings.ChannelDir bool`（`json:"channelDir"`）
  - `settings.DownloadDir string`（`json:"downloadDir"`，默认 `""`）
  - `settings.channelIdPrefix() bool`
  - `settings.channelDir() bool`
  - `settings.downloadDir() string`（返回非空值，空则返回 `st.path` 所在 exe 目录 + "downloads"——见下注）
  - `updateSettingsRequest.ChannelIdPrefix *bool`、`ChannelDir *bool`、`DownloadDir *string`

> **downloadDir() 兜底注记**：`settings` 结构本身不知道 `srv.defaultDir`。实现 `downloadDir()` 返回 `st.DownloadDir`，若为空则由调用方（`buildDlOptions`/`handleConfig`）用 `srv.defaultDir` 兜底，避免 settings 依赖 server 循环。测试断言 getter 只透传值。

- [ ] **Step 1: 写失败测试 `server/settings_test.go`**

```go
package server

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettings_NewFieldsDefaults(t *testing.T) {
	st := defaultSettings("test.json")
	require.False(t, st.channelIdPrefix())
	require.False(t, st.channelDir())
	require.Equal(t, "", st.downloadDir())
}

func TestSettings_LoadPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	write := `{"channelIdPrefix":true,"downloadDir":"D:\\dl\\x"}`
	require.NoError(t, os.WriteFile(path, []byte(write), 0o644))
	st := defaultSettings(path)
	require.NoError(t, st.load())
	require.True(t, st.channelIdPrefix())
	require.False(t, st.channelDir(), "未设置字段保持默认")
	require.Equal(t, `D:\dl\x`, st.downloadDir())
}

func TestSettings_SnapshotIncludesNewFields(t *testing.T) {
	st := defaultSettings("t.json")
	st.ChannelIdPrefix = true
	st.ChannelDir = true
	st.DownloadDir = "/dl"
	snap := st.snapshot()
	require.True(t, snap["channelIdPrefix"].(bool))
	require.True(t, snap["channelDir"].(bool))
	require.Equal(t, "/dl", snap["downloadDir"])
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd tdl && go test ./server/ -run 'TestSettings_' -v`
Expected: 编译失败（字段不存在）

- [ ] **Step 3: 实现 settings.go 改动**

`settings` struct 加字段（`MsgIdPrefix` 旁）：
```go
ChannelIdPrefix bool   `json:"channelIdPrefix"`
ChannelDir      bool   `json:"channelDir"`
DownloadDir     string `json:"downloadDir"`
```
`defaultSettings` 加 `ChannelIdPrefix: false, ChannelDir: false, DownloadDir: ""`（bool 零值可省，string 显式 `""`）。
`settingsJSON` 加：
```go
ChannelIdPrefix *bool   `json:"channelIdPrefix"`
ChannelDir      *bool   `json:"channelDir"`
DownloadDir     *string `json:"downloadDir"`
```
`load()` 加三行指针判空赋值（模式同 `MsgIdPrefix` 第 83-85 行）。
`update()` 加三段判空赋值（模式同第 179-181 行），其中 `DownloadDir` 非空时绝对化：
```go
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
```
（需在 settings.go import `path/filepath` 和 `os` —— `os` 已 import，加 `path/filepath`。）
新增 getter（`msgIdPrefix()` 后）：
```go
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd tdl && go test ./server/ -run 'TestSettings_' -v`
Expected: PASS（3 tests）

- [ ] **Step 5: 更新 handlers.go**

`updateSettingsRequest`（第 356-363 行）加：
```go
ChannelIdPrefix *bool   `json:"channelIdPrefix"`
ChannelDir      *bool   `json:"channelDir"`
DownloadDir     *string `json:"downloadDir"`
```
`handleConfig`（第 344-349 行）改：
```go
writeJSON(w, http.StatusOK, map[string]any{
	"downloadDir": s.downloadDirEffective(),
	"webRoot":     s.opts.WebRoot,
})
```
新增方法（放 settings.go）：
```go
// downloadDirEffective returns the effective download dir: settings value if
// set, else the server default.
func (s *Server) downloadDirEffective() string {
	if d := s.settings.downloadDir(); d != "" {
		return d
	}
	return s.defaultDir
}
```

- [ ] **Step 6: 跑全部 server 测试确认无回归**

Run: `cd tdl && go test ./server/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
cd tdl && git add server/settings.go server/handlers.go server/settings_test.go && git commit -m "feat(settings): 新增 channelIdPrefix/channelDir/downloadDir 设置项"
```

---

### Task 2: 文件名频道ID前缀（dl.go 模板层）

**Files:**
- Modify: `server/dl.go:269-272`（`buildDlOptions` 模板 prepend）
- Test: `server/settings_test.go`（追加）

**Interfaces:**
- Consumes: `settings.channelIdPrefix()`、`settings.msgIdPrefix()`（Task 1）。
- Produces: `opts.Template` 前缀逻辑——`channelIdPrefix` 开 → 加 `"#{{ .DialogID }} "`；`msgIdPrefix` 开 → 加 `"#{{ .MessageID }} "`（频道在前）。

- [ ] **Step 1: 追加失败测试**

```go
func TestBuildDlOptions_TemplatePrefix(t *testing.T) {
	s := &Server{}
	// 直接测模板拼装逻辑：抽成 helper 便于测
	cases := []struct {
		channel, msg bool
		want         string
	}{
		{false, false, ""},
		{true, false, "#{{ .DialogID }} "},
		{false, true, "#{{ .MessageID }} "},
		{true, true, "#{{ .DialogID }} #{{ .MessageID }} "},
	}
	for _, c := range cases {
		got := buildFilenamePrefix(c.channel, c.msg)
		require.Equal(t, c.want, got, "prefix for channel=%v msg=%v", c.channel, c.msg)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd tdl && go test ./server/ -run TestBuildDlOptions_TemplatePrefix`
Expected: FAIL（`buildFilenamePrefix` 未定义）

- [ ] **Step 3: 实现**

`server/dl.go` 加 helper：
```go
// buildFilenamePrefix assembles the filename prefix template parts from the
// two independent settings. Channel prefix goes first, then message prefix.
func buildFilenamePrefix(channel, msg bool) string {
	var sb strings.Builder
	if channel {
		sb.WriteString("#{{ .DialogID }} ")
	}
	if msg {
		sb.WriteString("#{{ .MessageID }} ")
	}
	return sb.String()
}
```
`buildDlOptions`（第 269-272 行）替换：
```go
// 全局设置：文件名前缀 #频道ID / #msgId（开关各自独立，频道在前、消息在后）
template = buildFilenamePrefix(srv.settings.channelIdPrefix(), srv.settings.msgIdPrefix()) + template
```
（`strings` 已在 `server/dl.go` import。）

- [ ] **Step 4: 跑测试确认通过**

Run: `cd tdl && go test ./server/ -run TestBuildDlOptions_TemplatePrefix`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd tdl && git add server/dl.go server/settings_test.go && git commit -m "feat(dl): 文件名前缀支持 #频道ID（channelIdPrefix 开关，独立于 msgIdPrefix）"
```

---

### Task 3: 批量重命名频道前缀（handlers.go）

**Files:**
- Modify: `server/handlers.go:182-193`（`handleRenameFile` 前缀逻辑）
- Test: `server/settings_test.go`（追加）

**Interfaces:**
- Consumes: `settings.channelIdPrefix()`（Task 1）、`req.ForceMsgIdPrefix`。
- Produces: `newName` 前缀——`needPrefix` 时 `#msgId` 强制；`channelIdPrefix()` 开时在 msgId 前加 `#频道ID`。

- [ ] **Step 1: 追加失败测试（抽 helper）**

```go
func TestRenamePrefix(t *testing.T) {
	cases := []struct {
		force, channelOn bool
		key              string
		want             string // full prefix incl. trailing space
	}{
		{false, false, "2070986972/22713", ""},
		{true, false, "2070986972/22713", "#22713 "},
		{false, true, "2070986972/22713", ""}, // 单文件不强制，开关也不影响（needPrefix=false）
		{true, true, "2070986972/22713", "#2070986972 #22713 "},
	}
	for _, c := range cases {
		got := renamePrefix(c.force, c.channelOn, c.key)
		require.Equal(t, c.want, got, "force=%v channelOn=%v key=%v", c.force, c.channelOn, c.key)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd tdl && go test ./server/ -run TestRenamePrefix`
Expected: FAIL（`renamePrefix` 未定义）

- [ ] **Step 3: 实现**

`server/handlers.go` 加 helper：
```go
// renamePrefix builds the forced/conditional "#msgId [optional #channelId] "
// prefix for a rename. force == forceMsgIdPrefix (批量重命名): #msgId always
// added. channelOn == channelIdPrefix setting: #channelId added before #msgId
// only when the switch is on. key format "peerID/msgID".
func renamePrefix(force, channelOn bool, key string) string {
	if !force {
		return ""
	}
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return "#" + key + " " // 异常 key 兜底：把整个 key 当 msgId
	}
	prefix := ""
	if channelOn {
		prefix += "#" + parts[0] + " "
	}
	return prefix + "#" + parts[1] + " "
}
```
`handleRenameFile` 前缀段（第 182-185 行）替换：
```go
newName := name
needPrefix := req.ForceMsgIdPrefix || s.settings.msgIdPrefix()
if needPrefix {
	newName = renamePrefix(req.ForceMsgIdPrefix, s.settings.channelIdPrefix(), req.Key) + name
}
```
> 注意：单文件重命名（`force=false`，开关 `msgIdPrefix` 开）时，`renamePrefix(false, _, _)` 返回 `""`，所以频道ID 只出现在「批量重命名 + 开关开」，符合 spec。单文件 `#msgId` 前缀由现有逻辑保留——但上述改动把单文件的 `#msgId` 也交给 `renamePrefix` 会丢失。需保留原行为：单文件时前缀仍 `#msgId `。修正：

实际实现应为：
```go
newName := name
needPrefix := req.ForceMsgIdPrefix || s.settings.msgIdPrefix()
if needPrefix {
	if req.ForceMsgIdPrefix {
		// 批量重命名：msgId 强制，频道ID 跟随开关
		newName = renamePrefix(true, s.settings.channelIdPrefix(), req.Key) + name
	} else {
		// 单文件：仅 msgId（现状不变）
		newName = fmt.Sprintf("#%d %s", fe.MsgID, name)
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd tdl && go test ./server/ -run TestRenamePrefix`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd tdl && git add server/handlers.go server/settings_test.go && git commit -m "feat(rename): 批量重命名频道ID前缀跟随 channelIdPrefix 开关，msgId 保持强制"
```

---

### Task 4: 按频道存放 + 下载目录（iter.go + dl.go）

**Files:**
- Modify: `app/dl/dl.go:30-78`（Options 加字段）
- Modify: `app/dl/iter.go:350`（processSingle 路径拼接）
- Modify: `server/dl.go:279-282, 324-358`（dir 计算 + Options 透传）
- Test: `server/settings_test.go` 或新 `app/dl/iter_test.go`

**Interfaces:**
- Consumes: `settings.channelDir()`、`settings.downloadDir()`（Task 1）；`Server.downloadDirEffective()`（Task 1）。
- Produces:
  - `app/dl.Options.ChannelDir bool`
  - iter.go 用 `from.ID()` + `from.VisibleName()` 拼 `#<id> <filenamify(name)>` 子目录。

- [ ] **Step 1: 写失败测试（iter 目录拼接 helper）**

```go
package dl_test // 或 package dl，视现有测试位置

func TestChannelSubdir(t *testing.T) {
	require.Equal(t, "#2070986972 频道名", channelSubdir(2070986972, "频道名"))
	require.Equal(t, "#2070986972 a_b", channelSubdir(2070986972, "a/b?c:d")) // 特殊字符清理
	require.Equal(t, "#2070986972", channelSubdir(2070986972, ""))            // 空名回退
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd tdl && go test ./app/dl/ -run TestChannelSubdir`
Expected: FAIL（`channelSubdir` 未定义）

- [ ] **Step 3: 实现 iter.go**

`app/dl/iter.go` 加 helper（复用 filenamify——iter.go 已 import `tplfunc`？检查；无则用 `tplfunc.Filenamify()` 的底层，或直接 import `github.com/flytam/filenamify`）：
```go
// channelSubdir builds the per-channel subdirectory name: "#<id> <cleaned name>".
// Special chars in the channel name are sanitized via filenamify; empty name
// (or empty after sanitize) falls back to "#<id>".
func channelSubdir(id int64, name string) string {
	s := name
	if s != "" {
		if cleaned, err := filenamify.FilenamifyV2(s, func(o *filenamify.Options) {}); err == nil && cleaned != "" {
			s = cleaned
		}
	}
	if s == "" {
		return fmt.Sprintf("#%d", id)
	}
	return fmt.Sprintf("#%d %s", id, s)
}
```
（import 需加 `github.com/flytam/filenamify` —— 检查 go.mod 是否已有该依赖：`pkg/tplfunc/string.go` 已用，确定有。）
`processSingle`（第 350 行）改：
```go
filename := fmt.Sprintf("%s%s", rendered, tempExt)
dir := i.opts.Dir
if i.opts.ChannelDir {
	dir = filepath.Join(dir, channelSubdir(from.ID(), from.VisibleName()))
}
path := filepath.Join(dir, filename)
```
> **SkipSame 修正（关键）**：`SkipSame` 检查（第 322-331 行）当前用 `filepath.Join(i.opts.Dir, toName.String())`，发生在目录拼接之前。channelDir 开时若用旧 dir 会永远 stat 不到已下载文件 → 每次都重下。**必须把 SkipSame 检查移到目录拼接之后**，用同一个 `dir` 变量：
```go
// 目录拼接（channelDir 子目录）先于 SkipSame 检查
dir := i.opts.Dir
if i.opts.ChannelDir {
	dir = filepath.Join(dir, channelSubdir(from.ID(), from.VisibleName()))
}
if i.opts.SkipSame {
	if stat, err := os.Stat(filepath.Join(dir, toName.String())); err == nil {
		// ... 原逻辑不变
	}
}
// 之后 path := filepath.Join(dir, filename)
```

- [ ] **Step 4: Options 加字段**

`app/dl/dl.go` Options（第 78 行 `FilenameMaxLen` 后）加：
```go
// ChannelDir stores each dialog's downloads under <dir>/#<dialogID> <name>/.
ChannelDir bool
```

- [ ] **Step 5: dl.go 组装**

`server/dl.go` `buildDlOptions`：
- `dir` 计算（第 279-282 行）改：
```go
dir := o.Dir
if dir == "" {
	dir = srv.downloadDirEffective()
}
```
- Options 组装（第 324-358 行）`FilenameMaxLen` 行附近加：
```go
ChannelDir: srv.settings.channelDir(),
```
（`dl.Options` 的 `ChannelDir` 字段传入。）

- [ ] **Step 6: 跑全部测试**

Run: `cd tdl && go test ./server/ ./app/dl/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
cd tdl && git add app/dl/dl.go app/dl/iter.go server/dl.go && git commit -m "feat(dl): 按频道存放子目录（channelDir）+ 下载目录可配置（downloadDir）"
```

---

### Task 5: 删除任务文件适配递归查找（task.go）

**Files:**
- Modify: `server/task.go:203-230`（`removeTaskFiles` glob）
- Test: `server/remove_files_nil_test.go`（追加或新测试）

**Interfaces:**
- Consumes: `task.Opts.Dir`、`task.Files[].Key`、`task.Selected`。
- Produces: 删除逻辑在 `dir` 下递归匹配 `{peer}_{msg}_*`（含 `.tmp`、`.tdlstate`）。

- [ ] **Step 1: 写失败测试**

```go
func TestRemoveTaskFiles_ChannelDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "#2070986972 频道")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	f1 := filepath.Join(sub, "2070986972_22713_video.mp4")
	require.NoError(t, os.WriteFile(f1, []byte("x"), 0o644))
	f2 := filepath.Join(dir, "other.txt") // 不应被删
	require.NoError(t, os.WriteFile(f2, []byte("x"), 0o644))

	// 调用 removeTaskFiles 的 glob helper
	paths := globTaskFiles(dir, "2070986972", "22713")
	require.ElementsMatch(t, []string{f1}, paths)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd tdl && go test ./server/ -run TestRemoveTaskFiles_ChannelDir`
Expected: FAIL（`globTaskFiles` 未定义）

- [ ] **Step 3: 实现**

`server/task.go` 加 helper：
```go
// globTaskFiles recursively finds files matching <peer>_<msg>_* under dir,
// covering the per-channel subdir layout (channelDir on) and flat layout.
func globTaskFiles(dir, peer, msg string) []string {
	prefix := peer + "_" + msg + "_"
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(filepath.Base(p), prefix) {
			out = append(out, p)
		}
		return nil
	})
	return out
}
```
`removeTaskFiles` 的删除循环（第 213-225 行）替换 glob 段：
```go
for _, t := range targets {
	matches := globTaskFiles(dir, t.peer, t.msg)
	for _, p := range matches {
		if err := os.Remove(p); err != nil {
			if !os.IsNotExist(err) {
				m.srv.log().Warn("remove task file failed",
					zap.String("task", task.ID), zap.String("path", p), zap.Error(err))
			}
			continue
		}
		removed++
	}
}
```
> 注意：`.tdlstate` 控制文件名为 `<filename>.tdlstate`，`filepath.Base` 不含 `{peer}_{msg}_*` 前缀（含 `.tdlstate` 后缀）。原 glob `{peer}_{msg}_*` 也不匹配 `.tdlstate`（`*` 在 `.` 后不匹配）——**现状已不删 `.tdlstate`**？检查：`tdldl.StatePath(path)` = `path + ".tdlstate"`，glob `{peer}_{msg}_*` 匹配 `2070986972_22713_video.mp4` 但不匹配 `..._video.mp4.tdlstate`。**原逻辑就没删 state 文件**，本任务保持现状（不扩大范围）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd tdl && go test ./server/ -run TestRemoveTaskFiles_ChannelDir`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd tdl && git add server/task.go server/remove_files_nil_test.go && git commit -m "fix(delete): 删除任务文件递归匹配频道子目录（channelDir 兼容）"
```

---

### Task 6: 前端设置页（Settings.vue + api/index.js + handlers_fs.go）

**Files:**
- Modify: `web/app/src/views/Settings.vue`（3 个新 UI）
- Modify: `web/app/src/api/index.js:61`（pickFolder 支持 title）
- Modify: `server/handlers_fs.go:23`（pickFolder title 参数化）

**Interfaces:**
- Consumes: `api.updateSettings`（payload 含新字段）、`api.pickFolder({title})`、`store.config.downloadDir`（Task 1 已改 handleConfig）。
- Produces: 设置页可改三个新项；「选择文件夹」返回路径填入输入框。

- [ ] **Step 1: 改 handlers_fs.go pickFolder 支持 title**

```go
title := r.URL.Query().Get("title")
if title == "" {
	title = "选择 Telegram Desktop 数据目录（tdata 所在目录）"
}
dir, err := dialog.Directory().Title(title).Browse()
```

- [ ] **Step 2: 改 api/index.js pickFolder**

```js
pickFolder: (title) => req('POST', `/api/fs/pick-folder${title ? `?title=${encodeURIComponent(title)}` : ''}`),
```

- [ ] **Step 3: Settings.vue 加三个设置**

「文件名」卡（第 67 行 `msgIdPrefix` 开关后）加：
```html
<div class="setrow">
  <div class="setinfo">
    <div class="setname">文件名添加频道ID前缀</div>
    <div class="setdesc muted">开启后，下载的文件名前会加上 <code class="code-hint">#2070986972</code> 前缀（频道ID + 空格）。与消息ID前缀同时开启时为 <code class="code-hint">#2070986972 #22713 video.mp4</code>，方便按频道查找文件。</div>
  </div>
  <el-switch v-model="settings.channelIdPrefix" @change="saveSettings" />
</div>
<div class="setrow">
  <div class="setinfo">
    <div class="setname">按频道存放下载文件</div>
    <div class="setdesc muted">开启后，每个频道的文件存到下载目录下 <code class="code-hint">#频道ID 频道名</code> 子目录，频道名中的特殊字符会被自动清理。如：<code class="code-hint">downloads/#2070986972 资源社区/</code></div>
  </div>
  <el-switch v-model="settings.channelDir" @change="saveSettings" />
</div>
```
「服务」卡（第 81 行 `kv` 下载目录处）改为可配置区：
```html
<div class="setrow">
  <div class="setinfo">
    <div class="setname">下载目录</div>
    <div class="setdesc muted">下载文件存放的目录，默认在程序所在目录的 downloads 文件夹。按频道存放开关开启时，文件存到此目录下 #频道ID 频道名 子目录。</div>
  </div>
  <div class="dl-dir-row">
    <el-input v-model="settings.downloadDir" placeholder="留空 = 默认目录（程序目录下 downloads）" clearable size="small" style="min-width: 220px" />
    <el-button size="small" @click="pickDownloadDir">选择文件夹</el-button>
  </div>
</div>
```
`script`：
- `settings` ref（第 101 行）加 `channelIdPrefix: false, channelDir: false, downloadDir: ''`
- `load`（第 106-112 行）加 `channelIdPrefix: s.channelIdPrefix === true, channelDir: s.channelDir === true, downloadDir: s.downloadDir || ''`
- `saveSettings`（第 118-124 行）加 `channelIdPrefix: settings.value.channelIdPrefix, channelDir: settings.value.channelDir, downloadDir: settings.value.downloadDir || undefined`
- 新函数：
```js
async function pickDownloadDir() {
  try {
    const r = await api.pickFolder('选择下载目录')
    if (!r.cancelled && r.path) {
      settings.value.downloadDir = r.path
      await saveSettings()
    }
  } catch (e) { ElMessage.error(e.message) }
}
```
- style 加 `.dl-dir-row { display: flex; gap: 8px; align-items: center; }`

- [ ] **Step 4: 前端 build**

Run: `cd tdl && ./build-tmm.sh web`（或 `cd web/app && npm run build` 后手动拷 dist）

- [ ] **Step 5: Commit**

```bash
cd tdl && git add server/handlers_fs.go web/app/src/api/index.js web/app/src/views/Settings.vue && git commit -m "feat(web): 设置页新增频道ID前缀/按频道存放/下载目录配置"
```

---

### Task 7: 构建 + 手动验收

**Files:**
- Modify: 无（构建产物拷到 `tmm.v11/`）

- [ ] **Step 1: 后端 + 前端构建**

Run: `cd tdl && ./build-tmm.sh web`
Expected: 成功，`tmm.exe` 更新、`web/app/dist` 生成

- [ ] **Step 2: 拷产物到 tmm.v11**

Run: 按项目惯例拷 `tmm.exe` + `web/` 到 `D:\Documents\workspace\telegram manager\tmm.v11\`

- [ ] **Step 3: 重启服务**

Run: `powershell -Command "Get-Process tmm | Stop-Process -Force"` 后 `tmm.v11\start.bat`（或后台 `./tmm.exe serve --port 8080`）

- [ ] **Step 4: 手动验收清单**（用户执行）

- [ ] 设置页三个新项显示，保存后刷新保留
- [ ] 只开 `channelIdPrefix`：新任务文件名 `#频道ID 原名`
- [ ] 只开 `msgIdPrefix`：`#msgID 原名`（不变）
- [ ] 都开：`#频道ID #msgID 原名`
- [ ] 开 `channelDir`：文件落 `downloads/#频道ID 频道名/`，特殊字符被清理
- [ ] 设 `downloadDir` 为自定义路径 + 开 `channelDir`：文件落 `<自定义>/#频道ID 频道名/`
- [ ] 批量重命名（右键多选）：开关开 → `#频道 #消息 新名`；关 → `#消息 新名`
- [ ] 删除任务（channelDir 开）：子目录文件被删
- [ ] 重启后设置保留

- [ ] **Step 5: 同步 spec 到 tmm.v11/docs**

```bash
cp docs/superpowers/specs/2026-08-02-channel-id-prefix-design.md ../tmm.v11/docs/
```

---

## 自查记录

**Spec 覆盖**：
- channelIdPrefix 独立开关 → Task 1 + 2 ✅
- 格式 `#频道 #消息` → Task 2（buildFilenamePrefix 顺序）✅
- 批量重命名解耦 → Task 3（renamePrefix force+channelOn）✅
- channelDir 目录格式 + filenamify → Task 4（channelSubdir）✅
- 下载目录可配置 + 基准 → Task 4（downloadDirEffective）+ Task 1（handleConfig）✅
- 相对路径绝对化 → Task 4 注记（实现时确认，见下）
- removeTaskFiles 递归 → Task 5 ✅
- 前端三个 UI → Task 6 ✅

**待实现时确认的点**：
1. `DownloadDir` 相对路径绝对化：spec 说按 exe 目录绝对化。实现时在 `settings.update()` 里对 `DownloadDir` 做 `filepath.Abs`（若相对则基于 exe 目录）——Task 1 Step 3 的 update 段需补此逻辑。
2. `SkipSame` 检查位置（Task 4 Step 3）：stat 检查必须用拼了子目录的 `dir`，否则 channelDir 开时永远 stat 不到 → 每次重下。**必改**。
