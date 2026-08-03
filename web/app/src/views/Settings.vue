<template>
  <div class="page">
    <h2 class="page-title">账号与设置</h2>

    <div class="card">
      <h3 class="sec">当前账号</h3>
      <el-empty v-if="!store.accounts.length" description="暂无账号">
        <el-button type="primary" @click="$router.push('/login')">去登录</el-button>
      </el-empty>

      <div v-for="a in store.accounts" :key="a.ns" class="acct">
        <div class="ainfo">
          <div class="aname">
            <span v-if="a.authorized">{{ a.firstName || a.username || ('用户 ' + a.userId) }}</span>
            <span v-else class="muted">未登录</span>
            <el-tag size="small" :type="a.authorized ? 'success' : 'info'" effect="light">
              {{ a.authorized ? '已连接' : '未授权' }}
            </el-tag>
          </div>
          <div class="ameta muted">
            <span v-if="a.username">@{{ a.username }}</span>
            <span v-if="a.username && a.userId"> · </span>
            <span v-if="a.userId">ID {{ a.userId }}</span>
          </div>
        </div>
        <el-button v-if="!a.authorized" size="small" type="primary" @click="$router.push('/login')">登录</el-button>
      </div>

      <el-divider />
      <el-button @click="$router.push('/login')"><Plus :size="16" /> 添加 / 重新登录账号</el-button>
    </div>

    <div class="card">
      <h3 class="sec">预览与下载</h3>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">大视频预览走 takeout 通道</div>
          <div class="setdesc muted">开启后，大视频在线预览走 takeout（限流更低、更流畅），但会在你其他设备的 Telegram 上收到一条“数据导出”通知。实测本机普通通道已足够流畅，可按需开启。</div>
        </div>
        <el-switch v-model="settings.previewTakeout" @change="saveSettings" />
      </div>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">本地缓存上限（MB）</div>
          <div class="setdesc muted">预览图/缩略图等本地缓存的最大占用，超出后自动清理最旧的文件。</div>
        </div>
        <el-input-number v-model="settings.cacheMaxMB" :min="64" :max="102400" :step="128" @change="saveSettings" />
      </div>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">退出时清理预览缓存</div>
          <div class="setdesc muted">关闭服务时自动清除视频流式播放产生的预览分片（cache/preview）。已下载的媒体/缩略图不受影响，下次播放视频会重新缓冲。建议开启以避免预览文件无限增长。</div>
        </div>
        <el-switch v-model="settings.clearPreviewOnExit" @change="saveSettings" />
      </div>
    </div>

    <div class="card">
      <h3 class="sec">已读同步</h3>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">自动已读</div>
          <div class="setdesc muted">进入频道定位未读后，跳到未读末尾自动标记已读（仿桌面端）。关闭后进入频道只定位不标记，可手动点「标记已读」。</div>
        </div>
        <el-switch v-model="settings.autoRead" @change="saveSettings" />
      </div>
    </div>

    <div class="card">
      <h3 class="sec">下载设置</h3>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">下载目录</div>
          <div class="setdesc muted">下载文件存放的位置，留空使用程序所在目录\downloads</div>
        </div>
        <div class="dl-dir-row">
          <el-input v-model="settings.downloadDir" placeholder="\downloads" clearable size="small" class="dl-dir-input" @change="saveSettings" />
          <el-button size="small" @click="pickDownloadDir">选择文件夹</el-button>
        </div>
      </div>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">文件名添加消息ID前缀</div>
          <div class="setdesc muted">开启后，文件名前会加上 <code class="code-hint">#消息ID</code> 前缀，方便按消息查找。</div>
        </div>
        <el-switch v-model="settings.msgIdPrefix" @change="saveSettings" />
      </div>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">文件名添加频道ID前缀</div>
          <div class="setdesc muted">开启后，文件名前会加上 <code class="code-hint">#频道ID</code> 前缀，方便按频道查找。</div>
        </div>
        <el-switch v-model="settings.channelIdPrefix" @change="saveSettings" />
      </div>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">按频道存放下载文件</div>
          <div class="setdesc muted">开启后，每个频道的文件存到下载目录下的 <code class="code-hint">#频道ID 频道名</code> 子目录，频道名中的特殊字符会被自动清理。</div>
        </div>
        <el-switch v-model="settings.channelDir" @change="saveSettings" />
      </div>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">文件名最大长度（字节）</div>
          <div class="setdesc muted">
            文件名（不含扩展名）超出此长度时自动截断。<br />
            <span class="os-hint">Windows / macOS / Linux 限制均为 255 字节，建议留余量给扩展名和临时后缀，默认 200。</span>
          </div>
        </div>
        <el-input-number v-model="settings.filenameMaxLen" :min="0" :max="255" :step="10" @change="saveSettings" />
      </div>
    </div>

    <div class="card">
      <h3 class="sec">服务</h3>
      <div class="kv"><span class="k">前端目录</span><span class="v mono">{{ store.config.webRoot }}</span></div>
      <el-divider />
      <el-popconfirm title="确定停止服务吗？正在下载的任务会被暂停。" @confirm="shutdown">
        <template #reference>
          <el-button type="danger" plain>停止服务</el-button>
        </template>
      </el-popconfirm>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../api'
import { Plus } from 'lucide-vue-next'
import { useAppStore } from '../stores/app'

const store = useAppStore()
const settings = ref({ previewTakeout: false, cacheMaxMB: 5120, clearPreviewOnExit: true, autoRead: true, msgIdPrefix: false, channelIdPrefix: false, channelDir: false, downloadDir: '', filenameMaxLen: 200 })

onMounted(async () => {
  try {
    const s = await api.getSettings()
    settings.value = {
      previewTakeout: !!s.previewTakeout,
      cacheMaxMB: s.cacheMaxMB || 5120,
      clearPreviewOnExit: s.clearPreviewOnExit !== false,
      autoRead: s.autoRead !== false,
      msgIdPrefix: s.msgIdPrefix === true,
      channelIdPrefix: s.channelIdPrefix === true,
      channelDir: s.channelDir === true,
      downloadDir: s.downloadDir || '',
      filenameMaxLen: s.filenameMaxLen || 200,
    }
  } catch { /* use defaults */ }
})

async function saveSettings() {
  try {
    await api.updateSettings({
      previewTakeout: settings.value.previewTakeout,
      cacheMaxMB: settings.value.cacheMaxMB,
      clearPreviewOnExit: settings.value.clearPreviewOnExit,
      autoRead: settings.value.autoRead,
      msgIdPrefix: settings.value.msgIdPrefix,
      channelIdPrefix: settings.value.channelIdPrefix,
      channelDir: settings.value.channelDir,
      downloadDir: settings.value.downloadDir || '',
      filenameMaxLen: settings.value.filenameMaxLen,
    })
    ElMessage.success('设置已保存')
  } catch (e) { ElMessage.error(e.message) }
}

async function pickDownloadDir() {
  // 服务器端原生目录选择器：弹窗在服务器桌面弹出（能拿绝对路径）。
  // 前端先提示，避免用户不知道已弹窗。
  try {
    ElMessage.info('正在打开选择窗口…')
    const r = await api.pickFolder('选择下载目录')
    if (!r.cancelled && r.path) {
      settings.value.downloadDir = r.path
      await saveSettings()
    } else if (r.cancelled) {
      ElMessage.info('已取消选择')
    }
  } catch (e) { ElMessage.error(e.message) }
}

async function shutdown() {
  try {
    await api.shutdown()
    ElMessage.success('服务正在停止，可以关闭此页面')
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<style scoped>
.card { max-width: 640px; margin-bottom: 18px; }
.sec { margin: 0 0 14px; font-size: 15px; }
.acct { display: flex; align-items: center; gap: 12px; padding: 10px 0; }
.ainfo { flex: 1; min-width: 0; }
.aname { display: flex; align-items: center; gap: 8px; font-weight: 600; }
.ameta { font-size: 12px; margin-top: 3px; }
.kv { display: flex; gap: 12px; padding: 6px 0; font-size: 13px; }
.kv .k { width: 90px; color: var(--text-2); flex: none; }
.kv .v { word-break: break-all; }
.dl-dir-row { display: flex; gap: 8px; align-items: center; }
.dl-dir-input { width: 240px; }
.dl-dir-input :deep(.el-input__wrapper) { border-radius: 4px; }
.setrow { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 12px 0; }
.setrow + .setrow { border-top: 1px solid var(--border); }
.setinfo { flex: 1; min-width: 0; padding-right: 8px; }
.setname { font-weight: 600; font-size: 14px; }
.setdesc { font-size: 12.5px; margin-top: 4px; line-height: 1.6; }
.code-hint { background: var(--bg-hover); padding: 1px 5px; border-radius: 4px; font-family: ui-monospace, Consolas, monospace; font-size: 12px; }
.os-hint { color: var(--text-3); font-size: 11.5px; }
</style>
