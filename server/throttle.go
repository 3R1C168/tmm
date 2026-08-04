package server

import (
	"context"

	"golang.org/x/time/rate"
)

// 前台播放时后台下载的总带宽上限：100KB/s（用户指定）。
const playbackThrottleBPS = 100 * 1024
// 每个下载 chunk 的大小（core/downloader.MaxPartSize），限速按它请求令牌。
// 令牌桶 burst 必须 >= chunk 大小：golang.org/x/time/rate 规定 n > burst 时
// WaitN 直接返回错误（rate.go wait: "Wait(n) exceeds limiter's burst"），
// 若 burst 只有 100KB，WaitN(1MB) 每次都会报错而非等待，限速失效。
const throttleChunkBytes = 1024 * 1024

// throttle 是全局共享的下载限速器（所有任务合计 100KB/s）。
// 令牌桶（golang.org/x/time/rate，go.mod 已有 v0.15.0，无新依赖）。
type throttle struct {
	limiter  *rate.Limiter
	playback *playbackState
}

func newThrottle(p *playbackState) *throttle {
	return &throttle{
		// burst 用 throttleChunkBytes（1MB）：首次 WaitN(1MB) 直接通过（桶满），
		// 之后每个 chunk 等 ~10s 补满，平均速率仍 100KB/s。
		limiter:  rate.NewLimiter(rate.Limit(playbackThrottleBPS), throttleChunkBytes),
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
