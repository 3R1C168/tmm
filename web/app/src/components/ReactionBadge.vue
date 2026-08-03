<template>
  <span class="reaction-badge">
    <img
      v-if="imgSrc"
      :src="imgSrc"
      :alt="emoji"
      class="reaction-img"
      draggable="false"
    />
    <span v-else class="reaction-emoji">{{ emoji }}</span>
    <span class="reaction-count">{{ count }}</span>
  </span>
</template>

<script setup>
import { computed } from 'vue'
import { reactionTwemojiUrl } from '../utils/twemoji'

const props = defineProps({
  emoji: { type: String, required: true },
  count: { type: Number, default: 0 },
})
// 本地打包的 twemoji SVG（跨平台统一），未打包的 fallback 系统 emoji
const imgSrc = computed(() => reactionTwemojiUrl(props.emoji))
</script>

<style scoped>
/* 反应徽章：本地 twemoji SVG（跨平台统一渲染），未打包的 fallback emoji 字符 */
.reaction-badge {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 11px;
  color: var(--text-3);
  vertical-align: middle;
}
.reaction-img {
  width: 14px;
  height: 14px;
  object-fit: contain;
  display: inline-flex;
}
.reaction-emoji {
  display: inline-flex;
  align-items: center;
  font-size: 12px; /* emoji 字符略大于 SVG，视觉对齐 */
  line-height: 1;
}
</style>
