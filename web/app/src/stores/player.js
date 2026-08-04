import { defineStore } from 'pinia'
import { api } from '../api'
import { isAudio as isAudioMedia } from '../utils/media'
import { reportPlayback } from '../utils/playback'

// Global audio player: a single <audio> element (owned by GlobalPlayer.vue,
// outside the virtual-scrolled message list and any route) driven by this
// store. Playback survives scrolling, chat switches, and route changes, and
// only one track can ever play at a time (natural mutual exclusion).
//
// The float bar is visible whenever `current` is set (playing OR paused);
// it hides only when `current` is null (nothing loaded / closed).
export const usePlayerStore = defineStore('player', {
  state: () => ({
    current: null,      // { chatId, msgId, name, size } | null
    playing: false,
    currentTime: 0,
    duration: 0,
    volume: 1,
    muted: false,
    loading: false,
    error: '',
    queue: [],          // [{ chatId, msgId, name, size }] newest-first, for prev/next
    queueIndex: -1,
    locateMsgId: 0,     // set to ask Messages view to scroll a message into view
    _el: null,          // the single <audio> element, registered by GlobalPlayer
  }),

  getters: {
    hasTrack: (s) => !!s.current,
    hasPrev: (s) => s.queueIndex > 0,
    hasNext: (s) => s.queueIndex >= 0 && s.queueIndex < s.queue.length - 1,
    progress: (s) => (s.duration > 0 ? s.currentTime / s.duration : 0),
  },

  actions: {
    registerEl(el) {
      this._el = el
      if (el) {
        el.volume = this.volume
        el.muted = this.muted
      }
    },

    play(track) {
      if (!this._el) return
      const same = this.current && this.current.msgId === track.msgId && this.current.chatId === track.chatId
      this.error = ''
      if (same) { this.toggle(); return }
      this.current = { ...track }
      this.queueIndex = this.queue.findIndex((t) => t.msgId === track.msgId && t.chatId === track.chatId)
      this.currentTime = 0
      this.duration = 0
      this.loading = true
      const el = this._el
      el.src = api.mediaStreamUrl(track.chatId, track.msgId)
      el.currentTime = 0
      const p = el.play()
      if (p && p.catch) {
        p.catch((e) => {
          this.loading = false
          this.error = e?.message || '播放失败'
        })
      }
    },

    toggle() {
      if (!this._el || !this.current) return
      if (this.playing) this._el.pause()
      else { const p = this._el.play(); if (p && p.catch) p.catch(() => {}) }
    },

    seek(frac) {
      if (!this._el || !this.duration) return
      this._el.currentTime = Math.max(0, Math.min(1, frac)) * this.duration
    },

    setVolume(v) {
      this.volume = v
      if (this._el) this._el.volume = v
      if (v > 0 && this.muted) this.setMuted(false)
    },

    setMuted(m) {
      this.muted = m
      if (this._el) this._el.muted = m
    },

    prev() { if (this.hasPrev) this.play(this.queue[this.queueIndex - 1]) },
    next() { if (this.hasNext) this.play(this.queue[this.queueIndex + 1]) },

    close() {
      if (this._el) {
        this._el.pause()
        this._el.removeAttribute('src')
        this._el.load()
      }
      this.current = null
      this.playing = false
      reportPlayback(false)
      this.currentTime = 0
      this.duration = 0
      this.loading = false
      this.error = ''
    },

    // Messages view supplies the chat's audio messages (newest first) after
    // every page load. Only updates the queue — never touches the playing
    // track, so switching chats doesn't stop background playback.
    setQueue(messages, chatId) {
      this.queue = (messages || [])
        .filter((m) => m.hasMedia && m.media && isAudioMedia(m.media))
        .map((m) => ({ chatId, msgId: m.id, name: m.media.name, size: m.media.size }))
      if (this.current) {
        this.queueIndex = this.queue.findIndex((t) => t.msgId === this.current.msgId && t.chatId === this.current.chatId)
      }
    },

    locate() { if (this.current) this.locateMsgId = this.current.msgId },

    // --- element event hooks, bound directly in GlobalPlayer's template ---
    _onPlay() { this.playing = true; this.loading = false; reportPlayback(true) },
    _onPause() { this.playing = false; reportPlayback(false) },
    _onTimeUpdate(t) { this.currentTime = t },
    _onLoadedMetadata(d) { this.duration = d; this.loading = false },
    _onEnded() { this.playing = false; reportPlayback(false) }, // 单曲播完即停，不自动连播
    _onError(msg) { this.loading = false; this.error = msg || '音频加载失败' },
  },
})
