<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { NAlert, NButton, NCard, NDataTable, NEmpty, NGrid, NGridItem, NSelect, NSpace, NStatistic, NText, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../../api/client'
import TrafficChart from '../../components/TrafficChart.vue'
import EntityLink from '../../components/EntityLink.vue'
import { usePolling } from '../../composables/usePolling'
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
const days = computed(() => (usage.value?.daily ?? []).map((day) => day.day.slice(5)))
const series = computed(() => [{ name: '日用量', data: (usage.value?.daily ?? []).map(total), slot: 0 }])
const userColumns: DataTableColumns<Schemas['StackUserUsage']> = [
  { title: '用户', key: 'user', minWidth: 160, render: (row) => row.user_id > 0 ? h(EntityLink, { kind: 'user', id: row.user_id, name: row.user }) : row.user },
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
        <NText depth="3" style="display: block; margin-bottom: 12px">仅显示本 Stack 在这些时间段内有统计记录的用户，合并同一用户的全部凭据档案。共享认证或无法归属的入站流量不一定包含在用户明细中，因此各用户合计可能与上方入站总量不同；历史已删除用户保留记录。</NText>
        <NDataTable :columns="userColumns" :data="usage.by_user" :row-key="(row: Schemas['StackUserUsage']) => row.user_id" :pagination="{ pageSize: 10 }" :scroll-x="760" size="small" />
      </NCard>
      <NCard title="近 30 天用量趋势" size="small">
        <TrafficChart v-if="usage.daily.some((day) => total(day) > 0)" kind="bar" :labels="days" :series="series" :format="formatBytes" height="280px" />
        <NEmpty v-else description="最近 30 天没有用量" />
        <NText depth="3" style="display: block; margin-top: 8px">按系统设置时区的自然日统计，历史明细受流量保留策略影响。</NText>
      </NCard>
    </template>
    <NEmpty v-else :description="loading ? '正在加载用量' : '暂无用量数据'" />
  </NSpace>
</template>
