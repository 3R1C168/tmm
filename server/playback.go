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
