# tmm 项目交接文档（2026-08-04 会话 5 — 手动全读按钮，移除视口自动已读）

> **给新对话的完整交接**：本文档汇总本次会话（手动全读 + 移除视口自动已读 + UI 调整）全部工作。新对话据此了解现状与遗留事项，无需依赖旧会话上下文。
> 本目录（`tdl/docs/`）为跟随 git 的交接目录；独立交接目录在 `handoff/交接-2026-08-04-5-manual-read-all.md`。
> 上一会话交接（视口自动已读 spec+plan）见 `handoff/交接-2026-08-04-4-visible-auto-read.md`。

---

## 1. 项目概况（与前会话一致，简述）

- **仓库**：`D:\Documents\workspace\telegram manager\tdl`（git 分支 `master`）
- **GitHub**：`https://github.com/3R1C168/tmm`（私有，主线 `master`）
- **运行目录**：`D:\Documents\workspace\telegram manager\tmm\`
- **运行**：`tmm\start.bat` 或 `./tmm.exe serve --port 8080 --no-browser` → `http://127.0.0.1:8080`
- **构建**：`cd tdl && ./build-tmm.sh`（后端）/ `./build-tmm.sh web`（前端）
- **日志**：`tmm/logs/server-*.log`；**杀进程**：`powershell -Command "Get-Process tmm | Stop-Process -Force"`
- **环境**：Git Bash；Go 1.26.5；gotd v0.140.0；Vue 3.5 + vue-virtual-scroller@2.0.0-beta.8 + Element Plus

## 2. 当前状态（2026-08-04 凌晨，本会话结束）

- ✅ 工作树干净（git 无未提交改动）
- ✅ 主线 = `master`，最新 commit **`5456615d`**（final fix：清理死绑定）
- ✅ exe 已重建（版本 `5456615d`）、前端已部署
- ✅ 服务器**当前运行中**（HTTP 200，端口 8080）
- ✅ 登录会话完好：`tmm/data/data/default`（Eric 5401443218）

## 3. 本次会话成果（手动全读 + 移除视口自动已读 + UI 调整，已实施）

**背景**：视口自动已读（会话 4 实施）实测功能正常但体验差——未读分隔线随滚动变动、左侧会话列表频繁刷新（每次已读都 `refreshChatsUnread`）。用户决策：**移除自动已读，改手动「全部已读」按钮**（弹确认框防误触）。

### 3.1 Spec：`docs/superpowers/specs/2026-08-04-manual-read-all-design.md`

| 部分 | 设计 |
|---|---|
| 移除视口自动已读 | `maybeAutoReadVisible`/`autoReadDebounce`/`updateInUnreadArea`/`inUnreadArea`/`jumpUnread`/`jump-btn` 全删；`onScrollerUpdate` 死绑定清理 |
| 标题栏「全部已读」 | `markReadCurrent` 无参化 + `ElMessageBox.confirm` 确认框 → `doMarkRead(unreadLastId)` 全读当前频道；`:disabled="!hasUnread"` |
| 标题栏布局 | 从右到左：刷新、全部已读、按范围下载、下载所选、只看媒体（按钮）、搜索框 |
| 「只看媒体」按钮化 | el-checkbox → el-button，点击高亮（`.active`），`reloadMessages(true)` 强刷 |
| 移除 autoRead | 后端 `settings.go`/`handlers.go` 字段全删 + 前端 Settings.vue 开关删 + Messages.vue 引用清 |
| 右下角按钮 | 只保留「回到最新」（back-top），jump-btn 双态删 |
| 加载更早悬浮 | `.load-more-float` 绝对定位底部中央（参照 `.chat-view` relative） |
| 分隔线文案 | 「未读消息」→「以上是未读消息」 |
| 卡片阴影加深 | `--shadow` token：亮 `0 1px 3px rgba(16,24,40,.10), 0 6px 20px rgba(16,24,40,.09)`；暗 `0 1px 3px rgba(0,0,0,.35), 0 6px 20px rgba(0,0,0,.32)` |
| 设置页卡片居中 | `.card { max-width: 640px; margin: 0 auto 18px; }` |

### 3.2 Plan：`docs/superpowers/plans/2026-08-04-manual-read-all.md`

**5 任务（Subagent-Driven 执行，每任务独立 commit + 两阶段 review）：**

| Task | 内容 | Commit |
|---|---|---|
| 1 | 后端移除 AutoRead 字段（settings.go/handlers.go/settings_test.go + boolPtr 死代码） | `3865911b` |
| 2 | Messages.vue 移除视口自动已读触发链（含 markReadCurrent 无参化） | `70bfecf0` |
| 3 | 标题栏「全部已读」按钮（确认框）+ 布局 + 只看媒体按钮化 | `cf1cc49a` |
| 4 | 加载更早悬浮 + 分隔线文案 + 阴影加深 + 设置居中 + 清理 .jump-btn CSS | `eee04f7c` |
| 5 | 设置页移除 autoRead 开关 + 部署 | `62f0e2bf` |

最终 review（opus）→ Ready to merge，3 Minor 修复 → `5456615d`（scoped re-review clean）。

### 3.3 关键现有代码（实施后状态）

```js
// Messages.vue — 公共已读（保留，手动全读复用）
async function doMarkRead(maxId) {
  if (!current.value || !maxId) return
  try {
    await api.markRead(String(current.value.id), maxId)
    current.value.readInboxMaxId = maxId // 推进本地未读分界
    refreshChatsUnread()
  } catch { /* 静默 */ }
}

// 标题栏「全部已读」：确认框防误触
async function markReadCurrent() {
  if (!current.value || !unreadLastId.value) return
  try {
    await ElMessageBox.confirm(
      `确认将「${current.value.visibleName || current.value.username || ''}」全部标记为已读？`,
      '全部已读',
      { confirmButtonText: '确认', cancelButtonText: '取消', type: 'warning' }
    )
  } catch { return }
  await doMarkRead(unreadLastId.value)
}
```

## 4. 实施经验（给新对话）

1. **Subagent-Driven 全程适用**：5 任务均新 subagent + 两阶段 review，最终 review 用最强大模型（opus），3 Minor 一次 fix 波 + scoped re-review 收敛
2. **移除 autoRead 的对称性**：后端 `settings`/`settingsJSON`/`updateSettingsRequest`/`load`/`update` + 前端 `Settings.vue` 的 ref 初始化/load/save + `Messages.vue` 引用，必须全链路清除（final review 专门核过无残留、无序列化不对称）
3. **E2E 浏览器受限**：IAB webview 交互通道不稳定（Playwright/CUA 点击间歇性超时）、滚动虚拟容器全面失效、tmm→Telegram TCP 超时——**功能代码全 review 通过，浏览器 E2E 留给用户手动验证**
4. **`reloadMessages(true)` 强刷**：只看媒体切换必须强刷（缓存 key 虽媒体感知但 TTL 2h 会显示旧数据）

## 5. 已实现功能状态（供新对话了解全貌）

- ✅ **手动全读按钮**（本次）：标题栏「全部已读」（确认框）+ 移除视口自动已读 + autoRead 移除
- ✅ **UI 调整**（本次）：只看媒体按钮化 + 加载更早悬浮 + 分隔线「以上是未读消息」+ 阴影加深 + 设置居中
- ✅ **评论区功能**（会话 3）：消息卡片反应/评论数 + 只读评论区弹层——见 `handoff/交接-2026-08-03-3-comments.md`
- ✅ **未读定位 + 已读同步**（会话 3 晚）：进入频道停在第一条未读 + 分隔线 + 右下角「回到最新」——见其 §3.12
- ✅ **下载历史** + **下载按钮小尾巴式** + **twemoji 反应图标** + **播放器进度条静态**——见会话 3 交接 §3.7-3.11
- ⏸️ **视口自动已读**（会话 4）：已实施后**被本次移除**（体验差：未读线变动 + 列表刷新），spec/plan 见会话 4 交接

## 6. 遗留事项（供新对话）

1. **E2E 手动验证**（本次核心）：功能已部署（`http://127.0.0.1:8080`），验证清单：
   - 标题栏从右到左：刷新、全部已读、按范围下载、下载所选、只看媒体、搜索框
   - 点「全部已读」→ 确认框 → 取消不变/确认未读归零 + 左侧徽标清除
   - 滚动浏览 → **未读线不自动变动**、左侧列表**不刷新**
   - 「只看媒体」点击高亮 + 只显示媒体消息
   - 右下角按钮跳最后一条消息；「加载更早」底部悬浮
   - 分隔线「以上是未读消息」；设置页卡片居中、无「已读同步」卡片；卡片阴影更深
2. **✅ 已修：全部已读按钮 + 服务消息未读**（`5f0fbe55`）：`hasUnread` 加 `unreadCount>0` 兜底 + `markReadCurrent` 用 `MARK_READ_MAX`(2147483647) 全读——覆盖 getHistory 跳过的服务消息未读（如众乐乐 1 条未读，实测 markRead(MaxInt32) 后 unreadCount 0→ readInboxMaxId 3249）。逻辑模拟 4 场景全对。
3. **分组/归档只读显示**（会话 3 早）：`docs/superpowers/specs/2026-08-03-folders-archive-design.md` 已写好，**未实施**
3. **作者头像 / 发评论 / 相册反应**：评论区 v2 候选
4. **下载历史/去重提醒**：记录已下载 (频道, msgID)，新建任务时提醒——未做
5. **22713-22717 bug 根因**：加过诊断日志，用户称修好但根因未确认——未深究
6. **IAB 浏览器交互通道不稳定**（环境）：Playwright/CUA 点击间歇性超时、滚动虚拟容器失效——需浏览器 E2E 时注意

## 7. 工作风格（沿用）

- 中文交流；一次改一个变量实测；测试前 clear-cache.bat + Ctrl+Shift+R
- 后端改动后必须重建 exe（`./build-tmm.sh`）；前端改动 build + 拷 web 到 tmm
- 性能问题要深挖根因（对比源码定位）；善用浏览器实测定位 CSS，别靠猜
- CSS 视觉统一是硬要求，延续现有 token（`--bg`/`--border`/`--radius`/`--accent`/`--shadow` 等）
- **执行方式**：功能开发一律 Subagent-Driven（subagent-driven-development skill）逐任务执行，每任务新 subagent + 任务间两阶段 review；流程走 spec → writing-plans 计划 → subagent 执行
- **调试经验**：gotd conditional 字段必须用 `GetXxx` helper；频道已读用 `channels.readHistory`；`getHistory` offset_id 排他；浏览器 lazy 加载需滚动才触发

## 8. 本会话重要教训（供新对话注意）

1. **「全部已读」的确认框是用户明确要求**（防误触，Telegram 无"重新标记未读"API）——交互设计要与用户确认防误触需求
2. **移除功能比加功能更需全局审查**：删除视口自动已读涉及 5 个 commit + 死代码（boolPtr、jump-btn CSS、emit-update 绑定）——final review 的 grep 全仓库验证是最后防线
3. **autoRead 移除必须前后端对称**：后端字段删除 + load/update 处理 + 测试，前端开关 + ref 初始化 + load/save 映射，缺一处就残留
4. **E2E 受环境限制时要如实标注**：IAB webview 不稳定 + Telegram 网络超时，代码 review 通过但浏览器验证留用户——交接文档必须明确"待手动验证"项
