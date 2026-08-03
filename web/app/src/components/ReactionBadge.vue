<template>
  <span class="reaction-badge">
    <component :is="icon" v-if="icon" :size="11" class="reaction-ico" />
    <span v-else class="reaction-emoji">{{ emoji }}</span>
    <span class="reaction-count">{{ count }}</span>
  </span>
</template>

<script setup>
import { computed } from 'vue'
import { reactionIcon } from '../utils/reactionIcon'

const props = defineProps({
  emoji: { type: String, required: true },
  count: { type: Number, default: 0 },
})
const icon = computed(() => reactionIcon(props.emoji))
</script>

<style scoped>
/* 反应徽章：命中映射用 lucide 线性图标（与 UI 灰色小字统一），未命中 fallback emoji */
.reaction-badge {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 11px;
  color: var(--text-3);
  vertical-align: middle;
}
.reaction-ico,
.reaction-emoji {
  display: inline-flex;
  align-items: center;
}
.reaction-emoji {
  font-size: 12px; /* emoji 字符略大于线性图标，视觉对齐 */
  line-height: 1;
}
</style>
