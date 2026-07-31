<template>
  <div class="page login-page">
    <div class="login-card card">
      <h2 class="page-title">登录 Telegram 账号</h2>
      <p class="muted subtitle">选择一种方式登录。登录后即可创建下载任务。</p>

      <el-tabs v-model="tab" class="login-tabs">
        <!-- 扫码登录 -->
        <el-tab-pane label="扫码登录" name="qr">
          <div class="qr-pane">
            <div v-if="!qr.session">
              <p class="muted">使用 Telegram 手机 App 扫描二维码登录（推荐）。</p>
              <el-button type="primary" :loading="qr.loading" @click="startQr">生成二维码</el-button>
            </div>

            <div v-else class="qr-body">
              <div v-if="qr.state === 'qr'" class="qr-box">
                <canvas ref="qrCanvas" />
                <p class="muted">打开 Telegram → 设置 → 设备 → 连接桌面设备，扫码登录</p>
              </div>

              <div v-else-if="qr.state === 'needPassword'" class="pwd-box">
                <p>该账号开启了两步验证，请输入 2FA 密码：</p>
                <el-input v-model="qr.password" type="password" show-password placeholder="2FA 密码" @keyup.enter="submitQrPassword" />
                <el-button type="primary" :loading="qr.loading" @click="submitQrPassword">确认</el-button>
              </div>

              <el-result v-else-if="qr.state === 'success'" icon="success" title="登录成功" :sub-title="`@${qr.username || qr.userId}`" />
              <el-result v-else-if="qr.state === 'error'" icon="error" title="登录失败" :sub-title="qr.error">
                <template #extra>
                  <el-button type="primary" @click="resetQr">重试</el-button>
                </template>
              </el-result>
            </div>
          </div>
        </el-tab-pane>

        <!-- 验证码登录 -->
        <el-tab-pane label="验证码登录" name="code">
          <div class="code-pane">
            <div v-if="!code.session">
              <p class="muted">通过手机号 + 短信验证码登录。</p>
              <el-button type="primary" :loading="code.loading" @click="startCode">开始</el-button>
            </div>

            <div v-else class="code-body">
              <div v-if="code.state === 'needPhone'" class="field">
                <p>请输入手机号（含国家区号）：</p>
                <el-input v-model="code.input" placeholder="+86 13800000000" @keyup.enter="sendCodeInput" />
                <el-button type="primary" :loading="code.loading" @click="sendCodeInput">发送验证码</el-button>
              </div>
              <div v-else-if="code.state === 'needCode'" class="field">
                <p>请输入收到的验证码：</p>
                <el-input v-model="code.input" placeholder="12345" @keyup.enter="sendCodeInput" />
                <el-button type="primary" :loading="code.loading" @click="sendCodeInput">登录</el-button>
              </div>
              <div v-else-if="code.state === 'needPassword'" class="field">
                <p>该账号开启了两步验证，请输入 2FA 密码：</p>
                <el-input v-model="code.input" type="password" show-password placeholder="2FA 密码" @keyup.enter="sendCodeInput" />
                <el-button type="primary" :loading="code.loading" @click="sendCodeInput">确认</el-button>
              </div>
              <el-result v-else-if="code.state === 'success'" icon="success" title="登录成功" :sub-title="`@${code.username || code.userId}`" />
              <el-result v-else-if="code.state === 'error'" icon="error" title="登录失败" :sub-title="code.error">
                <template #extra>
                  <el-button type="primary" @click="resetCode">重试</el-button>
                </template>
              </el-result>
            </div>
          </div>
        </el-tab-pane>

        <!-- 桌面导入 -->
        <el-tab-pane label="桌面客户端导入" name="desktop">
          <div class="desktop-pane">
            <p class="muted">从本机 Telegram Desktop 客户端导入登录状态（无需扫码）。</p>
            <el-form label-width="90px" class="desktop-form">
              <el-form-item label="数据目录">
                <div class="path-row">
                  <el-input v-model="desktop.path" placeholder="留空自动检测，或点右侧按钮选择目录" />
                  <el-button :loading="desktop.picking" @click="browseTdata">浏览…</el-button>
                </div>
                <div v-if="desktop.detected" class="hint muted">已自动检测：{{ desktop.detected }}</div>
              </el-form-item>
              <el-form-item label="本地密码">
                <el-input v-model="desktop.passcode" type="password" show-password placeholder="如设置了本地密码请填写，否则留空" />
              </el-form-item>
              <el-form-item>
                <el-button type="primary" :loading="desktop.loading" @click="probeDesktop">读取账号</el-button>
                <el-button v-if="desktop.detected && !desktop.path" text @click="useDetected">使用检测到的目录</el-button>
              </el-form-item>
            </el-form>

            <div v-if="desktop.accounts.length" class="desktop-accounts">
              <p>选择要导入的账号：</p>
              <el-radio-group v-model="desktop.userId">
                <el-radio v-for="a in desktop.accounts" :key="a.userId" :value="a.userId" class="acct-radio">
                  用户 ID：{{ a.userId }}
                </el-radio>
              </el-radio-group>
              <el-checkbox v-model="desktop.logout" class="logout-check">
                导入后退出桌面端该账号（避免会话冲突）
              </el-checkbox>
              <div>
                <el-button type="success" :loading="desktop.loading" @click="confirmDesktop">导入并登录</el-button>
              </div>
            </div>

            <el-result v-if="desktop.done" icon="success" title="导入成功" sub-title="账号已登录" />
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>
  </div>
</template>

<script setup>
import { ref, onUnmounted, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import QRCode from 'qrcode'
import { api, subscribe } from '../api'
import { useAppStore } from '../stores/app'

const router = useRouter()
const store = useAppStore()
const tab = ref('qr')

let stopEvents = null
onUnmounted(() => { if (stopEvents) stopEvents() })

function ensureEvents() {
  if (stopEvents) return
  stopEvents = subscribe('/api/events', (type, data) => {
    const p = data && data.data !== undefined ? data.data : data
    if (type === 'qr') onQrEvent(p)
    else if (type === 'code') onCodeEvent(p)
  })
}

function onSuccess() {
  store.refreshAccounts()
  ElMessage.success('登录成功')
  setTimeout(() => router.push('/tasks'), 800)
}

/* ---------- QR ---------- */
const qrCanvas = ref(null)
const qr = ref({ session: '', state: '', url: '', password: '', loading: false, error: '', username: '', userId: 0 })

async function startQr() {
  qr.value.loading = true
  try {
    ensureEvents()
    const s = await api.qrStart('')
    qr.value.session = s.session
    qr.value.state = s.state
    if (s.url) { qr.value.url = s.url; renderQr() }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    qr.value.loading = false
  }
}
function onQrEvent(p) {
  if (!p || p.session !== qr.value.session) return
  qr.value.state = p.state
  if (p.url) { qr.value.url = p.url; renderQr() }
  if (p.error) qr.value.error = p.error
  if (p.state === 'success') { qr.value.username = p.username; qr.value.userId = p.userId; onSuccess() }
}
async function renderQr() {
  await nextTick()
  if (qrCanvas.value && qr.value.url) {
    try { await QRCode.toCanvas(qrCanvas.value, qr.value.url, { width: 240, margin: 1 }) } catch { /* ignore */ }
  }
}
async function submitQrPassword() {
  if (!qr.value.password) return
  qr.value.loading = true
  try { await api.qrPassword(qr.value.session, qr.value.password) }
  catch (e) { ElMessage.error(e.message) }
  finally { qr.value.loading = false }
}
function resetQr() { qr.value = { session: '', state: '', url: '', password: '', loading: false, error: '', username: '', userId: 0 } }

/* ---------- Code ---------- */
const code = ref({ session: '', state: '', input: '', loading: false, error: '', username: '', userId: 0 })

async function startCode() {
  code.value.loading = true
  try {
    ensureEvents()
    const s = await api.codeStart('')
    code.value.session = s.session
    code.value.state = s.state
    code.value.input = ''
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    code.value.loading = false
  }
}
function onCodeEvent(p) {
  if (!p || p.session !== code.value.session) return
  code.value.state = p.state
  code.value.input = ''
  if (p.error) code.value.error = p.error
  if (p.state === 'success') { code.value.username = p.username; code.value.userId = p.userId; onSuccess() }
}
async function sendCodeInput() {
  if (!code.value.input) return
  code.value.loading = true
  try { await api.codeInput(code.value.session, code.value.input.trim()) }
  catch (e) { ElMessage.error(e.message); code.value.loading = false }
}
function resetCode() { code.value = { session: '', state: '', input: '', loading: false, error: '', username: '', userId: 0 } }

/* ---------- Desktop ---------- */
const desktop = ref({ path: '', passcode: '', accounts: [], userId: null, logout: false, loading: false, done: false, picking: false, detected: '' })

// auto-detect tdata on mount
api.detectTdata().then((r) => {
  if (r && r.detected) desktop.value.detected = r.detected
}).catch(() => {})

function useDetected() {
  desktop.value.path = desktop.value.detected
  probeDesktop()
}

async function browseTdata() {
  desktop.value.picking = true
  try {
    const r = await api.pickFolder()
    if (!r.cancelled && r.path) desktop.value.path = r.path
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    desktop.value.picking = false
  }
}

async function probeDesktop() {
  desktop.value.loading = true
  desktop.value.done = false
  try {
    const r = await api.desktopImport(desktop.value.path, desktop.value.passcode)
    desktop.value.accounts = r.accounts || []
    if (desktop.value.accounts.length) desktop.value.userId = desktop.value.accounts[0].userId
    else ElMessage.warning('未在该目录找到账号')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    desktop.value.loading = false
  }
}
async function confirmDesktop() {
  if (!desktop.value.userId) { ElMessage.warning('请选择账号'); return }
  desktop.value.loading = true
  try {
    await api.desktopConfirm({
      ns: '',
      path: desktop.value.path,
      passcode: desktop.value.passcode,
      userId: desktop.value.userId,
      logout: desktop.value.logout,
    })
    desktop.value.done = true
    onSuccess()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    desktop.value.loading = false
  }
}
</script>

<style scoped>
.login-page { display: flex; justify-content: center; align-items: flex-start; }
.login-card { width: 560px; max-width: 94vw; margin-top: 4vh; }
.subtitle { margin: -8px 0 18px; font-size: 13px; }
.login-tabs :deep(.el-tabs__item) { font-size: 14px; }

.qr-body, .code-body { min-height: 300px; display: flex; flex-direction: column; align-items: center; justify-content: center; }
.qr-box { text-align: center; }
.qr-box canvas { border-radius: var(--radius-sm); border: 1px solid var(--border); }
.qr-box p { margin-top: 14px; font-size: 13px; max-width: 320px; }

.pwd-box, .field { display: flex; flex-direction: column; gap: 12px; width: 320px; max-width: 100%; }
.pwd-box p, .field p { margin: 0; font-size: 13.5px; }

.code-pane, .desktop-pane { padding-top: 6px; }
.desktop-form { max-width: 520px; margin-top: 10px; }
.desktop-accounts { margin-top: 8px; display: flex; flex-direction: column; gap: 14px; }
.acct-radio { display: flex; margin: 6px 0; }
.logout-check { margin: 4px 0; }
.path-row { display: flex; gap: 8px; width: 100%; }
.path-row .el-input { flex: 1; }
.hint { font-size: 12px; margin-top: 4px; word-break: break-all; }

.el-result { padding: 20px 0; }
</style>
