<script setup lang="ts">
import { computed, h, ref, watch, type VNodeChild } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NAlert, NButton, NCard, NDataTable, NDatePicker, NEmpty, NGrid, NGridItem, NRadioButton, NRadioGroup, NSelect, NSpace, NStatistic, NSwitch, NTag, NText, NTooltip,
  useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, problemMessage, saveBlob, type Schemas } from '../../api/client'
import TrafficChart from '../../components/TrafficChart.vue'
import EntityLink from '../../components/EntityLink.vue'
import GapNotice from './GapNotice.vue'
import { bucketLabels, statsSeries } from '../../composables/useTrafficTrend'
import { formatBytes, formatRate, formatTime } from '../../util/format'
import {
  bucketCount, coarserLevel, convertRange, defaultTrafficRange, matchPreset, presetRange, rangeInFuture, shiftRange, trafficBounds, trafficPresets, trafficRangeLabel,
  type TrafficLevel, type TrafficPreset, type TrafficRange,
} from '../../util/traffic-range'
import { chartAreas, compareChange, totalOf } from '../../util/traffic-stats'

type Row = Schemas['TrafficStatsRow']
type Option<T> = { label: string; value: T }

const props = defineProps<{ view: 'user' | 'stack'; timezone: string; userOptions: Option<number>[]; stackOptions: Option<number>[] }>()
const message = useMessage()
const route = useRoute()
const router = useRouter()

// 筛选条件记在地址栏，「按用户」和「按 Stack」共用同一组条件，切换视角时保持不变。
const linked = route.query
const rangeParam = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/
const levels = ['hour', 'day', 'month', 'year'] as const
const ids = (value: unknown) => (typeof value === 'string' && value ? value.split(',').map(Number).filter((id) => Number.isInteger(id) && id > 0) : [])
// 默认按小时查看统计时区的当天，即快捷时间段「今日」。
const level = ref<TrafficLevel>((levels as readonly unknown[]).includes(linked.level) ? (linked.level as TrafficLevel) : 'hour')
const range = ref<TrafficRange>(
  typeof linked.from === 'string' && typeof linked.to === 'string' && rangeParam.test(linked.from) && rangeParam.test(linked.to) && linked.from <= linked.to
    ? [linked.from, linked.to]
    : defaultTrafficRange(level.value, props.timezone),
)
const users = ref<number[]>(ids(linked.users))
const stacks = ref<number[]>(ids(linked.stacks))
const compare = ref(linked.compare === '1')
const showZero = ref(linked.zero === '1')
const pickerType = computed(() => ({ hour: 'datetimerange', day: 'daterange', month: 'monthrange', year: 'yearrange' } as const)[level.value])
const bounds = computed(() => trafficBounds(level.value, range.value, props.timezone))
const rangeLabel = computed(() => trafficRangeLabel(bounds.value, props.timezone))

/** 服务端单次查询的时间桶上限。 */
const MAX_BUCKETS = 13000
const unitName: Record<TrafficLevel, string> = { hour: '小时', day: '天', month: '个月', year: '年' }
const activePreset = computed(() => matchPreset(level.value, range.value, props.timezone))
const spanLabel = computed(() => `共 ${bucketCount(level.value, range.value)} ${unitName[level.value]}`)
const canShiftForward = computed(() => !rangeInFuture(level.value, shiftRange(level.value, range.value, 1), props.timezone))

/** 快捷时间段同时设定粒度和范围，此时不再按粒度换算范围。 */
let applyingPreset = false
function applyPreset(preset: TrafficPreset) {
  const next = presetRange(preset, props.timezone)
  applyingPreset = next.level !== level.value
  level.value = next.level
  range.value = next.range
}
function shift(direction: 1 | -1) {
  range.value = shiftRange(level.value, range.value, direction)
}
/** 切换粒度时保留所选时间段并换算到新粒度；换算后时间桶过多，或按年只剩一年时，改用新粒度的默认范围。 */
watch(level, (next, previous) => {
  if (applyingPreset) {
    applyingPreset = false
    return
  }
  const converted = convertRange(previous, next, range.value, props.timezone)
  const count = bucketCount(next, converted)
  range.value = count > MAX_BUCKETS || (next === 'year' && count < 2) ? defaultTrafficRange(next, props.timezone) : converted
})
function switchCoarser() {
  const next = coarserLevel(level.value)
  if (next) level.value = next
}

watch([level, range, users, stacks, compare, showZero], () => {
  const custom = range.value.join() !== defaultTrafficRange(level.value, props.timezone).join()
  const ours = {
    level: level.value === 'hour' ? undefined : level.value,
    from: custom ? range.value[0] : undefined,
    to: custom ? range.value[1] : undefined,
    users: users.value.length ? users.value.join(',') : undefined,
    stacks: stacks.value.length ? stacks.value.join(',') : undefined,
    compare: compare.value ? '1' : undefined,
    zero: showZero.value ? '1' : undefined,
  }
  const current = route.query
  if ((Object.keys(ours) as (keyof typeof ours)[]).every((key) => (current[key] ?? undefined) === ours[key])) return
  void router.replace({ query: { ...current, ...ours } })
}, { immediate: true })

const result = ref<Schemas['TrafficStats'] | null>(null)
const loading = ref(false)
function params() {
  return { view: props.view, level: level.value, ...bounds.value, users: users.value.join(','), stacks: stacks.value.join(','), compare: compare.value }
}
let version = 0
async function load() {
  const id = ++version
  loading.value = true
  const { data, error } = await api.GET('/api/traffic/stats', { params: { query: params() } })
  if (id !== version) return
  loading.value = false
  if (!data) {
    result.value = null
    message.error(problemMessage(error, '统计查询失败'))
    return
  }
  result.value = data
}
watch([level, range, users, stacks, compare], load, { immediate: true })

async function exportCSV() {
  const { data, error, response } = await api.GET('/api/traffic/stats/export', { params: { query: params() }, parseAs: 'blob' })
  if (error || !data) {
    message.error(problemMessage(error, '导出失败'))
    return
  }
  const filename = /filename="([^"]+)"/.exec(response.headers.get('Content-Disposition') ?? '')?.[1] ?? 'traffic.csv'
  saveBlob(data as unknown as Blob, filename)
}

const levelName = computed(() => ({ hour: '小时', day: '日', month: '月', year: '年' })[level.value])
const filteredByUsers = computed(() => props.view === 'stack' && users.value.length > 0)
const rows = computed(() => (result.value?.rows ?? []).filter((row) => showZero.value || totalOf(row) > 0))
const hiddenZero = computed(() => (result.value?.rows.length ?? 0) - rows.value.length)
const total = computed(() => result.value?.total)
const totalChange = computed(() => total.value ? compareChange(totalOf(total.value), total.value.previous ? totalOf(total.value.previous) : undefined) : null)
const unavailable = computed(() => {
  const from = result.value?.available_from
  return from && Date.parse(from) > 0 ? from : null
})
const excludedNames = computed(() => (result.value?.rows ?? []).filter((row) => row.excluded).map((row) => row.name))

/** 只筛选了一个对象时，图表按它的明细拆分（例如一个用户在各 stack 的用量）。 */
const single = computed(() => result.value?.series_detail && result.value.rows.length === 1 ? result.value.rows[0]! : null)
const chartSeries = computed(() => single.value ? statsSeries(single.value.children) : statsSeries(rows.value))
const chartLabels = computed(() => result.value ? bucketLabels(result.value.buckets, level.value, result.value.timezone, level.value === 'hour' && result.value.buckets.length > 24) : [])
const areas = computed(() => result.value ? chartAreas(result.value.buckets, result.value.to, result.value.available_from, result.value.gaps) : [])
const chartTitle = computed(() => {
  if (single.value) return props.view === 'user' ? `${single.value.name} 在各 Stack 的用量` : `${single.value.name} 的各用户用量`
  return props.view === 'user' ? '各用户用量趋势' : '各 Stack 用量趋势'
})

const changeTag = (current: number, previous?: { up: number; down: number } | null) => {
  const change = compareChange(current, previous ? totalOf(previous) : undefined)
  if (change.trend === 'none') return null
  return h(NText, { depth: change.trend === 'flat' ? 3 : 1, type: change.trend === 'up' || change.trend === 'new' ? 'warning' : change.trend === 'down' ? 'success' : 'default' }, () => change.text)
}

function nameCell(row: Row): VNodeChild {
  const linkable = (row.kind === 'user' || row.kind === 'stack') && !row.deleted
  const name = linkable ? h(EntityLink, { kind: row.kind as 'user' | 'stack', id: row.id, name: row.name }) : h(NText, { depth: row.kind === 'relay' || row.kind === 'shared' || row.kind === 'unknown' ? 2 : 1 }, () => row.name)
  const tags: VNodeChild[] = []
  if (row.deleted) tags.push(h(NTag, { size: 'small', bordered: false }, () => '已删除'))
  if (row.role === 'auto') tags.push(h(NTag, { size: 'small', bordered: false, type: 'info' }, () => 'auto'))
  if (row.excluded) tags.push(h(NTooltip, null, { trigger: () => h(NTag, { size: 'small', bordered: false }, () => '不计入合计'), default: () => 'auto stack 的流量经 edge stack 的 relay 入站出去，已在 edge stack 计入' }))
  return tags.length ? h(NSpace, { size: 6, align: 'center', wrap: false }, () => [name, ...tags]) : name
}

const breakdownNote: Record<string, string> = {
  relay: 'edge stack 中未归属到用户、经 relay 入站进入的流量，主要是 auto stack 转发过来的用户流量',
  shared: '其余未归属到用户的流量，例如 SOCKS5 / HTTP 共享账号',
  unknown: '无法对应到现有用户的计数，例如用户删除前尚未采集完的部分',
}

function breakdown(title: string, items: Row[], parent: number): VNodeChild {
  if (!items.length) return null
  return h('div', { class: 'breakdown' }, [
    h(NText, { strong: true }, () => title),
    h('table', { class: 'breakdown-table' }, [
      h('tbody', items.map((item) => h('tr', { key: `${item.kind}:${item.id}` }, [
        h('td', { class: 'breakdown-name' }, [nameCell(item), breakdownNote[item.kind] ? h('div', h(NText, { depth: 3, style: 'font-size: 12px' }, () => breakdownNote[item.kind])) : null]),
        h('td', `↑ ${formatBytes(item.up)}`),
        h('td', `↓ ${formatBytes(item.down)}`),
        h('td', { class: 'breakdown-total' }, [
          h('div', { class: 'bar-cell' }, [
            h('span', { class: 'bar', style: { width: `${parent > 0 ? (totalOf(item) / parent) * 60 : 0}%` } }),
            h(NText, null, () => formatBytes(totalOf(item))),
          ]),
        ]),
      ]))),
    ]),
  ])
}

const columns = computed<DataTableColumns<Row>>(() => [
  {
    type: 'expand',
    expandable: (row) => row.children.length > 0 || row.profiles.length > 0,
    renderExpand: (row) => h('div', { class: 'breakdowns' }, props.view === 'user'
      ? [breakdown('各 Stack', row.children, totalOf(row)), breakdown('各凭据档案', row.profiles, totalOf(row))]
      : [breakdown(filteredByUsers.value ? '所选用户' : '各用户及未归属流量', row.children, totalOf(row))]),
  },
  { title: props.view === 'user' ? '用户' : 'Stack', key: 'name', minWidth: 180, render: nameCell },
  { title: '上行', key: 'up', minWidth: 100, sorter: (a, b) => a.up - b.up, render: (row) => formatBytes(row.up) },
  { title: '下行', key: 'down', minWidth: 100, sorter: (a, b) => a.down - b.down, render: (row) => formatBytes(row.down) },
  { title: '合计', key: 'total', minWidth: 110, defaultSortOrder: 'descend', sorter: (a, b) => totalOf(a) - totalOf(b), render: (row) => h(NText, { strong: true }, () => formatBytes(totalOf(row))) },
  ...(compare.value ? [{
    title: '环比', key: 'previous', minWidth: 120,
    render: (row: Row) => h('div', [changeTag(totalOf(row), row.previous), h('div', h(NText, { depth: 3, style: 'font-size: 12px' }, () => `上期 ${formatBytes(row.previous ? totalOf(row.previous) : 0)}`))]),
  }] : []),
  ...(result.value?.peak_available ? [{
    title: '峰值速率', key: 'peak', minWidth: 110, sorter: (a: Row, b: Row) => (a.peak ?? 0) - (b.peak ?? 0),
    render: (row: Row) => row.peak === undefined ? '—' : formatRate(row.peak),
  }] : []),
])

const summary = (): Record<string, { value: VNodeChild }> => {
  const row = total.value
  if (!row) return {}
  return {
    name: { value: h(NText, { strong: true }, () => props.view === 'stack' && !filteredByUsers.value && excludedNames.value.length ? '合计（不含 auto）' : '合计') },
    up: { value: formatBytes(row.up) },
    down: { value: formatBytes(row.down) },
    total: { value: h(NText, { strong: true }, () => formatBytes(totalOf(row))) },
    previous: { value: changeTag(totalOf(row), row.previous) },
    peak: { value: row.peak === undefined ? '—' : formatRate(row.peak) },
  }
}
</script>

<template>
  <NSpace vertical :size="20">
    <NCard size="small">
      <div class="stats-filters">
        <span class="filter-label">时间</span>
        <div class="preset-list" role="group" aria-label="快捷时间段">
          <NButton
            v-for="preset in trafficPresets" :key="preset.value" size="small"
            :type="activePreset === preset.value ? 'primary' : 'default'" :secondary="activePreset === preset.value" :quaternary="activePreset !== preset.value"
            :aria-pressed="activePreset === preset.value" @click="applyPreset(preset.value)"
          >{{ preset.label }}</NButton>
        </div>
        <span class="filter-label">范围</span>
        <div class="range-row">
          <div class="range-picker">
            <NButton size="small" secondary aria-label="前一段" title="前一段" @click="shift(-1)">‹</NButton>
            <NDatePicker
              v-model:formatted-value="range" :type="pickerType" value-format="yyyy-MM-dd HH:mm:ss"
              :format="level === 'hour' ? 'yyyy-MM-dd HH:00' : undefined" size="small" :clearable="false" :class="{ 'wide-picker': level === 'hour' }"
            />
            <NButton size="small" secondary aria-label="后一段" title="后一段" :disabled="!canShiftForward" @click="shift(1)">›</NButton>
          </div>
          <NText depth="3" class="span-label">{{ spanLabel }}<template v-if="!activePreset"> · 自定义</template></NText>
        </div>
        <span class="filter-label">粒度</span>
        <div class="range-row">
          <NRadioGroup v-model:value="level" size="small" aria-label="统计粒度">
            <NRadioButton value="hour">小时</NRadioButton>
            <NRadioButton value="day">日</NRadioButton>
            <NRadioButton value="month">月</NRadioButton>
            <NRadioButton value="year">年</NRadioButton>
          </NRadioGroup>
          <NText depth="3" class="span-label">切换粒度时保留所选时间段</NText>
        </div>
        <span class="filter-label">筛选</span>
        <div class="object-row">
          <NSelect v-model:value="users" :options="userOptions" multiple clearable filterable max-tag-count="responsive" size="small" placeholder="全部用户" />
          <NSelect v-model:value="stacks" :options="stackOptions" multiple clearable filterable max-tag-count="responsive" size="small" placeholder="全部 Stack" />
        </div>
        <div class="stats-switches">
          <label><NSwitch v-model:value="compare" size="small" /> 对比上一期</label>
          <label><NSwitch v-model:value="showZero" size="small" /> 显示零用量</label>
          <NButton size="small" @click="exportCSV">导出 CSV</NButton>
        </div>
      </div>
    </NCard>

    <NText depth="3" class="stats-note">
      统计范围：{{ rangeLabel }}，按完整的{{ levelName }}汇总，当前周期统计至最近一次采集。
      <template v-if="view === 'user'">按用户计数，合并同一用户的全部凭据档案；用户在哪个 stack 接入，流量就记在哪个 stack。</template>
      <template v-else-if="filteredByUsers">已筛选用户：按这些用户的计数统计，各 stack 之间没有重复，合计包含 auto stack。</template>
      <template v-else>按各 stack 的入站计数。<template v-if="excludedNames.length">合计不含 auto stack {{ excludedNames.join('、') }}：它们的流量经 edge stack 的 relay 入站出去，已计入 edge stack。</template>展开一行可查看各用户和未归属到用户的流量。</template>
    </NText>

    <NAlert v-if="unavailable && result" type="warning" :bordered="false">
      {{ levelName }}明细只保留 {{ result.retention_days }} 天，{{ formatTime(unavailable) }} 之前没有{{ levelName }}数据，图中以灰色标出，不代表没有流量。
      <NButton v-if="coarserLevel(level)" size="tiny" secondary type="warning" style="margin-left: 8px" @click="switchCoarser">改为按{{ ({ hour: '日', day: '月', month: '年', year: '' } as const)[level] }}查看</NButton>
    </NAlert>
    <GapNotice v-if="result" :gaps="result.gaps" />

    <NGrid cols="2 m:4" responsive="screen" :x-gap="20" :y-gap="20">
      <NGridItem>
        <NCard size="small" class="metric-card">
          <NStatistic :label="view === 'stack' && !filteredByUsers && excludedNames.length ? '合计（不含 auto）' : '合计'" :value="formatBytes(total ? totalOf(total) : 0)" />
          <NText v-if="totalChange && totalChange.trend !== 'none'" depth="3" class="metric-note">比上一期 {{ totalChange.text }}（上期 {{ formatBytes(total?.previous ? totalOf(total.previous) : 0) }}）</NText>
        </NCard>
      </NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="上行" :value="formatBytes(total?.up ?? 0)" /></NCard></NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="下行" :value="formatBytes(total?.down ?? 0)" /></NCard></NGridItem>
      <NGridItem>
        <NCard size="small" class="metric-card">
          <NStatistic label="峰值速率" :value="total?.peak !== undefined ? formatRate(total.peak) : '—'" />
          <NText v-if="result && !result.peak_available" depth="3" class="metric-note">峰值来自分钟明细，只覆盖其保留期内的范围</NText>
        </NCard>
      </NGridItem>
    </NGrid>

    <NCard :title="chartTitle" size="small">
      <TrafficChart v-if="result && chartSeries.length" kind="bar" :labels="chartLabels" :series="chartSeries" :format="formatBytes" :areas="areas" height="320px" />
      <NEmpty v-else :description="loading ? '正在加载' : '所选范围内没有用量'" />
    </NCard>

    <NCard :title="view === 'user' ? '各用户明细' : '各 Stack 明细'" size="small">
      <NText v-if="hiddenZero > 0" depth="3" class="hidden-zero">另有 {{ hiddenZero }} 个零用量的{{ view === 'user' ? '用户' : ' Stack' }}未显示，打开「显示零用量」查看。</NText>
      <NDataTable
        :columns="columns" :data="rows" :loading="loading" :summary="summary" summary-placement="top"
        :row-key="(row: Row) => `${row.kind}:${row.id}`" children-key="__none" :pagination="{ pageSize: 20 }" :scroll-x="760" size="small"
      />
    </NCard>
  </NSpace>
</template>

<style scoped>
.stats-filters { display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: center; gap: 12px 16px; }
.filter-label { font-size: 13px; font-weight: 600; color: var(--ps-muted); white-space: nowrap; }
.preset-list { display: flex; flex-wrap: wrap; gap: 4px 6px; }
.range-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 16px; }
.range-picker { display: flex; align-items: center; gap: 6px; }
.range-picker > .n-date-picker { width: 280px; }
.range-picker > .n-date-picker.wide-picker { width: 340px; }
.span-label { font-size: 12px; white-space: nowrap; }
.object-row { display: flex; flex-wrap: wrap; gap: 8px 12px; }
.object-row > .n-select { width: 240px; }
.stats-switches { grid-column: 1 / -1; display: flex; flex-wrap: wrap; align-items: center; gap: 8px 20px; padding-top: 4px; border-top: 1px solid var(--ps-border); }
.stats-switches label { display: inline-flex; align-items: center; gap: 6px; font-size: 13px; cursor: pointer; }
.stats-switches > :last-child { margin-left: auto; }
.stats-note { display: block; font-size: 12px; margin-top: -8px; }
.metric-note { display: block; margin-top: 4px; font-size: 12px; }
.hidden-zero { display: block; font-size: 12px; margin-bottom: 8px; }
/* 展开行由表格组件渲染，不带本组件的作用域标记。 */
:deep(.breakdowns) { display: grid; gap: 12px; padding: 4px 0 4px 8px; }
:deep(.breakdown-table) { width: 100%; border-collapse: collapse; margin-top: 4px; font-size: 13px; }
:deep(.breakdown-table td) { padding: 4px 12px 4px 0; vertical-align: top; white-space: nowrap; }
:deep(.breakdown-table td.breakdown-name) { white-space: normal; min-width: 160px; }
:deep(.breakdown-total) { width: 40%; }
:deep(.bar-cell) { display: flex; align-items: center; gap: 8px; }
:deep(.bar) { height: 8px; min-width: 2px; border-radius: 4px; background: var(--ps-accent); }
@media (max-width: 700px) {
  .stats-filters { grid-template-columns: minmax(0, 1fr); gap: 6px; }
  .filter-label { margin-top: 6px; }
  .range-picker { width: 100%; }
  .range-picker > .n-date-picker, .range-picker > .n-date-picker.wide-picker { flex: 1; width: auto; min-width: 0; }
  .object-row > .n-select { width: 100%; }
  .stats-switches > :last-child { margin-left: 0; }
}
</style>
