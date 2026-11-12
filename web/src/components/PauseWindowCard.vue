<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { NCard, NTag, NText } from 'naive-ui'
import { pauseWindow, refreshPauseWindow } from '../composables/usePauseWindow'
import { clockText, hasTime, windowsText } from '../util/pause-window'

/** 总览页的停服时段卡片：设置了全局时段或有用户单独设置时显示。 */
const status = computed(() => pauseWindow.status)
const shown = computed(() => !!status.value && (status.value.windows.length > 0 || status.value.custom > 0 || status.value.paused > 0))
const exceptions = computed(() => {
  const s = status.value
  if (!s) return ''
  return [s.custom ? `${s.custom} 人使用自定义时段` : '', s.exempt ? `${s.exempt} 人不限制` : ''].filter(Boolean).join('、')
})
onMounted(() => void refreshPauseWindow())
</script>

<template>
  <NCard v-if="shown && status" size="small" title="每日停服时段">
    <template #header-extra>
      <RouterLink :to="{ name: 'settings', query: { section: 'pause' } }">设置</RouterLink>
    </template>
    <div class="pause-row">
      <NText v-if="status.windows.length">每天 {{ windowsText(status.windows) }}<NText depth="3">（{{ status.timezone }}）</NText></NText>
      <NText v-else depth="3">未设置全局时段</NText>
      <NTag v-if="status.active" type="warning" size="small" :bordered="false">停服中 · {{ clockText(status.resume_at, status.timezone) }} 恢复</NTag>
      <NTag v-else-if="hasTime(status.next_start)" size="small" :bordered="false">下一次 {{ clockText(status.next_start, status.timezone) }} 开始</NTag>
      <RouterLink v-if="status.paused" :to="{ name: 'users', query: { filter: 'schedule' } }">当前暂停 {{ status.paused }} 人</RouterLink>
      <RouterLink v-if="exceptions" :to="{ name: 'users', query: { filter: 'window_exception' } }">{{ exceptions }}</RouterLink>
    </div>
  </NCard>
</template>

<style scoped>
.pause-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 16px; }
</style>
