# 前台播放时后台下载限速 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 前台播放视频/音频（MediaViewer 预览 + GlobalPlayer 底部播放器）时，后台所有下载任务合计限速 100KB/s；停止播放恢复全速。

**Architecture:** 后端新增全局播放状态（`server/playback.go`）+ 全局限速器（`server/throttle.go`，x/time/rate 令牌桶 100KB/s），叠加进下载任务的现有 `PauseCheck` 钩子（每 chunk 写前调用）；前端在播放/暂停/心跳时通过新端点 `POST /api/media/playback` 上报状态。播放本身（`/media/stream`）不经过 PauseCheck，不受限。

**Tech Stack:** Go 1.26.5 / gotd v0.140.0 / golang.org/x/time/rate（已有 v0.15.0）/ Vue 3.5 / Element Plus

## Global Constraints

- 后端改动必须重建 exe：`./build-tmm.sh`；前端 `cd web/app && npm run build` + `./build-tmm.sh web`
- 注释中文；样式用 token（禁硬编码色值）
- **限速只影响后台下载任务**（dl 的 chunk 写入），不影响 `/media/stream`（播放取流）
- 限速粒度：**总带宽 100KB/s**（所有任务共享一个令牌桶）
- 断连兜底：心跳 20s + 超时 30s（前端关页面不报 false 也能自动解除限速）
- **不引入新依赖**（golang.org/x/time/rate 已在 go.mod）
- PauseCheck 叠加限速**不得破坏现有暂停语义**（暂停检查在限速前，快速路径）

---

### Task 1: 后端播放状态 + 全局限速器 + 路由

**Files:**
- Create: `server/playback.go`（playbackState + handlePlayback）
- Create: `server/throttle.go`（throttle + waitForChunk）
- Modify: `server/server.go`（Server struct 加字段、New 初始化、路由注册）

**Interfaces:**
- Consumes: `writeErr`/`writeJSON`/`errString`（server 已有）、`*playbackState`/`*throttle`（本任务产出）
- Produces:
  - `newPlaybackState() *playbackState`；`(*playbackState).setPlaying(playing bool)`；`(*playbackState).isPlaying() bool`（playing && 30s 内续期）
  - `newThrottle(p *playbackState) *throttle`；`(*throttle).waitForChunk(ctx) error`（播放中限 100KB/s，非播放立即返回）
  - `POST /api/media/playback`（body `{"playing": bool}`）
  - Server 字段：`playback *playbackState`、`throttle *throttle`（Task 2 消费）

- [ ] **Step 1: 建 `server/playback.go`**

```go
package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// playbackState 维护"前台是否有媒体播放"。前端在播放/暂停/心跳时上报。
// lastActive 用于断连兜底：30s 无更新视为已停止（前端关页面没发 false 也能自动恢复）。
type playbackState struct {
	mu         sync.Mutex
	playing    bool
	lastActive time.Time
}

func newPlaybackState() *playbackState {
	return &playbackState{lastActive: time.Now()}
}

func (p *playbackState) setPlaying(playing bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.playing = playing
	p.lastActive = time.Now()
}

// isPlaying 返回当前是否处于"播放中"：playing 且 30s 内有心跳续期。
func (p *playbackState) isPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing && time.Since(p.lastActive) <= 30*time.Second
}

// handlePlayback 接收前端播放状态上报（播放开始/停止/心跳）。
func (s *Server) handlePlayback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Playing bool `json:"playing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errString("请求体无效"))
		return
	}
	s.playback.setPlaying(req.Playing)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
```

- [ ] **Step 2: 建 `server/throttle.go`**

```go
package server

import (
	"context"

	"golang.org/x/time/rate"
)

// 前台播放时后台下载的总带宽上限：100KB/s（用户指定）。
const playbackThrottleBPS = 100 * 1024
// 每个下载 chunk 的大小（core/downloader.MaxPartSize），限速按它请求令牌。
const throttleChunkBytes = 1024 * 1024

// throttle 是全局共享的下载限速器（所有任务合计 100KB/s）。
// 令牌桶（golang.org/x/time/rate，go.mod 已有 v0.15.0，无新依赖）。
type throttle struct {
	limiter  *rate.Limiter
	playback *playbackState
}

func newThrottle(p *playbackState) *throttle {
	return &throttle{
		limiter:  rate.NewLimiter(rate.Limit(playbackThrottleBPS), playbackThrottleBPS),
		playback: p,
	}
}

// waitForChunk 在写一个 chunk 前调用：播放中则按 100KB/s 领取令牌（约等 10s/MB），
// 非播放立即返回（零开销）。ctx 取消时返回错误（任务取消路径，不影响暂停语义）。
func (t *throttle) waitForChunk(ctx context.Context) error {
	if !t.playback.isPlaying() {
		return nil
	}
	return t.limiter.WaitN(ctx, throttleChunkBytes)
}
```

- [ ] **Step 3: `server/server.go` 加字段 + 初始化 + 路由**

Server struct（L33 附近）加：
```go
	playback *playbackState // 前台播放状态（限速调度用）
	throttle *throttle      // 前台播放时后台下载的全局限速器（100KB/s）
```

`New` 函数（L113 附近，`s := &Server{...}` 内）加：
```go
		playback:     newPlaybackState(),
```

`New` 函数末尾（`return s` 前）加：
```go
	s.throttle = newThrottle(s.playback)
```

路由注册（L283-286 `/media/...` 附近）加：
```go
	api.HandleFunc("/media/playback", s.handlePlayback).Methods(http.MethodPost)
```

- [ ] **Step 4: 构建 + 路由验证**

Run: `cd "D:/Documents/workspace/telegram manager/tdl" && go build ./...`
Expected: 成功

Run: `cd "D:/Documents/workspace/telegram manager/tdl" && go vet ./server/`
Expected: 无警告

- [ ] **Step 5: Commit**

```bash
cd "D:/Documents/workspace/telegram manager/tdl"
git add server/playback.go server/throttle.go server/server.go
git commit -m "feat(server): 前台播放状态 + 全局限速器（令牌桶 100KB/s）+ POST /api/media/playback"
```

---

### Task 2: 下载任务 PauseCheck 叠加限速

**Files:**
- Modify: `server/dl.go:399-413`（buildDlOptions 的 PauseCheck）

**Interfaces:**
- Consumes: `srv.throttle.waitForChunk(ctx)`（Task 1）、`dl.Elem`（已有）、`errFilePaused`（已有）
- Produces: PauseCheck 每 chunk 写前：暂停检查（快路径）→ 限速等待

- [ ] **Step 1: 改造 PauseCheck**

`server/dl.go` L399-413 现有（用 `defer task.mu.Unlock()`）：
```go
		PauseCheck: func(elem downloader.Elem) error {
			// downloader.Elem 无 Key()，需断言为 dl.Elem（progressAdapter.OnAdd 同款用法）
			e, ok := elem.(dl.Elem)
			if !ok {
				return nil
			}
			key := e.Key().String()
			task.mu.Lock()
			defer task.mu.Unlock()
			if fe := adapter.findFileLocked(key); fe != nil && fe.UserPaused {
				return errFilePaused
			}
			return nil
		},
```
改为（**全显式解锁**，避免与 defer 双解锁 panic；限速等待不持有锁）：
```go
		PauseCheck: func(elem downloader.Elem) error {
			// downloader.Elem 无 Key()，需断言为 dl.Elem（progressAdapter.OnAdd 同款用法）
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
			// 限速：前台播放时全局 100KB/s（写 chunk 前等待令牌）。
			// 必须释放 task.mu 再等待（限速可能等 ~10s，不能持有锁阻塞其他文件）。
			return srv.throttle.waitForChunk(ctx)
		},
```

> 确认：`buildDlOptions` 签名是 `func (srv *Server) buildDlOptions(ctx context.Context, a *account, task *Task, adapter *progressAdapter) (dl.Options, error)`——ctx 和 srv 都可用。

- [ ] **Step 2: 构建验证**

Run: `cd "D:/Documents/workspace/telegram manager/tdl" && go build ./... && go vet ./server/`
Expected: 成功

- [ ] **Step 3: Commit**

```bash
cd "D:/Documents/workspace/telegram manager/tdl"
git add server/dl.go
git commit -m "feat(server): 下载任务 PauseCheck 叠加限速——前台播放时全局 100KB/s（先暂停检查再限速）"
```

---

### Task 3: 前端 api + 共用心跳 + GlobalPlayer 接入

**Files:**
- Modify: `web/app/src/api/index.js`（setPlayback）
- Create: `web/app/src/utils/playback.js`（reportPlayback 共用心跳）
- Modify: `web/app/src/stores/player.js`（GlobalPlayer 播放/暂停上报）

**Interfaces:**
- Consumes: `api.setPlayback(playing)`（本任务产出）
- Produces: `reportPlayback(playing)`（含心跳 20s 管理），player.js 与 MediaViewer.vue（Task 4）共用

- [ ] **Step 1: `api/index.js` 加 setPlayback**

在 `markRead` 附近加：
```js
  // 播放状态上报：前台播放/停止时通知后端限速调度（后台下载限速 100KB/s）
  setPlayback: (playing) => req('POST', '/api/media/playback', { playing }),
```

> 确认 `req` 支持 POST + body：现有 `markRead: (chatId, maxId) => req('POST', \`/api/chats/${chatId}/read?max_id=${maxId}\`)` 用 query；需确认 `req` 第三个参数 body 或另写。若 `req` 不支持 body，用 `fetch` 直接 POST JSON（看现有 POST body 用法，如 `/api/media/playback` 需 body）。

- [ ] **Step 2: 建 `web/app/src/utils/playback.js`**

```js
import api from '../api'

// 播放状态上报 + 心跳：前台播放时通知后端限速（后台下载限速 100KB/s）。
// 心跳 20s 续期 lastActive，防前端断连后后端一直限速（30s 无续期自动解除）。
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

> 确认 `api/index.js` 的导出方式（`export default` 还是命名导出），import 路径对应。

- [ ] **Step 3: `stores/player.js`（GlobalPlayer）接入**

现有：
```js
    _onPlay() { this.playing = true; this.loading = false },
    _onPause() { this.playing = false },
    _onEnded() { this.playing = false }, // 单曲播完即停，不自动连播
```
改为：
```js
    _onPlay() { this.playing = true; this.loading = false; reportPlayback(true) },
    _onPause() { this.playing = false; reportPlayback(false) },
    _onEnded() { this.playing = false; reportPlayback(false) }, // 单曲播完即停，不自动连播
```

`close()`（L94-98 `this.playing = false` 处）后加 `reportPlayback(false)`。

文件头加：
```js
import { reportPlayback } from '../utils/playback'
```

> 确认 player.js 是 store 还是普通对象（`_onPlay` 是方法，需确认 this 上下文与 import 位置）。

- [ ] **Step 4: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 5: Commit**

```bash
cd "D:/Documents/workspace/telegram manager/tdl"
git add web/app/src/api/index.js web/app/src/utils/playback.js web/app/src/stores/player.js
git commit -m "feat(web): 播放状态上报 + 共用心跳（20s 续期）——GlobalPlayer 播放/暂停通知后端限速"
```

---

### Task 4: MediaViewer 视频接入 + 构建部署

**Files:**
- Modify: `web/app/src/components/MediaViewer.vue`（video 播放/暂停/结束上报）
- 无其他（验证）

**Interfaces:**
- Consumes: `reportPlayback(playing)`（Task 3 产出）
- Produces: MediaViewer 视频播放时上报播放状态

- [ ] **Step 1: MediaViewer.vue 接入**

文件头加：
```js
import { reportPlayback } from '../utils/playback'
```

video 元素（L40-48）或 Plyr 事件加播放/暂停/结束监听：
```html
<video
  ref="videoEl"
  class="mv-video"
  @play="onVideoPlay"
  @pause="onVideoPause"
  @ended="onVideoPause"
  ...
>
```

script 加：
```js
// 视频播放状态上报：播放→限速后台下载；暂停/结束→恢复
function onVideoPlay() { reportPlayback(true) }
function onVideoPause() { reportPlayback(false) }
```

> 注意：MediaViewer 用 Plyr 包装 video（setupPlyr），事件可能走 Plyr 的 `@play`/`@pause` 或原生 video 事件。**以实际代码为准**——若 Plyr 实例有事件监听（`plyr.on('play', ...)`），在那里加；若原生 video 事件可用，直接 `@play`/`@pause`。切换上/下一条、关闭弹层时也要上报 false（在关闭路径加 `reportPlayback(false)`）。

- [ ] **Step 2: 构建验证**

Run: `cd "/d/Documents/workspace/telegram manager/tdl/web/app" && npm run build`
Expected: 成功

- [ ] **Step 3: 部署 + 重建后端 exe**

Run: `cd "D:/Documents/workspace/telegram manager/tdl" && ./build-tmm.sh web && ./build-tmm.sh`
Expected: 前端部署 + 后端 exe 重建（Task 1-2 的后端改动在此生效）

- [ ] **Step 4: Commit**

```bash
cd "D:/Documents/workspace/telegram manager/tdl"
git add web/app/src/components/MediaViewer.vue
git commit -m "feat(web): MediaViewer 视频播放/暂停上报后端限速（共用 reportPlayback）"
```

---

### Task 5: E2E 实测（真实浏览器）

**Files:** 无（验证）

**Interfaces:** Consumes Task 1-4 全部

- [ ] **Step 1: 重启服务**

杀进程 + `cd "D:/Documents/workspace/telegram manager/tmm" && ./tmm.exe serve --port 8080 --no-browser`
验证 `curl -s -X POST http://127.0.0.1:8080/api/media/playback -d '{"playing":true}' -H "Content-Type: application/json"` 返回 `{"ok":true}`

- [ ] **Step 2: E2E 实测点**

1. 启动一个下载任务（后台跑）→ 记录下载速度（全速）
2. 前台播放视频（MediaViewer）/音频（GlobalPlayer）→ 等 10s → 下载速度降到 ~100KB/s（`/api/tasks` 的 `speed` 字段）
3. 暂停播放 → 等 10s → 下载恢复全速
4. 播放中直接关页面 → 等 30s+ → 下载恢复全速（心跳超时兜底）
5. 多任务并发下载 + 播放 → 总带宽 ≤100KB/s
6. 非播放时下载速度不受影响（零开销）

- [ ] **Step 3: 风险记录**

若限速未生效/速度不对/暂停语义破坏，记录现象，按 spec 边界调整（锁、令牌桶参数、心跳间隔）。

---

## Self-Review（writing-plans 要求）

**Spec 覆盖核对：**
- 后端播放状态 + 端点 → Task 1 ✓
- 全局限速器（100KB/s 令牌桶）→ Task 1 ✓
- PauseCheck 叠加限速 → Task 2 ✓
- 前端 api + 心跳 → Task 3 ✓
- GlobalPlayer + MediaViewer 上报 → Task 3 + Task 4 ✓
- 部署 + E2E → Task 4 + Task 5 ✓
- 断连兜底（心跳 20s + 超时 30s）→ Task 1（isPlaying 30s）+ Task 3（心跳 20s）✓

**占位符扫描：** 无 TBD/TODO；代码块完整（源码接入点经核实：server.go L33/L113/L283、dl.go L399、api/index.js、player.js L94-124、MediaViewer.vue L40-48）。

**类型一致性：** `newPlaybackState`/`setPlaying`/`isPlaying`/`newThrottle`/`waitForChunk` 各 Task 签名一致；`reportPlayback`（Task 3 定义）Task 3/4 共用；`srv.throttle`（Task 1 产出）Task 2 消费——无冲突。

**依赖顺序：** Task 2 依赖 Task 1（throttle）；Task 4 依赖 Task 3（reportPlayback）；Task 5 依赖全部。前端 Task 3/4 与后端 Task 1/2 可并行（不同目录），但部署（Task 4 Step 3）需后端 Task 1-2 先完成。

**风险：** MediaViewer 的 Plyr 事件接入点需实施时确认（原生 video vs Plyr 事件）；`req` 是否支持 POST body 需确认；PauseCheck 的 ctx 取消语义（限速等待返回 context.Canceled 走任务取消路径，不影响暂停）。
