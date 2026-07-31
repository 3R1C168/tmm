<template>
  <div class="page">
    <h2 class="page-title">新建下载任务</h2>

    <el-alert v-if="!account" type="warning" :closable="false" class="warn">
      尚未登录账号。请先到 <router-link to="/login">登录页</router-link> 登录。
    </el-alert>

    <div class="card form-card">
      <el-form :model="form" label-width="96px" label-position="left">
        <el-form-item label="消息链接" required>
          <el-input
            v-model="form.urlsText"
            type="textarea"
            :rows="4"
            placeholder="每行一个 Telegram 消息链接，例如：https://t.me/c/123456789/100"
          />
          <div class="hint muted">支持 t.me 消息链接；多个链接按行分隔。</div>
        </el-form-item>

        <el-form-item label="下载目录">
          <el-input v-model="form.dir" :placeholder="`默认：${store.config.downloadDir || 'downloads'}`" />
        </el-form-item>

        <el-form-item label="命名模板">
          <el-input v-model="form.template" placeholder="{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}" />
        </el-form-item>

        <el-form-item label="Takeout">
          <el-radio-group v-model="form.takeout">
            <el-radio-button value="auto">自动（&gt;50MB）</el-radio-button>
            <el-radio-button value="always">总是</el-radio-button>
            <el-radio-button value="never">从不</el-radio-button>
          </el-radio-group>
          <div class="hint muted">大文件用 takeout 可大幅减少限流中断，但会在其他设备提示“数据导出”。</div>
        </el-form-item>

        <el-form-item label="选项">
          <el-checkbox v-model="form.desc">倒序</el-checkbox>
          <el-checkbox v-model="form.group">按相册分组</el-checkbox>
          <el-checkbox v-model="form.rewriteExt">按内容修正扩展名</el-checkbox>
          <el-checkbox v-model="form.skipSame">跳过同名同大小</el-checkbox>
          <el-checkbox v-model="form.restart">重新开始（忽略续传记录）</el-checkbox>
        </el-form-item>

        <el-form-item label="包含扩展名">
          <el-input v-model="form.include" placeholder="如：mp4,zip（留空不过滤）" />
        </el-form-item>
        <el-form-item label="排除扩展名">
          <el-input v-model="form.exclude" placeholder="如：jpg,png" />
        </el-form-item>

        <el-form-item label="并发/线程">
          <div class="runtime">
            <el-input-number v-model="form.limit" :min="1" :max="16" />
            <span class="rlabel">同时下载文件数</span>
            <el-input-number v-model="form.threads" :min="1" :max="32" />
            <span class="rlabel">单文件线程</span>
          </div>
        </el-form-item>

        <el-form-item>
          <el-button type="primary" :disabled="!account" :loading="submitting" @click="submit">创建任务</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api } from '../api'
import { useAppStore } from '../stores/app'

const router = useRouter()
const store = useAppStore()
const account = computed(() => store.authorizedAccount)
const submitting = ref(false)

const blank = () => ({
  urlsText: '',
  dir: '',
  template: '',
  takeout: 'auto',
  desc: false,
  group: true,
  rewriteExt: false,
  skipSame: true,
  restart: false,
  include: '',
  exclude: '',
  limit: 2,
  threads: 4,
})
const form = ref(blank())

function splitList(s) {
  return s.split(',').map((x) => x.trim()).filter(Boolean)
}

async function submit() {
  const urls = form.value.urlsText.split('\n').map((x) => x.trim()).filter(Boolean)
  if (!urls.length) { ElMessage.warning('请至少填写一个消息链接'); return }

  const payload = {
    account: account.value ? account.value.ns : '',
    urls,
    opts: {
      dir: form.value.dir,
      template: form.value.template,
      takeout: form.value.takeout,
      desc: form.value.desc,
      group: form.value.group,
      rewriteExt: form.value.rewriteExt,
      skipSame: form.value.skipSame,
      restart: form.value.restart,
      include: splitList(form.value.include),
      exclude: splitList(form.value.exclude),
      limit: form.value.limit,
      threads: form.value.threads,
    },
  }

  submitting.value = true
  try {
    await api.createTask(payload)
    ElMessage.success('任务已创建')
    router.push('/tasks')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    submitting.value = false
  }
}
function reset() { form.value = blank() }
</script>

<style scoped>
.warn { margin-bottom: 16px; }
.form-card { max-width: 760px; }
.hint { font-size: 12px; margin-top: 4px; line-height: 1.4; }
.runtime { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.rlabel { color: var(--text-2); font-size: 12.5px; margin-right: 10px; }
</style>
