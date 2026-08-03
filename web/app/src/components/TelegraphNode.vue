<template>
  <!-- 文本节点：直接渲染 -->
  <template v-if="isText">{{ node }}</template>
  <!-- 元素节点：按白名单标签渲染 -->
  <template v-else>
    <component
      :is="safeTag"
      v-bind="attrs"
      :class="'tn-' + (isImg ? 'img' : node.tag)"
      @click="isImg ? openOrig : undefined"
    >
      <template v-for="(c, i) in children" :key="i">
        <TelegraphNode :node="c" />
      </template>
    </component>
  </template>
</template>

<script setup>
import { computed } from 'vue'
import { api } from '../api'

// Telegraph getPage 返回的 content 是嵌套节点树：节点是字符串（文本）或
// {tag, attrs, children} 对象。这里递归渲染。只放行白名单标签，其余渲染为
// 普通 div，避免引入任意 HTML。
const props = defineProps({
  node: { type: [String, Object], required: true },
})

defineOptions({ name: 'TelegraphNode' }) // 允许模板内递归引用自身

const isText = computed(() => typeof props.node === 'string')
const isImg = computed(() => props.node?.tag === 'img')

// 白名单：Telegraph 文章实际会用到的标签
const ALLOWED = new Set([
  'p', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
  'figure', 'img', 'a', 'blockquote',
  'b', 'strong', 'i', 'em', 'u', 's', 'sub', 'sup',
  'code', 'pre', 'br', 'hr',
  'ul', 'ol', 'li', 'table', 'thead', 'tbody', 'tr', 'td', 'th',
])

const safeTag = computed(() => {
  const t = props.node?.tag
  return ALLOWED.has(t) ? t : 'div'
})

const children = computed(() => props.node?.children || [])

// 只透传必要属性：a 的 href、img 的 src（走后端代理，避免浏览器直连外域）
const attrs = computed(() => {
  const raw = props.node?.attrs || {}
  const a = {}
  if (props.node?.tag === 'a' && raw.href) {
    a.href = raw.href
    a.target = '_blank'
    a.rel = 'noopener'
  }
  if (isImg.value && raw.src) {
    a.src = api.telegraphImgUrl(raw.src)
    a.loading = 'lazy'
    if (raw.alt) a.alt = raw.alt
  }
  return a
})

// 点击图片：新标签打开原图（跳过代理，查看完整大图）
function openOrig() {
  const raw = props.node?.attrs
  if (raw?.src) window.open(raw.src, '_blank', 'noopener')
}
</script>
