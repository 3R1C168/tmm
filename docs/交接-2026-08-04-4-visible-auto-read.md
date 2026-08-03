# tmm 项目交接文档（2026-08-04 会话 4 — 视口自动已读实施）

> **给新对话的完整交接**：本文档汇总本次会话（视口消息自动已读 spec + plan）的全部工作。新对话据此**实施**该功能，无需依赖旧会话上下文。
> 本目录（`tdl/docs/`）为跟随 git 的交接目录；独立交接目录在 `handoff/交接-2026-08-04-4-visible-auto-read.md`。
> 上一会话交接（评论区功能）见 `handoff/交接-2026-08-03-3-comments.md`；未读定位+已读同步见其 §3.12。

---

## 1. 项目概况（与前会话一致，简述）

- **仓库**：`D:\Documents\workspace\telegram manager\tdl`（git 分支 `master`）
- **GitHub**：`https://github.com/3R1C168/tmm`（私有，主线 `master`）
- **运行目录**：`D:\Documents\workspace\telegram manager\tmm\`
- **运行**：`tmm\start.bat` 或 `./tmm.exe serve --port 8080 --no-browser` → `http://127.0.0.1:8080`
- **构建**：`cd tdl && ./build-tmm.sh`（后端）/ `./build-tmm.sh web`（前端）
- **日志**：`tmm/logs/server-*.log`；**杀进程**：`powershell -Command "Get-Process tmm | Stop-Process -Force"`
- **环境**：Git Bash；Go 1.26.5；gotd v0.140.0；Vue 3.5 + vue-virtual-scroller@2.0.0-beta.8 + lucide-vue-next

## 2. 当前状态（2026-08-04 凌晨，本会话结束）

- ✅ 工作树干净（git 无未提交改动）
- ✅ 主线 = `master`，最新 commit **`15ed60ec`**（视口自动已读 plan）
- ✅ exe 已重建（评论区功能后 `0a446769` 版本）
- ⚠️ **服务器当前未运行**（上一会话验证后停服）——需要时 `start.bat` 启动
- ✅ 登录会话完好：`tmm/data/data/default`（Eric 5401443218）

## 3. 本次会话成果（视口自动已读：spec + plan 已就绪，**待实施**）

**背景**：用户要「滚动到当前显示区域的消息自动已读」——自动模式下滚动浏览时，视口内消息自动标已读，**未读数按已查看条数递减**（如未读 99，滚 9 条 → 未读 90，服务器端 `readHistory(maxID)` 天然支持，无需逐条 RPC）。

### 3.1 Spec：`docs/superpowers/specs/2026-08-04-visible-auto-read-design.md`

| 部分 | 设计 |
|---|---|
| 触发 | 自动模式（`autoRead=true`）下滚动时，视口内**最大消息 id** 自动已读 |
| 未读递减 | `readHistory(maxID)` 后服务器端未读 = maxID 之后数量——天然「看 N 条 → 未读减 N」 |
| 节流/去重 | 滚动停止 500ms 后调（debounce）+ `maxId > readInboxMaxId` 去重 |
| 复用 | 现有 `markReadCurrent`/`FetchMarkRead`/`onScrollerUpdate` 可视区追踪 |
| 手动模式 | 严格 gate，不自动视口已读；保留「点↑跳末尾=全读」 |

### 3.2 Plan：`docs/superpowers/plans/2026-08-04-visible-auto-read.md`

**3 任务（TDD，每任务独立 commit）：**

| Task | 内容 | 关键代码位置 |
|---|---|---|
| 1 | 可视区最大消息 id 追踪：`onScrollerUpdate` 存 `visibleEndIndex`；`visibleMaxMsgId()` 取视口顶部（倒序=最大 id）；`itemMaxId()` 处理 album | `Messages.vue` L934 `onScrollerUpdate`、L648 `displayItems` |
| 2 | 滚动视口自动已读：抽 `doMarkRead(maxId)` 公共已读；`maybeAutoReadVisible()`（仅自动模式 + maxId 去重）；`onMsgScroll` 节流 500ms；`onBeforeUnmount` 清理定时器 | `Messages.vue` L959 `markReadCurrent`、L920 `onMsgScroll` |
| 3 | 构建部署 + E2E 实测（未读递减验证） | — |

### 3.3 关键现有代码（实施时直接复用，勿重写）

```js
// Messages.vue L934 — 可视区追踪（已拿 visibleStartIndex，需加 visibleEndIndex）
function onScrollerUpdate(_startIndex, _endIndex, visibleStartIndex) {
  currentVisibleIndex.value = visibleStartIndex || 0
  updateInUnreadArea()
}

// Messages.vue L959 — 已读入口（force=true 跳过 autoRead；手动模式工具栏用）
async function markReadCurrent(force = false) {
  if (!current.value || !unreadLastId.value) return
  if (!force && !settings.value.autoRead) return
  try {
    await api.markRead(String(current.value.id), unreadLastId.value)
    current.value.readInboxMaxId = unreadLastId.value
    refreshChatsUnread() // loadChats(true) 刷新会话列表未读徽标
  } catch { /* 静默 */ }
}

// Messages.vue L920 — 滚动监听（loadMore + showBackTop + updateInUnreadArea）
function onMsgScroll() { /* 滚动视口自动已读在这里加节流调用 */ }

// displayItems（L648）：item 结构 {type:'msg', msg:{id}} | {type:'album', msgs:[{id}]} | {type:'divider'}
// 倒序：最新在上（index 0），视口顶部 = 最大 id
```

## 4. 实施指引（给新对话）

1. **按 Subagent-Driven 流程执行 plan**（交接文档标定：功能开发一律 Subagent-Driven，每任务新 subagent + 两阶段 review）
2. **无前端单测基建**：Task 1 的纯逻辑测试若 package.json 无 test 脚本则跳过，用 `npm run build` + E2E 验证（plan 已注明）
3. **E2E 验证关键**：找有未读频道（如科技圈 1125107539），记录初始未读数 → 滚动浏览 N 条 → 等节流 → `/api/chats?refresh=1` 确认 `unreadCount` 减少 N
4. **注意**：`readHistory` 会真实改变手机端已读状态（已验证过频道用 `channels.readHistory`，勿再踩 `messages.readHistory` 对频道 PEER_ID_INVALID 的坑）
5. **手动模式验证**：Settings 关 autoRead 后滚动不自动已读

## 5. 已实现功能状态（供新对话了解全貌）

- ✅ **评论区功能**（会话 3）：消息卡片反应/评论数 + 只读评论区弹层——见 `handoff/交接-2026-08-03-3-comments.md`
- ✅ **未读定位 + 已读同步**（会话 3 晚）：进入频道停在第一条未读 + 分隔线 + 跳转按钮（未读区↑跳末尾=已读、历史区↓回第一条）+ 自动/手动已读设置开关——见其 §3.12
- ✅ **下载历史**（已下载消息按钮变勾）+ **下载按钮小尾巴式** + **twemoji 反应图标** + **播放器进度条静态**——见会话 3 交接 §3.7-3.11
- 🔄 **视口自动已读**（本次会话）：spec + plan 就绪，**待实施**

## 6. 遗留事项（供新对话）

1. **视口自动已读实施**（本次会话主体）：按 plan 3 任务实施，见 §3/§4
2. **分组/归档只读显示**（会话 3 早）：`docs/superpowers/specs/2026-08-03-folders-archive-design.md` 已写好，**未实施**（用户先做未读功能，分组待后续）
3. **浏览器弹层点击交互**（评论区）：已实测过，可再人工确认
4. **作者头像 / 发评论 / 相册反应**：评论区 v2 候选
5. **下载历史/去重提醒**：记录已下载 (频道, msgID)，新建任务时提醒——未做
6. **22713-22717 bug 根因**：加过诊断日志，用户称修好但根因未确认——未深究

## 7. 工作风格（沿用）

- 中文交流；一次改一个变量实测；测试前 clear-cache.bat + Ctrl+Shift+R
- 后端改动后必须重建 exe（`./build-tmm.sh`）；前端改动 build + 拷 web 到 tmm
- 性能问题要深挖根因（对比源码定位）；善用浏览器实测定位 CSS，别靠猜
- CSS 视觉统一是硬要求，延续现有 token（`--bg`/`--border`/`--radius`/`--accent` 等）
- **执行方式（2026-08-03 定）**：功能开发一律用 **Subagent-Driven**（subagent-driven-development skill）逐任务执行，每任务新 subagent + 任务间两阶段 review；不用 Inline 批量。流程走 spec → writing-plans 计划 → subagent 执行
- **调试经验**：gotd conditional 字段必须用 `GetXxx` helper（直接字段访问返回 0）；频道已读用 `channels.readHistory`；`getHistory` offset_id 排他（offset=X 返回 id<X）；浏览器 lazy 加载需滚动才触发

## 8. 本会话重要教训（供新对话实施时注意）

1. **未读数递减是服务器端天然语义**：`readHistory(maxID)` 后未读 = maxID 之后数量，无需逐条 RPC——「看 N 条未读减 N」直接靠 maxID 推进实现
2. **倒序下视口顶部 = 最大 id**：`visibleStartIndex` 对应最大 id 消息，`visibleMaxMsgId()` 直接取顶部即可
3. **节流必须做**：`onMsgScroll` 高频触发，不用 debounce/节流会滚动时狂发 RPC；去重靠 `maxId > readInboxMaxId`
4. **勿重写已读链路**：`markReadCurrent`/`FetchMarkRead`/`refreshChatsUnread` 已实现且 E2E 验证，视口已读只是加一个触发源
