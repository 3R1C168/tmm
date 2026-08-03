# 前台播放时后台下载限速 设计（2026-08-04）

## 背景

用户播放视频（MediaViewer 预览）/音频（GlobalPlayer 底部播放器）时，后台下载任务仍全速跑，抢带宽导致播放卡顿。需求：**前台有播放时，后台所有下载任务合计限速 100KB/s**；停止播放恢复全速。

## 目标

1. 前台播放（MediaViewer 视频预览 / GlobalPlayer 音频播放）时 → 后台**所有下载任务合计**限速 100KB/s
2. 播放暂停/停止/关闭 → 下载恢复全速
3. 播放本身（`/media/stream` 取流）**不受限**——播放流畅优先
4. 前端断连（直接关页面）时自动解除限速（心跳超时兜底）

## 设计

### 架构总览

```
前端播放视频/音频
  → api.setPlayback(true) POST /api/media/playback
  → server playbackState.playing=true, lastActive=now
  → 心跳每 20s 续期 lastActive
下载任务每写 1MB chunk → PauseCheck → throttle.waitForChunk
  → 播放中：令牌桶 100KB/s → 每 chunk 等 ~10s → 总下载 ≤100KB/s
  → 非播放：立即返回（零开销）
前端暂停/关闭
  → api.setPlayback(false) → playing=false → 解除限速
断连兜底：30s 无 lastActive 更新 → isPlaying()=false → 自动解除
```

### 组件

#### 1. `server/playback.go`（新建）：全局播放状态

```go
// playbackState 维护"前台是否有媒体播放"。前端在播放/暂停/心跳时上报。
// lastActive 用于断连兜底：30s 无更新视为已停止（前端关页面没发 false 也能自动恢复）。
type playbackState struct {
	mu         sync.Mutex
	playing    bool
	lastActive time.Time
}

func newPlaybackState() *playbackState

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
```

**端点**：`POST /api/media/playback`，body `{"playing": true|false}`

```go
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

#### 2. `server/throttle.go`（新建）：全局限速器

```go
// 前台播放时后台下载的总带宽上限：100KB/s（用户指定）。
const playbackThrottleBPS = 100 * 1024
// 每个下载 chunk 的大小（core/downloader.MaxPartSize），限速按它请求令牌。
const chunkBytes = 1024 * 1024

// throttle 是全局共享的下载限速器（所有任务合计 100KB/s）。
// 令牌桶（golang.org/x/time/rate，go.mod 已有 v0.15.0，无新依赖）。
type throttle struct {
	limiter *rate.Limiter
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
	return t.limiter.WaitN(ctx, chunkBytes)
}
```

#### 3. `server/dl.go` 改造：PauseCheck 叠加限速

`buildDlOptions` 的现有 PauseCheck（L399-413，单文件暂停检查）**叠加限速**——先检查暂停（快速路径），再限速等待：

```go
PauseCheck: func(elem downloader.Elem) error {
	// 先检查用户手动暂停（快路径，暂停的文件不参与限速等待）
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
	// 注意：必须释放 task.mu 再等待（限速可能等 ~10s，不能持有锁阻塞其他文件）。
	return srv.throttle.waitForChunk(ctx)
},
```

> 锁处理：现有代码用 `defer task.mu.Unlock()`，叠加限速后**必须改为全显式解锁**（否则 waitForChunk 前的显式 Unlock 与 defer 双解锁 panic）。上面所有 return 路径都已显式解锁。

> ctx 来源：`buildDlOptions(ctx, ...)` 已有 ctx 参数（task 运行上下文）。限速等待期间任务取消 → ctx.Done → WaitN 返回错误 → PauseCheck 返回错误 → 该文件中止走任务取消路径（语义不变）。

#### 4. 前端

**`web/app/src/api/index.js`** 加方法：

```js
// 播放状态上报：前台播放/停止时通知后端限速调度（后台下载限速 100KB/s）
setPlayback: (playing) => req('POST', '/api/media/playback', { playing }),
```

**`web/app/src/stores/player.js`**（GlobalPlayer 音频播放器）：
- `_onPlay()`（L120 `this.playing = true`）后：`reportPlayback(true)` + 启动心跳
- `_onPause()` / `_onEnded()` / `close()`（L121/124/98 `this.playing = false`）后：`reportPlayback(false)` + 停心跳
- 心跳：播放中每 20s `reportPlayback(true)`（续期 lastActive）

```js
// 播放状态上报 + 心跳（防前端断连后后端一直限速：30s 无续期自动解除）
let _playbackTimer = null
function reportPlayback(playing) {
  api.setPlayback(playing).catch(() => {})
  if (playing && !_playbackTimer) {
    _playbackTimer = setInterval(() => api.setPlayback(true).catch(() => {}), 20000)
  } else if (!playing && _playbackTimer) {
    clearInterval(_playbackTimer)
    _playbackTimer = null
  }
}
```

**`web/app/src/components/MediaViewer.vue`**（视频预览）：
- Plyr 播放器事件：`@play` → `reportPlayback(true)` + 心跳；`@pause` / `@ended` → `reportPlayback(false)` + 停心跳
- 心跳逻辑与 player.js 共用（抽到 `utils/playback.js` 或各自实现——**优先共用**）

**`web/app/src/utils/playback.js`**（新建，共用）：导出 `reportPlayback(playing)`（含心跳管理），player.js 和 MediaViewer.vue 都引用。

### 关键决策

| 决策 | 选择 | 理由 |
|---|---|---|
| 限速粒度 | 全局 100KB/s（所有任务合计） | 用户确认；令牌桶天然全局共享 |
| 限速挂钩 | 现有 PauseCheck（每 chunk 写前调用） | 已有钩子，魔改最小；先暂停检查再限速 |
| 播放本身是否受限 | 不受限（/media/stream 不经过 PauseCheck） | 播放流畅优先 |
| 前端→后端通道 | 新增 `POST /api/media/playback` | 现有无前端→后端通道（只有 SSE 后端→前端） |
| 断连兜底 | 心跳 20s + 超时 30s | 前端关页面不报 false 也能自动解除 |
| 多播放器 | 任一在播即限速（playing 全局布尔） | 简单，符合"有播放就限" |
| 限速依赖 | golang.org/x/time/rate（已有 v0.15.0） | 无新依赖 |

### 边界 / 风险

- **限速 sleep 阻塞**：播放中 PauseCheck 等令牌 ~10s/chunk，该文件下载 goroutine 阻塞——下载变慢是预期（限速目的）
- **尾部小块**：<1MB 的 chunk 也请求 1MB 令牌 → 略保守（无害）
- **锁竞争**：限速等待期间不能持有 task.mu（否则所有文件暂停检查被阻塞）——先释放锁再 waitForChunk
- **ctx 取消**：限速等待返回 context.Canceled 时，走任务取消路径（不影响暂停语义）
- **播放状态残留**：心跳超时 30s 自动恢复；播放中正常心跳续期不会误解除

## 测试

- 无前端单测基建 → `npm run build` + E2E
- E2E 实测点：
  1. 启动下载任务（后台跑）→ 记录下载速度（全速）
  2. 前台播放视频（MediaViewer）/音频（GlobalPlayer）→ 等 10s → 下载速度降到 ~100KB/s
  3. 暂停播放 → 等 10s → 下载恢复全速
  4. 播放中直接关页面 → 等 30s+ → 下载恢复全速（心跳超时兜底）
  5. 多任务并发下载 + 播放 → 总带宽 ≤100KB/s
  6. 非播放时下载速度不受影响（零开销）

## 不做的事

- 不做"限速设置项"（固定 100KB/s，不做 UI 配置）
- 不做"播放体验分级"（如 4K 视频更高限速）
- 不改 /media/stream 取流（播放流畅优先）
- 不引入新依赖（x/time/rate 已有）

## 风险

- PauseCheck 叠加限速是本功能核心——需确保不破坏现有暂停语义（暂停检查在限速前，快速路径）
- 心跳/超时参数（20s/30s）为经验值，可后续调整
