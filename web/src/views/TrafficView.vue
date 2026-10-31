<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  NButton, NCard, NDataTable, NDatePicker, NEmpty, NRadioButton, NRadioGroup, NSelect, NSpace, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { api, problemMessage, saveBlob, type Schemas } from '../api/client'
import TrafficChart from '../components/TrafficChart.vue'
import type { ChartSeries } from '../util/chart'
import { formatBytes } from '../util/format'

type Level = 'hour' | 'day' | 'month' | 'year'
type Scope = 'user' | 'inbound' | 'outbound'
type Series = Schemas['TrafficSeries']

const DAY = 86400_000
const message = useMessage()
const level = ref<Level>('day')
const scope = ref<Scope>('user')
const range = ref<[number, number]>(defaultRange('day'))
const stacks = ref<number[]>([])
const subjects = ref<string[]>([])
const stackOptions = ref<{ label: string; value: number }[]>([])
const subjectOptions = ref<{ label: string; value: string }[]>([])
const result = ref<Schemas['TrafficResult'] | null>(null)
const loading = ref(false)

function defaultRange(next: Level): [number, number] {
  const now = Date.now()
  const span = { hour: DAY, day: 30 * DAY, month: 365 * DAY, year: 5 * 365 * DAY }[next]
  return [now - span, now]
}

function params() {
  return {
    level: level.value, scope: scope.value,
    from: Math.floor(range.value[0] / 1000), to: Math.ceil(range.value[1] / 1000) + 3600,
    stacks: stacks.value.join(','), subjects: subjects.value.join(','),
  }
}

async function load() {
  loading.value = true
  const { data, error } = await api.GET('/api/traffic/query', { params: { query: params() } })
  loading.value = false
  if (error) {
    message.error(problemMessage(error))
    return
  }
  result.value = data ?? null
}

async function loadSubjects() {
  const { data } = await api.GET('/api/traffic/subjects', { params: { query: { scope: scope.value } } })
  subjectOptions.value = (data ?? []).map((item) => ({ label: item.label, value: item.subject }))
}

onMounted(async () => {
  const { data } = await api.GET('/api/stacks')
  stackOptions.value = (data?.stacks ?? []).map((stack) => ({ label: stack.name, value: stack.id }))
  await loadSubjects()
  await load()
})
watch(level, (next) => {
  range.value = defaultRange(next)
})
watch(scope, () => {
  subjects.value = []
  slots.clear()
  loadSubjects()
})
watch([level, scope, range, stacks, subjects], load)

/** 颜色跟随对象而不是排名：对象首次出现时占一个槽位，筛选后保持不变。放不下的并入「其他」。 */
const MAX_SLOTS = 7
const slots = new Map<string, number>()
function slotOf(subject: string): number | undefined {
  if (!slots.has(subject) && slots.size < MAX_SLOTS) slots.set(subject, slots.size)
  return slots.get(subject)
}

const chartSeries = computed<ChartSeries[]>(() => {
  const out: ChartSeries[] = []
  let other: number[] | null = null
  for (const series of result.value?.series ?? []) {
    const data = series.up.map((up, index) => up + series.down[index]!)
    const slot = slotOf(series.subject)
    if (slot !== undefined) {
      out.push({ name: series.label, data, slot })
    } else {
      other = other ? other.map((value, index) => value + data[index]!) : data
    }
  }
  if (other) out.push({ name: '其他', data: other })
  return out
})

const labels = computed(() => {
  const data = result.value
  if (!data) return []
  const parts: Intl.DateTimeFormatOptions = {
    hour: { month: '2-digit', day: '2-digit', hour: '2-digit', hourCycle: 'h23' },
    day: { month: '2-digit', day: '2-digit' },
    month: { year: 'numeric', month: '2-digit' },
    year: { year: 'numeric' },
  }[level.value] as Intl.DateTimeFormatOptions
  const formatter = new Intl.DateTimeFormat('zh-CN', { ...parts, timeZone: data.timezone })
  return data.buckets.map((bucket) => formatter.format(new Date(bucket)))
})

const columns: DataTableColumns<Series> = [
  { title: '对象', key: 'label' },
  { title: '上行', key: 'up', render: (row) => formatBytes(row.up.reduce((a, b) => a + b, 0)) },
  { title: '下行', key: 'down', render: (row) => formatBytes(row.down.reduce((a, b) => a + b, 0)) },
  {
    title: '合计', key: 'total', defaultSortOrder: 'descend',
    sorter: (a, b) => a.up.reduce((x, y) => x + y, 0) + a.down.reduce((x, y) => x + y, 0) - b.up.reduce((x, y) => x + y, 0) - b.down.reduce((x, y) => x + y, 0),
    render: (row) => formatBytes(row.up.reduce((a, b) => a + b, 0) + row.down.reduce((a, b) => a + b, 0)),
  },
]

async function exportCSV() {
  const { data, error, response } = await api.GET('/api/traffic/export', { params: { query: params() }, parseAs: 'blob' })
  if (error || !data) {
    message.error(problemMessage(error, '导出失败'))
    return
  }
  const filename = /filename="([^"]+)"/.exec(response.headers.get('Content-Disposition') ?? '')?.[1] ?? 'traffic.csv'
  saveBlob(data as unknown as Blob, filename)
}
</script>

<template>
  <NSpace vertical :size="12">
    <NCard size="small">
      <NSpace align="center">
        <NRadioGroup v-model:value="level" size="small">
          <NRadioButton value="hour">小时</NRadioButton>
          <NRadioButton value="day">日</NRadioButton>
          <NRadioButton value="month">月</NRadioButton>
          <NRadioButton value="year">年</NRadioButton>
        </NRadioGroup>
        <NRadioGroup v-model:value="scope" size="small">
          <NRadioButton value="user">用户</NRadioButton>
          <NRadioButton value="inbound">入站</NRadioButton>
          <NRadioButton value="outbound">出站</NRadioButton>
        </NRadioGroup>
        <NDatePicker v-model:value="range" type="daterange" size="small" :clearable="false" />
        <NSelect v-model:value="stacks" :options="stackOptions" multiple clearable size="small" placeholder="全部 stack" style="min-width: 160px" />
        <NSelect v-model:value="subjects" :options="subjectOptions" multiple clearable filterable size="small" placeholder="全部对象" style="min-width: 160px" />
        <NButton size="small" @click="exportCSV">导出 CSV</NButton>
      </NSpace>
    </NCard>
    <NCard title="流量趋势" size="small">
      <TrafficChart v-if="result?.buckets.length" kind="bar" :labels="labels" :series="chartSeries" :format="formatBytes" height="320px" />
      <NEmpty v-else description="所选范围内没有数据" />
    </NCard>
    <NCard title="明细" size="small">
      <NDataTable :columns="columns" :data="result?.series ?? []" :loading="loading" :row-key="(row: Series) => row.subject" :pagination="{ pageSize: 20 }" size="small" />
    </NCard>
  </NSpace>
</template>
