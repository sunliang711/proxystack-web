<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { NButton, NCard, NDataTable, NSpace, NText, useMessage, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, saveBlob, type Schemas } from '../api/client'
import { loadTimezone } from '../composables/useTrafficTrend'
import { recentDayRange, type TrafficRange } from '../util/traffic-range'
import AccessRangePicker, { type AccessBounds, type AccessPeriod } from './AccessRangePicker.vue'
import AccessStatusNotice from './AccessStatusNotice.vue'

type Row = Schemas['AccessDomainCount']

/** 用户详情页「用量」标签里的常访问站点。 */
const props = defineProps<{ userId: number }>()
const message = useMessage()
const period = ref<AccessPeriod>('7d')
const timezone = ref('')
const custom = ref<TrafficRange>(['', ''])
onMounted(async () => {
  const zone = await loadTimezone()
  custom.value = recentDayRange(7, zone)
  timezone.value = zone
})
const bounds = ref<AccessBounds | null>(null)
const result = ref<Schemas['AccessDomains'] | null>(null)
const loading = ref(false)
let version = 0

async function load() {
  if (!bounds.value) return
  const id = ++version
  loading.value = true
  const { data, error } = await api.GET('/api/users/{id}/access', { params: { path: { id: props.userId }, query: { ...bounds.value, limit: 0 } } })
  if (id !== version) return
  loading.value = false
  result.value = data ?? null
  if (!data) message.error(problemMessage(error, '访问统计查询失败'))
}
watch([bounds, () => props.userId], load)

async function exportCSV() {
  if (!bounds.value) return
  const { data, error, response } = await api.GET('/api/access/export', { params: { query: { ...bounds.value, user: props.userId } }, parseAs: 'blob' })
  if (error || !data) {
    message.error(problemMessage(error, '导出失败'))
    return
  }
  const filename = /filename="([^"]+)"/.exec(response.headers.get('Content-Disposition') ?? '')?.[1] ?? 'access.csv'
  saveBlob(data as unknown as Blob, filename)
}

const peak = computed(() => Math.max(1, ...(result.value?.domains ?? []).map((row) => row.count)))
const hidden = computed(() => (result.value ? result.value.domain_count - result.value.domains.length : 0))
const columns = computed<DataTableColumns<Row>>(() => [
  { title: '名次', key: 'rank', width: 64, render: (_row, index) => index + 1 },
  { title: '主域名', key: 'domain', minWidth: 200, render: (row) => h('span', { class: 'domain' }, row.domain) },
  { title: '连接次数', key: 'count', minWidth: 100, render: (row) => row.count.toLocaleString() },
  { title: '有访问的天数', key: 'days', minWidth: 110, render: (row) => row.days ?? 0 },
  {
    title: '占比', key: 'bar', minWidth: 140,
    render: (row) => h('div', { class: 'bar-cell' }, [h('span', { class: 'bar', style: { width: `${(row.count / peak.value) * 80}%` } })]),
  },
])
</script>

<template>
  <NCard title="常访问站点" size="small" :bordered="false">
    <template #header-extra>
      <NButton size="small" :disabled="!result?.domains.length" @click="exportCSV">导出 CSV</NButton>
    </template>
    <NSpace vertical :size="12">
      <AccessStatusNotice />
      <AccessRangePicker v-if="timezone" v-model:period="period" v-model:custom="custom" :timezone="timezone" @change="(value) => (bounds = value)" />
      <NText v-if="result" depth="3" class="summary">
        {{ result.from_day === result.to_day ? result.from_day : `${result.from_day} ～ ${result.to_day}` }}：共 {{ result.total.toLocaleString() }} 次连接，{{ result.domain_count }} 个主域名<template v-if="hidden > 0">，只列出前 {{ result.domains.length }} 个，完整列表请导出 CSV</template>。
      </NText>
      <NDataTable :columns="columns" :data="result?.domains ?? []" :loading="loading" :row-key="(row: Row) => row.domain" :pagination="{ pageSize: 20 }" :scroll-x="640" size="small" />
    </NSpace>
  </NCard>
</template>

<style scoped>
.summary { display: block; font-size: 12px; }
:deep(.domain) { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-all; }
:deep(.bar-cell) { display: flex; align-items: center; }
:deep(.bar) { height: 8px; min-width: 2px; border-radius: 4px; background: var(--ps-accent); }
</style>
