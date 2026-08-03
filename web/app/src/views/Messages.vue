<template>
  <div class="msg-wrap">
    <!-- 左侧：会话列表（宽度可拖动调整） -->
    <aside class="chat-list" :style="{ width: chatListW + 'px' }">
      <div class="chat-search">
        <div class="chat-search-input-wrap">
          <input v-model="search" placeholder="搜索会话…" />
          <button v-if="search" class="search-clear" @click="clearChatSearch" title="清除搜索">✕</button>
        </div>
        <button class="chat-refresh-btn" :disabled="chatsLoading" @click="loadChats(true)" title="刷新会话列表">
          <RefreshCw v-if="!chatsLoading" :size="15" />
          <Loader2 v-else :size="15" class="spin" />
        </button>
      </div>
      <div class="chat-items" ref="chatListEl" v-loading="chatsLoading">
        <el-empty v-if="!chatsLoading && !filteredChats.length" description="暂无会话，请先登录" :image-size="60" />
        <div
          v-for="c in filteredChats"
          :key="c.id"
          class="chat-item"
          :class="{ active: current && current.id === c.id }"
          @click="selectChat(c)"
        >
          <div class="meta">
            <div class="row1">
              <span class="name">{{ c.visibleName || c.username || ('会话 ' + c.id) }}</span>
            </div>
            <div class="preview">{{ typeLabel(c.type) }}<span v-if="c.username"> · @{{ c.username }}</span></div>
          </div>
          <span class="unread" v-if="c.unreadCount">{{ c.unreadCount > 99 ? '99+' : c.unreadCount }}</span>
        </div>
        <div v-if="visibleChatCount < allFilteredCount" ref="chatSentinelEl" class="load-more muted">加载更多…</div>
      </div>
      <div class="chat-resizer" @mousedown="startResizeChat"></div>
    </aside>

    <!-- 右侧：消息流（最新在上） -->
    <section class="chat-view">
      <div v-if="!current" class="chat-empty">
        <el-empty description="选择左侧会话查看消息，点击图片/视频可放大预览与下载" />
      </div>

      <template v-else>
        <header class="chat-header">
          <div class="hinfo">
            <div class="title">{{ current.visibleName || current.username }}</div>
            <div class="sub muted">{{ typeLabel(current.type) }}<span v-if="selected.size"> · 已选 {{ selected.size }} 条</span></div>
          </div>
          <div class="spacer" />
          <div class="chat-search-inline" :class="{ open: searchOpen }">
            <input
              v-model="searchQuery"
              placeholder="搜索本频道…"
              @input="onSearchInput"
              @compositionstart="composing = true"
              @compositionend="composing = false; nextTick(onSearchInput)"
              @keydown.esc="closeSearch"
              @focus="searchOpen = true"
            />
            <button v-if="searchQuery" class="search-clear" @click="clearSearch">✕</button>
          </div>
          <el-checkbox v-model="mediaOnly" size="small" @change="reloadMessages">只看媒体</el-checkbox>
          <button class="chat-refresh-btn" :disabled="msgsLoading" @click="reloadMessages(true)" title="刷新消息">
            <RefreshCw v-if="!msgsLoading" :size="14" />
            <Loader2 v-else :size="14" class="spin" />
          </button>
          <el-button type="primary" size="small" :disabled="!selected.size" @click="downloadSelected">下载所选 ({{ selected.size }})</el-button>
          <el-button size="small" @click="rangeDialog = true">按范围下载</el-button>
        </header>

        <!-- 频道内搜索下拉：覆盖在消息区上方，独立于消息列表状态 -->
        <div v-if="searchOpen && validSearchQuery(searchQuery)" class="search-dropdown" @scroll="onSearchScroll">
          <div class="search-drop-head">
            <span>{{ searchCount }} 条匹配</span>
            <span v-if="searchLoading" class="muted">搜索中…</span>
          </div>
          <div v-for="res in searchResults" :key="res.id" class="search-item" @click="jumpToResult(res)">
            <div v-if="res.hasMedia" class="search-thumb">
              <LazyThumb :src="api.mediaThumbUrl(current.id, res.id)" :placeholder="res.media?.stripped?.dataUri || ''" />
            </div>
            <div class="search-item-body">
              <div class="search-item-text">{{ searchSnippet(res.text) || '(无文本)' }}</div>
              <div class="search-item-date muted">{{ dateStrFromTs(res.date) }}</div>
            </div>
          </div>
          <div v-if="!searchResults.length && !searchLoading" class="muted search-empty">无匹配消息</div>
          <div v-if="searchHasMore" class="muted search-more">加载更多…</div>
        </div>

        <div class="msg-scroll">
          <div v-if="reconnecting" class="loading-tip reconnect-tip">连接重连中，自动重试…</div>
          <div v-if="msgsLoading && !displayItems.length && !reconnecting" class="skeleton-list">
            <div v-for="n in 6" :key="n" class="skel-row">
              <div class="skel-bubble">
                <div class="skel-media" :style="{ width: (160 + (n % 3) * 60) + 'px' }" />
                <div class="skel-line w60" />
                <div class="skel-line w40" />
              </div>
            </div>
          </div>
          <div v-if="!msgsLoading && !displayItems.length && !reconnecting" class="loading-tip muted">该会话暂无消息</div>

          <DynamicScroller
            ref="scrollerRef"
            :key="'msgs-' + (current ? current.id : '')"
            :items="displayItems"
            :min-item-size="320"
            class="scroller"
            key-field="key"
            @scroll="onMsgScroll"
            @scroll-end="onMsgScroll"
            v-slot="{ item, index, active }"
          >
            <DynamicScrollerItem
              :item="item"
              :active="active"
              :size-dependencies="[item.msgs?.length, item.msg?.text]"
              :data-index="index"
            >
              <div class="msg-row">
                <!-- 未读分隔线：未读消息与已读消息的分界 -->
                <div v-if="item.type === 'divider'" class="unread-divider">
                  <span>未读消息</span>
                </div>
                <!-- 相册（groupedId 聚合） -->
                <div v-else-if="item.type === 'album'" class="msg" :class="{ out: item.out }">
                  <el-checkbox
                    class="pick"
                    :model-value="item.msgs.every((x) => selected.has(x.id))"
                    @change="toggleSelectAlbum(item)"
                  />
                  <div class="bubble-wrap">
                    <div class="bubble album" :style="{ width: (albumLayout(item).width + 8) + 'px' }">
                      <!-- 拼图布局（照 telegram-tt）：按宽高比拼，不裁正方形 -->
                      <div class="album-mosaic" :style="{ width: albumLayout(item).width + 'px', height: albumLayout(item).height + 'px' }">
                        <div
                          v-for="(m, ci) in item.msgs"
                          :key="m.id"
                          class="album-cell"
                          :style="albumCellStyle(item, ci)"
                          @click="openViewer(m)"
                        >
                          <LazyThumb :src="api.mediaThumbUrl(current.id, m.id)" :placeholder="m.media?.stripped?.dataUri || ''" @load="onThumbLoad(m, $event)" @error="onThumbError" />
                          <span v-if="isVideo(m.media)" class="cell-play"><Play :size="32" /></span>
                          <span v-if="m.media?.size" class="cell-dur">{{ fmtBytes(m.media.size) }}</span>
                        </div>
                      </div>
                      <!-- 相册文字（caption 挂在组内某一条上，取第一个非空的） -->
                      <div v-if="albumText(item)" class="text">{{ albumText(item) }}</div>
                      <div class="meta">
                        <span v-if="item.views" class="meta-views"><Eye :size="11" /> {{ item.views }}</span>
                        <!-- 相册评论入口：组内第一条带评论区的消息（同组讨论组相同） -->
                        <span
                          v-if="albumComments(item)"
                          class="meta-views meta-comments"
                          title="查看评论区"
                          @click.stop="openComments(albumComments(item))"
                        >
                          <MessageCircle :size="11" /> {{ albumComments(item).replies.count }}
                        </span>
                        <span>{{ fmtDate(item.date) }}</span>
                      </div>
                    </div>
                    <!-- 下载按钮：气泡外右下角悬浮（meta 行只留信息）。已下载变勾 -->
                    <button
                      class="media-dl-float"
                      :class="{ downloaded: albumAllDownloaded(item) }"
                      @click.stop="downloadAlbum(item)"
                      :title="albumAllDownloaded(item) ? '已下载，点击可重新下载' : '下载整组(' + item.msgs.length + ')'"
                    >
                      <CircleCheckBig v-if="albumAllDownloaded(item)" :size="18" />
                      <Download v-else :size="16" />
                    </button>
                  </div>
                </div>

                <!-- 单条消息 -->
                <div v-else class="msg" :class="{ out: item.msg.out, selected: selected.has(item.msg.id) }">
                  <el-checkbox class="pick" :model-value="selected.has(item.msg.id)" @change="toggleSelect(item.msg.id)" />
                  <div class="bubble-wrap">
                  <div class="bubble" :class="{ media: item.msg.hasMedia }" :style="item.msg.hasMedia && (isImage(item.msg.media) || isVideo(item.msg.media)) ? { width: (mediaWidth(item.msg) + 8) + 'px' } : {}">
                    <div v-if="item.msg.hasMedia" class="media-box" @click="openViewer(item.msg)">
                      <template v-if="isImage(item.msg.media) || isVideo(item.msg.media)">
                        <div class="thumb-wrap" :style="thumbStyle(item.msg)">
                          <LazyThumb :src="api.mediaThumbUrl(current.id, item.msg.id)" :placeholder="item.msg.media?.stripped?.dataUri || ''" @load="onThumbLoad(item.msg, $event)" @error="onThumbError" />
                          <div v-if="isVideo(item.msg.media)" class="play-overlay">
                            <span class="play-ico"><Play :size="24" stroke-width="2.5" /></span>
                            <span class="dur">{{ fmtBytes(item.msg.media.size) }}</span>
                          </div>
                        </div>
                      </template>
                      <template v-else-if="isAudio(item.msg.media)">
                        <!-- 音频：点击交给全局底部播放器（@click.stop 阻止冒泡触发 openViewer 误下载） -->
                        <div class="audio-chip" :class="{ playing: player.current?.msgId === item.msg.id }" @click.stop="playAudio(item.msg)">
                          <span class="fico"><Pause v-if="player.current?.msgId === item.msg.id && player.playing" :size="14" /><Play v-else :size="14" /></span>
                          <div class="ainfo">
                            <div class="aname" :title="item.msg.media.name">{{ item.msg.media.name }}</div>
                            <div class="asub">{{ player.current?.msgId === item.msg.id ? (player.playing ? '播放中' : '已暂停') : '点击播放' }}</div>
                          </div>
                          <span class="fsize">{{ fmtBytes(item.msg.media.size) }}</span>
                        </div>
                      </template>
                      <template v-else>
                        <div class="file-chip">
                          <span class="fico"><FileText :size="18" /></span>
                          <span class="fname" :title="item.msg.media.name">{{ item.msg.media.name }}</span>
                          <span class="fsize">{{ fmtBytes(item.msg.media.size) }}</span>
                        </div>
                      </template>
                    </div>
                    <div v-if="item.msg.text" class="text">{{ item.msg.text }}</div>
                    <!-- 链接预览卡片（Telegram 随消息附带的 web-page 预览，如 Telegraph 链接） -->
                    <div
                      v-if="item.msg.linkPreview"
                      class="link-preview"
                      @click.stop="openTelegraph(item.msg)"
                    >
                      <div v-if="item.msg.linkPreview.hasPhoto" class="lp-thumb">
                        <LazyThumb :src="api.mediaThumbUrl(current.id, item.msg.id)" placeholder="" />
                      </div>
                      <div class="lp-body">
                        <div class="lp-title">{{ item.msg.linkPreview.title || item.msg.linkPreview.url }}</div>
                        <div v-if="item.msg.linkPreview.desc" class="lp-desc">{{ item.msg.linkPreview.desc }}</div>
                        <div class="lp-site">
                          <ExternalLink :size="11" />
                          {{ item.msg.linkPreview.site || siteFromUrl(item.msg.linkPreview.url) }}
                        </div>
                      </div>
                    </div>
                    <div class="meta">
                      <span v-if="item.msg.views" class="meta-views"><Eye :size="11" /> {{ item.msg.views }}</span>
                      <ReactionBadge v-for="(rc, i) in item.msg.reactions" :key="i" :emoji="rc.emoji" :count="rc.count" />
                      <span
                        v-if="item.msg.replies && item.msg.replies.count > 0 && item.msg.replies.channelId"
                        class="meta-views meta-comments"
                        title="查看评论区"
                        @click.stop="openComments(item.msg)"
                      >
                        <MessageCircle :size="11" /> {{ item.msg.replies.count }}
                      </span>
                      <span>{{ fmtDate(item.msg.date) }}</span>
                    </div>
                  </div>
                  <!-- 下载按钮：气泡外右下角悬浮（meta 行只留信息）。已下载变勾 -->
                  <button
                    class="media-dl-float"
                    :class="{ downloaded: isDownloaded(String(current.id), item.msg.id) }"
                    :disabled="!item.msg.hasMedia"
                    @click.stop="downloadSingle(item.msg)"
                    :title="!item.msg.hasMedia ? '无媒体' : (isDownloaded(String(current.id), item.msg.id) ? '已下载，点击可重新下载' : '下载媒体')"
                  >
                    <CircleCheckBig v-if="item.msg.hasMedia && isDownloaded(String(current.id), item.msg.id)" :size="18" />
                    <Download v-else :size="16" />
                  </button>
                  </div>
                </div>
              </div>
            </DynamicScrollerItem>
          </DynamicScroller>

          <div v-if="hasMore" class="loading-tip">
            <el-button size="small" text :loading="loadingMore" @click="loadMore">{{ loadingMore ? '加载中…' : '加载更早的消息' }}</el-button>
          </div>
          <div v-else-if="displayItems.length" class="loading-tip muted">已到最早的消息</div>
        </div>

        <!-- 回到最新 -->
        <transition name="fab">
          <button v-if="showBackTop" class="back-top" @click="backToTop" title="回到最新"><ArrowUp :size="20" /></button>
        </transition>
      </template>
    </section>

    <MediaViewer
      v-model="viewer.show"
      :list="mediaMessages"
      :start-index="viewer.index"
      :chat-id="current ? current.id : ''"
      @download="downloadSingle"
    />

    <TelegraphReader
      v-if="telegraphReader.show"
      :chat-id="telegraphReader.chatId"
      :msg-id="telegraphReader.msgId"
      @close="telegraphReader.show = false"
    />

    <CommentsPanel
      v-if="commentsPanel.show"
      :chat-id="commentsPanel.chatId"
      :discussion-group-id="commentsPanel.discussionGroupId"
      :msg-id="commentsPanel.msgId"
      @close="commentsPanel.show = false"
    />

    <el-dialog v-model="rangeDialog" title="按范围下载" width="520px">
      <el-form label-width="90px">
        <el-form-item label="会话"><span>{{ current?.visibleName || current?.username }}</span></el-form-item>
        <el-form-item label="范围">
          <el-radio-group v-model="range.mode">
            <el-radio value="all">全部媒体</el-radio>
            <el-radio value="date">按日期</el-radio>
            <el-radio value="count">最近 N 条</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="range.mode === 'date'" label="日期范围">
          <div class="date-range-wrap">
            <el-date-picker
              v-model="range.dateRange"
              type="daterange"
              range-separator="至"
              start-placeholder="开始日期"
              end-placeholder="结束日期"
              :disabled-date="disableDateOutOfBounds"
              format="YYYY-MM-DD"
              value-format="YYYY-MM-DD"
              unlink-panels
              clearable
              style="width: 100%"
            />
            <div class="date-bounds-hint">
              <span v-if="rangeDateBounds.loading" class="muted">正在探测频道日期范围…</span>
              <span v-else-if="rangeDateBounds.min" class="muted">
                频道媒体范围：{{ rangeDateBounds.min }} ～ {{ rangeDateBounds.max }}
                <el-button link type="primary" size="small" @click="range.dateRange = [rangeDateBounds.min, rangeDateBounds.max]">选全部</el-button>
              </span>
              <span v-else class="muted">未探测到日期范围，可直接手动选择
                <el-button link type="primary" size="small" @click="probeDateBounds(true)">重试</el-button>
              </span>
            </div>
          </div>
        </el-form-item>
        <el-form-item v-if="range.mode === 'count'" label="条数">
          <el-input-number v-model="range.count" :min="1" :max="5000" />
        </el-form-item>
        <el-form-item label="类型过滤">
          <el-input v-model="range.include" placeholder="包含扩展名，如 mp4,zip（留空全部）" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="rangeDialog = false">取消</el-button>
        <el-button @click="downloadAll" :loading="rangeLoading">全部下载</el-button>
        <el-button type="primary" :loading="rangeLoading" @click="downloadRange">创建下载任务</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { DynamicScroller, DynamicScrollerItem } from 'vue-virtual-scroller'
import 'vue-virtual-scroller/dist/vue-virtual-scroller.css'
import { api } from '../api'
import { useAppStore } from '../stores/app'
import { usePlayerStore } from '../stores/player'
import { fmtBytes } from '../utils/format'
import { isImage, isVideo, isAudio } from '../utils/media'
import MediaViewer from '../components/MediaViewer.vue'
import LazyThumb from '../components/LazyThumb.vue'
import TelegraphReader from '../components/TelegraphReader.vue'
import CommentsPanel from '../components/CommentsPanel.vue'
import ReactionBadge from '../components/ReactionBadge.vue'
import { Play, Pause, Download, RefreshCw, Eye, FileText, Loader2, ArrowUp, ExternalLink, MessageCircle, CircleCheckBig } from 'lucide-vue-next'
import { calculateAlbumLayout } from '../utils/albumLayout'
import { loadDownloadHistory, addDownloadedKey, isDownloaded } from '../utils/downloadHistory'

const router = useRouter()
const store = useAppStore()
const player = usePlayerStore()

const chats = ref([])
const chatsLoading = ref(false)
const search = ref('')
const visibleChatCount = ref(30)
const chatListEl = ref(null)
const chatListW = ref(parseInt(localStorage.getItem('tmm-chatlist-w')) || 300)
const chatSentinelEl = ref(null)
const current = ref(null)

// 拖动调整群组列表宽度：mousedown 记录起点，mousemove 改宽度，mouseup 解绑。
// 限制 220~560px，避免过窄压字/过宽挤压消息区。
function startResizeChat(e) {
  e.preventDefault()
  const startX = e.clientX
  const startW = chatListW.value
  const onMove = (ev) => {
    const w = startW + (ev.clientX - startX)
    chatListW.value = Math.min(560, Math.max(220, w))
  }
  const onUp = () => {
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    localStorage.setItem('tmm-chatlist-w', String(chatListW.value))
  }
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
}

const messages = ref([])
const msgsLoading = ref(false)
const loadingMore = ref(false)
const nextCursor = ref(0)
const hasMore = ref(false)
const mediaOnly = ref(false)
const selected = ref(new Set())
const showBackTop = ref(false)
const scrollerRef = ref(null) // DynamicScroller，用于"定位到消息" + 滚动元素
// 连接重连中提示：后端返回 503 时置 true，前端自动轮询重试
const reconnecting = ref(false)

// --- 频道内搜索状态（独立于 messages/session，不污染现有分页/筛选） ---
const searchOpen = ref(false)
const searchQuery = ref('')
// IME 组合态标记：中文输入（拼音）组合期间不触发搜索，compositionend 后才搜
const composing = ref(false)
const searchResults = ref([])
const searchOffset = ref(0) // 全文源续拉游标（0 = 已拉完）
const searchOffsetTag = ref(0) // 标签源续拉游标（0 = 已拉完），无 # 并集搜索用
const searchCount = ref(0)
const searchLoading = ref(false)
const searchHasMore = ref(false)
let searchTimer = null
let searchSeq = 0 // 搜索请求序号：被丢弃的旧响应不得误清新请求的 loading 态

// 空查询校验（spec §3.0）：去 # 后仍有非空白字符才有效，否则不发起请求
function validSearchQuery(q) {
  return q.replace(/#/g, '').trim().length > 0
}

function onSearchInput() {
  clearTimeout(searchTimer)
  if (composing.value) return // IME 组合中（拼音），等 compositionend 再搜
  if (!validSearchQuery(searchQuery.value)) {
    searchResults.value = []
    searchHasMore.value = false
    return // 空查询不发起请求（spec §3.0）
  }
  searchTimer = setTimeout(() => doSearch(0), 300) // 防抖
}

async function doSearch(offset, offsetTag = 0) {
  if (!current.value || !validSearchQuery(searchQuery.value)) return
  const chatId = current.value.id
  const q = searchQuery.value // 捕获发起时的查询词
  const seq = ++searchSeq
  searchLoading.value = true
  try {
    const d = await api.searchMessages(chatId, { q, offset, offsetTag })
    // 竞态守卫：响应时查询词或频道已变则丢弃（旧词响应不得覆盖新词结果）
    if (current.value?.id !== chatId || searchQuery.value !== q) return
    if (offset === 0) {
      searchResults.value = d.results || []
      searchOffset.value = d.nextOffset || 0
      searchOffsetTag.value = d.nextOffsetTag || 0
      searchCount.value = d.count || 0
    } else {
      searchResults.value.push(...(d.results || []))
      searchOffset.value = d.nextOffset || 0
      searchOffsetTag.value = d.nextOffsetTag || 0
    }
    // 并集双游标：任一源还有余量即可续拉（0 = 该源已拉完）
    searchHasMore.value = searchOffset.value > 0 || searchOffsetTag.value > 0
  } catch (e) {
    if (e.name === 'AbortError') return
    ElMessage.error(e.message || '搜索失败')
  } finally {
    // 只有最新一次请求才允许清 loading，避免被丢弃的旧响应误清新请求的加载态
    if (seq === searchSeq) searchLoading.value = false
  }
}

function closeSearch() {
  clearTimeout(searchTimer)
  searchOpen.value = false
  searchQuery.value = ''
  searchResults.value = []
  searchHasMore.value = false
}
function clearSearch() {
  searchQuery.value = ''
  searchResults.value = []
  searchHasMore.value = false
}
// 清除左侧会话搜索框内容（✕ 按钮）
function clearChatSearch() {
  search.value = ''
}
// 点击搜索框/下拉以外区域关闭下拉（document 级 mousedown，仿 Tasks.vue onDocClick 模式）。
// 下拉本身也视为搜索组件的一部分：点滚动条/空白处不清，点结果项由 jumpToResult 关闭。
function onDocMouseDown(e) {
  if (!searchOpen.value) return
  const t = e.target
  if (t && t.closest && !t.closest('.chat-search-inline') && !t.closest('.search-dropdown')) {
    closeSearch()
  }
}
// 滚动结果到底部续拉（并集双游标：全文/标签各自推进，任一有余量继续）
function onSearchScroll(e) {
  const el = e.target
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 100 && searchHasMore.value && !searchLoading.value) {
    doSearch(searchOffset.value, searchOffsetTag.value)
  }
}
// 点击结果：取上下文 → 替换列表 → 定位
async function jumpToResult(res) {
  if (!current.value) return
  const chatId = current.value.id // 捕获跳转发起时的频道
  closeSearch()
  try {
    const d = await api.chatAround(chatId, res.id, 50)
    // 竞态守卫：请求期间切了频道则丢弃，避免旧频道上下文覆盖新频道列表/污染 chatState
    if (current.value?.id !== chatId) return
    const msgs = d.messages || []
    if (!msgs.length) {
      ElMessage.warning('消息不存在或已删除')
      return
    }
    messages.value = msgs // 替换列表（进入目标上下文视图，spec §3.4）
    nextCursor.value = msgs[msgs.length - 1].id // 对齐：往前可继续加载
    hasMore.value = true
    // 同步会话内存缓存（chatState 为 per-chat 会话内存），切走再切回仍停留在目标上下文
    chatState.set(chatId, {
      messages: msgs,
      nextCursor: msgs[msgs.length - 1].id,
      hasMore: true,
      mediaOnly: mediaOnly.value,
      scrollTop: 0,
    })
    await nextTick()
    scrollToMsg(res.id)
  } catch (e) {
    ElMessage.error(e.message || '跳转失败')
  }
}
// searchSnippet 把消息全文的换行/连续空白转成单个空格，形成连续文本再截断。
// 与 firstLine（只取首行）不同：多行消息（首行 #标签 标题 + 后续正文）会完整
// 铺开填充搜索下拉的右侧空白，避免只显示一行标题。
function searchSnippet(text, max = 150) {
  if (!text) return ''
  const s = String(text).replace(/\s+/g, ' ').trim()
  if (!s) return ''
  return s.length > max ? s.slice(0, max) + '…' : s
}

const viewer = ref({ show: false, index: 0 })
// Telegraph 站内阅读弹层：点击消息卡片上的链接预览打开
const telegraphReader = ref({ show: false, chatId: '', msgId: 0 })
// 评论区弹层：点击消息卡片 meta 行的评论数打开（只读）。
// chatId = 帖子所在频道（getReplies 的 peer），discussionGroupId = 讨论组
// （评论媒体缩略图所在会话），msgId = 频道帖子 ID。
const commentsPanel = ref({ show: false, chatId: '', discussionGroupId: '', msgId: 0 })

function openComments(m) {
  if (!current.value || !m.replies || !m.replies.channelId) return
  commentsPanel.value = { show: true, chatId: String(current.value.id), discussionGroupId: String(m.replies.channelId), msgId: m.id }
}
const rangeDialog = ref(false)
const rangeLoading = ref(false)
const range = ref({ mode: 'all', count: 100, include: '', dateRange: [] })
// 频道媒体消息的日期边界（最早/最晚），切到"按日期"模式时懒探测。
// 只翻少量页拿近似边界，避免对海量频道全量翻页触发大量 getHistory RPC。
const rangeDateBounds = ref({ min: null, max: null, loading: false })
let probedChatId = null // 已探测过的频道，切回不重复探测

// per-chat session memory: { messages, nextCursor, hasMore, scrollTop, mediaOnly }
const chatState = new Map()
let msgAbort = null
let retryTimer = null // 503 重连重试的定时器，切频道时清除

const allFilteredCount = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return chats.value.length
  return chats.value.filter((c) =>
    (c.visibleName || '').toLowerCase().includes(q) || (c.username || '').toLowerCase().includes(q)).length
})

const filteredChats = computed(() => {
  const q = search.value.trim().toLowerCase()
  const list = [...chats.value]
  const filtered = !q ? list : list.filter((c) =>
    (c.visibleName || '').toLowerCase().includes(q) || (c.username || '').toLowerCase().includes(q))
  return filtered.slice(0, visibleChatCount.value)
})

// group consecutive messages with the same groupedId into albums.
const displayItems = computed(() => {
  // key 必须跨频道唯一：Telegram 消息 ID 是 per-chat 的（每频道从 1 开始），
  // 不同频道的 msg 100 会产生相同 key 'msg-100'，导致 DynamicScroller 切频道时
  // 复用旧 DOM -> 旧频道的缩略图错误显示在新频道消息上。加 chatId 前缀。
  const chatKey = current.value?.id || '0'
  const items = []
  const seen = new Map()
  // 未读分界：readInboxMaxId 处插入「未读消息」分隔线（列表倒序，第一条
  // id <= readInboxMaxId 的消息即已读区顶部，在其前插入）。
  const readBoundary = current.value?.readInboxMaxId > 0 ? current.value.readInboxMaxId : 0
  let dividerAdded = false
  for (const m of messages.value) {
    if (readBoundary > 0 && !dividerAdded && m.id <= readBoundary) {
      items.push({ type: 'divider', key: chatKey + '-divider-' + m.id })
      dividerAdded = true
    }
    if (m.groupedId && m.hasMedia && (isImage(m.media) || isVideo(m.media))) {
      if (seen.has(m.groupedId)) {
        items[seen.get(m.groupedId)].msgs.push(m)
      } else {
        seen.set(m.groupedId, items.length)
        items.push({
          type: 'album', key: chatKey + '-album-' + m.groupedId, groupedId: m.groupedId,
          msgs: [m], out: m.out, date: m.date, views: m.views,
        })
      }
    } else {
      items.push({ type: 'msg', key: chatKey + '-msg-' + m.id, msg: m })
    }
  }
  return items
})

const mediaMessages = computed(() => messages.value.filter((m) => m.hasMedia && (isImage(m.media) || isVideo(m.media))))

// Lazy-load more chats when the sentinel scrolls into view.
let chatObserver = null
// 下载历史 SSE 订阅（组件卸载时关闭）；task 事件里 files[].status=done → 更新已下载
let downloadHistoryEvents = null
function setupChatObserver() {
  if (chatObserver) chatObserver.disconnect()
  chatObserver = new IntersectionObserver((entries) => {
    for (const e of entries) {
      if (e.isIntersecting && visibleChatCount.value < allFilteredCount.value) {
        visibleChatCount.value += 30
      }
    }
  }, { root: chatListEl.value, rootMargin: '200px' })
  if (chatSentinelEl.value) chatObserver.observe(chatSentinelEl.value)
}

onMounted(async () => {
  await loadChats()
  await nextTick()
  setupChatObserver()
  document.addEventListener('mousedown', onDocMouseDown) // 外部点击关闭搜索下拉
  // 下载历史：初始化已下载坐标集（判断下载按钮是否变勾）
  refreshDownloadHistory()
  // 订阅任务事件：下载完成实时更新已下载状态（按钮从下载变勾）
  downloadHistoryEvents = subscribe('/api/events', (type, data) => {
    if (type !== 'task' || !data || !Array.isArray(data.files)) return
    for (const f of data.files) {
      if (f.status === 'done' && f.key) addDownloadedKey(f.key)
    }
  })
})

// 组件卸载时清理：IntersectionObserver、待中的消息请求、重试定时器，
// 避免 observer 泄漏和卸载后 ref 仍被写入。
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocMouseDown)
  if (chatObserver) { chatObserver.disconnect(); chatObserver = null }
  if (searchTimer) clearTimeout(searchTimer)
  if (downloadHistoryEvents) { downloadHistoryEvents(); downloadHistoryEvents = null }
  abortPending()
})
// 滚动元素 = DynamicScroller 的根 .vue-recycle-scroller（自带 overflow-y:auto）
// 外层 .msg-scroll 不再滚动，避免双层滚动脱节导致 loadMore 不触发
function scrollEl() {
  return scrollerRef.value?.$el || null
}

watch(chatSentinelEl, () => { setupChatObserver() })

// 同步当前频道的音频消息到全局播放器队列（prev/next 用）。
// watch messages+current，任何路径更新（reload/loadMore/backfill/切频道）
// 都会自动同步，且不会打断正在后台播放的歌。
watch([messages, current], () => {
  if (current.value) player.setQueue(messages.value, current.value.id)
}, { deep: false })

async function loadChats(refresh = false) {
  chatsLoading.value = true
  try {
    const r = await api.listChats(refresh)
    chats.value = r.chats || []
  } catch (e) { ElMessage.error(e.message) } finally { chatsLoading.value = false }
}

function selectChat(c) {
  // 先 abort 旧请求，防止旧数据覆盖新会话
  abortPending()
  closeSearch() // 切频道重置搜索状态，避免旧频道的结果残留
  if (current.value) saveChatState(current.value.id)
  current.value = c
  selected.value = new Set()
  // 切频道时重置按范围下载状态，避免旧频道探测结果/日期残留
  rangeDialog.value = false
  probedChatId = null
  rangeDateBounds.value = { min: null, max: null, loading: false }
  // 立即清空消息和布局缓存，避免新频道渲染瞬间残留旧频道的消息/缩略图
  albumLayoutCache.clear()

  const saved = chatState.get(c.id)
  if (saved) {
    messages.value = saved.messages
    nextCursor.value = saved.nextCursor
    hasMore.value = saved.hasMore
    mediaOnly.value = saved.mediaOnly
    nextTick(() => { const el = scrollEl(); if (el) el.scrollTop = saved.scrollTop })
  } else {
    // 无缓存：立即清空，reloadMessages 里也会清，但这里提前清避免旧消息闪现
    messages.value = []
    nextCursor.value = 0
    hasMore.value = false
    // 首次进入且频道有未读：从未读起点定位（reloadMessages 里 readInboxMaxId 生效）
    reloadMessages(false, true)
  }
}

function saveChatState(id) {
  // 不保存加载中的空状态：若 messages 还是空的（reloadMessages 尚未返回），
  // 存进 chatState 会让下次切回该频道命中空缓存，误显示"暂无消息"。
  if (!messages.value || messages.value.length === 0) return
  chatState.set(id, {
    messages: messages.value,
    nextCursor: nextCursor.value,
    hasMore: hasMore.value,
    mediaOnly: mediaOnly.value,
    scrollTop: scrollEl() ? scrollEl().scrollTop : 0,
  })
}

function abortPending() {
  if (msgAbort) { msgAbort.abort(); msgAbort = null }
  if (retryTimer) { clearTimeout(retryTimer); retryTimer = null }
}

function warmupThumbs() {
  if (!current.value) return
  const chatId = current.value.id
  const ids = messages.value.filter((m) => m.hasMedia).slice(0, 30).map((m) => m.id)
  let i = 0
  const step = () => {
    if (i >= ids.length || !current.value || current.value.id !== chatId) return
    const img = new Image()
    img.src = api.mediaThumbUrl(chatId, ids[i++])
    img.onload = img.onerror = () => setTimeout(step, 60)
  }
  for (let k = 0; k < 4; k++) setTimeout(step, k * 80)
}

async function reloadMessages(refresh = false, positionAtUnread = false) {
  if (!current.value) return
  await loadMessagesWithRetry(current.value.id, refresh, positionAtUnread)
}

// loadMessagesWithRetry 加载消息，遇到 503（连接重连中）时自动轮询重试。
// 最多重试 8 次，间隔 3s（总上限 ~24s），期间显示"连接重连中"提示。
// 成功或非 503 错误则正常返回。切频道时 abortPending 清除定时器终止循环。
// positionAtUnread：首次进入时定位到未读起点（readInboxMaxId+1）。
async function loadMessagesWithRetry(chatId, refresh, positionAtUnread = false) {
  const MAX_RETRY = 8
  const RETRY_INTERVAL = 3000
  // 首次加载定位：有未读时从第一条未读（readInboxMaxId+1）开始，仿桌面端
  const firstUnreadOffset = positionAtUnread && current.value?.readInboxMaxId > 0 ? current.value.readInboxMaxId + 1 : 0
  for (let attempt = 0; attempt <= MAX_RETRY; attempt++) {
    if (current.value?.id !== chatId) return // 已切走
    abortPending()
    msgAbort = new AbortController()
    const signal = msgAbort.signal
    msgsLoading.value = true
    if (attempt === 0) {
      // 首次清空，避免停留在旧频道内容的错觉
      messages.value = []
      albumLayoutCache.clear()
      nextTick(() => { const el = scrollEl(); if (el) el.scrollTop = 0 })
    }
    try {
      const r = await api.listMessages(chatId, { offsetId: firstUnreadOffset, limit: 50, mediaOnly: mediaOnly.value, refresh, signal })
      if (current.value?.id !== chatId) return // 已切走
      reconnecting.value = false
      msgsLoading.value = false
      messages.value = r.messages || []
      nextCursor.value = r.nextCursor || 0
      hasMore.value = (r.messages || []).length > 0 && r.nextCursor > 0
      // 数据到达后强制 scroller 定位到第一条（最新消息）：DynamicScroller 在
      // items 从空增长时内部偏移可能错乱（最新消息被推到视口外 -9864px），
      // scrollTop=0 不够（偏移是内部 itemSize 累积），用 scrollToItem(0) 让
      // 虚拟滚动按实测高度重排到顶部。
      nextTick(() => {
        try {
          if (scrollerRef.value?.scrollToItem) scrollerRef.value.scrollToItem(0)
        } catch { /* 首屏偶发，非致命 */ }
      })
      warmupThumbs()
      if (hasMore.value) backfill(chatId, signal)
      return
    } catch (e) {
      if (e.name === 'AbortError') return // 切走取消
      // 503 = 连接重连中：显示提示，延迟后重试
      if (e.status === 503 && attempt < MAX_RETRY && current.value?.id === chatId) {
        reconnecting.value = true
        msgsLoading.value = false
        await new Promise((resolve) => { retryTimer = setTimeout(resolve, RETRY_INTERVAL) })
        retryTimer = null
        continue
      }
      // 非 503 或重试耗尽：显示错误
      reconnecting.value = false
      msgsLoading.value = false
      if (e.status === 503) ElMessage.warning('连接重连超时，请稍后手动刷新')
      else ElMessage.error(e.message)
      return
    }
  }
}

// backfill fetches the next page in the background right after the first
// screen renders, so scrolling down doesn't hit an unloaded gap.
async function backfill(chatId, signal) {
  try {
    const r = await api.listMessages(chatId, { offsetId: nextCursor.value, limit: 200, mediaOnly: mediaOnly.value, signal })
    if (current.value?.id !== chatId) return
    const older = r.messages || []
    if (older.length) {
      messages.value = [...messages.value, ...older]
      nextCursor.value = r.nextCursor || 0
      hasMore.value = r.nextCursor > 0
    }
  } catch (e) { if (e.name !== 'AbortError') { /* backfill failure is non-fatal */ } }
}

async function loadMore() {
  if (!current.value || !nextCursor.value || loadingMore.value) return
  abortPending()
  msgAbort = new AbortController()
  loadingMore.value = true
  try {
    const r = await api.listMessages(current.value.id, { offsetId: nextCursor.value, limit: 200, mediaOnly: mediaOnly.value, signal: msgAbort.signal })
    const older = r.messages || []
    messages.value = [...messages.value, ...older]
    nextCursor.value = r.nextCursor || 0
    hasMore.value = older.length > 0 && r.nextCursor > 0
  } catch (e) {
    if (e.name !== 'AbortError') ElMessage.error(e.message)
  } finally { loadingMore.value = false }
}

function onMsgScroll() {
  const el = scrollEl()
  if (!el) return
  // 提前到距底部 600px 就加载更早消息，配合服务端 warmup，
  // 滑到的时候缩略图已缓存，不会卡在"刚加载还没下载"的状态
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 600 && hasMore.value && !loadingMore.value) {
    loadMore()
  }
  showBackTop.value = el.scrollTop > 400
}

function backToTop() {
  if (scrollEl()) scrollEl().scrollTo({ top: 0, behavior: 'smooth' })
}

function toggleSelect(id) {
  const s = new Set(selected.value)
  if (s.has(id)) s.delete(id); else s.add(id)
  selected.value = s
}

function toggleSelectAlbum(album) {
  const s = new Set(selected.value)
  const all = album.msgs.every((x) => s.has(x.id))
  for (const m of album.msgs) { if (all) s.delete(m.id); else s.add(m.id) }
  selected.value = s
}

function albumText(album) {
  for (const m of album.msgs) { if (m.text && m.text.trim()) return m.text }
  return ''
}

// albumComments returns the first message in the album that carries a real
// comment section (replies.count>0 && channelId). Same discussion group for
// the whole album, so any qualifying message opens the right comment panel.
function albumComments(album) {
  for (const m of album.msgs) {
    if (m.replies && m.replies.count > 0 && m.replies.channelId) return m
  }
  return null
}

// albumAllDownloaded 整组都下载过才算已下载（部分下载仍是下载图标，可补下）。
function albumAllDownloaded(album) {
  if (!album.msgs.length) return false
  return album.msgs.every((m) => isDownloaded(String(current.value?.id), m.id))
}

// --- 相册拼图布局（照 telegram-tt calculateAlbumLayout） ---
const albumLayoutCache = new Map()
function albumLayout(album) {
  const cached = albumLayoutCache.get(album.key)
  if (cached) return cached
  const ratios = album.msgs.map((m) => {
    const w = m.media?.width, h = m.media?.height
    if (w && h) return w / h
    const d = mediaDims.get(mediaDimKey(m))
    if (d && d.w && d.h) return d.w / d.h
    return 1
  })
  const layout = calculateAlbumLayout(ratios, MEDIA_MAX_W, 2)
  albumLayoutCache.set(album.key, layout)
  return layout
}
function albumCellStyle(album, ci) {
  const it = albumLayout(album).items[ci] || { x: 0, y: 0, width: 100, height: 100 }
  return {
    position: 'absolute',
    left: it.x + 'px', top: it.y + 'px',
    width: it.width + 'px', height: it.height + 'px',
  }
}

// media natural dimensions (plain Map, NOT reactive — no re-render on decode).
// key 带 chatId：消息 ID 是 per-chat 的，裸 ID 跨频道会串用旧频道的自然尺寸
// （和 DynamicScroller key 串台是同一类问题，见 displayItems 的 chatKey 前缀）。
// 跨频道累积复用（切回某频道无需 re-decode 已见过的图片），但设上限防止长时间
// 浏览无限增长：超过 MEDIA_DIMS_MAX 时丢弃最早插入的项（Map 保持插入顺序）。
const mediaDims = new Map()
const MEDIA_DIMS_MAX = 2000
function mediaDimKey(m) { return (current.value?.id ?? '0') + ':' + m.id }

function onThumbLoad(m, e) {
  const img = e.target
  if (img && img.naturalWidth && !mediaDims.has(mediaDimKey(m))) {
    if (mediaDims.size >= MEDIA_DIMS_MAX) {
      // 删最早插入的项（FIFO 淘汰，近似 LRU —— 简单且足够：浏览顺序通常是
      // 时间流式推进，旧消息很少再被看到）。
      mediaDims.delete(mediaDims.keys().next().value)
    }
    mediaDims.set(mediaDimKey(m), { w: img.naturalWidth, h: img.naturalHeight })
  }
}

const MEDIA_MAX_W = 464   // tt: 29rem = 464px
const MEDIA_MAX_H = 432   // tt: 27rem = 432px
const MEDIA_MIN_W = 192   // tt: 12rem = 192px

// mediaWidth returns the rendered pixel width of a message's media, so the
// bubble can size itself to the media (text wraps inside it) instead of
// letting long text stretch the bubble wider than the media — which left an
// empty gutter on the media's right. Mirrors tt's calculateDimensions.
function mediaWidth(m) {
  let w0 = m.media?.width, h0 = m.media?.height
  if (!w0 || !h0) {
    const d = mediaDims.get(mediaDimKey(m))
    if (d && d.w && d.h) { w0 = d.w; h0 = d.h }
  }
  if (!w0 || !h0) return MEDIA_MAX_W
  let w = Math.min(w0, MEDIA_MAX_W)
  let h = Math.round(w * (h0 / w0))
  if (h > MEDIA_MAX_H) {
    h = MEDIA_MAX_H
    w = Math.round(h * (w0 / h0))
  }
  const minW = MEDIA_MIN_W
  if (w < minW && minW - w < 12) {
    w = minW
  }
  return w
}

function thumbStyle(m) {
  let w0 = m.media?.width, h0 = m.media?.height
  if (!w0 || !h0) {
    const d = mediaDims.get(mediaDimKey(m))
    if (d && d.w && d.h) { w0 = d.w; h0 = d.h }
  }
  if (!w0 || !h0) {
    return { width: MEDIA_MAX_W + 'px', aspectRatio: '4 / 3' }
  }
  return { width: mediaWidth(m) + 'px', aspectRatio: w0 + ' / ' + h0 }
}

function openViewer(m) {
  if (!isImage(m.media) && !isVideo(m.media)) { downloadSingle(m); return }
  const idx = mediaMessages.value.findIndex((x) => x.id === m.id)
  viewer.value = { show: true, index: Math.max(0, idx) }
}

// 点击消息卡片上的链接预览：Telegraph 链接站内阅读，其余在新标签打开
function openTelegraph(m) {
  if (!current.value || !m.linkPreview) return
  if (isTelegraphUrl(m.linkPreview.url)) {
    telegraphReader.value = { show: true, chatId: String(current.value.id), msgId: m.id }
  } else {
    window.open(m.linkPreview.url, '_blank', 'noopener')
  }
}

// 判断链接是否是 telegra.ph（决定站内阅读还是跳外链）
function isTelegraphUrl(u) {
  try { return new URL(u).hostname.replace(/^www\./, '') === 'telegra.ph' } catch { return false }
}

// 从链接里取站点名（预览缺 site 字段时的兜底显示）
function siteFromUrl(u) {
  try { return new URL(u).hostname } catch { return u || '' }
}

// 点击音频 chip：交给全局底部播放器（单实例互斥，滚动/切频道不断播）
function playAudio(m) {
  if (!current.value) return
  player.setQueue(messages.value, current.value.id)
  player.play({ chatId: current.value.id, msgId: m.id, name: m.media.name, size: m.media.size })
}

// 定位到消息：在 displayItems（相册聚合后）里找该消息的索引并滚动过去。
// 命中消息可能被聚合成相册项（type === 'album'，msgs 数组），需在 msgs 里
// 匹配——否则搜索命中带 caption 的相册消息时找不到目标，scroller 停在旧位置。
function scrollToMsg(msgId) {
  const idx = displayItems.value.findIndex((it) =>
    (it.type === 'msg' && it.msg?.id === msgId) ||
    (it.type === 'album' && (it.msgs || []).some((m) => m.id === msgId))
  )
  if (idx >= 0 && scrollerRef.value) {
    scrollerRef.value.scrollToItem(idx)
  }
}
// 全局播放器"定位到消息"：player.locateMsgId 变化时滚动
watch(() => player.locateMsgId, (id) => {
  if (id) nextTick(() => scrollToMsg(id))
})
// 切到"按日期"模式才懒探测频道日期边界（不在打开对话框时探测，
// 避免对海量频道连发 getHistory RPC 拖垮切频道/媒体加载）
watch(() => range.value.mode, (mode) => {
  if (mode === 'date') probeDateBounds()
})

function onThumbError() { /* LazyThumb 内部处理：404 时保留清晰 placeholder，无需降透明度 */ }

async function downloadSelected() {
  if (!selected.value.size || !current.value) return
  await createTask([{ chatId: String(current.value.id), msgIds: [...selected.value] }], {})
}
// refreshDownloadHistory 拉取任务列表，重建已下载坐标集（判断下载按钮是否变勾）。
// 失败静默（下载历史是增强，不影响主流程）。
async function refreshDownloadHistory() {
  try {
    const tasks = await api.listTasks()
    loadDownloadHistory(Array.isArray(tasks) ? tasks : (tasks && tasks.tasks))
  } catch { /* 忽略：历史加载失败不阻塞浏览 */ }
}

async function downloadSingle(m) {
  await createTask([{ chatId: String(current.value.id), msgIds: [m.id] }], {})
}

async function downloadAlbum(album) {
  const ids = album.msgs.map((m) => m.id)
  await createTask([{ chatId: String(current.value.id), msgIds: ids }], { group: true })
}
// 把 unix 秒戳格式化成 YYYY-MM-DD（本地时区），用于日期边界与过滤
function dateStrFromTs(ts) {
  if (!ts) return null
  const d = new Date(ts * 1000)
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}
// [start, end] 闭区间，start/end 为 'YYYY-MM-DD'，返回 [startTs00:00, endTs23:59:59]（秒）
function dateRangeToTsBounds(start, end) {
  const s = start ? Math.floor(new Date(start + 'T00:00:00').getTime() / 1000) : -Infinity
  const e = end ? Math.floor(new Date(end + 'T23:59:59').getTime() / 1000) : Infinity
  return [s, e]
}

// 探测频道媒体消息的日期边界，作为日期选择参考。
// 设计要点：绝不全量翻页（曾因此对海量频道连发几十次 getHistory RPC，
// 触发网络抖动导致切频道/媒体加载卡死）。只取首页拿最新日期，再翻少量
// 页拿一个近似的最早日期作为参考下界；用户实际选择时仍可自由输入。
// 串行、限页、同频道只探测一次。
async function probeDateBounds(force = false) {
  const chatId = current.value?.id
  if (!chatId) return
  if (!force && probedChatId === chatId && rangeDateBounds.value.min) return
  probedChatId = chatId
  rangeDateBounds.value = { min: null, max: null, loading: true }
  try {
    const first = await api.listMessages(chatId, { offsetId: 0, limit: 100, mediaOnly: true })
    const batch = (first.messages || []).filter((m) => m.hasMedia)
    if (!batch.length) { rangeDateBounds.value = { min: null, max: null, loading: false }; return }
    const max = dateStrFromTs(batch[0].date) // 首页第一条最新
    // 最早日期：最多再翻 3 页取一个近似下界，避免海量频道全量翻页。
    let cursor = first.nextCursor || 0
    let lastBatch = batch
    for (let i = 0; i < 3 && cursor; i++) {
      const r = await api.listMessages(chatId, { offsetId: cursor, limit: 100, mediaOnly: true })
      const b = (r.messages || []).filter((m) => m.hasMedia)
      if (!b.length) break
      lastBatch = b
      cursor = r.nextCursor || 0
    }
    const min = dateStrFromTs(lastBatch[lastBatch.length - 1].date)
    rangeDateBounds.value = { min, max, loading: false }
  } catch (e) {
    rangeDateBounds.value = { min: null, max: null, loading: false }
  }
}
function disableDateOutOfBounds(d) {
  if (!rangeDateBounds.value.max) return false
  // 只禁未来日期（超过频道最新消息）；最早下界是近似值（仅翻 3 页），
  // 不强制禁用以免误挡合法的更早消息，用户可自由选择。
  const max = new Date(rangeDateBounds.value.max + 'T23:59:59').getTime()
  return d.getTime() > max
}

// "全部下载"：跳过范围设定，直接下载该频道全部媒体
async function downloadAll() {
  range.value.mode = 'all'
  await downloadRange()
}

async function downloadRange() {
  rangeLoading.value = true
  try {
    // 日期模式校验
    if (range.value.mode === 'date') {
      const dr = range.value.dateRange
      if (!dr || dr.length !== 2 || !dr[0] || !dr[1]) {
        ElMessage.warning('请选择日期范围'); return
      }
    }
    const ids = []
    let cursor = 0
    const [lo, hi] = range.value.mode === 'date'
      ? dateRangeToTsBounds(range.value.dateRange[0], range.value.dateRange[1])
      : [-Infinity, Infinity]
    const want = range.value.mode === 'count' ? range.value.count : Infinity
    // 消息按时间倒序返回：先经过 hi 上界，进入 [lo,hi] 区间收集，越过 lo 下界即提前停止。
    let stop = false
    while (ids.length < want && !stop) {
      const r = await api.listMessages(current.value.id, { offsetId: cursor, limit: 100, mediaOnly: true })
      const batch = r.messages || []
      if (!batch.length) break
      for (const m of batch) {
        if (!m.hasMedia) continue
        // 比当前 hi 新 → 还未进入区间，跳过
        if (m.date > hi) continue
        // 比当前 lo 旧 → 已越过区间下界，停止整次收集
        if (m.date < lo) { stop = true; break }
        ids.push(m.id)
        if (ids.length >= want) break
      }
      cursor = r.nextCursor || 0
      if (!cursor) break
    }
    if (!ids.length) { ElMessage.warning('该范围内没有媒体消息'); return }
    const include = range.value.include.split(',').map((x) => x.trim()).filter(Boolean)
    await createTask([{ chatId: String(current.value.id), msgIds: ids }], { include })
    rangeDialog.value = false
  } finally { rangeLoading.value = false }
}

async function createTask(sel, extraOpts) {
  const account = store.authorizedAccount
  if (!account) { ElMessage.warning('请先登录账号'); router.push('/login'); return }
  try {
    await api.createTask({
      account: account.ns, urls: [], selected: sel,
      opts: { takeout: 'auto', group: true, skipSame: true, ...extraOpts },
    })
    ElMessage.success('下载任务已创建')
    selected.value = new Set()
    router.push('/tasks')
  } catch (e) { ElMessage.error(e.message) }
}

function typeLabel(t) { return { private: '私聊', group: '群组', channel: '频道' }[t] || t }
function fmtDate(ts) {
  if (!ts) return ''
  return new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
</script>

<style scoped>
.msg-wrap { flex: 1; display: flex; min-height: 0; }
.chat-list { flex: none; display: flex; flex-direction: column; background: var(--bg-panel); border-right: 1px solid var(--border); min-height: 0; position: relative; }
.chat-resizer { position: absolute; top: 0; right: -3px; width: 6px; height: 100%; cursor: col-resize; z-index: 5; }
.chat-resizer:hover, .chat-resizer:active { background: var(--accent); opacity: .3; }
.chat-search { padding: 10px 12px; display: flex; align-items: center; gap: 6px; }
/* 会话搜索框：input 外包一层定位容器，容纳右侧 ✕ 清除按钮 */
.chat-search-input-wrap { flex: 1; min-width: 0; position: relative; }
.chat-search input { width: 100%; height: 36px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg); color: var(--text); padding: 0 28px 0 12px; outline: none; font-size: 13px; }
.chat-search input:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
.chat-refresh-btn { flex: none; width: 32px; height: 32px; border: none; border-radius: var(--radius-sm); background: transparent; color: var(--text-3); cursor: pointer; display: flex; align-items: center; justify-content: center; transition: .15s; }
.chat-refresh-btn:hover:not(:disabled) { background: var(--bg-hover); color: var(--text); }
.chat-refresh-btn:disabled { opacity: .6; cursor: default; }
.chat-refresh-btn .spin { animation: spin 0.8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
.chat-items { flex: 1; overflow-y: auto; padding: 8px 10px 12px; display: flex; flex-direction: column; gap: 6px; }
.chat-item { display: flex; padding: 9px 12px; border-radius: var(--radius); cursor: pointer; transition: .13s; align-items: center; background: var(--bg-panel); border: 1px solid var(--border); box-shadow: var(--shadow); }
.chat-item:hover { border-color: var(--accent); }
.chat-item.active { border-color: var(--accent); background: var(--accent-soft); }
.chat-item .meta { flex: 1; min-width: 0; }
.chat-item .row1 { display: flex; align-items: center; gap: 8px; }
.chat-item .name { font-weight: 600; font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
.chat-item .unread { min-width: 20px; height: 20px; border-radius: var(--radius-sm); background: var(--accent); color: #fff; font-size: 11px; font-weight: 700; display: inline-flex; align-items: center; justify-content: center; padding: 0 6px; flex: none; }
.chat-item .preview { font-size: 12.5px; color: var(--text-2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; margin-top: 3px; }
.load-more { text-align: center; padding: 10px; font-size: 12px; }

.chat-view { flex: 1; display: flex; flex-direction: column; min-width: 0; background: var(--bg); position: relative; }
.chat-empty { flex: 1; display: flex; align-items: center; justify-content: center; }
.chat-header { height: 56px; flex: none; display: flex; align-items: center; gap: 12px; padding: 0 18px; background: var(--bg-panel); border-bottom: 1px solid var(--border); }
.chat-header .hinfo { min-width: 0; }
.chat-header .title { font-weight: 700; font-size: 15px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.chat-header .sub { font-size: 12px; }
.chat-header .spacer { flex: 1; }

/* 频道内全文搜索：头部内联搜索框 */
.chat-search-inline { flex: none; position: relative; }
.chat-search-inline input {
  width: 260px; height: 32px;
  border: 1px solid var(--border); border-radius: var(--radius-sm);
  background: var(--bg); color: var(--text);
  padding: 0 28px 0 12px; outline: none; font-size: 13px;
  transition: border-color .15s, box-shadow .15s, width .2s;
}
.chat-search-inline input::placeholder { color: var(--text-3); }
.chat-search-inline input:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
.chat-search-inline.open input { width: 320px; }
.search-clear {
  position: absolute; right: 4px; top: 50%; transform: translateY(-50%);
  width: 20px; height: 20px; border: none; border-radius: 50%;
  background: var(--bg-hover); color: var(--text-3);
  font-size: 11px; line-height: 1; cursor: pointer;
  display: flex; align-items: center; justify-content: center;
  transition: .13s;
}
.search-clear:hover { background: var(--accent); color: #fff; }

/* 搜索结果下拉：覆盖在消息区上方 */
.search-dropdown {
  position: absolute; top: 56px; left: 18px; right: 18px; z-index: 20;
  max-height: 380px; overflow-y: auto;
  background: var(--bg-panel); border: 1px solid var(--border);
  border-radius: var(--radius); box-shadow: var(--shadow);
  padding: 6px;
}
.search-drop-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 4px 8px 6px; font-size: 12px; color: var(--text-3);
}
.search-item {
  display: flex; align-items: center; gap: 10px;
  padding: 7px 8px; border-radius: var(--radius-sm);
  cursor: pointer; transition: .13s;
}
.search-item:hover { background: var(--bg-hover); }
.search-thumb { flex: none; width: 36px; height: 36px; border-radius: var(--radius-sm); overflow: hidden; background: var(--bg-hover); }
.search-item-body { flex: 1; min-width: 0; }
.search-item-text {
  font-size: 13px; line-height: 1.5;
  /* 多行展示（最多 3 行），填充右侧空白；超长省略号 */
  display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical;
  overflow: hidden; white-space: normal; word-break: break-word;
}
.search-item-date { font-size: 11px; margin-top: 2px; }
.search-empty, .search-more { text-align: center; padding: 12px 0; font-size: 12px; }

.msg-scroll { flex: 1; display: flex; flex-direction: column; min-height: 0; padding: 0; }
/* DynamicScroller 自带 overflow-y:auto 作为唯一滚动容器（.vue-recycle-scroller），
   不再用外层 .msg-scroll 滚动——之前 height:100% 把它的内容高度钳制到视口高，
   导致虚拟滚动 padding 撑不开、外层 scrollHeight 只有 ~648px、loadMore 不触发、
   相册多图 cell 被定位到视口外(-9864px)永远不加载真实缩略图 */
.msg-scroll .scroller { flex: 1; min-height: 0; padding: 16px 24px 20px; }
.loading-tip { text-align: center; padding: 8px 0; font-size: 12.5px; }
.reconnect-tip { color: var(--accent, #4f7cff); animation: reconnect-pulse 1.4s ease-in-out infinite; }

/* 骨架屏：加载消息时的占位卡片，按媒体比例预绘 + 波形动画（借鉴 tt Skeleton）。
   主题色驱动，亮暗自适应。 */
.skeleton-list { padding: 16px 24px; display: flex; flex-direction: column; gap: 16px; }
.skel-row { display: flex; justify-content: flex-start; }
.skel-bubble { background: var(--bg-panel); border-radius: var(--radius-lg); padding: 8px; box-shadow: 0 1px 2px rgba(0,0,0,.06); overflow: hidden; }
.skel-media { height: 120px; border-radius: var(--radius); margin-bottom: 8px; background: var(--bg-hover); }
.skel-line { height: 10px; border-radius: var(--radius-sm); margin: 5px 0; background: var(--bg-hover); }
.skel-line.w60 { width: 160px; }
.skel-line.w40 { width: 100px; }
.skel-media, .skel-line { background: linear-gradient(90deg, var(--bg-hover) 25%, var(--border) 50%, var(--bg-hover) 75%); background-size: 200% 100%; animation: skel-wave 1.4s ease-in-out infinite; }
@keyframes skel-wave { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }
@keyframes reconnect-pulse { 0%,100% { opacity: .5 } 50% { opacity: 1 } }
.msg-row { padding: 3px 0; width: 100%; }
/* 未读分隔线：居中短横线 + 文字，仿桌面端「未读消息」标记 */
.unread-divider {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 0; font-size: 11px; color: var(--accent);
}
.unread-divider::before, .unread-divider::after {
  content: ''; flex: 1; height: 1px; background: var(--accent);
}
/* telegram-tt 布局：flex 行，内容定宽，CSS max-width 封顶 */
.msg { display: flex; align-items: flex-start; gap: 8px; margin-bottom: 6px; }
.msg.out { flex-direction: row-reverse; margin-left: auto; }
.msg .pick { flex: none; margin-top: 10px; }
.msg.selected .bubble { outline: 2px solid var(--accent); }

/* 下载按钮：小尾巴式悬浮——按钮整体在气泡右下角外侧，右侧与气泡留小空隙，
   底部贴齐气泡下缘（不盖住气泡内容、不越过气泡底部）。气泡有 overflow:hidden
   （圆角裁图），按钮必须放 wrap 层才能露在气泡外。 */
.bubble-wrap { position: relative; width: max-content; max-width: 100%; }
/* 悬浮下载按钮：白底圆钮 hover 显蓝。有媒体才显示（纯文本消息无下载） */
.media-dl-float {
  position: absolute;
  right: -34px;
  bottom: 0;
  width: 30px; height: 30px;
  border: none; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  background: var(--bg-panel);
  color: var(--text-3);
  box-shadow: 0 1px 4px rgba(0,0,0,.25);
  cursor: pointer;
  transition: .15s;
  z-index: 3;
  padding: 0;
}
.media-dl-float:hover:not(:disabled) { background: var(--accent); color: #fff; box-shadow: 0 2px 8px rgba(79,124,255,.4); }
.media-dl-float:disabled { display: none; }
/* 已下载状态：圆勾图标绿色（--green token），提示已下载过 */
.media-dl-float.downloaded { color: var(--green); }

/* 气泡：内容宽度，CSS max-width 封顶（tt: 29rem = 464px） */
.bubble {
  position: relative;
  padding: 9px 12px 8px; /* 纯文字气泡：上下 9/8px，左右 12px，不贴边（tt 文字气泡比媒体气泡 padding 大） */
  border-radius: var(--radius-lg); /* tt: 1rem */
  background: var(--bg-panel);
  box-shadow: var(--shadow);
  min-width: 200px;
  max-width: 464px; /* tt: 29rem */
  width: max-content; /* 内容定宽 */
  font-size: 14px;
  line-height: 1.3125; /* tt 行高 */
  text-rendering: geometricPrecision; /* 虚拟滚动合成层内文字用几何精度，避免小字号发糊 */
  -webkit-font-smoothing: antialiased;
}
.bubble.media { padding: 4px; overflow: hidden; max-width: 472px; box-sizing: border-box; }
/* 媒体气泡内：
   .text（正文）左右 8px 文字留白，顶部 8px 与上方媒体拉开呼吸（否则文字紧贴媒体底部）；
   .meta（时间+下载按钮行）右侧贴齐媒体（0），让下载按钮右边缘 = 媒体右边缘 = bubble 的 4px，
   左侧 8px 与 .text 文字对齐。上下 0（顶部由 margin-top 隔开，底部贴 bubble 4px）。*/
.bubble.media .text { padding: 0 8px; margin-top: 8px; }
.bubble.media .meta { padding: 0 0 0 8px; }
/* 相册气泡同样定宽到拼图宽度，文字在媒体宽度内换行，右侧不留白 */
.bubble.album { padding: 4px; max-width: 472px; overflow: hidden; box-sizing: border-box; }
.bubble.album .text { padding: 0 8px; margin-top: 8px; }
.bubble.album .meta { padding: 0 0 0 8px; }
/* 媒体气泡的 meta 顶部留 5px 呼吸（替代被去掉的 padding-top），与上方媒体/文字隔开 */
.bubble.media .meta, .bubble.album .meta { margin-top: 5px; }
.msg.out .bubble { background: var(--accent-soft); }

/* 文字：不折叠，完整显示（对齐 Telegram 桌面端行为） */
.bubble .text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  unicode-bidi: plaintext;
  line-height: 1.3125;
}
.bubble .meta { display: flex; justify-content: flex-end; align-items: center; gap: 8px; font-size: 11px; color: var(--text-3); margin-top: 3px; user-select: none; }
/* 浏览数：图标和文字 inline-flex 垂直居中对齐 */
.meta-views { display: inline-flex; align-items: center; gap: 3px; }
.meta-views svg { display: block; }
/* 评论区入口：有评论数时可点击（hover 提示） */
.meta-comments { cursor: pointer; }
.meta-comments:hover { color: var(--accent); }

.media-box { cursor: zoom-in; }
.thumb-wrap { position: relative; border-radius: var(--radius); overflow: hidden; }
.play-overlay { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; }
.play-ico { width: 52px; height: 52px; border-radius: 50%; background: rgba(0,0,0,.5); color: #fff; font-size: 20px; display: flex; align-items: center; justify-content: center; transition: .15s; }
.thumb-wrap:hover .play-ico { background: rgba(0,0,0,.7); transform: scale(1.06); }
.play-overlay .dur { position: absolute; right: 8px; bottom: 8px; background: rgba(0,0,0,.55); color: #fff; font-size: 11px; padding: 2px 7px; border-radius: var(--radius-sm); }
/* 下载按钮：meta 行内右侧，圆形无底色，hover 才显蓝底 */
.media-dl-btn { width: 30px; height: 30px; flex: none; margin-left: auto; border: none; border-radius: 50%; display: flex; align-items: center; justify-content: center; background: transparent; color: var(--text-3); cursor: pointer; transition: .15s; padding: 0; }
.media-dl-btn:hover:not(:disabled) { background: var(--accent); color: #fff; }
.media-dl-btn:disabled { opacity: .35; cursor: default; }

/* 链接预览卡片（web-page preview，如 Telegraph 链接）：横向配图 + 右侧标题/描述/站点 */
.link-preview {
  display: flex; gap: 12px; align-items: stretch;
  margin-top: 8px; max-width: 360px;
  border: 1px solid var(--border); border-radius: var(--radius);
  overflow: hidden; cursor: pointer; background: var(--bg);
  transition: .15s;
}
.link-preview:hover { border-color: var(--accent); }
.lp-thumb { flex: none; width: 96px; min-height: 64px; background: var(--bg-hover); }
.lp-thumb .lazy-thumb { width: 96px; height: 100%; }
.lp-body { flex: 1; min-width: 0; padding: 8px 10px; display: flex; flex-direction: column; gap: 3px; }
.lp-title { font-weight: 600; font-size: 13px; line-height: 1.4; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.lp-desc { font-size: 12px; color: var(--text-2); line-height: 1.4; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.lp-site { margin-top: auto; font-size: 11px; color: var(--text-3); display: flex; align-items: center; gap: 4px; }

/* 文件卡片：内容宽度 */
.file-chip { display: flex; align-items: center; gap: 10px; padding: 8px 10px; background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); max-width: 100%; box-sizing: border-box; }
.file-chip .fico { font-size: 18px; }
.file-chip .fname { flex: 1; min-width: 0; font-weight: 600; font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.file-chip .fsize { font-size: 12px; color: var(--text-2); flex: none; }

/* 音频消息：点击交给全局底部播放器 */
.audio-chip {
  display: flex; align-items: center; gap: 10px; padding: 8px 10px;
  max-width: 320px; box-sizing: border-box; cursor: pointer;
  background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius);
  transition: .15s;
}
.audio-chip:hover { background: var(--bg-hover); }
.audio-chip.playing { border-color: var(--accent); background: var(--accent-soft); }
.audio-chip .fico {
  width: 34px; height: 34px; flex: none; border-radius: 50%;
  background: var(--accent); color: #fff; font-size: 14px;
  display: flex; align-items: center; justify-content: center;
}
.audio-chip .ainfo { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.audio-chip .aname { font-weight: 600; font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.audio-chip .asub { font-size: 11px; color: var(--text-3); }
.audio-chip.playing .asub { color: var(--accent); }
.audio-chip .fsize { font-size: 12px; color: var(--text-2); flex: none; }

/* 相册（拼图布局，照 telegram-tt） */
.album-mosaic { position: relative; overflow: hidden; max-width: 100%; border-radius: var(--radius); }
.album-cell { position: absolute; overflow: hidden; cursor: zoom-in; background: var(--bg-hover); }
.album-cell img { width: 100%; height: 100%; object-fit: cover; display: block; }
.album-cell .cell-play { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; color: #fff; background: rgba(0,0,0,.3); }
.album-cell .cell-play svg { filter: drop-shadow(0 1px 3px rgba(0,0,0,.5)); }
.album-cell .cell-dur { position: absolute; right: 5px; bottom: 5px; background: rgba(0,0,0,.55); color: #fff; font-size: 10px; padding: 1px 5px; border-radius: var(--radius-sm); pointer-events: none; }
.bubble.album .meta { display: flex; justify-content: flex-end; align-items: center; gap: 8px; }
/* 回到最新 */
.back-top {
  position: absolute; right: 24px; bottom: 24px; z-index: 10;
  width: 44px; height: 44px; border: none; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  background: var(--accent); color: #fff; cursor: pointer; transition: .15s;
  box-shadow: 0 4px 12px rgba(0,0,0,.25);
}
.back-top:hover { filter: brightness(1.1); }
/* 进出场动画 */
.fab-enter-active, .fab-leave-active { transition: all .2s ease; }
.fab-enter-from, .fab-leave-to { opacity: 0; transform: scale(.8) translateY(8px); }

/* 按范围下载 - 日期选择 */
.date-range-wrap { width: 100%; }
.date-bounds-hint { margin-top: 6px; font-size: 12px; min-height: 18px; }
.date-bounds-hint .muted { color: var(--text-2); display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
</style>
