<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NPopconfirm, NProgress, NSelect, NSpace, NTable, NTag, NText, useMessage } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { usePolling } from '../composables/usePolling'
import { formatBytes, formatTime } from '../util/format'
import TrafficChart from './TrafficChart.vue'

const props = defineProps<{ userId: number; dailyLimit: number; quota: number }>()
const message = useMessage()
const stackId = ref<number | null>(null)
const labels = { daily: '每日阈值', quota: '月度额度', expired: '到期' } as const

const { data: usage, loading, error, updatedAt, refresh: load } = usePolling<Schemas['UserUsage']>(() => props.userId, async (signal) => {
  const { data, error } = await api.GET('/api/users/{id}/usage', { params: { path: { id: props.userId } }, signal })
  if (!data) throw new Error(problemMessage(error, '用量刷新失败'))
  return data
})
watch(() => props.userId, () => { stackId.value = null })
watch(usage, (data) => {
  if (data && !data.by_stack.some((stack) => stack.stack_id === stackId.value)) stackId.value = null
})

const total = (bytes: { up: number; down: number }) => bytes.up + bytes.down
const percent = (used: number, limit: number) => Math.min(100, Math.round((used / limit) * 100))
const status = (value: number) => (value >= 100 ? 'error' : value >= 80 ? 'warning' : 'success')

async function release() {
  const { error } = await api.DELETE('/api/users/{id}/auto-pause/{reason}', { params: { path: { id: props.userId, reason: 'daily' } } })
  if (error) {
    message.error(problemMessage(error))
    return
  }
  message.success('已解除，今天不再按每日阈值暂停')
  load()
}

const selectedStacks = computed(() => (usage.value?.by_stack ?? []).filter((stack) => !stackId.value || stack.stack_id === stackId.value))
const selectedTotal = computed(() => selectedStacks.value.reduce((sum, stack) => ({ today: sum.today + total(stack.today), recent: sum.recent + total(stack.recent) }), { today: 0, recent: 0 }))
const daily = computed(() => stackId.value ? selectedStacks.value[0]?.daily ?? [] : usage.value?.daily ?? [])
const series = computed(() => {
  const stacks = selectedStacks.value
  if (!stacks.length) return [{ name: '日用量', data: daily.value.map(total), slot: 0 }]
  const shown = stacks.slice(0, 7).map((stack, index) => ({ name: stack.stack, data: stack.daily.map(total), slot: index as number | undefined }))
  const rest = stacks.slice(7)
  if (rest.length) shown.push({ name: '其他 Stack', data: daily.value.map((_, index) => rest.reduce((sum, stack) => sum + total(stack.daily[index]!), 0)), slot: undefined })
  return shown
})
const days = computed(() => daily.value.map((item) => item.day.slice(5)))
const reference = computed(() => (!stackId.value && props.dailyLimit ? { value: props.dailyLimit, label: '每日阈值' } : undefined))
</script>

<template>
  <NCard title="用量" size="small" class="card">
    <template #header-extra>
      <NSpace align="center" :size="8">
        <NText depth="3">每 10 秒更新<template v-if="updatedAt"> · {{ formatTime(updatedAt) }}</template></NText>
        <NButton size="tiny" :loading="loading" @click="load">刷新</NButton>
      </NSpace>
    </template>
    <NAlert v-if="error" type="warning" :bordered="false" style="margin-bottom: 12px">{{ error }}<template v-if="usage">；当前显示上次成功获取的数据</template></NAlert>
    <NSpace v-if="usage" vertical :size="12">
      <NSpace v-if="usage.auto_pauses.length" vertical>
        <NSpace v-for="pause in usage.auto_pauses" :key="pause.reason" align="center">
          <NTag type="warning" size="small">自动暂停：{{ labels[pause.reason] }}</NTag>
          <NText depth="3">
            {{ formatTime(pause.since) }} 起<template v-if="new Date(pause.resume_at).getFullYear() > 1">，预计 {{ formatTime(pause.resume_at) }} 恢复</template>
          </NText>
          <NPopconfirm v-if="pause.reason === 'daily'" @positive-click="release">
            <template #trigger><NButton size="tiny">解除</NButton></template>
            解除后今天不再按每日阈值暂停，次日重新判定。
          </NPopconfirm>
          <NText v-else depth="3">调整{{ pause.reason === 'quota' ? '额度' : '到期时间' }}后自动解除</NText>
        </NSpace>
      </NSpace>
      <NText v-if="usage.exempt" depth="3">今天已解除每日暂停，次日重新判定。</NText>
      <div>
        <NText>今日总用量（全部 Stack） {{ formatBytes(total(usage.today)) }}<template v-if="dailyLimit"> / {{ formatBytes(dailyLimit) }}</template></NText>
        <NProgress v-if="dailyLimit" type="line" :percentage="percent(total(usage.today), dailyLimit)" :status="status(percent(total(usage.today), dailyLimit))" />
      </div>
      <div>
        <NText>本周期总用量（全部 Stack） {{ formatBytes(total(usage.period)) }}<template v-if="quota"> / {{ formatBytes(quota) }}</template></NText>
        <NProgress v-if="quota" type="line" :percentage="percent(total(usage.period), quota)" :status="status(percent(total(usage.period), quota))" />
        <NText v-if="new Date(usage.next_reset).getFullYear() > 1" depth="3" style="display: block">{{ formatTime(usage.next_reset) }} 重置</NText>
      </div>
      <NSpace align="center">
        <NText strong>按 Stack 查看用量</NText>
        <NSelect v-model:value="stackId" :options="usage.by_stack.map((stack) => ({ label: stack.stack, value: stack.stack_id }))" clearable filterable placeholder="全部 Stack" style="width: 220px" />
      </NSpace>
      <NText depth="3">下方展示各 Stack 的今日用量和最近 30 天已保留的流量明细；额度和暂停仍以全部 Stack 的总用量判定。</NText>
      <NTable v-if="selectedStacks.length" :bordered="false" size="small">
        <thead><tr><th>Stack</th><th>今日上行 / 下行</th><th>今日合计</th><th>近 30 天上行 / 下行</th><th>近 30 天合计</th></tr></thead>
        <tbody>
          <tr v-for="stack in selectedStacks" :key="stack.stack_id">
            <td>{{ stack.stack }}</td>
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
      </NTable>
      <TrafficChart v-if="daily.some((day) => total(day) > 0)" kind="bar" :labels="days" :series="series" :format="formatBytes" :reference="reference" height="220px" />
      <NEmpty v-else size="small" description="最近 30 天没有用量" />
    </NSpace>
    <NEmpty v-else size="small" :description="loading ? '正在加载用量' : '暂无用量数据'" />
  </NCard>
</template>
