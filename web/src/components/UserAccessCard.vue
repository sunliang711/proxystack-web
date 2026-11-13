<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { NButton, NCard, NDataTable, NSpace, NTag, NText, useMessage, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, saveBlob, type Schemas } from '../api/client'
import { loadTimezone } from '../composables/useTrafficTrend'
import { recentDayRange, type TrafficRange } from '../util/traffic-range'
import AccessRangePicker, { type AccessBounds, type AccessPeriod } from './AccessRangePicker.vue'
import AccessStatusNotice from './AccessStatusNotice.vue'
import { useBlockDomain } from '../composables/useBlockDomain'

type Row = Schemas['AccessDomainCount']

/** 用户详情页「用量」标签里的常访问站点。 */
const props = defineProps<{ userId: number }>()
/** 一键拉黑或移出改了该用户的名单（条目即主域名），所在页面据此同步表单，避免之后保存时覆盖。 */
const emit = defineEmits<{ blocklist: [entry: string, blocked: boolean] }>()
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

const { busy, setBlocked } = useBlockDomain()
async function toggleBlock(row: Row) {
  const blocked = row.listed !== 'user'
  const listed = await setBlocked(row.domain, props.userId, blocked)
  if (listed === null) return
  row.listed = listed
  emit('blocklist', row.domain, blocked)
}

const peak = computed(() => Math.max(1, ...(result.value?.domains ?? []).map((row) => row.count + row.blocked)))
const hidden = computed(() => (result.value ? result.value.domain_count - result.value.domains.length : 0))
const columns = computed<DataTableColumns<Row>>(() => [
  { title: '名次', key: 'rank', width: 64, render: (_row, index) => index + 1 },
  {
    title: '主域名', key: 'domain', minWidth: 200,
    render: (row) => h(NSpace, { size: 6, align: 'center', wrap: false }, () => [
      h('span', { class: 'domain' }, row.domain),
      row.listed ? h(NTag, { size: 'small', type: 'error', bordered: false }, () => (row.listed === 'global' ? '全局黑名单' : '已拉黑')) : null,
    ]),
  },
  { title: '放行次数', key: 'count', minWidth: 100, render: (row) => row.count.toLocaleString() },
  { title: '拦截次数', key: 'blocked', minWidth: 100, render: (row) => (row.blocked ? h(NText, { type: 'error' }, () => row.blocked.toLocaleString()) : '0') },
  { title: '有访问的天数', key: 'days', minWidth: 110, render: (row) => row.days ?? 0 },
  {
    title: '占比', key: 'bar', minWidth: 140,
    render: (row) => h('div', { class: 'bar-cell' }, [
      h('span', { class: 'bar', style: { width: `${(row.count / peak.value) * 80}%` } }),
      row.blocked ? h('span', { class: 'bar blocked', style: { width: `${(row.blocked / peak.value) * 80}%` } }) : null,
    ]),
  },
  {
    title: '用户黑名单', key: 'action', width: 100, fixed: 'right',
    // 由全局名单拦截的域名在全局名单中管理。
    render: (row) => row.listed === 'global'
      ? h(NText, { depth: 3 }, () => '—')
      : h(NButton, { size: 'tiny', secondary: true, type: row.listed === 'user' ? 'default' : 'error', loading: busy.value === row.domain, onClick: () => toggleBlock(row) },
        () => (row.listed === 'user' ? '移出' : '拉黑')),
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
        {{ result.from_day === result.to_day ? result.from_day : `${result.from_day} ～ ${result.to_day}` }}：放行 {{ result.total.toLocaleString() }} 次，拦截 {{ result.blocked.toLocaleString() }} 次，{{ result.domain_count }} 个主域名；「拉黑」只拦截该用户，应用配置后生效<template v-if="hidden > 0">，只列出前 {{ result.domains.length }} 个，完整列表请导出 CSV</template>。
      </NText>
      <NDataTable :columns="columns" :data="result?.domains ?? []" :loading="loading" :row-key="(row: Row) => row.domain" :pagination="{ pageSize: 20 }" :scroll-x="860" size="small" />
    </NSpace>
  </NCard>
</template>

<style scoped>
.summary { display: block; font-size: 12px; }
:deep(.domain) { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-all; }
:deep(.bar-cell) { display: flex; align-items: center; }
:deep(.bar) { height: 8px; min-width: 2px; border-radius: 4px; background: var(--ps-accent); }
:deep(.bar.blocked) { margin-left: 2px; background: var(--ps-danger); }
</style>
