# tmm 项目交接文档（2026-08-04 会话 6 — 前台播放时后台下载限速）

> **给新对话的完整交接**：本文档汇总本次会话（前台播放限速 spec + plan）全部工作。新对话据此**实施**该功能，无需依赖旧会话上下文。
> 本目录（`tdl/docs/`）为跟随 git 的交接目录；独立交接目录在 `handoff/交接-2026-08-04-6-playback-throttle.md`。
> 上一会话交接（手动全读按钮）见 `handoff/交接-2026-08-04-5-manual-read-all.md`；视口自动已读（已移除）见 `交接-2026-08-04-4-visible-auto-read.md`。

---

## 1. 项目概况（与前会话一致，简述）

- **仓库**：`D:\Documents\workspace\telegram manager\tdl`（git 分支 `master`）
- **GitHub**：`https://github.com/3R1C168/tmm`（私有，主线 `master`）
- **运行目录**：`D:\Documents\workspace\telegram manager\tmm\`
- **运行**：`tmm\start.bat` 或 `./tmm.exe serve --port 8080 --no-browser` → `http://127.0.0.1:8080`
- **构建**：`cd tdl && ./build-tmm.sh`（后端）/ `./build-tmm.sh web`（前端）
- **日志**：`tmm/logs/server-*.log`；**杀进程**：`powershell -Command "Get-Process tmm | Stop-Process -Force"`
- **环境**：Git Bash；Go 1.26.5；gotd v0.140.0；golang.org/x/time/rate v0.15.0（已有）；Vue 3.5 + vue-virtual-scroller@2.0.0-beta.8 + Element Plus

## 2. 当前状态（2026-08-04，本会话结束）

- ✅ 工作树干净（git 无未提交改动）
- ✅ 主线 = `master`，最新 commit **`0afa7f2d`**（限速 plan）
- ✅ 服务器运行中（HTTP 200，端口 8080），exe 版本 `0744658c`（meta 修复后）
- ✅ 登录会话完好：`tmm/data/data/default`（Eric 5401443218）
- ⚠️ 限速功能**待实施**（spec + plan 已就绪）

## 3. 本次会话成果（前台播放限速：spec + plan 已就绪，**待实施**）

**背景**：用户播放视频（MediaViewer 预览）/音频（GlobalPlayer 底部播放器）时，后台下载任务仍全速跑，抢带宽导致播放卡顿。需求：**前台有播放时，后台所有下载任务合计限速 100KB/s**；停止播放恢复全速。

### 3.1 Spec：`docs/superpowers/specs/2026-08-04-playback-download-throttle-design.md`

| 部分 | 设计 |
|---|---|
| 触发 | 前台播放（MediaViewer 视频 / GlobalPlayer 音频）→ 下载限速 100KB/s；停止 → 恢复 |
| 限速粒度 | **总带宽 100KB/s**（所有任务共享一个令牌桶） |
| 实现 | `x/time/rate` 令牌桶（已有依赖），叠加进下载任务现有 `PauseCheck` 钩子（每 chunk 写前调用） |
| 播放状态 | 前端播放/暂停/心跳 → `POST /api/media/playback {playing}` |
| 断连兜底 | 心跳 20s + 超时 30s（前端关页面不报 false 也能自动解除） |
| 播放本身 | `/media/stream` 不经过 PauseCheck，**不受限**（播放流畅优先） |

### 3.2 Plan：`docs/superpowers/plans/2026-08-04-playback-download-throttle.md`

**5 任务（TDD/构建验证，每任务独立 commit）：**

| Task | 内容 | 关键代码位置 |
|---|---|---|
| 1 | 后端 `playback.go`（播放状态 + 端点）+ `throttle.go`（令牌桶 100KB/s）+ Server 字段/路由 | 新文件；`server/server.go` L33 struct、L113 New、L283 路由 |
| 2 | 下载任务 `PauseCheck` 叠加限速（先暂停检查再限速，**全显式解锁**） | `server/dl.go` L399-413 |
| 3 | 前端 `api.js` setPlayback + `utils/playback.js` 共用心跳 + `stores/player.js`（GlobalPlayer）接入 | `web/app/src/api/index.js`、新 `utils/playback.js`、`stores/player.js` L94-124 |
| 4 | `MediaViewer.vue` 视频播放/暂停上报 + 构建部署 | `web/app/src/components/MediaViewer.vue` L40-48 |
| 5 | E2E 实测（限速生效/恢复/断连兜底/多任务总带宽） | — |

### 3.3 关键现有代码（实施时直接复用，勿重写）

```go
// server/dl.go L399 — 现有 PauseCheck（单文件暂停）。改造：先暂停检查（快路径），
// 释放 task.mu 后调 srv.throttle.waitForChunk(ctx) 限速。全显式解锁（勿用 defer）。
PauseCheck: func(elem downloader.Elem) error {
	e, ok := elem.(dl.Elem)
	if !ok {
		return nil
	}
	key := e.Key().String()
	task.mu.Lock()
	if fe := adapter.findFileLocked(key); fe != nil && fe.UserPaused {
		task.mu.Unlock()
		return errFilePaused
	}
	task.mu.Unlock()
	return srv.throttle.waitForChunk(ctx) // 前台播放时全局 100KB/s
},
```

```js
// web/app/src/utils/playback.js（新建，共用）——心跳 20s 续期，防断连后后端一直限速
let _playbackTimer = null
export function reportPlayback(playing) {
  api.setPlayback(playing).catch(() => {})
  if (playing && !_playbackTimer) {
    _playbackTimer = setInterval(() => api.setPlayback(true).catch(() => {}), 20000)
  } else if (!playing && _playbackTimer) {
    clearInterval(_playbackTimer)
    _playbackTimer = null
  }
}
```

## 4. 实施指引（给新对话）

1. **按 Subagent-Driven 流程执行 plan**（本项目功能开发一律 Subagent-Driven，每任务新 subagent + 两阶段 review）
2. **Task 1 可独立验证**：`go build` + `go vet` + curl `POST /api/media/playback` 返回 `{"ok":true}`
3. **Task 2 关键风险**：PauseCheck 改全显式解锁（原 `defer task.mu.Unlock()` 与显式 Unlock 双解锁会 panic）；限速等待期间不持有 task.mu
4. **Task 3/4 需确认**：`req` 是否支持 POST body（api.js）；MediaViewer 用 Plyr 包装 video（事件走 Plyr 还是原生 video，以实际代码为准）；player.js 的 import 位置与 this 上下文
5. **E2E 验证关键**：启动下载任务 → 播放视频/音频 → 等 10s → `/api/tasks` 的 `speed` 降到 ~100KB/s；暂停 → 恢复全速；播放中关页面 → 30s 后恢复（心跳超时）
6. **注意**：后端改动必须重建 exe（`./build-tmm.sh`）；前端 build + `./build-tmm.sh web`

## 5. 本会话附加调研（tdl vs gotd 架构盘点，供了解）

用户问"抛开 tdl 直接用 gotd 可能性/值不值"——已完整盘点：

**结论：不值得整体抛开 tdl**（混合架构已最优）：
- 业务代码**大量直接 gotd**：流式播放引擎（`server/file_downloader.go` 纯裸 `UploadGetFile`）、已读/搜索/分页/评论区全是直接 RPC
- tdl 只在**下载器/连接池/中间件/存储**兜底（`core/` 本地 vendored 副本 ~3600 行，可随意魔改）
- 且**你们魔改了 core/ 15 次**（aria2 式 piece 续传、完整性校验、PauseCheck、速度计算、dcpool 并发修复）——这些是项目核心竞争力，换 gotd 要手工迁移
- 真想减负：砍不用的 `forwarder`/`uploader` 比整体解耦实际

## 6. 已实现功能状态（供新对话了解全貌）

- ✅ **手动全读按钮**（会话 5）：标题栏「全部已读」（确认框）+ 移除视口自动已读 + autoRead 移除；`hasUnread` unreadCount 兜底 + `MARK_READ_MAX` 全读（`5f0fbe55`）
- ✅ **消息卡片视觉**（会话 5）：meta 行日期右对齐留 8px 边距（`0f954f7c`）+ 反应 emoji 独立网格行（`0c446cbf`）
- ✅ **评论区功能**（会话 3）：消息卡片反应/评论数 + 只读评论区弹层——见 `handoff/交接-2026-08-03-3-comments.md`
- ✅ **未读定位 + 已读同步**（会话 3 晚）——见其 §3.12
- 🔄 **前台播放限速**（本次会话）：spec + plan 就绪，**待实施**

## 7. 遗留事项（供新对话）

1. **前台播放限速实施**（本次会话主体）：按 plan 5 任务实施，见 §3/§4
2. **分组/归档只读显示**（会话 3 早）：`docs/superpowers/specs/2026-08-03-folders-archive-design.md` 已写好，**未实施**
3. **作者头像 / 发评论 / 相册反应**：评论区 v2 候选
4. **下载历史/去重提醒**：记录已下载 (频道, msgID)，新建任务时提醒——未做
5. **22713-22717 bug 根因**：加过诊断日志，用户称修好但根因未确认——未深究
6. **IAB 浏览器交互通道不稳定**（环境）：Playwright/CUA 点击间歇性超时、滚动虚拟容器失效——需浏览器 E2E 时注意（可能需用户手动验证）

## 8. 工作风格（沿用）

- 中文交流；一次改一个变量实测；测试前 clear-cache.bat + Ctrl+Shift+R
- 后端改动后必须重建 exe（`./build-tmm.sh`）；前端改动 build + 拷 web 到 tmm
- 性能问题要深挖根因（对比源码定位）；善用浏览器实测定位 CSS，别靠猜
- CSS 视觉统一是硬要求，延续现有 token（`--bg`/`--border`/`--radius`/`--accent`/`--shadow` 等）
- **执行方式**：功能开发一律 Subagent-Driven（subagent-driven-development skill）逐任务执行，每任务新 subagent + 任务间两阶段 review；流程走 spec → writing-plans 计划 → subagent 执行
- **调试经验**：gotd conditional 字段必须用 `GetXxx` helper；频道已读用 `channels.readHistory`；`getHistory` offset_id 排他；浏览器 lazy 加载需滚动才触发
- **tdl 魔改经验**：`core/` 是本仓库 vendored 副本（go.work 指向本地目录），可随意改；`PauseCheck` 是每 chunk 写前钩子（暂停/限速都用它）
