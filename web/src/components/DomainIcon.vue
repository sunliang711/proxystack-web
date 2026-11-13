<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { panelBase } from '../api/client'

/**
 * 主域名旁的网站图标：先显示首字母色块，图标由面板经下载代理获取并缓存，加载成功后盖在色块上。
 * fallbacks 是 domain 没有图标时依次尝试的域名（例如服务的成员域名）；都没有图标时保留色块。
 */
const props = withDefaults(defineProps<{ domain: string; fallbacks?: string[]; size?: number }>(), { size: 16, fallbacks: () => [] })
const loaded = ref(false)
// 正在尝试的候选域名的下标，超出候选个数表示都没有图标。
const index = ref(0)
// 最多尝试的域名个数：每个没取过的域名都要面板现场获取，同时获取的个数有限，不宜一次试太多。
const maxCandidates = 3
const candidates = computed(() => [...new Set([props.domain, ...props.fallbacks].filter(Boolean))].slice(0, maxCandidates))
watch(() => candidates.value.join(' '), () => { loaded.value = false; index.value = 0 })

const current = computed(() => candidates.value[index.value])
const src = computed(() => `${panelBase}api/access/favicon?domain=${encodeURIComponent(current.value ?? '')}`)
const letter = computed(() => (props.domain.match(/[a-z0-9]/i)?.[0] ?? '?').toUpperCase())
// 按域名取一个稳定的色相，同一域名每次颜色相同。
const hue = computed(() => {
  let hash = 0
  for (const char of props.domain) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
  return hash % 360
})
</script>

<template>
  <span class="domain-icon" :class="{ loaded }" :style="{ width: `${size}px`, height: `${size}px`, '--hue': hue, fontSize: `${Math.round(size * 0.62)}px` }" aria-hidden="true">
    <span v-if="!loaded" class="letter">{{ letter }}</span>
    <!-- 图片不能用 display: none 隐藏：浏览器不会加载不显示的懒加载图片。先透明叠在色块上，加载成功后再显示。 -->
    <img v-if="current" :key="current" :class="{ ready: loaded }" :src="src" alt="" loading="lazy" decoding="async" referrerpolicy="no-referrer" @load="loaded = true" @error="index++">
  </span>
</template>

<style scoped>
.domain-icon {
  position: relative; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0;
  border-radius: 4px; overflow: hidden; vertical-align: middle;
}
.letter {
  width: 100%; height: 100%; display: inline-flex; align-items: center; justify-content: center;
  background: hsl(var(--hue) 55% 42%); color: #fff; font-weight: 600; line-height: 1;
  font-family: system-ui, sans-serif;
}
.domain-icon img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: contain; opacity: 0; }
.domain-icon img.ready { opacity: 1; }
/* 多数图标按白色网页设计，深色主题下黑色图标会看不见，加载后垫一块浅色底。 */
.domain-icon.loaded { background: #fff; }
.domain-icon.loaded img { inset: 1px; width: calc(100% - 2px); height: calc(100% - 2px); }
</style>
