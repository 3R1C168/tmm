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
