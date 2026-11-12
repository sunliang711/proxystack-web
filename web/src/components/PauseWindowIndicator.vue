<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { NIcon, NTag, NTooltip } from 'naive-ui'
import { MoonOutline } from '@vicons/ionicons5'
import { pauseWindow } from '../composables/usePauseWindow'
import { clockText, windowsText } from '../util/pause-window'

/** 顶栏的停服提示：只在有用户因停服时段暂停时显示，点击查看这些用户。 */
const status = computed(() => pauseWindow.status)
const label = computed(() => {
  const s = status.value
  if (!s || !s.paused) return ''
  return s.active ? `停服中 · ${clockText(s.resume_at, s.timezone)} 恢复 · ${s.paused} 人` : `${s.paused} 人停服中`
})
const detail = computed(() => {
  const s = status.value
  if (!s) return ''
  const global = s.windows.length ? `全局停服时段：每天 ${windowsText(s.windows)}（${s.timezone}）。` : '未设置全局停服时段。'
  const current = s.active ? `当前处于全局停服时段，${clockText(s.resume_at, s.timezone)} 恢复。` : '当前暂停的是使用自定义时段的用户。'
  return `${current}${global}点击查看停服暂停的用户。`
})
</script>

<template>
  <NTooltip v-if="label" placement="bottom">
    <template #trigger>
      <RouterLink :to="{ name: 'users', query: { filter: 'schedule' } }" class="indicator" :aria-label="`${label}。${detail}`">
        <NTag type="warning" size="small" round :bordered="false" class="indicator-tag">
          <template #icon><NIcon><MoonOutline /></NIcon></template>
          <span class="indicator-label">{{ label }}</span>
        </NTag>
      </RouterLink>
    </template>
    {{ detail }}
  </NTooltip>
</template>

<style scoped>
.indicator { text-decoration: none; display: inline-flex; }
.indicator-tag { cursor: pointer; }
.indicator:hover .indicator-tag { filter: brightness(1.1); }
@media (max-width: 640px) {
  .indicator-label { display: none; }
}
</style>
