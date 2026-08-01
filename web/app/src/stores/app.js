import { defineStore } from 'pinia'
import { api, subscribe } from '../api'

export const useAppStore = defineStore('app', {
  state: () => ({
    theme: localStorage.getItem('theme') || 'light',
    accounts: [],
    config: { downloadDir: '', webRoot: '' },
    tasks: [],
    connected: false,
    _unsubscribe: null,
  }),
  getters: {
    authorizedAccount(state) {
      return state.accounts.find((a) => a.authorized) || null
    },
  },
  actions: {
    applyTheme() {
      document.documentElement.setAttribute('data-theme', this.theme)
      localStorage.setItem('theme', this.theme)
    },
    toggleTheme() {
      this.theme = this.theme === 'light' ? 'dark' : 'light'
      this.applyTheme()
    },
    async refreshAccounts() {
      try {
        const r = await api.listAccounts()
        this.accounts = r.accounts || []
      } catch { /* service may be starting */ }
    },
    // 启动时账号 auth 探测需要几秒，期间 listAccounts 返回空。
    // ensureAccounts 轮询直到账号出现或超时，让网页秒开后账号自动加载。
    async ensureAccounts() {
      const maxTries = 12 // 12 × 1.5s ≈ 18s，覆盖 auth 探测 + MTProto 握手
      for (let i = 0; i < maxTries; i++) {
        await this.refreshAccounts()
        if (this.accounts.length) return // 账号已就绪
        await new Promise(r => setTimeout(r, 1500))
      }
    },
    async refreshConfig() {
      try {
        this.config = await api.config()
      } catch { /* ignore */ }
    },
    async refreshTasks() {
      try {
        const r = await api.listTasks()
        this.tasks = r.tasks || []
      } catch { /* ignore */ }
    },
    upsertTask(snap) {
      const i = this.tasks.findIndex((t) => t.id === snap.id)
      if (i >= 0) this.tasks.splice(i, 1, snap)
      else this.tasks.push(snap)
    },
    startEvents() {
      if (this._unsubscribe) return
      this._unsubscribe = subscribe('/api/events', (type, data) => {
        this.connected = true
        const payload = data && data.data !== undefined ? data.data : data
        if (type === 'task' || type === 'file') {
          if (payload && payload.id) this.upsertTask(payload)
        }
      })
    },
  },
})
