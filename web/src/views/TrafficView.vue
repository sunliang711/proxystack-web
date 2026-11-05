<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import {
  NButton, NCard, NDataTable, NDatePicker, NEmpty, NGrid, NGridItem, NRadioButton, NRadioGroup, NSelect, NSpace, NStatistic, NTag, NText, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { api, problemMessage, saveBlob, type Schemas } from '../api/client'
import TrafficChart from '../components/TrafficChart.vue'
import EntityLink from '../components/EntityLink.vue'
import type { ChartSeries } from '../util/chart'
import { formatBytes } from '../util/format'
import { defaultTrafficRange, trafficBounds, trafficRangeLabel, type TrafficLevel, type TrafficRange } from '../util/traffic-range'

type Level = TrafficLevel
type Scope = 'user' | 'inbound' | 'outbound'
type Series = Schemas['TrafficSeries']

const message = useMessage()
// 其他页面可通过链接参数预置筛选，例如用户详情页带上 scope=user 和该用户。
const linked = useRoute().query
const pick = <T extends string>(value: unknown, allowed: readonly T[], fallback: T): T => ((allowed as readonly unknown[]).includes(value) ? (value as T) : fallback)
const listParam = (value: unknown) => (typeof value === 'string' && value ? value.split(',') : [])
const level = ref<Level>(pick(linked.level, ['hour', 'day', 'month', 'year'], 'hour'))
const scope = ref<Scope>(pick(linked.scope, ['user', 'inbound', 'outbound'], 'inbound'))
const groupBy = ref<'stack' | 'subject'>(pick(linked.group_by, ['stack', 'subject'], 'stack'))
const timezone = ref('UTC')
const ready = ref(false)
const range = ref<TrafficRange>(defaultTrafficRange(level.value, timezone.value))
const pickerType = computed(() => ({ hour: 'datetimerange', day: 'daterange', month: 'monthrange', year: 'yearrange' } as const)[level.value])
const bounds = computed(() => trafficBounds(level.value, range.value, timezone.value))
const rangeLabel = computed(() => trafficRangeLabel(bounds.value, timezone.value))
const stacks = ref<number[]>(listParam(linked.stacks).map(Number).filter((id) => Number.isInteger(id) && id > 0))
const subjects = ref<string[]>(listParam(linked.subjects))
const stackOptions = ref<{ label: string; value: number }[]>([])
const subjectOptions = ref<{ label: string; value: string }[]>([])
const result = ref<Schemas['TrafficResult'] | null>(null)
const loading = ref(false)

function params() {
  return {
    level: level.value, scope: scope.value, group_by: groupBy.value,
    ...bounds.value,
    stacks: stacks.value.join(','), subjects: subjects.value.join(','),
  }
}

let loadVersion = 0
async function load() {
  if (!ready.value) return
  const version = ++loadVersion
  loading.value = true
  result.value = null
  const { data, error } = await api.GET('/api/traffic/query', { params: { query: params() } })
  if (version !== loadVersion) return
  loading.value = false
  if (error) {
    result.value = null
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
  const [{ data }, { data: settings, error }] = await Promise.all([api.GET('/api/stacks'), api.GET('/api/config/settings')])
  if (!settings) {
    message.error(problemMessage(error, '无法读取统计时区'))
    return
  }
  timezone.value = settings.basic.timezone || 'UTC'
  range.value = defaultTrafficRange(level.value, timezone.value)
  stackOptions.value = (data?.stacks ?? []).map((stack) => ({ label: stack.name, value: stack.id }))
  await loadSubjects()
  ready.value = true
  await load()
})
watch(level, (next) => {
  range.value = defaultTrafficRange(next, timezone.value)
})
watch(scope, () => {
  subjects.value = []
  slots.clear()
  loadSubjects()
})
watch(groupBy, () => slots.clear())
watch([level, scope, groupBy, range, stacks, subjects], load)

/** 颜色跟随对象而不是排名：对象首次出现时占一个槽位，筛选后保持不变。放不下的并入「其他」。 */
const MAX_SLOTS = 7
const slots = new Map<string, number>()
function slotOf(subject: string): number | undefined {
  if (!slots.has(subject) && slots.size < MAX_SLOTS) slots.set(subject, slots.size)
  return slots.get(subject)
}

/** 聚合 stack 只经成员 stack 的 relay 出网；入站和出站维度下它的流量已在成员 stack 计入，合计不含它。 */
const deduplicated = computed(() => scope.value !== 'user')
const aggregatorIds = computed(() => new Set((result.value?.by_stack ?? []).filter((stack) => stack.aggregator).map((stack) => String(stack.stack_id))))
const aggregatorNames = computed(() => (result.value?.by_stack ?? []).filter((stack) => stack.aggregator).map((stack) => stack.label))
const isAggregatorRow = (subject: string) => deduplicated.value && groupBy.value === 'stack' && aggregatorIds.value.has(subject)

const chartSeries = computed<ChartSeries[]>(() => {
  const out: ChartSeries[] = []
  let other: number[] | null = null
  for (const series of result.value?.series ?? []) {
    const data = series.up.map((up, index) => up + series.down[index]!)
    const slot = slotOf(series.subject)
    if (slot !== undefined) {
      out.push({
        name: series.label, data, slot, excludeFromTotal: isAggregatorRow(series.subject),
        entity: groupBy.value === 'stack' || scope.value === 'user' ? { kind: groupBy.value === 'stack' ? 'stack' : 'user', id: series.subject } : undefined,
      })
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
  {
    title: () => groupBy.value === 'stack' ? 'Stack' : '对象', key: 'label',
    render: (row) => {
      if (!(groupBy.value === 'stack' || scope.value === 'user')) return row.label
      const link = h(EntityLink, { kind: groupBy.value === 'stack' ? 'stack' : 'user', id: row.subject, label: row.label })
      return isAggregatorRow(row.subject)
        ? h(NSpace, { size: 6, align: 'center', wrap: false }, () => [link, h(NTag, { size: 'small', bordered: false }, () => '聚合，不计入合计')])
        : link
    },
  },
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
  <NSpace vertical :size="20">
    <NCard size="small">
      <div class="traffic-filters">
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
        <NDatePicker v-model:formatted-value="range" :type="pickerType" value-format="yyyy-MM-dd HH:mm:ss" size="small" :clearable="false" :disabled="!ready" />
        <NSelect v-model:value="stacks" :options="stackOptions" multiple clearable size="small" placeholder="全部 stack" style="min-width: 0" />
        <NSelect v-model:value="subjects" :options="subjectOptions" multiple clearable filterable size="small" :placeholder="scope === 'user' ? '全部用户（可指定）' : '全部对象'" style="min-width: 0" />
        <NButton size="small" :disabled="!ready" @click="exportCSV">导出 CSV</NButton>
      </div>
    </NCard>
    <NSpace v-if="stacks.length || (scope === 'user' && subjects.length)" align="center" :size="8">
      <NText depth="3">已筛选：</NText>
      <EntityLink v-for="id in stacks" :key="`stack:${id}`" kind="stack" :id="id" />
      <template v-if="scope === 'user'"><EntityLink v-for="id in subjects" :key="`user:${id}`" kind="user" :id="id" :label="subjectOptions.find((option) => option.value === id)?.label" /></template>
    </NSpace>
    <NText depth="3">当前{{ scope === 'user' ? '按用户计数' : scope === 'inbound' ? '按入站计数' : '按出站计数' }}；总和与下方 Stack 明细使用相同筛选条件。切换「用户」可指定用户；共享账号流量可能无法归属到用户。</NText>
    <NText v-if="deduplicated && aggregatorNames.length" depth="3">
      合计不含聚合 stack {{ aggregatorNames.join('、') }}：它们的流量经成员 stack 的 relay 出去，已计入成员 stack。
    </NText>
    <NText v-if="ready" depth="3">统计范围：{{ rangeLabel }}；按完整{{ level === 'hour' ? '小时' : level === 'day' ? '自然日' : level === 'month' ? '自然月' : '自然年' }}汇总，当前周期统计至最近一次采集。</NText>
    <NGrid cols="2 s:3" responsive="screen" :x-gap="20" :y-gap="20" item-responsive>
      <NGridItem span="2 s:1"><NCard size="small" class="metric-card"><NStatistic label="所选范围合计" :value="formatBytes((result?.total.up ?? 0) + (result?.total.down ?? 0))" /></NCard></NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="上行合计" :value="formatBytes(result?.total.up ?? 0)" /></NCard></NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="下行合计" :value="formatBytes(result?.total.down ?? 0)" /></NCard></NGridItem>
    </NGrid>
    <NCard title="流量趋势" size="small">
      <template #header-extra>
        <NRadioGroup v-model:value="groupBy" size="small">
          <NRadioButton value="stack">按 Stack</NRadioButton>
          <NRadioButton value="subject">按{{ scope === 'user' ? '用户' : scope === 'inbound' ? '入站' : '出站' }}</NRadioButton>
        </NRadioGroup>
      </template>
      <!-- 按入站或出站对象分组时，同名对象跨 stack 合并，无法剔除聚合 stack 的重复部分，提示框不显示合计。 -->
      <TrafficChart v-if="result?.buckets.length" kind="bar" :labels="labels" :series="chartSeries" :format="formatBytes" height="320px" :hide-total="deduplicated && groupBy === 'subject' && aggregatorNames.length > 0" />
      <NEmpty v-else description="所选范围内没有数据" />
    </NCard>
    <NCard :title="groupBy === 'stack' ? '各 Stack 流量明细' : '对象流量明细'" size="small">
      <p v-if="ready" class="traffic-range"><NText depth="3">{{ rangeLabel }}</NText></p>
      <NDataTable :columns="columns" :data="result?.series ?? []" :loading="loading" :row-key="(row: Series) => row.subject" :pagination="{ pageSize: 20 }" size="small" />
    </NCard>
  </NSpace>
</template>

<style scoped>
.traffic-range { margin: 0 0 16px; font-size: 12px; }
.traffic-filters {
  display: grid;
  grid-template-columns: auto auto minmax(240px, 1fr);
  align-items: center;
  gap: 16px;
}
.traffic-filters > :last-child { justify-self: end; }
@media (max-width: 1100px) {
  .traffic-filters { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .traffic-filters > .n-date-picker { grid-column: 1 / -1; }
}
@media (max-width: 600px) {
  .traffic-filters { grid-template-columns: minmax(0, 1fr); gap: 12px; }
  .traffic-filters > :last-child { justify-self: start; }
}
</style>
