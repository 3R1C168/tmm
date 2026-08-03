// Twemoji 跨平台统一渲染：把反应 emoji 转成本地打包的 twemoji SVG。
// twemoji 文件名 = emoji 的 Unicode code point（十六进制，- 连接），
// 且统一用不含 FE0F 变体选择符的形式（twemoji 用 2764 表示 ❤️）。
// SVG 图集离线打包在 public/twemoji/（见 scripts 或手动下载），
// docker 端/内网/离线都能用，不依赖系统 emoji 字体、不引 CDN。

// emojiToCodePoints converts an emoji to its twemoji filename code points
// (FE0F variation selector dropped — twemoji keys on the base glyph).
export function emojiToCodePoints(emoji) {
  const cps = []
  for (const ch of emoji) {
    const hex = ch.codePointAt(0).toString(16)
    if (hex !== 'fe0f') cps.push(hex)
  }
  return cps.join('-')
}

// 本地已打包的 SVG 文件集（构建时由 twemoji 图集生成，勿手改）。
// 前端只渲染这里有文件的反应用 twemoji 图；未打包的 fallback 系统 emoji。
import twemojiFiles from '../data/twemojiFiles.js'

// reactionTwemojiUrl returns the local twemoji SVG URL for an emoji, or null
// if the emoji isn't in the bundled set (caller falls back to the emoji char).
export function reactionTwemojiUrl(emoji) {
  const cp = emojiToCodePoints(emoji)
  if (cp && twemojiFiles.has(cp)) return `/twemoji/${cp}.svg`
  return null
}
