# Telegram 消息管理器程序图标 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 生成原 G“电光紫消息切面”完整图标资产，并接入 Vue/Vite 网页与 Windows amd64 `tmm.exe`。

**Architecture:** 唯一矢量母版放在 `web/app/public/icons/icon.svg`；一个无业务耦合的 Node 脚本使用 `sharp` 生成 PNG，并用 Node 标准库把 PNG 帧封装为 ICO。Vite 从 `public/icons` 原样发布网页图标；Go 自动链接一次性由 `rsrc` 生成并提交的架构限定 `.syso`，现有 `go build` 命令无需改造。

**Tech Stack:** SVG、Node.js 24、sharp、Vite 5、Go 1.25+、akavel/rsrc v0.10.2、PowerShell/System.Drawing（Windows 验证）

## Global Constraints

- 视觉必须严格采用 `docs/superpowers/specs/2026-08-04-tmm-app-icon-design.md` 中确认的原 G 方案，不采用后续圆角、气泡或纸飞机变体。
- 色值固定为 `#9C5CF0`、`#5133A6`、`#7045C4`、`#C8A7F6`、`#F4EEFF`、`#FFFFFF`、`#E6D7FF`、`#6841BE`。
- SVG 基准画布为 `256 × 256`；底板为 `(16,16) 224 × 224`，圆角 `54px`。
- 16px 允许省略圆点和内部浅切面；24–32px 保留圆点和主要双色切面；48px 及以上完整显示原 G。
- 输出 `16/24/32/48/64/128/256/512px` PNG、`180px` Apple Touch Icon、favicon ICO 和 Windows ICO。
- 不新增 PWA Manifest，不修改 Art Design Pro UI 主色 `#4f7cff`。
- Windows 首次接入只覆盖现有 `build-tmm.sh` 的 `windows/amd64` 产物；不为未实际使用的 GoReleaser Windows 架构增加 `.syso`。

## File Structure

- Create: `web/app/public/icons/icon.svg` — 原 G 唯一矢量母版。
- Create: `web/app/public/icons/icon-{16,24,32,48,64,128,256,512}.png` — 网页和平台 PNG。
- Create: `web/app/public/icons/apple-touch-icon.png` — iOS 180px 图标。
- Create: `web/app/public/icons/favicon.ico` — 浏览器 ICO（16/24/32/48px）。
- Create: `web/app/public/icons/app.ico` — Windows ICO（16/24/32/48/64/128/256px）。
- Create: `web/app/scripts/generate-icons.mjs` — SVG 栅格化与 ICO 封装。
- Create: `web/app/scripts/verify-icons.mjs` — 无图像依赖的 PNG/ICO 结构和尺寸检查。
- Modify: `web/app/package.json` — 增加 `icons:generate`、`icons:verify` 和 `sharp` 开发依赖。
- Modify: `web/app/package-lock.json` — 锁定 `sharp`。
- Modify: `web/app/index.html:3-7` — 接入 SVG favicon、ICO fallback 和 Apple Touch Icon。
- Modify: `build-tmm.sh:37-44` — 部署 Vite 生成的 `icons` 目录。
- Create: `rsrc_windows_amd64.syso` — Windows amd64 图标资源，Go 自动链接。

---

### Task 1: 生成并验证原 G 图标资产

**Files:**
- Create: `web/app/public/icons/icon.svg`
- Create: `web/app/public/icons/icon-16.png`
- Create: `web/app/public/icons/icon-24.png`
- Create: `web/app/public/icons/icon-32.png`
- Create: `web/app/public/icons/icon-48.png`
- Create: `web/app/public/icons/icon-64.png`
- Create: `web/app/public/icons/icon-128.png`
- Create: `web/app/public/icons/icon-256.png`
- Create: `web/app/public/icons/icon-512.png`
- Create: `web/app/public/icons/apple-touch-icon.png`
- Create: `web/app/public/icons/favicon.ico`
- Create: `web/app/public/icons/app.ico`
- Create: `web/app/scripts/generate-icons.mjs`
- Create: `web/app/scripts/verify-icons.mjs`
- Modify: `web/app/package.json:6-10,20-23`
- Modify: `web/app/package-lock.json`

**Interfaces:**
- Consumes: confirmed original G geometry and colors from the design spec.
- Produces: `public/icons/icon.svg`, PNG set, `favicon.ico`, and `app.ico`; later tasks consume these exact paths.

- [ ] **Step 1: Add the dependency and script commands**

Run:

```bash
cd web/app
npm install --save-dev sharp@0.34.3
npm pkg set scripts.icons:generate="node scripts/generate-icons.mjs"
npm pkg set scripts.icons:verify="node scripts/verify-icons.mjs"
```

Expected `package.json` additions:

```json
{
  "scripts": {
    "icons:generate": "node scripts/generate-icons.mjs",
    "icons:verify": "node scripts/verify-icons.mjs"
  },
  "devDependencies": {
    "sharp": "^0.34.3"
  }
}
```

- [ ] **Step 2: Write the verifier before assets exist**

Create `web/app/scripts/verify-icons.mjs`:

```js
import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "public", "icons")
const pngSizes = [16, 24, 32, 48, 64, 128, 256, 512]

function pngSize(buffer) {
  assert.deepEqual([...buffer.subarray(1, 4)], [0x50, 0x4e, 0x47], "not a PNG")
  return [buffer.readUInt32BE(16), buffer.readUInt32BE(20)]
}

function icoSizes(buffer) {
  assert.equal(buffer.readUInt16LE(0), 0)
  assert.equal(buffer.readUInt16LE(2), 1)
  const count = buffer.readUInt16LE(4)
  return Array.from({ length: count }, (_, index) => {
    const offset = 6 + index * 16
    return [buffer[offset] || 256, buffer[offset + 1] || 256]
  })
}

for (const size of pngSizes) {
  const buffer = await readFile(join(root, `icon-${size}.png`))
  assert.deepEqual(pngSize(buffer), [size, size])
}

assert.deepEqual(
  pngSize(await readFile(join(root, "apple-touch-icon.png"))),
  [180, 180],
)
assert.deepEqual(
  icoSizes(await readFile(join(root, "favicon.ico"))),
  [16, 24, 32, 48].map((size) => [size, size]),
)
assert.deepEqual(
  icoSizes(await readFile(join(root, "app.ico"))),
  [16, 24, 32, 48, 64, 128, 256].map((size) => [size, size]),
)

const svg = await readFile(join(root, "icon.svg"), "utf8")
for (const value of ["#9C5CF0", "#5133A6", "#C8A7F6", "#F4EEFF", "#6841BE"]) {
  assert.match(svg, new RegExp(value, "i"))
}
assert.match(svg, /viewBox="0 0 256 256"/)
console.log("icon assets verified")
```

- [ ] **Step 3: Run the verifier and confirm the expected failure**

Run:

```bash
cd web/app
npm run icons:verify
```

Expected: FAIL with `ENOENT` for `public/icons/icon-16.png`.

- [ ] **Step 4: Create the exact original G SVG master**

Create `web/app/public/icons/icon.svg`:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256" role="img" aria-labelledby="title">
  <title id="title">Telegram 消息管理器</title>
  <defs>
    <linearGradient id="background" x1="28" y1="22" x2="229" y2="235" gradientUnits="userSpaceOnUse">
      <stop stop-color="#9C5CF0"/>
      <stop offset="1" stop-color="#5133A6"/>
    </linearGradient>
    <linearGradient id="message" x1="50" y1="75" x2="204" y2="180" gradientUnits="userSpaceOnUse">
      <stop stop-color="#FFFFFF"/>
      <stop offset="1" stop-color="#E6D7FF"/>
    </linearGradient>
  </defs>
  <rect x="16" y="16" width="224" height="224" rx="54" fill="url(#background)"/>
  <path d="M51 74h72l83 54-83 54H51l83-54-83-54Z" fill="url(#message)"/>
  <path d="m51 74 83 54-83 54 72-54L51 74Z" fill="#C8A7F6"/>
  <path d="m134 128 72 0-83 54 11-54Z" fill="#F4EEFF"/>
  <circle cx="61" cy="128" r="10" fill="#6841BE"/>
</svg>
```

Do not round or otherwise alter the three path definitions.

- [ ] **Step 5: Write the minimal PNG and ICO generator**

Create `web/app/scripts/generate-icons.mjs`:

```js
import { mkdir, readFile, writeFile } from "node:fs/promises"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import sharp from "sharp"

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "public", "icons")
const pngSizes = [16, 24, 32, 48, 64, 128, 256, 512]
const faviconSizes = [16, 24, 32, 48]
const appSizes = [16, 24, 32, 48, 64, 128, 256]

await mkdir(root, { recursive: true })
const svg = await readFile(join(root, "icon.svg"))

async function render(size, file = `icon-${size}.png`) {
  const output = await sharp(svg, { density: 384 })
    .resize(size, size, { fit: "fill" })
    .png({ compressionLevel: 9, palette: false })
    .toBuffer()
  await writeFile(join(root, file), output)
  return output
}

function ico(frames) {
  const header = Buffer.alloc(6 + frames.length * 16)
  header.writeUInt16LE(0, 0)
  header.writeUInt16LE(1, 2)
  header.writeUInt16LE(frames.length, 4)
  let imageOffset = header.length
  frames.forEach(({ size, png }, index) => {
    const offset = 6 + index * 16
    header[offset] = size === 256 ? 0 : size
    header[offset + 1] = size === 256 ? 0 : size
    header[offset + 2] = 0
    header[offset + 3] = 0
    header.writeUInt16LE(1, offset + 4)
    header.writeUInt16LE(32, offset + 6)
    header.writeUInt32LE(png.length, offset + 8)
    header.writeUInt32LE(imageOffset, offset + 12)
    imageOffset += png.length
  })
  return Buffer.concat([header, ...frames.map(({ png }) => png)])
}

const pngs = new Map()
for (const size of pngSizes) pngs.set(size, await render(size))
await render(180, "apple-touch-icon.png")
await writeFile(
  join(root, "favicon.ico"),
  ico(faviconSizes.map((size) => ({ size, png: pngs.get(size) }))),
)
await writeFile(
  join(root, "app.ico"),
  ico(appSizes.map((size) => ({ size, png: pngs.get(size) }))),
)
console.log("icon assets generated")
```

- [ ] **Step 6: Generate and verify every asset**

Run:

```bash
cd web/app
npm run icons:generate
npm run icons:verify
```

Expected:

```text
icon assets generated
icon assets verified
```

- [ ] **Step 7: Visually inspect the master and small sizes**

Open `web/app/public/icons/icon.svg`, `icon-16.png`, `icon-32.png`, and `icon-256.png` on both light and dark backgrounds. Confirm the SVG matches original G exactly and the 16px image remains a purple rounded square with one clear white directional mark.

- [ ] **Step 8: Commit the asset pipeline and generated files**

```bash
git add web/app/package.json web/app/package-lock.json web/app/scripts web/app/public/icons
git commit -m "feat: add application icon assets"
```

### Task 2: Connect icons to the web build and deployment

**Files:**
- Modify: `web/app/index.html:3-7`
- Modify: `build-tmm.sh:37-44`

**Interfaces:**
- Consumes: Task 1 assets under `web/app/public/icons/`.
- Produces: Vite output and deployed `../tmm/web/icons/` with working document metadata links.

- [ ] **Step 1: Build before HTML integration to capture the missing-link state**

Run:

```bash
cd web/app
npm run build
```

Expected: build succeeds, but `dist/index.html` contains no `rel="icon"` or `apple-touch-icon` link.

- [ ] **Step 2: Add favicon metadata to the page head**

Modify `web/app/index.html` so lines 3–7 become:

```html
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <link rel="icon" type="image/svg+xml" href="/icons/icon.svg" />
    <link rel="alternate icon" href="/icons/favicon.ico" sizes="any" />
    <link rel="apple-touch-icon" href="/icons/apple-touch-icon.png" />
    <title>Telegram消息管理器</title>
  </head>
```

- [ ] **Step 3: Extend the existing deployment copy list**

Modify `build-tmm.sh` after the existing `twemoji` copy:

```bash
  rm -rf ../tmm/web/icons
  cp -r web/app/dist/icons ../tmm/web/icons
  cp web/app/dist/index.html ../tmm/web/index.html
```

Keep the current explicit deployment style; do not replace it with broad directory synchronization.

- [ ] **Step 4: Build and assert the generated links and files**

Run:

```bash
cd web/app
npm run build
grep -F 'rel="icon" type="image/svg+xml" href="/icons/icon.svg"' dist/index.html
grep -F 'rel="apple-touch-icon" href="/icons/apple-touch-icon.png"' dist/index.html
test -f dist/icons/favicon.ico
test -f dist/icons/icon.svg
test -f dist/icons/apple-touch-icon.png
```

Expected: both `grep` commands print one matching line; all `test` commands exit 0.

- [ ] **Step 5: Deploy the web build through the real script**

Run from repository root:

```bash
./build-tmm.sh web
test -f ../tmm/web/icons/favicon.ico
test -f ../tmm/web/icons/icon.svg
test -f ../tmm/web/icons/apple-touch-icon.png
grep -F '/icons/icon.svg' ../tmm/web/index.html
```

Expected: build completes; deployed files exist; deployed HTML contains the icon URL.

- [ ] **Step 6: Verify in the browser**

Start the app normally, load its web UI, and confirm the browser tab displays the purple original G favicon. Check one light browser theme and one dark browser theme.

- [ ] **Step 7: Commit web integration**

```bash
git add web/app/index.html build-tmm.sh
git commit -m "feat: use application icon on web"
```

### Task 3: Embed the icon in Windows amd64 executable

**Files:**
- Create: `rsrc_windows_amd64.syso`
- Consume without modification: `web/app/public/icons/app.ico`
- Consume without modification: `build-tmm.sh`

**Interfaces:**
- Consumes: Task 1 `web/app/public/icons/app.ico`.
- Produces: a committed `rsrc_windows_amd64.syso` automatically linked into Windows amd64 builds.

- [ ] **Step 1: Record the current missing resource state**

Run from repository root:

```bash
test ! -f rsrc_windows_amd64.syso
```

Expected: exits 0 before resource generation.

- [ ] **Step 2: Generate the architecture-limited Windows resource**

Run:

```bash
go run github.com/akavel/rsrc@v0.10.2 \
  -arch amd64 \
  -ico web/app/public/icons/app.ico \
  -o rsrc_windows_amd64.syso
```

Expected: exits 0 and creates non-empty `rsrc_windows_amd64.syso`. This versioned `go run` is intentionally one-time; do not add `rsrc` to `go.mod` and do not modify `build-tmm.sh`.

- [ ] **Step 3: Build through the existing production path**

Run:

```bash
./build-tmm.sh
test -s ../tmm/tmm.exe
```

Expected: build succeeds and creates a non-empty Windows amd64 executable.

- [ ] **Step 4: Extract and sanity-check the executable icon with Windows APIs**

Run in Git Bash:

```bash
powershell.exe -NoProfile -Command '
Add-Type -AssemblyName System.Drawing
$exe = (Resolve-Path "../tmm/tmm.exe").Path
$icon = [System.Drawing.Icon]::ExtractAssociatedIcon($exe)
if ($null -eq $icon) { throw "tmm.exe has no associated icon" }
$out = Join-Path $env:TEMP "tmm-icon-check.png"
$icon.ToBitmap().Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
if ((Get-Item $out).Length -le 0) { throw "extracted icon is empty" }
Write-Host $out
'
```

Expected: prints a temporary PNG path without throwing. Open that PNG and confirm it shows original G, not the generic application icon.

- [ ] **Step 5: Verify normal application behavior remains intact**

Run:

```bash
../tmm/tmm.exe version
```

Expected: prints the injected version and exits normally.

- [ ] **Step 6: Commit Windows resource**

```bash
git add rsrc_windows_amd64.syso
git commit -m "feat: embed Windows application icon"
```

### Task 4: Final cross-surface verification

**Files:**
- Verify: `web/app/public/icons/*`
- Verify: `web/app/dist/icons/*`
- Verify: `../tmm/web/icons/*`
- Verify: `../tmm/tmm.exe`

**Interfaces:**
- Consumes: Tasks 1–3 deliverables.
- Produces: evidence that generated, built, deployed, and embedded forms agree.

- [ ] **Step 1: Run all automated asset and web checks**

```bash
cd web/app
npm run icons:verify
npm run build
```

Expected: `icon assets verified` and successful Vite build.

- [ ] **Step 2: Run the complete project build/deploy path**

```bash
cd ../..
./build-tmm.sh web
../tmm/tmm.exe version
```

Expected: backend and frontend build/deploy succeed; version command exits normally.

- [ ] **Step 3: Inspect all user-visible surfaces**

Confirm:

1. `web/app/public/icons/icon.svg` is exactly original G.
2. Browser favicon is visible in light and dark themes.
3. `tmm.exe` shows original G in Windows Explorer at small, medium, and large icon sizes.
4. A running `tmm.exe` shows original G on the Windows taskbar.
5. No Art Design Pro UI token or in-app accent color changed.

- [ ] **Step 4: Confirm repository state**

```bash
git status --short
git log -4 --oneline
```

Expected: no uncommitted implementation files; recent commits include asset generation, web integration, and Windows embedding.
