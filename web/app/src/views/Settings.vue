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
            <span v-if="a.username">@{{ a.username }} · </span>
            <span v-if="a.userId">ID {{ a.userId }} · </span>
            <span class="mono">ns: {{ a.ns }}</span>
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
    </div>

    <div class="card">
      <h3 class="sec">服务</h3>
      <div class="kv"><span class="k">下载目录</span><span class="v mono">{{ store.config.downloadDir }}</span></div>
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
const settings = ref({ previewTakeout: false, cacheMaxMB: 5120 })

onMounted(async () => {
  try {
    const s = await api.getSettings()
    settings.value = {
      previewTakeout: !!s.previewTakeout,
      cacheMaxMB: s.cacheMaxMB || 5120,
    }
  } catch { /* use defaults */ }
})

async function saveSettings() {
  try {
    await api.updateSettings({
      previewTakeout: settings.value.previewTakeout,
      cacheMaxMB: settings.value.cacheMaxMB,
    })
    ElMessage.success('设置已保存')
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
.setrow { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 10px 0; }
.setrow + .setrow { border-top: 1px solid var(--border); }
.setinfo { flex: 1; min-width: 0; }
.setname { font-weight: 600; font-size: 14px; }
.setdesc { font-size: 12.5px; margin-top: 3px; line-height: 1.5; }
</style>
