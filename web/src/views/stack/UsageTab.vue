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

/** 入站总量减去各用户之和：共享账号、被聚合 stack 经 relay 转发等无法归属到用户的流量。 */
const UNATTRIBUTED = 0
const userRows = computed<Schemas['StackUserUsage'][]>(() => {
  const data = usage.value
  if (!data) return []
  const gap = (period: 'today' | 'month' | 'recent') => {
    const users = data.by_user.reduce((sum, row) => ({ up: sum.up + row[period].up, down: sum.down + row[period].down }), { up: 0, down: 0 })
    return { up: Math.max(0, data[period].up - users.up), down: Math.max(0, data[period].down - users.down) }
  }
  const rest = { user_id: UNATTRIBUTED, user: '未归属到用户', today: gap('today'), month: gap('month'), recent: gap('recent') }
  return total(rest.today) + total(rest.month) + total(rest.recent) > 0 ? [...data.by_user, rest] : data.by_user
})

/** 趋势：近 30 天按天或最近若干小时按小时；全部用户时按用户或按入站拆分，指定用户时只显示该用户。 */
const level = ref<'day' | 'hour'>('day')
const hours = ref(24)
const hourOptions = [{ label: '24 小时', value: 24 }, { label: '48 小时', value: 48 }, { label: '7 天', value: 168 }]
const groupBy = ref<'user' | 'inbound'>('user')
const trendScope = computed(() => (userId.value || groupBy.value === 'user' ? 'user' : 'inbound'))
const { trend, error: trendError, load: loadTrend } = useTrafficTrend(() => ({
  level: level.value, span: level.value === 'hour' ? hours.value : 30, scope: trendScope.value, groupBy: 'subject',
  stacks: [props.stackId], subjects: userId.value ? [String(userId.value)] : [], entity: trendScope.value === 'user' ? 'user' : undefined,
}))
watch(usage, () => void loadTrend())
const trafficLink = computed(() => ({
  name: 'traffic',
  query: { level: level.value, scope: trendScope.value, group_by: 'subject', stacks: String(props.stackId), ...(userId.value ? { subjects: String(userId.value) } : {}) },
}))

const userColumns: DataTableColumns<Schemas['StackUserUsage']> = [
  {
    title: '用户', key: 'user', minWidth: 160,
    render: (row) => row.user_id === UNATTRIBUTED
      ? h('div', [h(NText, { depth: 2 }, () => row.user), h('div', [h(NText, { depth: 3, style: 'font-size: 12px' }, () => '共享账号、relay 转发等')])])
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
        <NText depth="3" style="display: block; margin-bottom: 12px">仅显示本 Stack 在这些时间段内有统计记录的用户，合并同一用户的全部凭据档案；历史已删除用户保留记录。「未归属到用户」是入站总量减去各用户之和，来自共享账号的 SOCKS5 / HTTP 入站，或聚合 Stack 经本 Stack relay 入站转发的流量。</NText>
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
          <NRadioGroup v-if="!userId" v-model:value="groupBy" size="small">
            <NRadioButton value="user">按用户</NRadioButton>
            <NRadioButton value="inbound">按入站</NRadioButton>
          </NRadioGroup>
        </div>
        <NAlert v-if="trendError" type="warning" :bordered="false" style="margin-bottom: 8px">{{ trendError }}</NAlert>
        <TrafficChart v-if="trend && !trend.empty" kind="bar" :labels="trend.labels" :series="trend.series" :format="formatBytes" height="280px" />
        <NEmpty v-else-if="trend" :description="level === 'day' ? '最近 30 天没有用量' : '这段时间没有用量'" />
        <NEmpty v-else-if="!trendError" description="正在加载趋势" />
        <NText depth="3" style="display: block; margin-top: 8px">
          按系统设置时区的{{ level === 'day' ? '自然日' : '整点' }}汇总，当前周期统计至最近一次写入；历史明细受流量保留策略影响。<template v-if="!userId && groupBy === 'user'">按用户拆分时不含未归属到用户的流量。</template>
        </NText>
      </NCard>
    </template>
    <NEmpty v-else :description="loading ? '正在加载用量' : '暂无用量数据'" />
  </NSpace>
</template>

<style scoped>
.trend-controls { display: flex; flex-wrap: wrap; gap: 8px 12px; margin-bottom: 12px; }
</style>
