<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { NAlert, NButton, NCard, NEmpty, NGrid, NGridItem, NSelect, NSpace, NStatistic, NTable, NTag, NText, useMessage } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import TrafficChart from '../components/TrafficChart.vue'
import { onTrafficEvent } from '../runtime'
import { formatBytes, formatRate } from '../util/format'

/** 实时曲线保留的时长，与服务端一致。 */
const LIVE_WINDOW = 3600_000
/** 排行第一的条形占单元格的比例，其余按用量等比缩短，右侧留给数值。 */
const MAX_BAR = 70
/** 今日、本月合计的刷新间隔。 */
const REFRESH = 30_000

const overview = ref<Schemas['TrafficOverview'] | null>(null)
const live = ref<Schemas['LivePoint'][]>([])
const userId = ref<number | null>(null)
const users = ref<Schemas['User'][]>([])
const loading = ref(false)
const message = useMessage()
let loadVersion = 0

async function load() {
  const version = ++loadVersion
  loading.value = true
  const { data, error } = await api.GET('/api/traffic/overview', { params: { query: { user_id: userId.value ?? 0 } } })
  if (version !== loadVersion) return
  loading.value = false
  if (!data) {
    message.error(problemMessage(error))
    return
  }
  overview.value = data
  live.value = data.live
}

watch(userId, () => {
  overview.value = null
  load()
})

const stopEvents = onTrafficEvent((point) => {
  const cutoff = Date.now() - LIVE_WINDOW
  live.value = [...live.value.filter((item) => new Date(item.at).getTime() >= cutoff), point]
})
let timer: number | undefined
onMounted(() => {
  api.GET('/api/users').then(({ data }) => { users.value = data?.users ?? [] })
  load()
  timer = window.setInterval(load, REFRESH)
})
onUnmounted(() => {
  stopEvents()
  window.clearInterval(timer)
})

function selectedPoint(point: Schemas['LivePoint']) {
  return userId.value ? point.users?.[userId.value] ?? { up: 0, down: 0, stacks: {} } : point
}
const filteredLive = computed(() => live.value.map(selectedPoint))
const latest = computed(() => filteredLive.value[filteredLive.value.length - 1])
const labels = computed(() => live.value.map((point) => new Date(point.at).toLocaleTimeString('zh-CN', { hour12: false })))
const rateSeries = computed(() => [
  { name: '上行', data: filteredLive.value.map((point) => point.up), slot: 0 },
  { name: '下行', data: filteredLive.value.map((point) => point.down), slot: 1 },
])
const total = (bytes: { up: number; down: number }) => bytes.up + bytes.down
const peak = computed(() => Math.max(1, ...(overview.value?.top_users ?? []).map(total)))
const collectErrors = computed(() => (overview.value?.stacks ?? []).filter((stack) => stack.error))
</script>

<template>
  <NSpace vertical :size="12">
    <NCard title="流量统计" size="small">
      <NSpace align="center">
        <NSelect v-model:value="userId" :options="users.map((user) => ({ label: user.name, value: user.id! }))" clearable filterable placeholder="全部流量（可指定用户）" style="width: 260px" />
        <NButton size="small" :loading="loading" @click="load">刷新</NButton>
        <NText depth="3">{{ userId ? '当前只统计所选用户及其档案的流量。' : '全部流量按各 Stack 的入站计数汇总。' }}</NText>
      </NSpace>
    </NCard>
    <template v-if="overview">
    <NAlert v-if="!overview.enabled" type="info" :bordered="false">流量监控未开启，可以在「系统设置 → 流量」中开启。</NAlert>
    <NAlert v-if="overview.failed_notifications" type="error" :bordered="false">
      有 {{ overview.failed_notifications }} 封通知发送失败，请查看<RouterLink :to="{ name: 'notifications' }">通知记录</RouterLink>。
    </NAlert>
    <NAlert v-for="stack in collectErrors" :key="stack.stack_id" type="warning" :bordered="false">stack {{ stack.stack }} 采集失败：{{ stack.error }}</NAlert>

    <NGrid cols="2 m:4" responsive="screen" :x-gap="12" :y-gap="12">
      <NGridItem><NCard size="small"><NStatistic label="今日流量" :value="formatBytes(total(overview.today))" /></NCard></NGridItem>
      <NGridItem><NCard size="small"><NStatistic label="本月流量" :value="formatBytes(total(overview.month))" /></NCard></NGridItem>
      <NGridItem><NCard size="small"><NStatistic label="当前上行" :value="formatRate(latest?.up ?? 0)" /></NCard></NGridItem>
      <NGridItem><NCard size="small"><NStatistic label="当前下行" :value="formatRate(latest?.down ?? 0)" /></NCard></NGridItem>
    </NGrid>

    <NCard title="实时速率（最近 1 小时）" size="small">
      <TrafficChart v-if="live.length > 1" kind="line" :labels="labels" :series="rateSeries" :format="formatRate" />
      <NEmpty v-else description="最近一小时还没有采集到速率数据">
        <template #extra><RouterLink :to="{ name: 'health' }">查看采集状态</RouterLink></template>
      </NEmpty>
    </NCard>

    <NCard title="各 Stack 用量" size="small">
      <NEmpty v-if="!overview.by_stack.length" description="还没有 Stack 流量数据" />
      <NTable v-else :bordered="false" size="small" :single-line="false">
        <thead><tr><th>Stack</th><th>今日上行 / 下行</th><th>今日合计</th><th>本月上行 / 下行</th><th>本月合计</th><th>当前上行 / 下行</th></tr></thead>
        <tbody>
          <tr v-for="stack in overview.by_stack" :key="stack.stack_id">
            <td>{{ stack.stack }}</td>
            <td>{{ formatBytes(stack.today.up) }} / {{ formatBytes(stack.today.down) }}</td>
            <td>{{ formatBytes(total(stack.today)) }}</td>
            <td>{{ formatBytes(stack.month.up) }} / {{ formatBytes(stack.month.down) }}</td>
            <td>{{ formatBytes(total(stack.month)) }}</td>
            <td>{{ formatRate(latest?.stacks[stack.stack]?.up ?? 0) }} / {{ formatRate(latest?.stacks[stack.stack]?.down ?? 0) }}</td>
          </tr>
        </tbody>
      </NTable>
    </NCard>

    <NCard :title="userId ? '所选用户本月用量' : '本月用量排行'" size="small">
      <NEmpty v-if="!overview.top_users.length" description="本月还没有用量">
        <template #extra><RouterLink :to="{ name: 'users' }">查看用户用量</RouterLink></template>
      </NEmpty>
      <NTable v-else :bordered="false" size="small">
        <thead><tr><th>用户</th><th>上行</th><th>下行</th><th style="width: 45%">合计</th></tr></thead>
        <tbody>
          <tr v-for="user in overview.top_users" :key="user.id">
            <td><RouterLink :to="{ name: 'user', params: { id: user.id } }">{{ user.name }}</RouterLink></td>
            <td>{{ formatBytes(user.up) }}</td>
            <td>{{ formatBytes(user.down) }}</td>
            <td>
              <div class="bar-cell">
                <span class="bar" :style="{ width: `${(total(user) / peak) * MAX_BAR}%` }" />
                <NText>{{ formatBytes(total(user)) }}</NText>
              </div>
            </td>
          </tr>
        </tbody>
      </NTable>
    </NCard>

    <NCard v-if="overview.stacks.length" title="采集状态" size="small">
      <NSpace>
        <NTag v-for="stack in overview.stacks" :key="stack.stack_id" :type="stack.error ? 'warning' : 'success'" size="small">
          {{ stack.stack }}：{{ stack.error ? '异常' : '正常' }}
        </NTag>
      </NSpace>
    </NCard>
    </template>
  </NSpace>
</template>

<style scoped>
.bar-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}
.bar {
  height: 8px;
  min-width: 2px;
  border-radius: 0 4px 4px 0;
  background: #2a78d6;
}
:root[data-theme='dark'] .bar {
  background: #3987e5;
}
</style>
