<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { NAlert, NCard, NEmpty, NGrid, NGridItem, NSpace, NStatistic, NTable, NTag, NText } from 'naive-ui'
import { api, type Schemas } from '../api/client'
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

async function load() {
  const { data } = await api.GET('/api/traffic/overview')
  if (!data) return
  overview.value = data
  live.value = data.live
}

const stopEvents = onTrafficEvent((point) => {
  const cutoff = Date.now() - LIVE_WINDOW
  live.value = [...live.value.filter((item) => new Date(item.at).getTime() >= cutoff), point]
})
let timer: number | undefined
onMounted(() => {
  load()
  timer = window.setInterval(load, REFRESH)
})
onUnmounted(() => {
  stopEvents()
  window.clearInterval(timer)
})

const latest = computed(() => live.value[live.value.length - 1])
const labels = computed(() => live.value.map((point) => new Date(point.at).toLocaleTimeString('zh-CN', { hour12: false })))
const rateSeries = computed(() => [
  { name: '上行', data: live.value.map((point) => point.up), slot: 0 },
  { name: '下行', data: live.value.map((point) => point.down), slot: 1 },
])
const total = (bytes: { up: number; down: number }) => bytes.up + bytes.down
const peak = computed(() => Math.max(1, ...(overview.value?.top_users ?? []).map(total)))
const collectErrors = computed(() => (overview.value?.stacks ?? []).filter((stack) => stack.error))
</script>

<template>
  <NSpace v-if="overview" vertical :size="12">
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

    <NCard title="本月用量排行" size="small">
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
