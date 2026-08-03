// 下载历史：判断消息是否已下载过（任务历史 server-tasks.json → /api/tasks）。
// 判断标准：已完成任务（status=done）的 files 里有该 (peerID, msgID)。
// downloaded 用 Vue ref 包装——模板里 isDownloaded() 读取它，加载完成后触发重渲染。
import { ref } from 'vue'

// 已下载坐标集合："peerID/msgID"（与后端 FileEntry.Key 同格式）
const downloaded = ref(new Set())

// loadDownloadHistory 从任务列表构建已下载坐标集。
// tasks: 任务数组（每个含 files[].key/status）。
// 只把 status=done 的文件算已下载（running/paused/failed 不算）。
export function loadDownloadHistory(tasks) {
  const next = new Set()
  for (const t of tasks || []) {
    for (const f of t.files || []) {
      if (f.status === 'done' && f.key) next.add(f.key)
    }
  }
  downloaded.value = next
  return downloaded.value
}

// addDownloadedKey 下载完成后手动加入（配合 SSE 事件实时刷新）。
export function addDownloadedKey(key) {
  if (key) {
    const s = downloaded.value
    if (!s.has(key)) {
      s.add(key)
      downloaded.value = new Set(s) // 触发 ref 更新 → 模板重渲染
    }
  }
}

// isDownloaded 查询某 (chatId, msgId) 是否已下载。
// chatId 是频道/群裸正数 ID（与后端 peerID 一致，见 FileEntry.Key "peerID/msgID"）。
export function isDownloaded(chatId, msgId) {
  if (!chatId || !msgId) return false
  return downloaded.value.has(`${chatId}/${msgId}`)
}
