// Reaction emoji → lucide 线性图标映射。
// Telegram 反应是开放 emoji 集，lucide 只有固定图标——常见反应映射成线性
// 图标（与 UI 灰色小字风格统一），未命中的 fallback 回 emoji 字符。
import { markRaw } from 'vue'
import {
  ThumbsUp, ThumbsDown, Heart, Flame, PartyPopper, Smile, Laugh,
  Star, Angry, Frown, Ghost, Skull, Trophy, Crown, Check, X,
  Zap, Sparkles, Rocket, Meh,
} from 'lucide-vue-next'

// 键是 Telegram 反应返回的 Emoticon 原文（注意 ❤️ 的 ZWJ/变体选择符变体）。
// markRaw 防止 lucide 组件进 Vue 响应式代理（否则警告 + 性能损失）。
const REACTION_ICONS = {
  '👍': ThumbsUp,
  '👎': ThumbsDown,
  '❤️': Heart,
  '❤': Heart,
  '🧡': Heart,
  '💛': Heart,
  '💚': Heart,
  '💙': Heart,
  '💜': Heart,
  '🔥': Flame,
  '🎉': PartyPopper,
  '😁': Smile,
  '😃': Smile,
  '😄': Smile,
  '😂': Laugh,
  '🤣': Laugh,
  '😡': Angry,
  '😠': Angry,
  '😞': Frown,
  '😟': Frown,
  '😢': Meh,
  '😐': Meh,
  '🤔': Meh,
  '👻': Ghost,
  '💀': Skull,
  '🏆': Trophy,
  '👑': Crown,
  '✅': Check,
  '✔️': Check,
  '❌': X,
  '⚡': Zap,
  '✨': Sparkles,
  '🚀': Rocket,
  '⭐': Star,
  '🌟': Star,
}
for (const k in REACTION_ICONS) REACTION_ICONS[k] = markRaw(REACTION_ICONS[k])

// reactionIcon returns the lucide component for a reaction emoji, or null if
// the emoji has no mapped icon (caller falls back to rendering the emoji).
export function reactionIcon(emoji) {
  return REACTION_ICONS[emoji] || null
}
