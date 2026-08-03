# 文件级控制（单文件暂停/恢复）设计文档

> 日期：2026-08-03
> 状态：已批准
> 关联：交接文档 §6.4「R3-2 文件级控制：单文件 pause/resume，需后端文件级 API——未做」

---

## 1. 背景与目标

当前 tmm 只有**任务级**控制：暂停/恢复整个任务（`POST /api/tasks/{id}/pause|resume`）。一个任务包含多个文件（一个频道选一批消息，或批量下载），用户无法只暂停其中某一个文件——想停掉一个下载中的大文件，只能把整个任务停掉，其他文件也跟着停。

**目标**：实现**单文件独立暂停/恢复**——暂停一个文件不打断任务里其他文件的下载；恢复该文件时从断点续传。

## 2. 需求（已与用户确认）

1. **粒度**：单文件独立暂停/恢复（文件级），任务级控制保持现有行为。
2. **操作入口**：
   - 右键文件行菜单：单文件「暂停此文件」/「恢复此文件」（按状态对称显示）；
   - 多选批量：右键菜单「暂停选中 N 个文件」/「恢复选中 N 个文件」；
   - 任务级顶部按钮（开始/暂停/删除）保持现有。
3. **任务完成判定**：任务里部分文件被暂停后，其他文件照常下载；全部非暂停文件下完 → 任务显示「已完成」，被暂停的文件保持「已暂停」，之后可单独恢复续传。
4. **恢复语义**：
   - 任务 running 时恢复单个文件 → **立即续传**（从 .tdlstate 断点续）；
   - 任务非 running（done/failed/paused）时恢复 → 复用任务 resume 机制重跑 dl.Run，只下载恢复的文件（其他 finished 文件被跳过）。
5. **正在下载的文件点暂停** → 立即中断（下到一半停，标「已暂停」，断点保留）。
6. **持久化**：用户手动暂停的文件状态持久化，进程重启后保持「已暂停」，不会被任务级 resume 自动带起来。

## 3. 设计

### 3.1 语义模型：区分「任务级暂停」与「文件级手动暂停」

冲突点：若不做区分，任务级 resume 重跑 dl.Run 时会把用户手动暂停的文件也一起续传，破坏「手动暂停留待恢复」的语义。

**决策**：`FileEntry` 新增布尔字段 `UserPaused`（`json:"userPaused"`）。

| 操作 | Status | UserPaused |
|---|---|---|
| 文件级暂停（右键/批量） | `FilePaused` | `true` |
| 文件级恢复 | `FilePending`（排队等待下载） | `false` |
| 任务级暂停（现有） | 下载中的文件 `FilePaused`、排队中的保持 `FilePending`（现有逻辑） | 不变（保持原值） |
| 任务级 resume（现有） | 重跑 dl.Run | `UserPaused==true` 的文件保持跳过 |

- `UserPaused` 持久化在 store（`Task` 序列化随 `Files` 一起落盘），重启后仍记住。
- UI 显示不区分两种暂停（都显示「已暂停」），仅内部语义区分。

### 3.2 下载链路「暂停检查」钩子（core/downloader + app/dl）

**core/downloader**：
- `Options` 新增可选字段：`PauseCheck func(elem Elem) error`（nil 表示不检查，行为与现状逐字节一致）。
- `writeAt` 新增 `pauseCheck` 字段；`WriteAt` 每次写入前调用，返回非 nil 错误即中止该文件下载。
- 构造链路：`downloader.New(opts)` → `newWriteAt(elem, d.opts.Progress, MaxPartSize)` 传入 `d.opts.PauseCheck`。

**app/dl**：
- `Options` 新增同名字段 `PauseCheck func(elem downloader.Elem) error`，`dl.Run` 里透传给 `downloader.Options.PauseCheck`。

**为什么用 `context.Canceled` 作为中断错误**：
- `downloader.download` 对 `context.Canceled` 有 `errors.Is` 分支直接返回；
- server 的 `progressAdapter.OnDone` 已有 `errors.Is(ferr, context.Canceled) → FilePaused` 判定（`server/dl.go`），中断判定零新增代码；
- 该中断只作用于**当前文件的下载**，不动 `errgroup.WithContext(ctx)` 的 wgctx，其他文件的下载不受影响。

**覆盖两条下载路径**：全新下载（gotd `Parallel`）与 resume（`fetchPiece`）都经 `writeAt.WriteAt`，钩子对两条路径均生效。

### 3.3 iter 层「暂停集合 + 恢复队列（requeue）」（app/dl/iter.go）

iter 新增字段：
```go
paused  map[MsgKey]struct{} // 暂停中的文件：processSingle 跳过，不产出 elem，不标 finished
requeue []MsgKey            // 恢复队列：Next 时优先重新产出
```

- **跳过**：`processSingle` 开头检查 `paused` 命中 → `return false, true`（skip）。命中时**不**调用 `Finish`，保证恢复后能重新下载。
- **初始化**：`adapter.hook(it)` 时从 `task.Files` 收集 `UserPaused==true` 的文件 key 调 `iter.PauseKeys`——任务级 resume 重跑时，用户手动暂停的文件保持跳过。
- **恢复（requeue）**：`iter.Unpause(key)` → 从 `paused` 移除 + 追加到 `requeue`。
- **requeue 产出（状态机隔离，核心）**：
  - `Next` 循环开头检查 `requeue` 非空：弹出队首 key，经**独立索引**（由 `dialogs` 构建 `peerID → []msgID` + peer 本体）取回 `(peer, msgID)`，走与 `processSingle` 相同构造逻辑产出 elem（复用 template 渲染、SkipSame、resume state 打开、`i.elem <-` 等路径）。
  - **不触碰** `dialogIndex` / `messageIndex` / `logicalPos`（主状态机完全隔离），避免插队导致顺序/进度记录错乱。
  - requeue 的文件未标 finished，其 resume state 文件仍在磁盘（.tdlstate），续传自然发生。
- iter 新增公开方法：`PauseKeys(keys []MsgKey)`、`Unpause(key MsgKey)`、`PausedKeys() []MsgKey`（供 server 读取）。

### 3.4 server 侧（model / task / handler / router）

**API（单端点，风格对齐 `/api/tasks/batch`）**：
```
POST /api/tasks/{id}/files/batch
body: { "action": "pause" | "resume", "keys": ["peerID/msgID", ...] }
resp: { "results": [ { "key": "...", "ok": true|false, "error": "..." } ] }
```
- 404：任务不存在。
- 400：action 非法、keys 为空、key 不属该任务。

**pause 流程**：
1. `task.mu` 下：对每个 key，`Status→FilePaused`、`UserPaused=true`（仅对 `FileRunning`/`FilePending` 的文件；`FileDone`/`FileFailed` 不动）。
2. 任务 running：`iter.PauseKeys(keys)` —— 排队中的被 iter 跳过，**正在下载的**由 PauseCheck 钩子即刻中断（每次 WriteAt 查 `UserPaused`）。
3. `publishTask` + `store.upsert`。

**resume 流程**：
1. `task.mu` 下：对每个 key（仅 `FilePaused` 且 `UserPaused==true`），`UserPaused=false`、`Status→FilePending`。
2. 任务 running：`iter.Unpause(key)` → requeue 即时续传。
3. 任务非 running：复用 `taskManager.resume(id)` 重跑 dl.Run。重跑时 hook 初始化 `paused` 只含剩余 `UserPaused==true` 文件，其余 finished 文件被 `isFinished` 跳过 → **只下恢复的文件**。

**run() 成功分支修正（关键，防止破坏「留待恢复」语义）**：
- 现有 `task.go:run()` 成功分支把 `FilePending || FilePaused` 的文件标 `FileDone`（为处理 SkipSame 跳过残留）。若不改，任务其他文件下完后文件级手动暂停的文件会被强制标成「已完成」，无法再恢复。
- **修改**：成功分支只把 `FilePending` 标 `FileDone`；`FilePaused` 的文件保持 paused（此时剩余的 `FilePaused` 只能是 `UserPaused==true` 的手动暂停文件——任务级暂停产生的 paused 文件在 resume 重跑时会重新进入下载流、由 OnDone 正常置 done）。
- 保持 `FilePending → FileDone` 是为兼容 SkipSame 跳过场景（iter 不产出、无 OnDone，残留 pending 需标 done）。

**progressAdapter**：
- 持 iter 引用（`hook` 已有），提供 `pauseFiles(keys)` / `resumeFiles(keys)` 包装（内部锁序：先 `task.mu` 后 `iter.mu`，单向，防死锁）。
- 注入 `PauseCheck`：`buildDlOptions` 里设置
  ```go
  PauseCheck: func(elem downloader.Elem) error {
      key := elem.Key().String()
      task.mu.Lock(); defer task.mu.Unlock()
      if fe := findFileLocked(key); fe != nil && fe.UserPaused {
          return context.Canceled
      }
      return nil
  }
  ```

### 3.5 前端（web/app/src/views/Tasks.vue + api.js）

- `api.js` 新增 `fileBatchAction(taskId, action, keys)`。
- 右键菜单（非批量，`ctxMenu.batch == false`）：
  - 文件 `running`/`pending` → 「暂停此文件」；
  - 文件 `paused` → 「恢复此文件」；
  - `done`/`failed` → 不显示暂停/恢复项。
- 右键菜单（批量，选中 >1）：选中中有非 done 文件 → 「暂停选中 N 个文件」；选中中有 paused → 「恢复选中 N 个文件」。
- 恢复后文件短暂显示「排队中」→ OnAdd 后「下载中」，属正常流转，不做额外处理。
- 样式沿用现有 `.ctx-item`，视觉 token 不变。

### 3.6 错误处理

- 暂停/恢复不存在的 key：`results` 里该项 `ok=false` + error，其他项照常处理（非事务，对齐现有 `Batch` 行为）。
- 任务已删除：404。
- 对 `done`/`failed` 文件执行暂停：跳过（ok=false，原因「文件已完成/失败」）。
- 对非 `UserPaused` 的 paused 文件执行恢复（任务级暂停产生的 paused）：**允许**，等同文件级恢复（清除 UserPaused 并续传）。
- PauseCheck 钩子返回错误只中断单文件，不影响任务、不写 FLOOD_WAIT 等副作用。

## 4. 测试

### 4.1 后端单测
| 层 | 用例 |
|---|---|
| core/downloader | PauseCheck 返回 error → `WriteAt` 不再写入、`download` 返回该错误；PauseCheck 为 nil 时行为不变 |
| app/dl iter | `paused` 文件 skip 不产出；`Unpause` 后 requeue 重新产出；requeue 不改变 dialogIndex/messageIndex 主序（参照 `iter_test.go` 模式） |
| server | files/batch API：pause 置 `UserPaused+FilePaused`、resume 状态流转、非法 action/key 校验（参照 `task_batch_test.go` 模式） |

### 4.2 回归
现有 `task_batch_test.go`、`resume_test.go`、`media_stream_test.go`、`settings_test.go` 全绿。

### 4.3 手动浏览器验证清单
1. 多文件任务运行中 → 暂停其中 1 个 → 其他文件继续下 → 该文件「已暂停」
2. 运行中恢复 → 从断点续传（进度不回退）
3. 任务级 pause → resume → **用户手动暂停的文件不被自动恢复**
4. 任务 done 后恢复文件 → 任务重跑只下该文件
5. 重启服务器 → 手动暂停文件保持「已暂停」（UserPaused 持久化）
6. 文件级暂停不触发 FLOOD_WAIT、服务器日志无异常

## 5. 风险与缓解

| 风险 | 等级 | 缓解 |
|---|---|---|
| requeue 与 iter 主状态机冲突（顺序/进度错乱） | 中 | requeue 走独立索引+独立产出路径，不碰 dialogIndex/messageIndex/logicalPos |
| PauseCheck 影响两条下载路径（Parallel / fetchPiece） | 低 | 两条路径都经 writeAt；可选回调 nil 时零行为变化；tdl CLI 不传该回调 |
| 任务级 pause/resume 回归 | 低 | 现有路径不动；UserPaused 仅新增字段；跑现有测试回归 |
| task.mu / iter.mu 锁死 | 低 | 锁序固定为 task.mu → iter.mu 单向；PauseCheck 只读 task.mu |
| 恢复后状态流转异常 | 低 | OnAdd existing 命中 → FileRunning 已有逻辑；diskComplete 兜底已下完边界 |

## 6. 非目标（明确不做）

- 单文件删除（有任务级删除带文件；文件级删除单文件不在本需求）。
- 下载历史/去重提醒（交接遗留事项 §6.3，另一功能）。
- 文件级速率限制、优先级排序。
- **任务组级控制**（组级「开始」优先恢复组内所有暂停文件）——用户 2026-08-03 确认本次仅做单文件级控制，组级优先语义不在本需求。
