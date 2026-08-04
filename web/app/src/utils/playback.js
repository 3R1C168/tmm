import { api } from '../api'

// 播放状态上报 + 心跳：前台有任一播放源在播时，通知后端限速（后台下载合计 100KB/s）。
// 引用计数：GlobalPlayer 音频与 MediaViewer 视频可同时在播，任一源停止不解除限速，
// 全部停止才上报 false 并停心跳。心跳 20s 续期，防断连后后端一直限速（30s 超时自动解除）。
let _playbackTimer = null
let _activeCount = 0

export function reportPlayback(playing) {
  if (playing) {
    const wasIdle = _activeCount === 0
    _activeCount++
    if (!wasIdle) return // 已有源在播，不重复上报/重启心跳
    api.setPlayback(true).catch(() => {})
    _playbackTimer = setInterval(() => api.setPlayback(true).catch(() => {}), 20000)
  } else {
    if (_activeCount > 0) _activeCount--
    if (_activeCount > 0) return // 还有其他源在播，保持限速
    api.setPlayback(false).catch(() => {})
    if (_playbackTimer) { clearInterval(_playbackTimer); _playbackTimer = null }
  }
}
