// 下载常用反应 emoji 的 twemoji SVG 到 public/twemoji/，并重新生成 src/data/twemojiFiles.js。
// 用法：node tools/download-twemoji.mjs
// 跨平台统一渲染：twemoji SVG 离线打包，docker 端/内网不依赖系统 emoji 字体/CDN。
import { mkdirSync, readdirSync, writeFileSync, existsSync } from 'fs'
import { dirname, join } from 'path'
import { fileURLToPath } from 'url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const outDir = join(root, 'public', 'twemoji')
const dataFile = join(root, 'src', 'data', 'twemojiFiles.js')

// 常用反应（Telegram 反应集常见项 + 映射过的）。code point 用 twemoji 命名
// （无 FE0F 变体选择符；肤色是 base-1f3fb..1f3ff 后缀）。
const EMOJIS = [
  '👍','👎','❤️','🧡','💛','💚','💙','💜','🖤','🤍','🤎','🔥','🎉','😁','😃','😄','😂','🤣',
  '😡','😠','😞','😟','😢','😐','🤔','👻','💀','🏆','👑','✅','❌','⚡','✨','🚀','⭐','🌟',
  '🖕','👏','🤯','🐳','😱','💊','🍾','😮','🥰','😍','🤩','😎','🤗','😭','😅','🙏','👌','🤝',
  '💯','❤️‍🔥','💔','❤️‍🩹','😈','🤡','👽','🤖','🎃','💩','🐶','🐱','🦊','🌹','🌸','🎁','🎂',
]
// 肤色变体（👍🏻👍🏼👍🏽👍🏾👍🏿 等）
const SKIN = ['1f3fb','1f3fc','1f3fd','1f3fe','1f3ff']
const BASES = ['1f44d','1f44e','1f44f','1f90e','1f91d','1f44a']

function emojiToCp(emoji) {
  const cps = []
  for (const ch of emoji) {
    const hex = ch.codePointAt(0).toString(16)
    if (hex !== 'fe0f') cps.push(hex)
  }
  return cps.join('-')
}

async function download(cp) {
  const url = `https://raw.githubusercontent.com/jdecked/twemoji/main/assets/svg/${cp}.svg`
  const res = await fetch(url)
  if (!res.ok) return false
  writeFileSync(join(outDir, `${cp}.svg`), Buffer.from(await res.arrayBuffer()))
  return true
}

mkdirSync(outDir, { recursive: true })
let ok = 0, fail = []
const need = new Set(EMOJIS.map(emojiToCp))
for (const base of BASES) for (const s of SKIN) need.add(`${base}-${s}`)
for (const cp of need) {
  try { if (await download(cp)) ok++; else fail.push(cp) } catch { fail.push(cp) }
}
// 重新生成集合文件
const files = readdirSync(outDir).filter(f => f.endsWith('.svg')).map(f => f.replace('.svg', ''))
writeFileSync(dataFile,
  '// 自动生成：public/twemoji/ 里的 SVG 文件名集合（勿手改，用 tools/download-twemoji.mjs 重新生成）。\n' +
  `export default new Set(${JSON.stringify(files)})\n`)
console.log(`下载 ${ok}/${need.size}，失败 ${fail.length}：${fail.join(' ') || '无'}`)
console.log(`public/twemoji/ 现有 ${files.length} 个 SVG`)
