const BASE = ''

async function req(method, path, body, signal) {
  const opts = {
    method,
    headers: {},
    signal,
  }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(BASE + path, opts)
  const text = await res.text()
  let data = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = { error: text }
  }
  if (!res.ok) {
    const e = new Error((data && data.error) || `请求失败 (${res.status})`)
    e.status = res.status
    throw e
  }
  return data
}

export const api = {
  // tasks
  listTasks: () => req('GET', '/api/tasks'),
  createTask: (payload) => req('POST', '/api/tasks', payload),
  getTask: (id) => req('GET', `/api/tasks/${id}`),
  pauseTask: (id) => req('POST', `/api/tasks/${id}/pause`),
  resumeTask: (id) => req('POST', `/api/tasks/${id}/resume`),
  deleteTask: (id, deleteFiles = false) => req('DELETE', `/api/tasks/${id}${deleteFiles ? '?deleteFiles=true' : ''}`),
  renameFile: (taskId, key, name) => req('POST', `/api/tasks/${taskId}/rename`, { key, name }),
  batchTasks: (ids, action, deleteFiles = false) => req('POST', '/api/tasks/batch', { ids, action, deleteFiles }),

  // accounts
  listAccounts: () => req('GET', '/api/accounts'),
  config: () => req('GET', '/api/config'),

  // chats & messages
  listChats: (refresh) => req('GET', `/api/chats${refresh ? '?refresh=1' : ''}`),
  listMessages: (chatId, { offsetId = 0, limit = 30, mediaOnly = false, refresh = false, signal } = {}) =>
    req('GET', `/api/chats/${chatId}/messages?offset_id=${offsetId}&limit=${limit}&media_only=${mediaOnly ? 1 : 0}${refresh ? '&refresh=1' : ''}`, undefined, signal),

  // media
  mediaInfo: (chatId, msgId) => req('GET', `/api/media/${chatId}/${msgId}/info`),
  mediaThumbUrl: (chatId, msgId) => `${BASE}/api/media/${chatId}/${msgId}/thumb`,
  mediaFileUrl: (chatId, msgId) => `${BASE}/api/media/${chatId}/${msgId}/file`,
  mediaStreamUrl: (chatId, msgId) => `${BASE}/api/media/${chatId}/${msgId}/stream`,

  // settings
  getSettings: () => req('GET', '/api/settings'),
  updateSettings: (payload) => req('POST', '/api/settings', payload),

  // filesystem
  pickFolder: () => req('POST', '/api/fs/pick-folder'),
  detectTdata: () => req('GET', '/api/fs/detect-tdata'),

  // auth
  qrStart: (ns) => req('POST', '/api/auth/qr/start', { ns }),
  qrPassword: (session, password) => req('POST', '/api/auth/qr/password', { session, password }),
  codeStart: (ns) => req('POST', '/api/auth/code/start', { ns }),
  codeInput: (session, value) => req('POST', '/api/auth/code/input', { session, value }),
  desktopImport: (path, passcode) => req('POST', '/api/auth/desktop/import', { path, passcode }),
  desktopConfirm: (payload) => req('POST', '/api/auth/desktop/confirm', payload),

  // system
  shutdown: () => req('POST', '/api/shutdown'),
}

// subscribe opens an SSE connection and invokes onEvent(type, data) for each
// message. Returns a close function. Reconnects automatically on drop.
export function subscribe(path, onEvent) {
  let closed = false
  let es = null
  let retry = 1000

  const connect = () => {
    if (closed) return
    es = new EventSource(BASE + path)

    es.onopen = () => { retry = 1000 }

    const handler = (e) => {
      try {
        const payload = JSON.parse(e.data)
        onEvent(e.type, payload)
      } catch { /* ignore malformed */ }
    }
    // listen to the named events we publish plus default messages
    ;['task', 'file', 'qr', 'code', 'status', 'message'].forEach((t) =>
      es.addEventListener(t, handler)
    )

    es.onerror = () => {
      es.close()
      if (closed) return
      setTimeout(connect, retry)
      retry = Math.min(retry * 2, 10000)
    }
  }
  connect()

  return () => {
    closed = true
    if (es) es.close()
  }
}
