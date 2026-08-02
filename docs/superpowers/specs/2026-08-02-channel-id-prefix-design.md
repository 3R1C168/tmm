# 文件名频道ID前缀开关 + 按频道存放设计

- **日期**：2026-08-02
- **范围**：新增设置开关「文件名添加频道ID前缀」+「按频道存放下载文件」+ 批量重命名强制前缀扩展到频道ID（跟随开关）。
- **目标用户**：项目所有者本人（非程序员，中文交流，用 tmm.v11 实际运行）。
- **交付目录**：`tmm.v11/`（后端重建 exe + 前端 build 拷 web）。

---

## 1. 背景与现状

现有设置 `msgIdPrefix`（`server/settings.go:34`）控制下载文件名是否加消息ID前缀。

**完整链路**：
- 设置项：`server/settings.go` `MsgIdPrefix bool` → 前端 `Settings.vue` 开关（第 63-65 行）→ `saveSettings` 走 `/api/settings`。
- 应用点：`server/dl.go:270` `buildDlOptions` 里，`msgIdPrefix()` 开启时在 Go 模板前 prepend `"#{{ .MessageID }} "`。
- 模板字段：`app/dl/iter.go:39` `fileTemplate.DialogID` 即频道数字 ID（`server/task.go:168` 注释明确 "ChatID 即文件名里的 DialogID"），模板引擎已支持 `.DialogID`。
- 批量重命名：`server/handlers.go:184` `needPrefix := req.ForceMsgIdPrefix || s.settings.msgIdPrefix()`，`ForceMsgIdPrefix` 由前端批量重命名对话框传 true，只加 `#msgId`。

**路径生成（按频道存放的基础）**：
- `app/dl/iter.go:350` `path := filepath.Join(i.opts.Dir, filename)` —— 文件路径 = `opts.Dir` + 模板渲染文件名。
- `iter.go:353` `os.MkdirAll(filepath.Dir(path))` —— 已支持嵌套目录（注释 #113 "If path contains dirs, create it"）。
- `processGrouped`（`iter.go:435`）最终都走 `processSingle`，路径逻辑只在 `processSingle` 一处。
- 频道名来源：gotd `peers.Peer.VisibleName()`（`channel.go:54` 返回 `c.raw.GetTitle()`），与 `/api/chats` 的 `visibleName`（`app/chat/ls.go:220` `c.Title`）**同一来源**（`tg.Channel.Title`），零额外 RPC。
- 特殊字符清理：`pkg/tplfunc/string.go:72` `filenamify`（`flytam/filenamify`，Windows 安全文件名处理 `\/:*?"<>|`）。

**缺口**：
1. 文件名只能追溯消息ID，无法直接看出来自哪个频道 → 需要「文件名频道ID前缀」开关。
2. 所有下载平铺在 `downloads/`，频道多时混乱 → 需要「按频道存放」开关。

---

## 2. 目标

新增两个独立设置开关（均默认关闭）：
1. **文件名添加频道ID前缀**：开启后下载文件名变为 `#<频道ID> #<msgID> <文件名>`。
2. **按频道存放下载文件**：开启后文件存到 `<下载目录>/#<频道ID> <频道名>/` 子目录。

**已确认的需求决策**（用户拍板）：
1. **文件名频道ID前缀独立开关**：新增 `channelIdPrefix`，与 `msgIdPrefix` 完全独立，各自开关互不影响。
2. **格式**：两个独立 `#`，`#频道ID #msgID 文件名`。
3. **批量重命名解耦**：批量重命名强制加 `#msgId`（现状，`forceMsgIdPrefix` 不变）；频道ID **不强制**，跟随 `channelIdPrefix` 开关 —— 开关开才加 `#频道ID`。
4. **按频道存放独立开关**：`channelDir` 与文件名前缀开关完全独立。
5. **目录格式**：`#频道ID 频道名`，频道名取自 gotd `peers.Peer.VisibleName()`（=`tg.Channel.Title`，与 `/api/chats` 的 `visibleName` 同一来源，零额外 RPC）；频道名特殊字符用 `filenamify` 清理。
6. **仅新任务生效**：开关在任务运行时（`buildDlOptions`）计算目录，存量任务不受影响（任务 `opts.Dir` 固化，不迁移）。

**非目标**（YAGNI）：
- 不改 `defaultTemplate`（`dl.go:20`）本身 —— 默认模板仍为 `{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}`，前缀由开关在模板层 prepend。
- 不加频道「显示名」或 `@username` —— 只加数字 ID（用户明确要求数字 ID）。
- 不改单文件重命名路径的前缀逻辑（单文件重命名不强制前缀，沿用现有：`needPrefix := ForceMsgIdPrefix || msgIdPrefix()` 时加 `#msgId`；频道ID 不参与单文件重命名）。
- 存量任务不迁移目录。
- 频道无标题时不回退取用户名 —— 目录退化为纯 `#频道ID`。

---

## 3. 设计

### 3.1 后端

**`server/settings.go`**：
- `settings` struct 加两个字段：
  - `ChannelIdPrefix bool`（`json:"channelIdPrefix"`）—— 文件名频道ID前缀
  - `ChannelDir bool`（`json:"channelDir"`）—— 按频道存放
- `defaultSettings` 均默认 `false`。
- `settingsJSON` load 辅助结构加 `ChannelIdPrefix *bool`、`ChannelDir *bool`，`load()` 对应加指针判空赋值。
- `update(*updateSettingsRequest)` 加两个字段的判空赋值。
- 新增 getter `channelIdPrefix()`、`channelDir()`（复刻 `msgIdPrefix()`，第 139-144 行）。
- `snapshot()` 走 `json.Marshal`，自动带出新字段，无需改。

**`server/handlers.go`**：
- `updateSettingsRequest` struct 加 `ChannelIdPrefix *bool`、`ChannelDir *bool`（`json` tag 对应）。

**`server/dl.go`** `buildDlOptions`（约 262-320 行）：
1. **文件名前缀**（约 269-272 行，现有 `msgIdPrefix` 旁）：
```go
// 全局设置：文件名前缀 #频道ID / #msgId（开关各自独立，频道在前、消息在后）
if srv.settings.channelIdPrefix() {
    template = "#{{ .DialogID }} " + template
}
if srv.settings.msgIdPrefix() {
    template = "#{{ .MessageID }} " + template
}
```
2. **按频道存放开关透传**：`dl.Options` 加 `ChannelDir` 字段，组装时传 `srv.settings.channelDir()`。目录拼接不在此层做（peer 名需迭代器已解析的 `from`），由 iter.go 的 `processSingle` 完成（见下）。

**`app/dl/iter.go`**：
- `Options` struct 加字段 `ChannelDir bool`（`server/dl.go:324` 组装时传 `srv.settings.channelDir()`）。
- `processSingle`（约 350 行）路径拼装处：
```go
filename := fmt.Sprintf("%s%s", rendered, tempExt)
dir := i.opts.Dir
if i.opts.ChannelDir {
    name := from.VisibleName()
    dir = filepath.Join(dir, "#"+itoa(from.ID())+" "+filenamify(name))
}
path := filepath.Join(dir, filename)
```
- 频道名清理：复用 `tplfunc.Filenamify()` 的 `filenamify` 函数（`pkg/tplfunc/string.go:72`），需在 iter.go import `filenamify`（或抽一个 helper）。频道名空/清理后为空 → 回退纯 `#频道ID`。

### 3.2 前端

**`web/app/src/views/Settings.vue`**：
- 「文件名」区（第 59-71 行）在「文件名添加消息ID前缀」开关下方新增「文件名添加频道ID前缀」开关：
  ```html
  <el-switch v-model="settings.channelIdPrefix" @change="saveSettings" />
  ```
  - 名称：「文件名添加频道ID前缀」
  - 描述示例：`#2070986972 #22713 video.mp4`，说明"开启后下载的文件名前会加上 #频道ID + #消息ID 前缀（均开启时），方便按频道查找文件"。
- 新增「下载目录」区（或「文件名」区下方）新增「按频道存放」开关：
  ```html
  <el-switch v-model="settings.channelDir" @change="saveSettings" />
  ```
  - 名称：「按频道存放下载文件」
  - 描述示例：`downloads/#2070986972 💚资源社区/`，说明"开启后每个频道的文件存到单独子目录（#频道ID + 频道名），频道名中的特殊字符会被清理"。
- `settings` ref（第 101 行）加 `channelIdPrefix: false, channelDir: false`。
- `load`（第 110 行）加 `channelIdPrefix: s.channelIdPrefix === true, channelDir: s.channelDir === true`。
- `save` payload（第 122 行）加 `channelIdPrefix: settings.value.channelIdPrefix, channelDir: settings.value.channelDir`。

### 3.3 边界行为

**文件名前缀**（`channelIdPrefix` × `msgIdPrefix`）：

| 开关组合 | 下载文件名 |
|---|---|
| 都关 | `22713_video.mp4`（默认模板，无前缀） |
| 只开频道 | `#2070986972 22713_video.mp4` |
| 只开消息 | `#22713 22713_video.mp4` |
| 都开 | `#2070986972 #22713 22713_video.mp4` |
| 批量重命名（开关关） | `#22713 新名`（msgId 强制，现状不变） |
| 批量重命名（开关开） | `#2070986972 #22713 新名`（msgId 强制 + 频道跟随开关） |

**按频道存放**（`channelDir`，与文件名前缀独立）：

| 开关组合 | 落盘路径 |
|---|---|
| `channelDir` 关 | `downloads/22713_video.mp4`（现状） |
| `channelDir` 开 | `downloads/#2070986972 💚资源社区/22713_video.mp4` |
| `channelDir` 开 + 频道名含特殊字符 | `downloads/#2070986972 清理后名/22713_video.mp4` |
| `channelDir` 开 + 频道无标题 | `downloads/#2070986972/22713_video.mp4` |

- 文件名截断（`FilenameMaxLen`）在模板渲染后执行，前缀计入长度 —— 与现有一致，无需改动。
- 前缀内的空格：`#{{ .DialogID }} ` 和 `#{{ .MessageID }} ` 各含一个尾部空格，两个都开时天然形成 `#频道 #消息 `，不会粘连。
- **删除任务文件**（`server/task.go:170` `removeTaskFiles`）：现有按 `task.Opts.Dir` 下 `glob {chatID}_{msgID}_*` 找文件。`channelDir` 开启后文件在子目录，glob 需适配 —— 改为在 `task.Opts.Dir` 下**递归查找**（或子目录下 glob）。此项是必改的连带影响。

---

## 4. 测试

**后端单测**（`server/` 下新增或复用现有测试文件）：
- `buildDlOptions` 模板拼装：mock settings 组合（都关/只频道/只消息/都开），断言 `opts.Template` 前缀正确。
- `handleRenameFile` 前缀：force 时 `#msgId` 强制；`channelIdPrefix` 开/关 两种状态下频道前缀的有无；key 拆分 `"2070986972/22713"` → 频道 `2070986972`。
- `iter.go` 路径：mock `ChannelDir` 开/关，断言 `processSingle` 生成的 `path` 含/不含频道子目录；频道名特殊字符清理（`:` `?` `<` 等）；空频道名回退。

**手动验收**（用户执行，沿用用户工作风格）：
1. clear-cache.bat + Ctrl+Shift+R，设置页打开「文件名添加频道ID前缀」+「按频道存放下载文件」。
2. 新建任务下载，检查：
   - 文件名四种组合；
   - 文件落在 `downloads/#频道ID 频道名/` 子目录；
   - 频道名特殊字符被清理。
3. 批量重命名（右键多选 → 批量重命名），检查 `#频道 #消息 新名`。
4. 删除任务，确认 `channelDir` 开时文件被正确删除（递归）。
5. 设置保存 → 重启 → 值仍在。

---

## 5. 风险与注意

- `dialogID` 可能为负数？—— 频道 ID 是正整数（`tutil.GetInputPeerID` 对 channel 返回正 ID），与现有默认模板行为一致，无新风险。
- 兼容性：`msgIdPrefix` 行为不变；新开关默认关，存量用户无感知。
- **删除文件递归**：`removeTaskFiles` 从平铺 glob 改递归，要避免误删其它目录文件（只删匹配 `{chatID}_{msgID}_*` 的，且限定在下载目录内）。
- 频道名可能含 `#` 本身、emoji、超长 —— `filenamify` 处理长度和非法字符；空名回退纯 ID。
