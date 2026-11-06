<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NDataTable, NEmpty, NGrid, NGridItem, NRadioButton, NRadioGroup, NSelect, NSpace, NStatistic, NText, type DataTableColumns } from 'naive-ui'
import { RouterLink } from 'vue-router'
import { api, problemMessage, type Schemas } from '../../api/client'
import TrafficChart from '../../components/TrafficChart.vue'
import EntityLink from '../../components/EntityLink.vue'
import { usePolling } from '../../composables/usePolling'
import { useTrafficTrend } from '../../composables/useTrafficTrend'
import { formatBytes, formatTime } from '../../util/format'

const props = defineProps<{ stackId: number; users: Schemas['User'][] }>()
const userId = ref<number | null>(null)
const userOptions = computed(() => props.users.map((user) => ({ label: user.remark ? `${user.name}（${user.remark}）` : user.name, value: user.id! })))
const { data: usage, loading, error, updatedAt, refresh } = usePolling<Schemas['StackUsage']>(() => [props.stackId, userId.value], async (signal) => {
  const { data, error } = await api.GET('/api/stacks/{id}/usage', { params: { path: { id: props.stackId }, query: { user_id: userId.value ?? 0 } }, signal })
  if (!data) throw new Error(problemMessage(error, '用量刷新失败'))
  return data
})
const total = (bytes: { up: number; down: number }) => bytes.up + bytes.down
const totals = computed(() => usage.value ? [
  { label: '今日用量', bytes: usage.value.today },
  { label: '本月用量', bytes: usage.value.month },
  { label: '近 30 天用量', bytes: usage.value.recent },
] : [])

/** 未归属到用户的流量排在各用户之后：经 relay 入站（主要是 auto stack 转发）和共享账号等，ID 用负数区分。 */
const UNATTRIBUTED: Record<string, { id: number; note: string }> = {
  relay: { id: -1, note: '未归属到用户、经 relay 入站进入的流量，主要是 auto stack 转发过来的用户流量' },
  shared: { id: -2, note: 'SOCKS5 / HTTP 共享账号等无法归属到用户的流量' },
}
const notes = new Map(Object.values(UNATTRIBUTED).map((item) => [item.id, item.note]))
const userRows = computed<Schemas['StackUserUsage'][]>(() => {
  const data = usage.value
  if (!data) return []
  return [...data.by_user, ...data.unattributed.map((part) => ({ user_id: UNATTRIBUTED[part.kind]!.id, user: part.name, today: part.today, month: part.month, recent: part.recent }))]
})

/** 趋势：近 30 天按天或最近若干小时按小时，按用户拆分；指定用户时只显示该用户。 */
const level = ref<'day' | 'hour'>('day')
const hours = ref(24)
const hourOptions = [{ label: '24 小时', value: 24 }, { label: '48 小时', value: 48 }, { label: '7 天', value: 168 }]
const { trend, error: trendError, load: loadTrend } = useTrafficTrend(() => ({
  level: level.value, span: level.value === 'hour' ? hours.value : 30, view: 'user',
  stacks: [props.stackId], users: userId.value ? [userId.value] : [],
}))
watch(usage, () => void loadTrend())
const trafficLink = computed(() => ({
  name: 'traffic',
  query: userId.value
    ? { level: level.value, users: String(userId.value), stacks: String(props.stackId) }
    : { tab: 'stack', level: level.value, stacks: String(props.stackId) },
}))

const userColumns: DataTableColumns<Schemas['StackUserUsage']> = [
  {
    title: '用户', key: 'user', minWidth: 160,
    render: (row) => notes.has(row.user_id)
      ? h('div', [h(NText, { depth: 2 }, () => row.user), h('div', [h(NText, { depth: 3, style: 'font-size: 12px' }, () => notes.get(row.user_id))])])
      : row.user_id > 0 ? h(EntityLink, { kind: 'user', id: row.user_id, name: row.user }) : row.user,
  },
  ...(['today', 'month', 'recent'] as const).map((period, index) => ({
    title: ['今日用量', '本月用量', '近 30 天用量'][index], key: period, minWidth: 200,
    sorter: (a: Schemas['StackUserUsage'], b: Schemas['StackUserUsage']) => total(a[period]) - total(b[period]),
    render: (row: Schemas['StackUserUsage']) => h('div', [
      h(NText, { strong: true }, () => formatBytes(total(row[period]))),
      h('div', [h(NText, { depth: 3 }, () => `上行 ${formatBytes(row[period].up)} / 下行 ${formatBytes(row[period].down)}`)]),
    ]),
  })),
]
</script>

<template>
  <NSpace vertical :size="20">
    <NCard size="small">
      <NSpace justify="space-between" align="center">
        <NSpace align="center">
          <NText>统计用户</NText>
          <NSelect v-model:value="userId" :options="userOptions" clearable filterable placeholder="全部用户" style="width: 240px" />
          <EntityLink v-if="userId" kind="user" :id="userId" label="查看用户" />
        </NSpace>
        <NSpace align="center" :size="8">
          <NText depth="3">每 10 秒更新<template v-if="updatedAt"> · {{ formatTime(updatedAt) }}</template></NText>
          <NButton size="small" :loading="loading" @click="refresh">刷新</NButton>
        </NSpace>
      </NSpace>
      <NText depth="3" style="display: block; margin-top: 8px">
        仅统计当前 Stack。全部用户按入站流量统计；指定用户时合并该用户全部凭据档案的流量，共享认证入站的流量可能无法归属到用户。
      </NText>
    </NCard>
    <NAlert v-if="error" type="warning" :bordered="false">{{ error }}<template v-if="usage">；当前显示上次成功获取的数据</template></NAlert>
    <template v-if="usage">
      <NAlert v-if="!usage.enabled" type="info" :bordered="false">流量监控已关闭，当前显示历史统计。开启流量监控后恢复采集。</NAlert>
      <NAlert v-else-if="usage.collection.error" type="warning" :bordered="false">采集异常：{{ usage.collection.error }}</NAlert>
      <NGrid cols="1 s:3" responsive="screen" :x-gap="20" :y-gap="20">
        <NGridItem v-for="item in totals" :key="item.label">
          <NCard size="small" class="metric-card">
            <NStatistic :label="item.label" :value="formatBytes(total(item.bytes))" />
            <NText depth="3">上行 {{ formatBytes(item.bytes.up) }} / 下行 {{ formatBytes(item.bytes.down) }}</NText>
          </NCard>
        </NGridItem>
      </NGrid>
      <NCard v-if="!userId" title="各用户用量明细" size="small">
        <NText depth="3" style="display: block; margin-bottom: 12px">仅显示本 Stack 在这些时间段内有统计记录的用户，合并同一用户的全部凭据档案；历史已删除用户保留记录。入站总量中未归属到用户的部分列在最后，分为「经 relay 入站」（主要是 auto stack 转发过来的用户流量，这些用户已在 auto stack 计入）和「共享账号等」。</NText>
        <NDataTable :columns="userColumns" :data="userRows" :row-key="(row: Schemas['StackUserUsage']) => row.user_id" :pagination="{ pageSize: 10 }" :scroll-x="760" size="small" />
      </NCard>
      <NCard title="用量趋势" size="small">
        <template #header-extra>
          <RouterLink :to="trafficLink">在流量明细中查看</RouterLink>
        </template>
        <div class="trend-controls">
          <NRadioGroup v-model:value="level" size="small">
            <NRadioButton value="day">近 30 天</NRadioButton>
            <NRadioButton value="hour">按小时</NRadioButton>
          </NRadioGroup>
          <NRadioGroup v-if="level === 'hour'" v-model:value="hours" size="small">
            <NRadioButton v-for="option in hourOptions" :key="option.value" :value="option.value">{{ option.label }}</NRadioButton>
          </NRadioGroup>
        </div>
        <NAlert v-if="trendError" type="warning" :bordered="false" style="margin-bottom: 8px">{{ trendError }}</NAlert>
        <TrafficChart v-if="trend && !trend.empty" kind="bar" :labels="trend.labels" :series="trend.series" :format="formatBytes" height="280px" />
        <NEmpty v-else-if="trend" :description="level === 'day' ? '最近 30 天没有用量' : '这段时间没有用量'" />
        <NEmpty v-else-if="!trendError" description="正在加载趋势" />
        <NText depth="3" style="display: block; margin-top: 8px">
          按系统设置时区的{{ level === 'day' ? '自然日' : '整点' }}汇总，当前周期统计至最近一次写入；历史明细受流量保留策略影响。<template v-if="!userId">按用户拆分，不含未归属到用户的流量。</template>
        </NText>
      </NCard>
    </template>
    <NEmpty v-else :description="loading ? '正在加载用量' : '暂无用量数据'" />
  </NSpace>
</template>

<style scoped>
.trend-controls { display: flex; flex-wrap: wrap; gap: 8px 12px; margin-bottom: 12px; }
</style>
