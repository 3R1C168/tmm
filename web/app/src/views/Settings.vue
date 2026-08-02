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
      <h3 class="sec">文件名</h3>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">文件名添加消息ID前缀</div>
          <div class="setdesc muted">开启后，下载的文件名前会加上 <code class="code-hint">#22713</code> 前缀（消息ID + 空格），方便按消息查找文件。如：<code class="code-hint">#22713 video.mp4</code></div>
        </div>
        <el-switch v-model="settings.msgIdPrefix" @change="saveSettings" />
      </div>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">文件名添加频道ID前缀</div>
          <div class="setdesc muted">开启后，下载的文件名前会加上 <code class="code-hint">#2070986972</code> 前缀（频道ID + 空格）。与消息ID前缀同时开启时为 <code class="code-hint">#2070986972 #22713 video.mp4</code>，方便按频道查找文件。</div>
        </div>
        <el-switch v-model="settings.channelIdPrefix" @change="saveSettings" />
      </div>
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">按频道存放下载文件</div>
          <div class="setdesc muted">开启后，每个频道的文件存到下载目录下 <code class="code-hint">#频道ID 频道名</code> 子目录，频道名中的特殊字符会被自动清理。如：<code class="code-hint">downloads/#2070986972 资源社区/</code></div>
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
      <div class="setrow">
        <div class="setinfo">
          <div class="setname">下载目录</div>
          <div class="setdesc muted">下载文件存放的目录，默认在程序所在目录的 downloads 文件夹。按频道存放开关开启时，文件存到此目录下 #频道ID 频道名 子目录。</div>
        </div>
        <div class="dl-dir-row">
          <el-input v-model="settings.downloadDir" placeholder="留空 = 默认目录（程序目录下 downloads）" clearable size="small" style="min-width: 220px" @change="saveSettings" />
          <el-button size="small" @click="pickDownloadDir">选择文件夹</el-button>
        </div>
      </div>
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
const settings = ref({ previewTakeout: false, cacheMaxMB: 5120, clearPreviewOnExit: true, msgIdPrefix: false, channelIdPrefix: false, channelDir: false, downloadDir: '', filenameMaxLen: 200 })

onMounted(async () => {
  try {
    const s = await api.getSettings()
    settings.value = {
      previewTakeout: !!s.previewTakeout,
      cacheMaxMB: s.cacheMaxMB || 5120,
      clearPreviewOnExit: s.clearPreviewOnExit !== false,
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
  try {
    const r = await api.pickFolder('选择下载目录')
    if (!r.cancelled && r.path) {
      settings.value.downloadDir = r.path
      await saveSettings()
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
.setrow { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 10px 0; }
.setrow + .setrow { border-top: 1px solid var(--border); }
.setinfo { flex: 1; min-width: 0; }
.setname { font-weight: 600; font-size: 14px; }
.setdesc { font-size: 12.5px; margin-top: 3px; line-height: 1.5; }
.code-hint { background: var(--bg-hover); padding: 1px 5px; border-radius: 4px; font-family: ui-monospace, Consolas, monospace; font-size: 12px; }
.os-hint { color: var(--text-3); font-size: 11.5px; }
</style>
