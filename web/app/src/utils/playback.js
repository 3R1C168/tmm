import { api } from '../api'

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
