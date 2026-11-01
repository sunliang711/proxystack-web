<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { NAlert, NButton, NCard, NEmpty, NGrid, NGridItem, NRadioButton, NRadioGroup, NSelect, NSpace, NStatistic, NTable, NTag, NText, useMessage } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import TrafficChart from '../components/TrafficChart.vue'
import EntityLink from '../components/EntityLink.vue'
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
const rankingPeriod = ref<'day' | 'month'>('day')
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
const rankedUsers = computed(() => (rankingPeriod.value === 'day' ? overview.value?.top_users_today : overview.value?.top_users) ?? [])
const peak = computed(() => Math.max(1, ...rankedUsers.value.map(total)))
const collectErrors = computed(() => (overview.value?.stacks ?? []).filter((stack) => stack.error))
</script>

<template>
  <NSpace vertical :size="20">
    <NCard size="small">
      <div class="filter-bar overview-filters">
        <span class="section-label" style="margin: 0">统计范围</span>
        <NSelect v-model:value="userId" :options="users.map((user) => ({ label: user.name, value: user.id! }))" clearable filterable placeholder="全部流量（可指定用户）" />
        <NButton size="small" :loading="loading" @click="load">刷新</NButton>
        <NText class="filter-note">{{ userId ? '当前只统计所选用户及其档案的流量。' : '全部流量按各 Stack 的入站计数汇总。' }} <EntityLink v-if="userId" kind="user" :id="userId" label="查看用户" /></NText>
      </div>
    </NCard>
    <template v-if="overview">
    <NAlert v-if="!overview.enabled" type="info" :bordered="false">流量监控未开启，可以在「系统设置 → 流量」中开启。</NAlert>
    <NAlert v-if="overview.failed_notifications" type="error" :bordered="false">
      有 {{ overview.failed_notifications }} 封通知发送失败，请查看<RouterLink :to="{ name: 'notifications' }">通知记录</RouterLink>。
    </NAlert>
    <NAlert v-for="stack in collectErrors" :key="stack.stack_id" type="warning" :bordered="false">Stack <EntityLink kind="stack" :id="stack.stack_id" :name="stack.stack" /> 采集失败：{{ stack.error }}</NAlert>

    <NGrid cols="2 m:4" responsive="screen" :x-gap="20" :y-gap="20">
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="今日流量" :value="formatBytes(total(overview.today))" /></NCard></NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="本月流量" :value="formatBytes(total(overview.month))" /></NCard></NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="当前上行" :value="formatRate(latest?.up ?? 0)" /></NCard></NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="当前下行" :value="formatRate(latest?.down ?? 0)" /></NCard></NGridItem>
    </NGrid>

    <NCard title="实时速率（最近 1 小时）" size="small">
      <TrafficChart v-if="live.length > 1" kind="line" :labels="labels" :series="rateSeries" :format="formatRate" />
      <NEmpty v-else description="最近一小时还没有采集到速率数据">
        <template #extra><RouterLink :to="{ name: 'health' }">查看采集状态</RouterLink></template>
      </NEmpty>
    </NCard>

    <NCard title="各 Stack 用量" size="small">
      <NEmpty v-if="!overview.by_stack.length" description="还没有 Stack 流量数据" />
      <div v-else class="table-scroll"><NTable :bordered="false" size="small" :single-line="false">
        <thead><tr><th>Stack</th><th>今日上行 / 下行</th><th>今日合计</th><th>本月上行 / 下行</th><th>本月合计</th><th>当前上行 / 下行</th></tr></thead>
        <tbody>
          <tr v-for="stack in overview.by_stack" :key="stack.stack_id">
            <td><EntityLink kind="stack" :id="stack.stack_id" :name="stack.stack" /></td>
            <td>{{ formatBytes(stack.today.up) }} / {{ formatBytes(stack.today.down) }}</td>
            <td>{{ formatBytes(total(stack.today)) }}</td>
            <td>{{ formatBytes(stack.month.up) }} / {{ formatBytes(stack.month.down) }}</td>
            <td>{{ formatBytes(total(stack.month)) }}</td>
            <td>{{ formatRate(latest?.stacks[stack.stack]?.up ?? 0) }} / {{ formatRate(latest?.stacks[stack.stack]?.down ?? 0) }}</td>
          </tr>
        </tbody>
      </NTable></div>
    </NCard>

    <NCard title="用量排行" size="small">
      <template #header-extra>
        <NRadioGroup v-model:value="rankingPeriod" size="small" aria-label="排行周期">
          <NRadioButton value="day">日</NRadioButton>
          <NRadioButton value="month">月</NRadioButton>
        </NRadioGroup>
      </template>
      <NEmpty v-if="!rankedUsers.length" :description="rankingPeriod === 'day' ? '今日还没有用量' : '本月还没有用量'">
        <template #extra><RouterLink :to="{ name: 'users' }">查看用户用量</RouterLink></template>
      </NEmpty>
      <div v-else class="table-scroll"><NTable :bordered="false" size="small">
        <thead><tr><th>用户</th><th>上行</th><th>下行</th><th style="width: 45%">合计</th></tr></thead>
        <tbody>
          <tr v-for="user in rankedUsers" :key="user.id">
            <td><EntityLink kind="user" :id="user.id" :name="user.name" /></td>
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
      </NTable></div>
    </NCard>

    <NCard v-if="overview.stacks.length" title="采集状态" size="small">
      <NSpace>
        <RouterLink v-for="stack in overview.stacks" :key="stack.stack_id" :to="{ name: 'stack', params: { id: stack.stack_id } }" class="collection-link" :aria-label="`查看 ${stack.stack} 的详情，采集${stack.error ? '异常' : '正常'}`">
          <NTag :type="stack.error ? 'warning' : 'success'" size="small" class="collection-tag">
            {{ stack.stack }}：{{ stack.error ? '异常' : '正常' }} <span aria-hidden="true">↗</span>
          </NTag>
        </RouterLink>
      </NSpace>
    </NCard>
    </template>
  </NSpace>
</template>

<style scoped>
.collection-link { text-decoration: none; border-radius: 6px; }
.collection-tag { cursor: pointer; }
.collection-link:hover .collection-tag { filter: brightness(1.15); text-decoration: underline; }
.overview-filters > .n-select { width: 260px; }
@media (max-width: 767px) {
  .overview-filters { display: grid; grid-template-columns: minmax(0, 1fr) auto; }
  .overview-filters > .n-select { width: 100%; }
  .overview-filters > .section-label, .overview-filters > .filter-note { grid-column: 1 / -1; }
}
.bar-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}
.bar {
  height: 8px;
  min-width: 2px;
  border-radius: 4px;
  background: var(--ps-accent);
}
</style>
