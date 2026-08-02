# 文件名频道ID前缀开关设计

- **日期**：2026-08-02
- **范围**：新增设置开关「文件名添加频道ID前缀」+ 批量重命名强制前缀扩展到频道ID。
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

**缺口**：文件名只能追溯消息ID，无法直接看出来自哪个频道。用户需求：可选地在文件名前加频道ID，格式 `#频道ID #msgID`。

---

## 2. 目标

新增独立设置开关「文件名添加频道ID前缀」（默认关闭），开启后下载文件名变为 `#<频道ID> #<msgID> <文件名>`。

**已确认的需求决策**（用户拍板）：
1. **独立开关**：新增 `channelIdPrefix`，与 `msgIdPrefix` 完全独立，各自开关互不影响。
2. **格式**：两个独立 `#`，`#频道ID #msgID 文件名`。
3. **批量重命名也强制加频道ID**：右键/铅笔批量重命名无论开关状态，一律 `#频道ID #msgID 文件名`（频道ID 从文件 key `"peerID/msgID"` 拆出，无需 RPC）。

**非目标**（YAGNI）：
- 不改 `defaultTemplate`（`dl.go:20`）本身 —— 默认模板仍为 `{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}`，前缀由开关在模板层 prepend。
- 不加频道「显示名」或 `@username` —— 只加数字 ID（用户明确要求数字 ID）。
- 不改单文件重命名路径的前缀逻辑（单文件重命名不强制前缀，沿用现有：`needPrefix` 时加 `#msgId`，频道ID 仅批量重命名强制）。

---

## 3. 设计

### 3.1 后端

**`server/settings.go`**：
- `settings` struct 加字段 `ChannelIdPrefix bool`（`json:"channelIdPrefix"`），放在 `MsgIdPrefix` 旁。
- `defaultSettings` 默认 `false`。
- `settingsJSON` load 辅助结构加 `ChannelIdPrefix *bool`，`load()` 加 `if a.ChannelIdPrefix != nil { st.ChannelIdPrefix = *a.ChannelIdPrefix }`。
- `update(*updateSettingsRequest)` 加 `if a.ChannelIdPrefix != nil { st.ChannelIdPrefix = *a.ChannelIdPrefix }`。
- 新增 getter `channelIdPrefix()`（复刻 `msgIdPrefix()`，第 139-144 行）。
- `snapshot()` 走 `json.Marshal`，自动带出新字段，无需改。

**`server/handlers.go`**：
- `updateSettingsRequest` struct 加 `ChannelIdPrefix *bool`（`json:"channelIdPrefix"`）。

**`server/dl.go`** `buildDlOptions`（约 270 行）：
```go
// 全局设置：文件名前缀 #频道ID / #msgId（开关各自独立，频道在前、消息在后）
if srv.settings.channelIdPrefix() {
    template = "#{{ .DialogID }} " + template
}
if srv.settings.msgIdPrefix() {
    template = "#{{ .MessageID }} " + template
}
```

**`server/handlers.go`** `handleRenameFile`（约 182-184 行）：
- 批量重命名（`forceMsgIdPrefix`）时，前缀从 `#msgId` 扩展为 `#频道ID #msgId`。
- 频道ID 从请求 `key`（格式 `"peerID/msgID"`）拆分 `strings.Split(key, "/")[0]`，无需 RPC。
- 单文件重命名（非 force）保持现状：`needPrefix := req.ForceMsgIdPrefix || s.settings.msgIdPrefix()` 只加 `#msgId`（`channelIdPrefix` 不参与单文件重命名，符合"仅批量重命名强制频道ID"的决策）。

### 3.2 前端

**`web/app/src/views/Settings.vue`**：
- 「文件名」区（第 59-71 行）在「文件名添加消息ID前缀」开关下方新增：
  ```html
  <el-switch v-model="settings.channelIdPrefix" @change="saveSettings" />
  ```
  - 名称：「文件名添加频道ID前缀」
  - 描述示例：`#2070986972 #22713 video.mp4`，说明"开启后下载的文件名前会加上 #频道ID + #消息ID 前缀（均开启时），方便按频道查找文件"。
- `settings` ref（第 101 行）加 `channelIdPrefix: false`。
- `load`（第 110 行）加 `channelIdPrefix: s.channelIdPrefix === true`。
- `save` payload（第 122 行）加 `channelIdPrefix: settings.value.channelIdPrefix`。

### 3.3 边界行为

| 开关组合 | 下载文件名 |
|---|---|
| 都关 | `22713_video.mp4`（默认模板，无前缀） |
| 只开频道 | `#2070986972 22713_video.mp4` |
| 只开消息 | `#22713 22713_video.mp4` |
| 都开 | `#2070986972 #22713 22713_video.mp4` |
| 批量重命名（任意开关态） | `#2070986972 #22713 新名` |

- 文件名截断（`FilenameMaxLen`）在模板渲染后执行，前缀计入长度 —— 与现有一致，无需改动。
- 前缀内的空格：`#{{ .DialogID }} ` 和 `#{{ .MessageID }} ` 各含一个尾部空格，两个都开时天然形成 `#频道 #消息 `，不会粘连。

---

## 4. 测试

**后端单测**（`server/` 下新增或复用现有测试文件）：
- `buildDlOptions` 模板拼装：mock settings 组合（都关/只频道/只消息/都开），断言 `opts.Template` 前缀正确。
- `handleRenameFile` key 拆分：`"2070986972/22713"` → 前缀 `#2070986972 #22713 `；force 与非 force 分支。

**手动验收**（用户执行，沿用用户工作风格）：
1. clear-cache.bat + Ctrl+Shift+R，设置页打开「文件名添加频道ID前缀」。
2. 新建任务下载，检查文件名四种组合。
3. 批量重命名（右键多选 → 批量重命名），检查 `#频道 #消息 新名`。
4. 设置保存 → 重启 → 值仍在。

---

## 5. 风险与注意

- `dialogID` 可能为负数？—— 频道 ID 是正整数（`tutil.GetInputPeerID` 对 channel 返回正 ID），与现有默认模板行为一致，无新风险。
- 兼容性：`msgIdPrefix` 行为不变；新开关默认关，存量用户无感知。
