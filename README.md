# tmm — Telegram Download Manager (Web UI)

<img align="right" src="docs/assets/img/logo.png" height="280" alt="">

> 📥 A self-hosted Telegram download manager with a browser UI — browse channels, pick messages, download at full speed, and manage tasks from your browser.

English | <a href="README_zh.md">简体中文</a>

Based on [iyear/tdl](https://github.com/iyear/tdl), `tmm` wraps the battle-tested tdl download engine in a local web service. Log in once with your Telegram account, browse your channels in the browser, select messages to download, and watch tasks progress live — all without touching the CLI.

## ✨ Features

### Web UI (browser-based)
- **Account login** — QR code (recommended), phone verification code, or import a Telegram Desktop `tdata` session
- **Channel & message browser** — list your chats, browse media messages with thumbnails, filter by type
- **One-click downloads** — select messages (single, multiple, or whole albums) and enqueue a download task
- **Task manager** — live progress, per-file status, pause / resume / delete (with optional file cleanup), task history survives restarts
- **Media preview** — watch videos and view images inline via HTTP range streaming (seekable, resumable)
- **Batch rename** — rename many files at once with a `{n}` counter, forced `#msgId` prefix keeps files unique
- **Per-channel organization** — optional `#channelId 频道名` subdirectories keep downloads tidy

### Download engine (from tdl)
- Single-file binary, low resource usage
- Full bandwidth utilization — faster than official clients
- Downloads from protected/restricted chats
- Resume support (partial downloads continue from `.tdlstate`, not from scratch)

### Configurable naming & storage
- Prefix filenames with `#channelId` / `#msgId` (independent switches)
- Configurable download directory (defaults to `<exe>/downloads`), pickable via native folder dialog
- Filename length truncation for filesystem limits
- Settings persisted in `settings.json` next to the executable

## 🚀 Quick Start

### Pre-built binary (Windows)
Grab the latest `tmm.exe` from the [Releases](https://github.com/3R1C168/tmm/releases) page (or run your own build), then:

```bash
# start the web service (auto-opens http://127.0.0.1:8080)
tmm.exe serve --port 8080
```

Open `http://127.0.0.1:8080` in your browser, log in with your Telegram account, and start downloading.

### Build from source

```bash
# backend only
./build-tmm.sh

# backend + web frontend
./build-tmm.sh web
```

The script builds `tmm.exe` with an embedded version stamp and deploys the web UI next to it.

### `serve` command flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8080` | Port to listen on (bound to `127.0.0.1`) |
| `--web-root` | `<exe>/web` | Directory of the built web frontend |
| `--dir, -d` | `<exe>/downloads` | Default download directory |
| `--no-browser` | `false` | Do not open the browser automatically |

## ⚙️ Settings

All settings are editable in the browser (Settings page) and persisted to `settings.json`:

| Setting | Description |
|---------|-------------|
| `channelIdPrefix` | Prefix download filenames with `#channelId` |
| `msgIdPrefix` | Prefix download filenames with `#msgId` |
| `channelDir` | Store downloads in per-channel subdirectories (`#channelId 频道名/`) |
| `downloadDir` | Download directory (empty = default `<exe>/downloads`) |
| `filenameMaxLen` | Max filename length in bytes (0 = unlimited) |
| `previewTakeout` | Use takeout channel for large-file previews (faster, but shows a "data export" notice on other devices) |
| `cacheMaxMB` | Media/thumb cache cap (MB), LRU-evicted |
| `clearPreviewOnExit` | Wipe streaming preview cache on shutdown |

## 🖥 CLI (from tdl)

The underlying tdl CLI is preserved — `tmm login`, `tmm download`, `tmm forward`, `tmm up`, `tmm chat` etc. work as before. See the [original tdl documentation](https://docs.iyear.me/tdl/) for details.

## 🔧 Development

- **Backend**: Go (`server/`, `app/`, `core/`), gorilla/mux, gotd/td
- **Frontend**: Vue 3 + Element Plus (`web/app/src`)
- **Build**: `./build-tmm.sh` (backend) / `./build-tmm.sh web` (backend + frontend)

## 📄 License

AGPL-3.0 License
