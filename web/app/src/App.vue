<template>
  <div class="app">
    <header class="topbar">
      <div class="logo">
        Telegram消息管理器
      </div>
      <div class="spacer" />
      <el-dropdown v-if="store.accounts.length" trigger="click" @command="switchAccount">
        <div class="conn" :title="connTitle">
          <span class="dot" :class="{ off: !account }" />
          <span v-if="account">{{ accountName(account) }}</span>
          <span v-else>未登录</span>
        </div>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item
              v-for="a in store.accounts"
              :key="a.ns"
              :command="a.ns"
              :disabled="a.active"
            >{{ accountName(a) }}{{ a.active ? ' · 当前' : '' }}</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
      <div v-else class="conn" :title="connTitle">
        <span class="dot off" />
        <span>未登录</span>
      </div>
      <button class="iconbtn" :title="store.theme === 'light' ? '切换到暗色' : '切换到亮色'" @click="store.toggleTheme()">
        <Moon v-if="store.theme === 'light'" :size="18" />
        <Sun v-else :size="18" />
      </button>
    </header>

    <div class="main">
      <aside class="sider">
        <router-link
          v-for="item in nav"
          :key="item.path"
          :to="item.path"
          class="nav-item"
          :class="{ active: isActive(item.path) }"
        >
          <component :is="item.ico" class="ico" :size="20" />
          <span>{{ item.label }}</span>
        </router-link>
      </aside>

      <main class="content">
        <router-view />
      </main>
    </div>

    <!-- 全局底部音频播放条：只有播放内容时才显示（v-if 在组件内部） -->
    <GlobalPlayer />
  </div>
</template>

<script setup>
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAppStore } from './stores/app'
import GlobalPlayer from './components/GlobalPlayer.vue'
import { Moon, Sun, MessageSquare, Inbox, UserRound } from 'lucide-vue-next'

const store = useAppStore()
const route = useRoute()

const nav = [
  { path: '/messages', label: '消息', ico: MessageSquare },
  { path: '/tasks', label: '任务', ico: Inbox },
  { path: '/settings', label: '账号', ico: UserRound },
]

const account = computed(() => store.accounts.find((a) => a.active) || store.authorizedAccount)
const connTitle = computed(() =>
  account.value ? `命名空间: ${account.value.ns}` : '尚未登录任何账号'
)
// 显示用户名 + ID：优先 username，无则 firstName，都无则 ns
function accountName(a) {
  if (!a) return '未登录'
  const name = a.username ? '@' + a.username : (a.firstName || a.ns)
  return a.userId ? `${name} (${a.userId})` : name
}
// 切换账号：后端单活跃模型，需重启服务生效。前端先高亮目标账号并提示。
function switchAccount(ns) {
  const target = store.accounts.find((a) => a.ns === ns)
  if (!target || target.active) return
  store.accounts.forEach((a) => { a.active = (a.ns === ns) })
  ElMessage.success(`已切换到 ${accountName(target)}，重启服务后生效`)
}

const isActive = (p) => route.path.startsWith(p)

onMounted(async () => {
  store.applyTheme()
  store.startEvents()
  await Promise.all([store.refreshAccounts(), store.refreshConfig(), store.refreshTasks()])
})
</script>

<style scoped>
.app { display: flex; flex-direction: column; height: 100vh; }
.topbar {
  height: 56px; flex: none; display: flex; align-items: center; gap: 12px;
  padding: 0 20px; background: var(--bg-panel); border-bottom: 1px solid var(--border);
}
.topbar .logo { font-size: 18px; font-weight: 700; letter-spacing: .3px; display: flex; align-items: center; gap: 8px; }
.topbar .logo .badge {
  font-size: 11px; font-weight: 600; color: var(--accent); background: var(--accent-soft);
  padding: 2px 8px; border-radius: 20px;
}
.topbar .spacer { flex: 1; }
.conn { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-2); }
.conn .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--green); box-shadow: 0 0 0 3px rgba(34,176,125,.18); }
.conn .dot.off { background: var(--text-3); box-shadow: 0 0 0 3px rgba(154,161,173,.18); }
.iconbtn {
  width: 34px; height: 34px; border: none; border-radius: var(--radius-sm); background: transparent;
  cursor: pointer; color: var(--text-2); font-size: 16px; display: flex; align-items: center;
  justify-content: center; transition: .15s;
}
.iconbtn:hover { background: var(--bg-hover); color: var(--text); }

.main { flex: 1; display: flex; min-height: 0; }
.sider {
  width: 76px; flex: none; background: var(--bg-panel); border-right: 1px solid var(--border);
  display: flex; flex-direction: column; align-items: center; padding: 14px 0; gap: 6px;
}
.nav-item {
  width: 56px; padding: 9px 0; border-radius: var(--radius-sm); border: none; background: transparent;
  cursor: pointer; color: var(--text-2); display: flex; flex-direction: column; align-items: center;
  gap: 4px; font-size: 11px; transition: .15s; text-decoration: none;
}
.nav-item:hover { background: var(--bg-hover); color: var(--text); }
.nav-item.active { background: var(--accent-soft); color: var(--accent); font-weight: 600; }
.nav-item .ico { font-size: 19px; }
.content { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--bg); }
</style>
