# tmm — Telegram 下载管理器（Web 界面）

<img align="right" src="docs/assets/img/logo.png" height="280" alt="">

> 📥 自带浏览器界面的 Telegram 下载管理器——在浏览器里浏览频道、勾选消息、全速下载、管理任务。

<a href="README.md">English</a> | 简体中文

`tmm` 基于 [iyear/tdl](https://github.com/iyear/tdl) 的成熟下载引擎，外面包了一层本地 Web 服务。用你的 Telegram 账号登录一次，就能在浏览器里浏览频道、勾选消息下载、实时查看任务进度——完全不用碰命令行。

> [!NOTE]
> **关于本项目**：本项目由作者通过 **vibe coding**（AI 辅助编程）构建，作者本人不懂代码。欢迎提 issue，但不保证一定能修复——能用多少取决于 AI 协作的效果。任何形式的贡献（issue、PR、建议）都欢迎。

## ✨ 功能

### Web 界面
- **账号登录** — 扫码登录（推荐）、手机验证码登录、或导入 Telegram Desktop 的 `tdata` 会话
- **频道/消息浏览** — 列出你的会话，带缩略图浏览媒体消息，按类型筛选
- **一键下载** — 勾选单条/多条消息或整个相册，加入下载任务队列
- **任务管理** — 实时进度、逐文件状态、暂停/继续/删除（可选连带删除磁盘文件），任务历史重启不丢
- **媒体预览** — 视频内嵌播放（HTTP Range 流式、可拖动进度、可续传），图片直接查看
- **批量重命名** — 一次给多个文件套同一个名字，`{n}` 序号占位，强制 `#msgId` 前缀保证不重名
- **按频道存放** — 可选的 `#频道ID 频道名` 子目录，下载文件井然有序

### 下载引擎（来自 tdl）
- 单文件启动，低资源占用
- 吃满带宽——比官方客户端更快
- 支持从受保护/受限会话下载
- 断点续传（从 `.tdlstate` 继续，不从头下载）

### 可配置的命名与存储
- 文件名前缀 `#频道ID` / `#msgId`（两个独立开关）
- 可配置下载目录（默认 `<exe>/downloads`），支持原生目录选择器
- 文件名长度截断，适配文件系统限制
- 设置持久化在可执行文件旁的 `settings.json`

## 🚀 快速开始

### Windows 预编译（推荐）
从 [Releases](https://github.com/3R1C168/tmm/releases) 下载最新 `tmm.exe`（或自己构建），然后：

```bash
# 启动 Web 服务（自动打开 http://127.0.0.1:8080）
tmm.exe serve --port 8080
```

浏览器打开 `http://127.0.0.1:8080`，用 Telegram 账号登录，即可开始下载。

### 从源码构建

```bash
# 只构建后端
./build-tmm.sh

# 后端 + 前端一起
./build-tmm.sh web
```

脚本会构建带版本号的 `tmm.exe`，并把 Web 前端部署到 exe 旁边。

### `serve` 命令参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--port` | `8080` | 监听端口（绑定 127.0.0.1） |
| `--web-root` | `<exe>/web` | 前端静态文件目录 |
| `--dir, -d` | `<exe>/downloads` | 默认下载目录 |
| `--no-browser` | `false` | 不自动打开浏览器 |

## ⚙️ 设置项

所有设置都可在浏览器「设置」页修改，持久化到 `settings.json`：

| 设置 | 说明 |
|------|------|
| `channelIdPrefix` | 下载文件名加 `#频道ID` 前缀 |
| `msgIdPrefix` | 下载文件名加 `#msgId` 前缀 |
| `channelDir` | 按频道存放到子目录（`#频道ID 频道名/`） |
| `downloadDir` | 下载目录（留空 = 默认 `<exe>/downloads`） |
| `filenameMaxLen` | 文件名最大长度（字节），0 = 不限制 |
| `previewTakeout` | 大文件预览走 takeout 通道（更快，但其他设备会收到「数据导出」通知） |
| `cacheMaxMB` | 媒体/缩略图缓存上限（MB），LRU 自动清理 |
| `clearPreviewOnExit` | 退出时清理流式预览缓存 |

## 🖥 命令行（来自 tdl）

底层的 tdl CLI 完整保留——`tmm login`、`tmm download`、`tmm forward`、`tmm up`、`tmm chat` 等照常可用。详见[原版 tdl 文档](https://docs.iyear.me/tdl/)。

## 🔧 开发

- **后端**：Go（`server/`、`app/`、`core/`），gorilla/mux，gotd/td
- **前端**：Vue 3 + Element Plus（`web/app/src`）
- **构建**：`./build-tmm.sh`（后端）/ `./build-tmm.sh web`（后端 + 前端）

## 📄 License

AGPL-3.0 License
