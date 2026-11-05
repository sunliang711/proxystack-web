<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { NAlert, NButton, NCard, NEmpty, NGrid, NGridItem, NRadioButton, NRadioGroup, NSelect, NSpace, NStatistic, NTable, NTag, NText, useMessage } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import TrafficChart from '../components/TrafficChart.vue'
import EntityLink from '../components/EntityLink.vue'
import SetupChecklist from '../components/SetupChecklist.vue'
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
/** 聚合 stack 的流量经成员 stack 的 relay 出去，已在成员 stack 计入，全部流量的合计不含它们。 */
const aggregators = computed(() => (overview.value?.by_stack ?? []).filter((stack) => stack.aggregator).map((stack) => stack.stack))
/**
 * 今日、本月流量按用户计数，与用户详情、排行和额度判定一致；去重后的入站计数只用来估算没能归属到用户的部分
 * （例如 SOCKS5/HTTP 共享账号）。两套计数器在同一入站上本身相差 1%～3%，差额很小时不提示。
 */
function unattributed(all: { up: number; down: number }, users: { up: number; down: number }) {
  const gap = total(all) - total(users)
  return gap >= 1024 * 1024 && gap > total(all) * 0.01 ? gap : 0
}
/** 最近一次采集中速率最高的用户，用于回答「现在是谁在占带宽」。 */
const ACTIVE_LIMIT = 5
const activeUsers = computed(() => {
  const point = live.value[live.value.length - 1]
  if (!point?.users) return []
  const names = new Map(users.value.map((user) => [user.id, user.name]))
  return Object.entries(point.users)
    .map(([id, rate]) => ({ id: Number(id), name: names.get(Number(id)) ?? `#${id}`, up: rate.up, down: rate.down, stacks: Object.entries(rate.stacks ?? {}).filter(([, bytes]) => bytes.up + bytes.down > 0).map(([name]) => name) }))
    .filter((user) => user.up + user.down > 0)
    .sort((a, b) => b.up + b.down - (a.up + a.down))
})
const activePeak = computed(() => Math.max(1, ...activeUsers.value.map((user) => user.up + user.down)))
const hasUnattributed = computed(() => !!overview.value && (unattributed(overview.value.today, overview.value.user_today) > 0 || unattributed(overview.value.month, overview.value.user_month) > 0))
</script>

<template>
  <NSpace vertical :size="20">
    <SetupChecklist />
    <NCard size="small">
      <div class="filter-bar overview-filters">
        <span class="section-label" style="margin: 0">统计范围</span>
        <NSelect v-model:value="userId" :options="users.map((user) => ({ label: user.name, value: user.id! }))" clearable filterable placeholder="全部流量（可指定用户）" />
        <NButton size="small" :loading="loading" @click="load">刷新</NButton>
        <NText v-if="userId" class="filter-note">当前只统计所选用户及其档案的流量。 <EntityLink kind="user" :id="userId" label="查看用户" /></NText>
      </div>
    </NCard>
    <template v-if="overview">
    <NAlert v-if="!overview.enabled" type="info" :bordered="false">
      流量监控未开启，下面的流量和速率不会更新。<RouterLink :to="{ name: 'settings', query: { section: 'traffic' } }">去系统设置开启</RouterLink>
    </NAlert>
    <NAlert v-if="overview.failed_notifications" type="error" :bordered="false">
      有 {{ overview.failed_notifications }} 封通知发送失败，请查看<RouterLink :to="{ name: 'records', query: { tab: 'notifications' } }">通知记录</RouterLink>。
    </NAlert>
    <NAlert v-for="stack in collectErrors" :key="stack.stack_id" type="warning" :bordered="false">Stack <EntityLink kind="stack" :id="stack.stack_id" :name="stack.stack" /> 采集失败：{{ stack.error }}</NAlert>

    <NGrid cols="2 m:4" responsive="screen" :x-gap="20" :y-gap="20">
      <NGridItem v-for="period in [{ label: '今日流量', all: overview.today, users: overview.user_today }, { label: '本月流量', all: overview.month, users: overview.user_month }]" :key="period.label">
        <NCard size="small" class="metric-card">
          <NStatistic :label="period.label" :value="formatBytes(total(period.users))" />
          <NText v-if="!userId && unattributed(period.all, period.users)" depth="3" class="metric-note">
            另有 {{ formatBytes(unattributed(period.all, period.users)) }} 未归属到用户
          </NText>
        </NCard>
      </NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="当前上行速率" :value="formatRate(latest?.up ?? 0)" /></NCard></NGridItem>
      <NGridItem><NCard size="small" class="metric-card"><NStatistic label="当前下行速率" :value="formatRate(latest?.down ?? 0)" /></NCard></NGridItem>
    </NGrid>
    <NText v-if="!userId" depth="3" class="aggregator-note">
      今日、本月流量按用户计数，与用户详情和用量排行一致。实时速率按各 stack 入站计数<template v-if="aggregators.length">，不含聚合 stack {{ aggregators.join('、') }}（它们的流量经成员 stack 的 relay 出去，已计入成员 stack）</template>。
      <template v-if="hasUnattributed">未归属到用户的流量通常来自 SOCKS5/HTTP 共享账号。</template>
    </NText>

    <NCard title="实时速率（最近 1 小时）" size="small">
      <TrafficChart v-if="live.length > 1" kind="line" :labels="labels" :series="rateSeries" :format="formatRate" />
      <NEmpty v-else description="最近一小时还没有采集到速率数据">
        <template #extra><RouterLink :to="{ name: 'health' }">查看采集状态</RouterLink></template>
      </NEmpty>
      <template v-if="!userId && live.length">
        <div class="active-head">
          <NText strong>当前占用带宽的用户</NText>
          <NText depth="3">最近一次采集的速率，按上下行合计排序<template v-if="activeUsers.length > ACTIVE_LIMIT">，共 {{ activeUsers.length }} 个用户在用</template></NText>
        </div>
        <NText v-if="!activeUsers.length" depth="3" class="active-empty">最近一次采集时没有用户在传输数据。</NText>
        <div v-else class="table-scroll"><NTable :bordered="false" size="small">
          <thead><tr><th>用户</th><th>↑</th><th>↓</th><th style="width: 40%">合计</th><th>Stack</th></tr></thead>
          <tbody>
            <tr v-for="user in activeUsers.slice(0, ACTIVE_LIMIT)" :key="user.id">
              <td><EntityLink kind="user" :id="user.id" :name="user.name" /></td>
              <td>{{ formatRate(user.up) }}</td>
              <td>{{ formatRate(user.down) }}</td>
              <td>
                <div class="bar-cell">
                  <span class="bar" :style="{ width: `${((user.up + user.down) / activePeak) * MAX_BAR}%` }" />
                  <NText>{{ formatRate(user.up + user.down) }}</NText>
                </div>
              </td>
              <td><NText depth="3">{{ user.stacks.join('、') }}</NText></td>
            </tr>
          </tbody>
        </NTable></div>
      </template>
    </NCard>

    <NCard title="用量排行" size="small">
      <template #header-extra>
        <NRadioGroup v-model:value="rankingPeriod" size="small" aria-label="排行周期">
          <NRadioButton value="day">今日</NRadioButton>
          <NRadioButton value="month">本月</NRadioButton>
        </NRadioGroup>
      </template>
      <NEmpty v-if="!rankedUsers.length" :description="rankingPeriod === 'day' ? '今日还没有用量' : '本月还没有用量'">
        <template #extra><RouterLink :to="{ name: 'users' }">查看用户用量</RouterLink></template>
      </NEmpty>
      <div v-else class="table-scroll"><NTable :bordered="false" size="small">
        <thead><tr><th>用户</th><th>↑</th><th>↓</th><th style="width: 45%">合计</th></tr></thead>
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

    <NCard title="各 Stack 用量" size="small">
      <NEmpty v-if="!overview.by_stack.length" description="还没有 Stack 流量数据">
        <template #extra><RouterLink :to="{ name: 'stacks' }">查看 Stack 列表</RouterLink></template>
      </NEmpty>
      <div v-else class="table-scroll"><NTable :bordered="false" size="small" :single-line="false">
        <thead><tr><th>Stack</th><th>{{ userId ? '今日流量' : '今日入站' }}</th><th v-if="!userId">今日用户流量</th><th>{{ userId ? '本月流量' : '本月入站' }}</th><th v-if="!userId">本月用户流量</th><th>当前速率</th></tr></thead>
        <tbody>
          <tr v-for="stack in overview.by_stack" :key="stack.stack_id">
            <td>
              <EntityLink kind="stack" :id="stack.stack_id" :name="stack.stack" />
              <NTag v-if="stack.aggregator && !userId" size="small" :bordered="false" style="margin-left: 6px">聚合，不计入合计</NTag>
            </td>
            <td><div>合计 {{ formatBytes(total(stack.today)) }}</div><NText depth="3">↑ {{ formatBytes(stack.today.up) }} · ↓ {{ formatBytes(stack.today.down) }}</NText></td>
            <td v-if="!userId"><div>合计 {{ formatBytes(total(stack.user_today)) }}</div><NText depth="3">↑ {{ formatBytes(stack.user_today.up) }} · ↓ {{ formatBytes(stack.user_today.down) }}</NText></td>
            <td><div>合计 {{ formatBytes(total(stack.month)) }}</div><NText depth="3">↑ {{ formatBytes(stack.month.up) }} · ↓ {{ formatBytes(stack.month.down) }}</NText></td>
            <td v-if="!userId"><div>合计 {{ formatBytes(total(stack.user_month)) }}</div><NText depth="3">↑ {{ formatBytes(stack.user_month.up) }} · ↓ {{ formatBytes(stack.user_month.down) }}</NText></td>
            <td><div>↑ {{ formatRate(latest?.stacks[stack.stack]?.up ?? 0) }}</div><NText depth="3">↓ {{ formatRate(latest?.stacks[stack.stack]?.down ?? 0) }}</NText></td>
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
.metric-note { display: block; margin-top: 4px; font-size: 12px; }
.aggregator-note { display: block; margin-top: -8px; font-size: 12px; }
.collection-link { text-decoration: none; border-radius: 6px; }
.active-head { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 12px; margin: 16px 0 8px; }
.active-empty { display: block; font-size: 13px; }
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
