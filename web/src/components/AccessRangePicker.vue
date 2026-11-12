<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { NDatePicker, NRadioButton, NRadioGroup } from 'naive-ui'
import { presetBounds, recentDayRange, trafficBounds, type TrafficRange } from '../util/traffic-range'

/** 访问域名统计的时间段：按统计时区的自然日计算，结果是 Unix 秒边界（to 不含）。 */
export type AccessPeriod = 'today' | 'yesterday' | '7d' | '30d' | 'month' | 'last_month' | 'custom'
export type AccessBounds = { from: number; to: number }

const props = defineProps<{ timezone: string; period: AccessPeriod; custom: TrafficRange }>()
const emit = defineEmits<{ 'update:period': [AccessPeriod]; 'update:custom': [TrafficRange]; change: [AccessBounds] }>()

const period = ref<AccessPeriod>(props.period)
const custom = ref<TrafficRange>(props.custom)
watch(() => props.period, (value) => { period.value = value })
watch(() => props.custom, (value) => { custom.value = value })

// 每分钟重新计算一次，页面跨过零点后「今日」等时间段随之切到新的一天。
const now = ref(Date.now())
const timer = setInterval(() => { now.value = Date.now() }, 60_000)
onBeforeUnmount(() => clearInterval(timer))

const bounds = computed<AccessBounds>(() => {
  switch (period.value) {
    case '7d': return trafficBounds('day', recentDayRange(7, props.timezone, now.value), props.timezone)
    case '30d': return trafficBounds('day', recentDayRange(30, props.timezone, now.value), props.timezone)
    case 'custom': return trafficBounds('day', custom.value, props.timezone)
    default: return presetBounds(period.value, props.timezone, now.value)
  }
})
watch(period, (value) => emit('update:period', value))
watch(custom, (value) => emit('update:custom', value))
watch(bounds, (value, previous) => {
  if (!previous || value.from !== previous.from || value.to !== previous.to) emit('change', value)
}, { immediate: true })
</script>

<template>
  <div class="access-range">
    <NRadioGroup v-model:value="period" size="small" aria-label="统计时间段">
      <NRadioButton value="today">今日</NRadioButton>
      <NRadioButton value="yesterday">昨日</NRadioButton>
      <NRadioButton value="7d">近 7 天</NRadioButton>
      <NRadioButton value="30d">近 30 天</NRadioButton>
      <NRadioButton value="month">本月</NRadioButton>
      <NRadioButton value="last_month">上月</NRadioButton>
      <NRadioButton value="custom">自定义</NRadioButton>
    </NRadioGroup>
    <NDatePicker v-if="period === 'custom'" v-model:formatted-value="custom" type="daterange" value-format="yyyy-MM-dd HH:mm:ss" size="small" :clearable="false" />
  </div>
</template>

<style scoped>
.access-range { display: flex; flex-wrap: wrap; align-items: center; gap: 12px 16px; min-width: 0; max-width: 100%; }
.access-range > .n-date-picker { width: 280px; }
/* 七个选项在窄屏上放不下，横向滚动而不是被卡片裁掉。 */
.access-range > .n-radio-group { max-width: 100%; overflow-x: auto; scrollbar-width: none; flex-wrap: nowrap; }
.access-range > .n-radio-group :deep(.n-radio-button) { flex-shrink: 0; }
@media (max-width: 600px) {
  .access-range > .n-date-picker { width: 100%; }
}
</style>
