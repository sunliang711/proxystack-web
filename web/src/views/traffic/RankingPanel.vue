<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NCard, NDataTable, NDatePicker, NGrid, NGridItem, NRadioButton, NRadioGroup, NSelect, NSpace, NStatistic, NSwitch, NTag, NText, useMessage, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../../api/client'
import EntityLink from '../../components/EntityLink.vue'
import GapNotice from './GapNotice.vue'
import { formatBytes } from '../../util/format'
import { presetBounds, recentDayRange, trafficBounds, trafficRangeLabel, type RankingPreset, type TrafficRange } from '../../util/traffic-range'
import { compareChange, rankBy, totalOf, type Bytes } from '../../util/traffic-stats'

type Row = Schemas['TrafficRankRow']
type Sort = 'total' | 'up' | 'down'

const props = defineProps<{ timezone: string; stackOptions: { label: string; value: number }[] }>()
const message = useMessage()
const route = useRoute()
const router = useRouter()

// 排行条件用 r_ 前缀记在地址栏，与按用户、按 Stack 的条件互不影响。
const linked = route.query
const pick = <T extends string>(value: unknown, allowed: readonly T[], fallback: T): T => ((allowed as readonly unknown[]).includes(value) ? (value as T) : fallback)
const dateParam = /^\d{4}-\d{2}-\d{2}$/
const period = ref<RankingPreset | 'custom'>(pick(linked.r_period, ['today', 'yesterday', 'month', 'last_month', 'custom'], 'today'))
const custom = ref<TrafficRange>(
  typeof linked.r_from === 'string' && typeof linked.r_to === 'string' && dateParam.test(linked.r_from) && dateParam.test(linked.r_to) && linked.r_from <= linked.r_to
    ? [`${linked.r_from} 00:00:00`, `${linked.r_to} 00:00:00`]
    : recentDayRange(7, props.timezone),
)
const stackParam = Number(linked.r_stack)
const stack = ref<number | null>(Number.isInteger(stackParam) && stackParam > 0 ? stackParam : null)
const sort = ref<Sort>(pick(linked.r_sort, ['total', 'up', 'down'], 'total'))
const compare = ref(linked.r_compare !== '0')
const showIdle = ref(linked.r_idle === '1')
const bounds = computed(() => period.value === 'custom' ? trafficBounds('day', custom.value, props.timezone) : presetBounds(period.value, props.timezone))

watch([period, custom, stack, sort, compare, showIdle], () => {
  const ours = {
    r_period: period.value === 'today' ? undefined : period.value,
    r_from: period.value === 'custom' ? custom.value[0].slice(0, 10) : undefined,
    r_to: period.value === 'custom' ? custom.value[1].slice(0, 10) : undefined,
    r_stack: stack.value ? String(stack.value) : undefined,
    r_sort: sort.value === 'total' ? undefined : sort.value,
    r_compare: compare.value ? undefined : '0',
    r_idle: showIdle.value ? '1' : undefined,
  }
  const current = route.query
  if ((Object.keys(ours) as (keyof typeof ours)[]).every((key) => (current[key] ?? undefined) === ours[key])) return
  void router.replace({ query: { ...current, ...ours } })
}, { immediate: true })

const result = ref<Schemas['TrafficRanking'] | null>(null)
const loading = ref(false)
let version = 0
async function load() {
  const id = ++version
  loading.value = true
  const { data, error } = await api.GET('/api/traffic/ranking', { params: { query: { ...bounds.value, stack: stack.value ?? 0, compare: compare.value } } })
  if (id !== version) return
  loading.value = false
  if (!data) {
    result.value = null
    message.error(problemMessage(error, '排行查询失败'))
    return
  }
  result.value = data
}
watch([bounds, stack, compare], load, { immediate: true })

const valueOf = (bytes: Bytes | undefined | null) => !bytes ? 0 : sort.value === 'total' ? totalOf(bytes) : bytes[sort.value]
const sortName = computed(() => ({ total: '用量', up: '上行', down: '下行' })[sort.value])
const allRows = computed(() => [...(result.value?.rows ?? [])].sort((a, b) => valueOf(b) - valueOf(a) || a.name.localeCompare(b.name)))
const ranks = computed(() => rankBy(allRows.value, (row) => valueOf(row)))
const previousRanks = computed(() => rankBy(allRows.value, (row) => valueOf(row.previous)))
const rows = computed(() => allRows.value.filter((row) => showIdle.value || valueOf(row) > 0))
const idleCount = computed(() => allRows.value.length - rows.value.length)
const peak = computed(() => Math.max(1, ...rows.value.map((row) => valueOf(row))))
const rangeLabel = computed(() => trafficRangeLabel(bounds.value, props.timezone))
const previousLabel = computed(() => {
  const data = result.value
  if (!data?.previous) return ''
  return trafficRangeLabel({ from: Date.parse(data.previous_from) / 1000, to: Date.parse(data.previous_to) / 1000 }, props.timezone)
})
const totalChange = computed(() => compareChange(valueOf(result.value?.total), result.value?.previous ? valueOf(result.value.previous) : undefined))

function rankMove(row: Row) {
  const now = ranks.value.get(row)
  if (!compare.value || !now) return null
  const before = previousRanks.value.get(row)
  if (!before) return h(NTag, { size: 'small', bordered: false, type: 'warning' }, () => '新上榜')
  if (before === now) return h(NText, { depth: 3 }, () => '—')
  return h(NText, { type: before > now ? 'warning' : 'success' }, () => (before > now ? `↑ ${before - now}` : `↓ ${now - before}`))
}

const columns = computed<DataTableColumns<Row>>(() => [
  { title: '名次', key: 'rank', width: 64, render: (row) => ranks.value.get(row) ?? '—' },
  {
    title: '用户', key: 'name', minWidth: 160,
    render: (row) => row.deleted
      ? h(NSpace, { size: 6, align: 'center', wrap: false }, () => [h(NText, null, () => row.name), h(NTag, { size: 'small', bordered: false }, () => '已删除')])
      : h(EntityLink, { kind: 'user', id: row.user_id, name: row.name }),
  },
  { title: '上行', key: 'up', minWidth: 100, render: (row) => formatBytes(row.up) },
  { title: '下行', key: 'down', minWidth: 100, render: (row) => formatBytes(row.down) },
  {
    title: '合计', key: 'total', minWidth: 110,
    render: (row) => h(NText, { strong: sort.value === 'total' }, () => formatBytes(totalOf(row))),
  },
  {
    title: () => `${sortName.value}占比`, key: 'bar', minWidth: 160,
    render: (row) => h('div', { class: 'bar-cell' }, [h('span', { class: 'bar', style: { width: `${(valueOf(row) / peak.value) * 70}%` } })]),
  },
  ...(compare.value ? [
    {
      title: '环比', key: 'change', minWidth: 110,
      render: (row: Row) => {
        const change = compareChange(valueOf(row), valueOf(row.previous))
        return h('div', [
          h(NText, { type: change.trend === 'up' || change.trend === 'new' ? 'warning' : change.trend === 'down' ? 'success' : 'default', depth: change.trend === 'flat' ? 3 : 1 }, () => change.text),
          h('div', h(NText, { depth: 3, style: 'font-size: 12px' }, () => `上期 ${formatBytes(valueOf(row.previous))}`)),
        ])
      },
    },
    { title: '名次变化', key: 'move', minWidth: 90, render: rankMove },
  ] : []),
])
</script>

<template>
  <NSpace vertical :size="20">
    <NCard size="small">
      <div class="ranking-filters">
        <NRadioGroup v-model:value="period" size="small" aria-label="排行时间段">
          <NRadioButton value="today">今日</NRadioButton>
          <NRadioButton value="yesterday">昨日</NRadioButton>
          <NRadioButton value="month">本月</NRadioButton>
          <NRadioButton value="last_month">上月</NRadioButton>
          <NRadioButton value="custom">自定义</NRadioButton>
        </NRadioGroup>
        <NDatePicker v-if="period === 'custom'" v-model:formatted-value="custom" type="daterange" value-format="yyyy-MM-dd HH:mm:ss" size="small" :clearable="false" />
        <NSelect v-model:value="stack" :options="stackOptions" clearable filterable size="small" placeholder="全部 Stack" />
        <NRadioGroup v-model:value="sort" size="small" aria-label="排序依据">
          <NRadioButton value="total">按合计</NRadioButton>
          <NRadioButton value="up">按上行</NRadioButton>
          <NRadioButton value="down">按下行</NRadioButton>
        </NRadioGroup>
        <div class="ranking-switches">
          <label><NSwitch v-model:value="compare" size="small" /> 对比上一期</label>
          <label><NSwitch v-model:value="showIdle" size="small" /> 显示零用量用户</label>
        </div>
      </div>
    </NCard>
    <NText depth="3" class="ranking-note">
      统计范围：{{ rangeLabel }}<template v-if="previousLabel">；上一期：{{ previousLabel }}（当前周期未结束时取同等的已过时长）</template>。
      合并同一用户的全部凭据档案<template v-if="stack">，只统计所选 Stack</template>；无法归属到用户的流量（共享账号、relay 转发等）不参与排行。
    </NText>
    <GapNotice v-if="result" :gaps="result.gaps" />
    <NGrid cols="2 m:3" responsive="screen" :x-gap="20" :y-gap="20">
      <NGridItem>
        <NCard size="small" class="metric-card">
          <NStatistic :label="`用户${sortName}合计`" :value="formatBytes(valueOf(result?.total))" />
          <NText v-if="totalChange.trend !== 'none'" depth="3" class="metric-note">比上一期 {{ totalChange.text }}</NText>
        </NCard>
      </NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="有用量的用户" :value="ranks.size" /></NCard></NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="零用量的用户" :value="allRows.length - ranks.size" /></NCard></NGridItem>
    </NGrid>
    <NCard :title="`用户${sortName}排行`" size="small">
      <NText v-if="idleCount > 0" depth="3" class="hidden-zero">另有 {{ idleCount }} 个零用量的用户未显示，打开「显示零用量用户」查看。</NText>
      <NDataTable :columns="columns" :data="rows" :loading="loading" :row-key="(row: Row) => row.user_id" :pagination="{ pageSize: 20 }" :scroll-x="820" size="small" />
    </NCard>
  </NSpace>
</template>

<style scoped>
.ranking-filters { display: flex; flex-wrap: wrap; align-items: center; gap: 12px 16px; }
.ranking-filters > .n-select { width: 220px; }
.ranking-filters > .n-date-picker { width: 280px; }
.ranking-switches { display: flex; flex-wrap: wrap; gap: 8px 20px; }
.ranking-switches label { display: inline-flex; align-items: center; gap: 6px; font-size: 13px; cursor: pointer; }
.ranking-note { display: block; font-size: 12px; margin-top: -8px; }
.metric-note { display: block; margin-top: 4px; font-size: 12px; }
.hidden-zero { display: block; font-size: 12px; margin-bottom: 8px; }
:deep(.bar-cell) { display: flex; align-items: center; }
:deep(.bar) { height: 8px; min-width: 2px; border-radius: 4px; background: var(--ps-accent); }
@media (max-width: 600px) {
  .ranking-filters > .n-select, .ranking-filters > .n-date-picker { width: 100%; }
}
</style>
