<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NPopconfirm, NProgress, NRadioButton, NRadioGroup, NSelect, NSpace, NTable, NText, useMessage } from 'naive-ui'
import { RouterLink } from 'vue-router'
import { api, problemMessage, type Schemas } from '../api/client'
import { usePolling } from '../composables/usePolling'
import { formatBytes, formatTime } from '../util/format'
import type { ChartSeries } from '../util/chart'
import { useTrafficTrend } from '../composables/useTrafficTrend'
import TrafficChart from './TrafficChart.vue'
import EntityLink from './EntityLink.vue'

const props = defineProps<{ userId: number; dailyLimit: number; quota: number }>()
/** 每次刷新到新的用量时通知页面，用于同步页面顶部的摘要。 */
const emit = defineEmits<{ update: [usage: Schemas['UserUsage']] }>()
const message = useMessage()
const stackId = ref<number | null>(null)
const labels = { daily: '每日阈值', quota: '月度额度', expired: '到期', schedule: '停服时段' } as const

const { data: usage, loading, error, updatedAt, refresh: load } = usePolling<Schemas['UserUsage']>(() => props.userId, async (signal) => {
  const { data, error } = await api.GET('/api/users/{id}/usage', { params: { path: { id: props.userId } }, signal })
  if (!data) throw new Error(problemMessage(error, '用量刷新失败'))
  return data
})
watch(() => props.userId, () => { stackId.value = null })
watch(usage, (data) => { if (data) emit('update', data) })
watch(usage, (data) => {
  if (data && !data.by_stack.some((stack) => stack.stack_id === stackId.value)) stackId.value = null
})

const total = (bytes: { up: number; down: number }) => bytes.up + bytes.down
const percent = (used: number, limit: number) => Math.min(100, Math.round((used / limit) * 100))
const status = (value: number) => (value >= 100 ? 'error' : value >= 80 ? 'warning' : 'success')

async function release(reason: 'daily' | 'schedule') {
  const { error } = await api.DELETE('/api/users/{id}/auto-pause/{reason}', { params: { path: { id: props.userId, reason } } })
  if (error) {
    message.error(problemMessage(error))
    return
  }
  message.success(reason === 'daily' ? '已解除，今天不再按每日阈值暂停' : '已放行，本次停服时段内不再暂停')
  load()
}
const hasTime = (value: string) => new Date(value).getFullYear() > 1

const selectedStacks = computed(() => (usage.value?.by_stack ?? []).filter((stack) => !stackId.value || stack.stack_id === stackId.value))
const selectedTotal = computed(() => selectedStacks.value.reduce((sum, stack) => ({ today: sum.today + total(stack.today), recent: sum.recent + total(stack.recent) }), { today: 0, recent: 0 }))
const daily = computed(() => stackId.value ? selectedStacks.value[0]?.daily ?? [] : usage.value?.daily ?? [])
const series = computed(() => {
  const stacks = selectedStacks.value
  if (!stacks.length) return [{ name: '日用量', data: daily.value.map(total), slot: 0 }]
  const shown: ChartSeries[] = stacks.slice(0, 7).map((stack, index) => ({ name: stack.stack, data: stack.daily.map(total), slot: index, entity: { kind: 'stack', id: stack.stack_id } }))
  const rest = stacks.slice(7)
  if (rest.length) shown.push({ name: '其他 Stack', data: daily.value.map((_, index) => rest.reduce((sum, stack) => sum + total(stack.daily[index]!), 0)), slot: undefined })
  return shown
})
const days = computed(() => daily.value.map((item) => item.day.slice(5)))
const reference = computed(() => (!stackId.value && props.dailyLimit ? { value: props.dailyLimit, label: '每日阈值' } : undefined))

/** 趋势图可在近 30 天日用量和最近若干小时的小时用量之间切换；小时用量来自流量明细，随用量轮询一起刷新。 */
const view = ref<'daily' | 'hourly'>('daily')
const hours = ref(24)
const hourOptions = [{ label: '24 小时', value: 24 }, { label: '48 小时', value: 48 }, { label: '7 天', value: 168 }]
const { trend: hourly, error: hourlyError, load: loadHourly } = useTrafficTrend(() => view.value === 'hourly'
  ? { level: 'hour', span: hours.value, view: 'stack', stacks: stackId.value ? [stackId.value] : [], users: [props.userId] }
  : null)
watch(usage, () => void loadHourly())
/** 在流量明细页查看同一用户、同一 Stack 的更长范围或导出。 */
const trafficLink = computed(() => ({
  name: 'traffic',
  query: { level: view.value === 'hourly' ? 'hour' : 'day', users: String(props.userId), ...(stackId.value ? { stacks: String(stackId.value) } : {}) },
}))
</script>

<template>
  <NCard title="用量" size="small" class="card">
    <template #header-extra>
      <NSpace align="center" :size="8">
        <NText depth="3">每 10 秒更新<template v-if="updatedAt"> · {{ formatTime(updatedAt) }}</template></NText>
        <NButton size="tiny" :loading="loading" @click="load">刷新</NButton>
      </NSpace>
    </template>
    <NAlert v-if="error" type="warning" :bordered="false" style="margin-bottom: 20px">{{ error }}<template v-if="usage">；当前显示上次成功获取的数据</template></NAlert>
    <NSpace v-if="usage" vertical :size="12">
      <NAlert v-for="pause in usage.auto_pauses" :key="pause.reason" type="warning" :bordered="false" :title="`自动暂停：${labels[pause.reason]}`">
        <NSpace align="center" :size="8">
          <NText>{{ formatTime(pause.since) }} 起<template v-if="new Date(pause.resume_at).getFullYear() > 1">，预计 {{ formatTime(pause.resume_at) }} 恢复</template>。</NText>
          <NPopconfirm v-if="pause.reason === 'daily'" @positive-click="release('daily')">
            <template #trigger><NButton size="tiny">解除</NButton></template>
            解除后今天不再按每日阈值暂停，次日重新判定。
          </NPopconfirm>
          <NPopconfirm v-else-if="pause.reason === 'schedule'" @positive-click="release('schedule')">
            <template #trigger><NButton size="tiny">本时段放行</NButton></template>
            放行到这段停服结束为止，下一次停服时段照常暂停。
          </NPopconfirm>
          <NText v-else depth="3">调整{{ pause.reason === 'quota' ? '额度' : '到期时间' }}后自动解除。</NText>
        </NSpace>
      </NAlert>
      <NText v-if="usage.exempt" depth="3">今天已解除每日暂停，次日重新判定。</NText>
      <NText v-if="hasTime(usage.window_exempt_until)" depth="3">本次停服时段已放行，至 {{ formatTime(usage.window_exempt_until) }}。</NText>
      <div class="usage-tiles">
        <div class="usage-tile">
          <NText depth="3" class="tile-label">今日用量（全部 Stack）</NText>
          <div class="tile-value">{{ formatBytes(total(usage.today)) }}<NText v-if="dailyLimit" depth="3" class="tile-limit"> / {{ formatBytes(dailyLimit) }}</NText></div>
          <NProgress v-if="dailyLimit" type="line" :percentage="percent(total(usage.today), dailyLimit)" :status="status(percent(total(usage.today), dailyLimit))" />
          <NText depth="3" class="tile-note">↑ {{ formatBytes(usage.today.up) }} · ↓ {{ formatBytes(usage.today.down) }}<template v-if="!dailyLimit"> · 未设每日阈值</template></NText>
        </div>
        <div class="usage-tile">
          <NText depth="3" class="tile-label">{{ new Date(usage.period_start).getFullYear() > 1 ? '本周期用量' : '累计用量' }}（全部 Stack）</NText>
          <div class="tile-value">{{ formatBytes(total(usage.period)) }}<NText v-if="quota" depth="3" class="tile-limit"> / {{ formatBytes(quota) }}</NText></div>
          <NProgress v-if="quota" type="line" :percentage="percent(total(usage.period), quota)" :status="status(percent(total(usage.period), quota))" />
          <NText depth="3" class="tile-note">
            <template v-if="new Date(usage.next_reset).getFullYear() > 1">{{ formatTime(usage.next_reset) }} 重置</template>
            <template v-else>不按周期重置，包含历史累计流量</template><template v-if="!quota"> · 不限额度</template>
          </NText>
        </div>
      </div>
      <div class="stack-head">
        <NText strong>各 Stack 用量</NText>
        <NSelect v-model:value="stackId" :options="usage.by_stack.map((stack) => ({ label: stack.stack, value: stack.stack_id }))" clearable filterable size="small" placeholder="全部 Stack" class="stack-select" />
      </div>
      <NText depth="3" class="stack-note">今日用量和最近 30 天已保留的明细，趋势图可按天或按小时查看；额度和暂停仍以全部 Stack 的合计判定。</NText>
      <div v-if="selectedStacks.length" class="table-scroll"><NTable :bordered="false" size="small">
        <thead><tr><th>Stack</th><th>今日上行 / 下行</th><th>今日合计</th><th>近 30 天上行 / 下行</th><th>近 30 天合计</th></tr></thead>
        <tbody>
          <tr v-for="stack in selectedStacks" :key="stack.stack_id">
            <td><EntityLink kind="stack" :id="stack.stack_id" :name="stack.stack" /></td>
            <td>{{ formatBytes(stack.today.up) }} / {{ formatBytes(stack.today.down) }}</td>
            <td>{{ formatBytes(total(stack.today)) }}</td>
            <td>{{ formatBytes(stack.recent.up) }} / {{ formatBytes(stack.recent.down) }}</td>
            <td>{{ formatBytes(total(stack.recent)) }}</td>
          </tr>
          <tr>
            <td><NText strong>所选 Stack 合计</NText></td><td />
            <td><NText strong>{{ formatBytes(selectedTotal.today) }}</NText></td><td />
            <td><NText strong>{{ formatBytes(selectedTotal.recent) }}</NText></td>
          </tr>
        </tbody>
      </NTable></div>
      <div class="trend-head">
        <NRadioGroup v-model:value="view" size="small">
          <NRadioButton value="daily">按天</NRadioButton>
          <NRadioButton value="hourly">按小时</NRadioButton>
        </NRadioGroup>
        <NRadioGroup v-if="view === 'hourly'" v-model:value="hours" size="small">
          <NRadioButton v-for="option in hourOptions" :key="option.value" :value="option.value">{{ option.label }}</NRadioButton>
        </NRadioGroup>
        <RouterLink :to="trafficLink" class="trend-link">在流量明细中查看</RouterLink>
      </div>
      <template v-if="view === 'daily'">
        <TrafficChart v-if="daily.some((day) => total(day) > 0)" kind="bar" :labels="days" :series="series" :format="formatBytes" :reference="reference" height="220px" />
        <NEmpty v-else size="small" description="最近 30 天没有用量" />
      </template>
      <template v-else>
        <NAlert v-if="hourlyError" type="warning" :bordered="false">{{ hourlyError }}</NAlert>
        <TrafficChart v-if="hourly && !hourly.empty" kind="bar" :labels="hourly.labels" :series="hourly.series" :format="formatBytes" height="220px" />
        <NEmpty v-else-if="hourly" size="small" :description="`最近 ${hourOptions.find((option) => option.value === hours)?.label} 没有用量`" />
        <NEmpty v-else-if="!hourlyError" size="small" description="正在加载小时用量" />
        <NText depth="3">按统计时区的整点汇总，当前小时统计至最近一次采集。</NText>
      </template>
    </NSpace>
    <NEmpty v-else size="small" :description="loading ? '正在加载用量' : '暂无用量数据'" />
  </NCard>
</template>

<style scoped>
.usage-tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 12px; }
.usage-tile { display: grid; gap: 6px; padding: 14px 16px; border: 1px solid var(--ps-border); border-radius: 10px; background: var(--ps-surface-raised); }
.tile-label { font-size: 12px; }
.tile-value { font-size: 22px; font-weight: 600; font-variant-numeric: tabular-nums; }
.tile-limit { font-size: 14px; font-weight: 400; }
.tile-note { font-size: 12px; }
.stack-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 12px; margin-top: 8px; }
.stack-select { width: 220px; }
.stack-note { font-size: 12px; }
.trend-head { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; }
.trend-link { margin-left: auto; }
</style>
