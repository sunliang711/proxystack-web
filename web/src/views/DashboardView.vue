<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { NAlert, NButton, NCard, NEmpty, NGrid, NGridItem, NIcon, NRadioButton, NRadioGroup, NSpace, NStatistic, NTable, NTag, NText, NTooltip, useMessage } from 'naive-ui'
import { RefreshOutline } from '@vicons/ionicons5'
import { api, problemMessage, type Schemas } from '../api/client'
import TrafficChart from '../components/TrafficChart.vue'
import EntityLink from '../components/EntityLink.vue'
import SetupChecklist from '../components/SetupChecklist.vue'
import GapNotice from './traffic/GapNotice.vue'
import { onTrafficEvent } from '../runtime'
import { formatBytes, formatRate } from '../util/format'
import { compareChange, totalOf } from '../util/traffic-stats'

/** 实时曲线保留的时长，与服务端一致。 */
const LIVE_WINDOW = 3600_000
/** 排行第一的条形占单元格的比例，其余按用量等比缩短，右侧留给数值。 */
const MAX_BAR = 70
/** 今日、本月合计的刷新间隔。 */
const REFRESH = 30_000

const overview = ref<Schemas['TrafficOverview'] | null>(null)
const live = ref<Schemas['LivePoint'][]>([])
const rankingPeriod = ref<'today' | 'month'>('today')
const users = ref<Schemas['User'][]>([])
const loading = ref(false)
const message = useMessage()
let loadVersion = 0

async function load() {
  const version = ++loadVersion
  loading.value = true
  const { data, error } = await api.GET('/api/traffic/overview')
  if (version !== loadVersion) return
  loading.value = false
  if (!data) {
    message.error(problemMessage(error))
    return
  }
  overview.value = data
  live.value = data.live
}

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

const latest = computed(() => live.value[live.value.length - 1])
/**
 * 实时速率按分钟取平均后画成堆叠柱：采集间隔内的突发流量画成折线是一根根尖刺，
 * 按分钟汇总后更容易看出哪段时间在用、用了多少。没有采集点的分钟留空。
 */
const minuteBuckets = computed(() => {
  const buckets = new Map<number, { up: number; down: number; count: number }>()
  for (const point of live.value) {
    const minute = Math.floor(new Date(point.at).getTime() / 60_000)
    const bucket = buckets.get(minute) ?? { up: 0, down: 0, count: 0 }
    bucket.up += point.up
    bucket.down += point.down
    bucket.count++
    buckets.set(minute, bucket)
  }
  const minutes = [...buckets.keys()]
  if (!minutes.length) return []
  const first = Math.min(...minutes)
  const last = Math.max(...minutes)
  const out: { minute: number; up: number; down: number }[] = []
  for (let minute = first; minute <= last; minute++) {
    const bucket = buckets.get(minute)
    out.push({ minute, up: bucket ? bucket.up / bucket.count : 0, down: bucket ? bucket.down / bucket.count : 0 })
  }
  return out
})
const labels = computed(() => minuteBuckets.value.map((bucket) => new Date(bucket.minute * 60_000).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' })))
// 上行、下行用色相差得远的两个槽位，深浅主题下都容易区分。
const rateSeries = computed(() => [
  { name: '上行', data: minuteBuckets.value.map((bucket) => bucket.up), slot: 2 },
  { name: '下行', data: minuteBuckets.value.map((bucket) => bucket.down), slot: 1 },
])
const periods = computed(() => overview.value ? [
  { key: 'today', label: '今日流量', previousLabel: '昨日同时段', data: overview.value.today },
  { key: 'month', label: '本月流量', previousLabel: '上月同期', data: overview.value.month },
] : [])
/** 两套计数器在同一入站上本身相差 1%～3%，未归属的差额很小时不提示。 */
function unattributed(period: Schemas['OverviewPeriod']) {
  const gap = totalOf(period.total) - totalOf(period.users)
  return gap >= 1024 * 1024 && gap > totalOf(period.total) * 0.01 ? gap : 0
}
const ranked = computed(() => (rankingPeriod.value === 'today' ? overview.value?.top_today : overview.value?.top_month) ?? [])
const peak = computed(() => Math.max(1, ...ranked.value.map(totalOf)))
const collectErrors = computed(() => (overview.value?.stacks ?? []).filter((stack) => stack.error))
const autoStacks = computed(() => (overview.value?.by_stack ?? []).filter((stack) => stack.excluded).map((stack) => stack.stack))
/** 最近一次采集中速率最高的用户，用于回答「现在是谁在占带宽」。 */
const ACTIVE_LIMIT = 5
const activeUsers = computed(() => {
  const point = latest.value
  if (!point?.users) return []
  const names = new Map(users.value.map((user) => [user.id, user.name]))
  return Object.entries(point.users)
    .map(([id, rate]) => ({ id: Number(id), name: names.get(Number(id)) ?? `#${id}`, up: rate.up, down: rate.down, stacks: Object.entries(rate.stacks ?? {}).filter(([, bytes]) => bytes.up + bytes.down > 0).map(([name]) => name) }))
    .filter((user) => user.up + user.down > 0)
    .sort((a, b) => b.up + b.down - (a.up + a.down))
})
const activePeak = computed(() => Math.max(1, ...activeUsers.value.map((user) => user.up + user.down)))
const changeType = (trend: string) => (trend === 'up' || trend === 'new' ? 'warning' : trend === 'down' ? 'success' : 'default')
</script>

<template>
  <NSpace vertical :size="20">
    <SetupChecklist />
    <template v-if="overview">
    <NAlert v-if="!overview.enabled" type="info" :bordered="false">
      流量监控未开启，下面的流量和速率不会更新。<RouterLink :to="{ name: 'settings', query: { section: 'traffic' } }">去系统设置开启</RouterLink>
    </NAlert>
    <NAlert v-if="overview.failed_notifications" type="error" :bordered="false">
      有 {{ overview.failed_notifications }} 封通知发送失败，请查看<RouterLink :to="{ name: 'records', query: { tab: 'notifications' } }">通知记录</RouterLink>。
    </NAlert>
    <NAlert v-for="stack in collectErrors" :key="stack.stack_id" type="warning" :bordered="false">Stack <EntityLink kind="stack" :id="stack.stack_id" :name="stack.stack" /> 采集失败：{{ stack.error }}</NAlert>
    <GapNotice :gaps="overview.gaps" scope="最近 24 小时内" />

    <NGrid cols="2 m:4" responsive="screen" :x-gap="20" :y-gap="20">
      <NGridItem v-for="period in periods" :key="period.key">
        <NCard size="small" class="metric-card">
          <NStatistic :label="period.label" :value="formatBytes(totalOf(period.data.total))" />
          <NText depth="3" class="metric-note">
            {{ period.previousLabel }} {{ formatBytes(totalOf(period.data.previous)) }}
            <NText :type="changeType(compareChange(totalOf(period.data.total), totalOf(period.data.previous)).trend)">{{ compareChange(totalOf(period.data.total), totalOf(period.data.previous)).text }}</NText>
          </NText>
          <NText depth="3" class="metric-note">
            归属到用户 {{ formatBytes(totalOf(period.data.users)) }}<template v-if="unattributed(period.data)"> · 未归属 {{ formatBytes(unattributed(period.data)) }}</template>
          </NText>
        </NCard>
      </NGridItem>
      <NGridItem>
        <NCard size="small" class="metric-card">
          <NStatistic label="当前速率" :value="formatRate((latest?.up ?? 0) + (latest?.down ?? 0))" />
          <NText depth="3" class="metric-note">↑ {{ formatRate(latest?.up ?? 0) }} · ↓ {{ formatRate(latest?.down ?? 0) }}</NText>
        </NCard>
      </NGridItem>
      <NGridItem>
        <NCard size="small" class="metric-card">
          <NStatistic label="正在使用的用户" :value="activeUsers.length" />
          <NText depth="3" class="metric-note">按最近一次采集计算</NText>
        </NCard>
      </NGridItem>
    </NGrid>
    <NText depth="3" class="aggregator-note">
      今日、本月流量是各 Stack 入站之和<template v-if="autoStacks.length">，不含 auto stack {{ autoStacks.join('、') }}（它们的流量经 edge stack 的 relay 入站出去，已计入 edge stack）</template>；对比按上一期的同等已过时长计算。未归属到用户的流量通常来自 SOCKS5 / HTTP 共享账号。
    </NText>

    <NCard title="实时速率（最近 1 小时，每分钟平均）" size="small">
      <TrafficChart v-if="live.length > 1" kind="bar" :labels="labels" :series="rateSeries" :format="formatRate" />
      <NEmpty v-else description="最近一小时还没有采集到速率数据">
        <template #extra><RouterLink :to="{ name: 'health' }">查看采集状态</RouterLink></template>
      </NEmpty>
      <template v-if="live.length">
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
        <NSpace align="center" :size="12">
          <NRadioGroup v-model:value="rankingPeriod" size="small" aria-label="排行周期">
            <NRadioButton value="today">今日</NRadioButton>
            <NRadioButton value="month">本月</NRadioButton>
          </NRadioGroup>
          <RouterLink :to="{ name: 'traffic', query: { tab: 'ranking', ...(rankingPeriod === 'month' ? { r_period: 'month' } : {}) } }">查看全部排行</RouterLink>
        </NSpace>
      </template>
      <NEmpty v-if="!ranked.length" :description="rankingPeriod === 'today' ? '今日还没有用量' : '本月还没有用量'">
        <template #extra><RouterLink :to="{ name: 'users' }">查看用户用量</RouterLink></template>
      </NEmpty>
      <div v-else class="table-scroll"><NTable :bordered="false" size="small">
        <thead><tr><th>用户</th><th>↑</th><th>↓</th><th style="width: 45%">合计</th></tr></thead>
        <tbody>
          <tr v-for="user in ranked" :key="user.user_id">
            <td>
              <NText v-if="user.deleted">{{ user.name }} <NTag size="small" :bordered="false">已删除</NTag></NText>
              <EntityLink v-else kind="user" :id="user.user_id" :name="user.name" />
            </td>
            <td>{{ formatBytes(user.up) }}</td>
            <td>{{ formatBytes(user.down) }}</td>
            <td>
              <div class="bar-cell">
                <span class="bar" :style="{ width: `${(totalOf(user) / peak) * MAX_BAR}%` }" />
                <NText>{{ formatBytes(totalOf(user)) }}</NText>
              </div>
            </td>
          </tr>
        </tbody>
      </NTable></div>
    </NCard>

    <NCard title="各 Stack 用量" size="small">
      <template #header-extra>
        <RouterLink :to="{ name: 'traffic', query: { tab: 'stack' } }">在流量明细中查看</RouterLink>
      </template>
      <NEmpty v-if="!overview.by_stack.length" description="还没有 Stack 流量数据">
        <template #extra><RouterLink :to="{ name: 'stacks' }">查看 Stack 列表</RouterLink></template>
      </NEmpty>
      <div v-else class="table-scroll"><NTable :bordered="false" size="small" :single-line="false">
        <thead><tr><th>Stack</th><th>今日入站</th><th>本月入站</th><th>本月未归属到用户</th><th>当前速率</th></tr></thead>
        <tbody>
          <tr v-for="stack in overview.by_stack" :key="stack.stack_id">
            <td>
              <NText v-if="stack.deleted">{{ stack.stack }} <NTag size="small" :bordered="false">已删除</NTag></NText>
              <EntityLink v-else kind="stack" :id="stack.stack_id" :name="stack.stack" />
              <NTooltip v-if="stack.excluded">
                <template #trigger><NTag size="small" :bordered="false" style="margin-left: 6px">auto，不计入合计</NTag></template>
                它的流量经 edge stack 的 relay 入站出去，已在 edge stack 计入
              </NTooltip>
            </td>
            <td><div>{{ formatBytes(totalOf(stack.today.total)) }}</div><NText depth="3">↑ {{ formatBytes(stack.today.total.up) }} · ↓ {{ formatBytes(stack.today.total.down) }}</NText></td>
            <td><div>{{ formatBytes(totalOf(stack.month.total)) }}</div><NText depth="3">用户 {{ formatBytes(totalOf(stack.month.users)) }}</NText></td>
            <td>
              <template v-if="totalOf(stack.month.relay) + totalOf(stack.month.shared) > 0">
                <div v-if="totalOf(stack.month.relay)">经 relay 入站 {{ formatBytes(totalOf(stack.month.relay)) }}</div>
                <div v-if="totalOf(stack.month.shared)"><NText depth="3">共享账号等 {{ formatBytes(totalOf(stack.month.shared)) }}</NText></div>
              </template>
              <NText v-else depth="3">—</NText>
            </td>
            <td><div>↑ {{ formatRate(latest?.stacks[stack.stack]?.up ?? 0) }}</div><NText depth="3">↓ {{ formatRate(latest?.stacks[stack.stack]?.down ?? 0) }}</NText></td>
          </tr>
        </tbody>
      </NTable></div>
    </NCard>

    <NCard v-if="overview.stacks.length" title="采集状态" size="small">
      <template #header-extra>
        <NButton size="small" secondary :loading="loading" @click="load">
          <template #icon><NIcon><RefreshOutline /></NIcon></template>
          刷新
        </NButton>
      </template>
      <NSpace>
        <RouterLink v-for="stack in overview.stacks" :key="stack.stack_id" :to="{ name: 'stack', params: { id: stack.stack_id } }" class="collection-link" :aria-label="`查看 ${stack.stack} 的详情，采集${stack.error ? '异常' : '正常'}`">
          <NTag :type="stack.error ? 'warning' : 'success'" size="small" class="collection-tag">
            {{ stack.stack }}：{{ stack.error ? '异常' : '正常' }} <span aria-hidden="true">↗</span>
          </NTag>
        </RouterLink>
      </NSpace>
    </NCard>
    </template>
    <NEmpty v-else :description="loading ? '正在加载' : '暂无数据'" />
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
