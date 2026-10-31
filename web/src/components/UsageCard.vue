<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NCard, NEmpty, NPopconfirm, NProgress, NSpace, NTag, NText, useMessage } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { formatBytes, formatTime } from '../util/format'
import TrafficChart from './TrafficChart.vue'

const props = defineProps<{ userId: number; dailyLimit: number; quota: number }>()
const message = useMessage()
const usage = ref<Schemas['UserUsage'] | null>(null)
const labels = { daily: '每日阈值', quota: '月度额度', expired: '到期' } as const

async function load() {
  usage.value = (await api.GET('/api/users/{id}/usage', { params: { path: { id: props.userId } } })).data ?? null
}
watch(() => [props.userId, props.dailyLimit, props.quota], load, { immediate: true })

const total = (bytes: { up: number; down: number }) => bytes.up + bytes.down
const percent = (used: number, limit: number) => Math.min(100, Math.round((used / limit) * 100))
const status = (value: number) => (value >= 100 ? 'error' : value >= 80 ? 'warning' : 'success')

async function release() {
  const { error } = await api.DELETE('/api/users/{id}/auto-pause/{reason}', { params: { path: { id: props.userId, reason: 'daily' } } })
  if (error) {
    message.error(problemMessage(error))
    return
  }
  message.success('已解除，今天不再按每日阈值暂停')
  load()
}

const series = computed(() => [{ name: '日用量', data: (usage.value?.daily ?? []).map(total), slot: 0 }])
const days = computed(() => (usage.value?.daily ?? []).map((item) => item.day.slice(5)))
const reference = computed(() => (props.dailyLimit ? { value: props.dailyLimit, label: '每日阈值' } : undefined))
</script>

<template>
  <NCard title="用量" size="small" class="card">
    <NSpace v-if="usage" vertical :size="12">
      <NSpace v-if="usage.auto_pauses.length" vertical>
        <NSpace v-for="pause in usage.auto_pauses" :key="pause.reason" align="center">
          <NTag type="warning" size="small">自动暂停：{{ labels[pause.reason] }}</NTag>
          <NText depth="3">
            {{ formatTime(pause.since) }} 起<template v-if="new Date(pause.resume_at).getFullYear() > 1">，预计 {{ formatTime(pause.resume_at) }} 恢复</template>
          </NText>
          <NPopconfirm v-if="pause.reason === 'daily'" @positive-click="release">
            <template #trigger><NButton size="tiny">解除</NButton></template>
            解除后今天不再按每日阈值暂停，次日重新判定。
          </NPopconfirm>
          <NText v-else depth="3">调整{{ pause.reason === 'quota' ? '额度' : '到期时间' }}后自动解除</NText>
        </NSpace>
      </NSpace>
      <NText v-if="usage.exempt" depth="3">今天已解除每日暂停，次日重新判定。</NText>
      <div>
        <NText>今日 {{ formatBytes(total(usage.today)) }}<template v-if="dailyLimit"> / {{ formatBytes(dailyLimit) }}</template></NText>
        <NProgress v-if="dailyLimit" type="line" :percentage="percent(total(usage.today), dailyLimit)" :status="status(percent(total(usage.today), dailyLimit))" />
      </div>
      <div>
        <NText>本周期 {{ formatBytes(total(usage.period)) }}<template v-if="quota"> / {{ formatBytes(quota) }}</template></NText>
        <NProgress v-if="quota" type="line" :percentage="percent(total(usage.period), quota)" :status="status(percent(total(usage.period), quota))" />
        <NText v-if="new Date(usage.next_reset).getFullYear() > 1" depth="3" style="display: block">{{ formatTime(usage.next_reset) }} 重置</NText>
      </div>
      <TrafficChart v-if="usage.daily.some((day) => total(day) > 0)" kind="bar" :labels="days" :series="series" :format="formatBytes" :reference="reference" height="220px" />
      <NEmpty v-else size="small" description="最近 30 天没有用量" />
    </NSpace>
  </NCard>
</template>
