<script setup lang="ts">
import { computed, h, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NCard, NDataTable, NGrid, NGridItem, NInput, NSpace, NStatistic, NTag, NText, useMessage, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, saveBlob, type Schemas } from '../../api/client'
import AccessRangePicker, { type AccessBounds, type AccessPeriod } from '../../components/AccessRangePicker.vue'
import AccessStatusNotice from '../../components/AccessStatusNotice.vue'
import EntityLink from '../../components/EntityLink.vue'
import { recentDayRange, type TrafficRange } from '../../util/traffic-range'

type DomainRow = Schemas['AccessDomainCount']
type UserRow = Schemas['AccessDomainUser']

const props = defineProps<{ timezone: string }>()
const message = useMessage()
const route = useRoute()
const router = useRouter()

// 条件用 a_ 前缀记在地址栏，与其他标签页的条件互不影响。
const linked = route.query
const periods: AccessPeriod[] = ['today', 'yesterday', '7d', '30d', 'month', 'last_month', 'custom']
const dateParam = /^\d{4}-\d{2}-\d{2}$/
const period = ref<AccessPeriod>(periods.includes(linked.a_period as AccessPeriod) ? (linked.a_period as AccessPeriod) : 'today')
const custom = ref<TrafficRange>(
  typeof linked.a_from === 'string' && typeof linked.a_to === 'string' && dateParam.test(linked.a_from) && dateParam.test(linked.a_to) && linked.a_from <= linked.a_to
    ? [`${linked.a_from} 00:00:00`, `${linked.a_to} 00:00:00`]
    : recentDayRange(7, props.timezone),
)
const domain = ref(typeof linked.a_domain === 'string' ? linked.a_domain : '')
const search = ref(domain.value)
const bounds = ref<AccessBounds | null>(null)

watch([period, custom, domain], () => {
  const ours = {
    a_period: period.value === 'today' ? undefined : period.value,
    a_from: period.value === 'custom' ? custom.value[0].slice(0, 10) : undefined,
    a_to: period.value === 'custom' ? custom.value[1].slice(0, 10) : undefined,
    a_domain: domain.value || undefined,
  }
  const current = route.query
  if ((Object.keys(ours) as (keyof typeof ours)[]).every((key) => (current[key] ?? undefined) === ours[key])) return
  void router.replace({ query: { ...current, ...ours } })
}, { immediate: true })

const top = ref<Schemas['AccessDomains'] | null>(null)
const topLoading = ref(false)
let topVersion = 0
async function loadTop() {
  if (!bounds.value) return
  const id = ++topVersion
  topLoading.value = true
  const { data, error } = await api.GET('/api/access/domains', { params: { query: { ...bounds.value, limit: 0 } } })
  if (id !== topVersion) return
  topLoading.value = false
  top.value = data ?? null
  if (!data) message.error(problemMessage(error, '访问统计查询失败'))
}
watch(bounds, loadTop)

const users = ref<Schemas['AccessDomainUsers'] | null>(null)
const usersLoading = ref(false)
let usersVersion = 0
async function loadUsers() {
  const id = ++usersVersion
  if (!bounds.value || !domain.value) {
    users.value = null
    return
  }
  usersLoading.value = true
  const { data, error } = await api.GET('/api/access/domain-users', { params: { query: { ...bounds.value, domain: domain.value } } })
  if (id !== usersVersion) return
  usersLoading.value = false
  users.value = data ?? null
  if (!data) message.error(problemMessage(error, '查询失败'))
}
watch([bounds, domain], loadUsers)

const usersCard = ref<HTMLElement | null>(null)
function pick(value: string) {
  search.value = value
  domain.value = value.trim()
  if (domain.value) void nextTick(() => usersCard.value?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }))
}

async function exportCSV() {
  if (!bounds.value) return
  const { data, error, response } = await api.GET('/api/access/export', { params: { query: { ...bounds.value, user: 0 } }, parseAs: 'blob' })
  if (error || !data) {
    message.error(problemMessage(error, '导出失败'))
    return
  }
  const filename = /filename="([^"]+)"/.exec(response.headers.get('Content-Disposition') ?? '')?.[1] ?? 'access.csv'
  saveBlob(data as unknown as Blob, filename)
}

const rangeText = (from?: string, to?: string) => (!from ? '' : from === to ? from : `${from} ～ ${to}`)
const peak = computed(() => Math.max(1, ...(top.value?.domains ?? []).map((row) => row.count)))
const hidden = computed(() => (top.value ? top.value.domain_count - top.value.domains.length : 0))
const domainColumns = computed<DataTableColumns<DomainRow>>(() => [
  { title: '名次', key: 'rank', width: 64, render: (_row, index) => index + 1 },
  {
    title: '主域名', key: 'domain', minWidth: 200,
    render: (row) => h(NButton, { text: true, type: row.domain === users.value?.domain ? 'primary' : 'default', class: 'domain', onClick: () => pick(row.domain) }, () => row.domain),
  },
  { title: '连接次数', key: 'count', minWidth: 100, render: (row) => row.count.toLocaleString() },
  { title: '用户数', key: 'users', minWidth: 80, render: (row) => row.users ?? 0 },
  {
    title: '占比', key: 'bar', minWidth: 140,
    render: (row) => h('div', { class: 'bar-cell' }, [h('span', { class: 'bar', style: { width: `${(row.count / peak.value) * 80}%` } })]),
  },
])
const userColumns: DataTableColumns<UserRow> = [
  { title: '名次', key: 'rank', width: 64, render: (_row, index) => index + 1 },
  {
    title: '用户', key: 'name', minWidth: 140,
    render: (row) => row.deleted
      ? h(NSpace, { size: 6, align: 'center', wrap: false }, () => [h(NText, null, () => row.name), h(NTag, { size: 'small', bordered: false }, () => '已删除')])
      : h(EntityLink, { kind: 'user', id: row.user_id, name: row.name, tab: 'usage' }),
  },
  { title: '连接次数', key: 'count', minWidth: 100, render: (row) => row.count.toLocaleString() },
  { title: '有访问的天数', key: 'days', minWidth: 110 },
]
</script>

<template>
  <NSpace vertical :size="20">
    <AccessStatusNotice />
    <NCard size="small">
      <div class="access-filters">
        <AccessRangePicker v-model:period="period" v-model:custom="custom" :timezone="timezone" @change="(value) => (bounds = value)" />
        <NInput v-model:value="search" size="small" clearable placeholder="输入域名查看访问过它的用户" class="domain-search" @keyup.enter="pick(search)" @clear="pick('')" />
        <NButton size="small" type="primary" :disabled="!search.trim()" @click="pick(search)">查询</NButton>
      </div>
    </NCard>
    <div v-if="domain" ref="usersCard">
    <NCard size="small" :title="users ? `访问 ${users.domain} 的用户` : '访问该域名的用户'">
      <template #header-extra><NButton size="small" quaternary @click="pick('')">关闭</NButton></template>
      <NText v-if="users" depth="3" class="summary">
        {{ rangeText(users.from_day, users.to_day) }}<template v-if="users.domain !== domain">；输入的 {{ domain }} 已合并为主域名 {{ users.domain }}</template>。
      </NText>
      <NDataTable :columns="userColumns" :data="users?.users ?? []" :loading="usersLoading" :row-key="(row: UserRow) => row.user_id" :pagination="{ pageSize: 20 }" :scroll-x="480" size="small" />
    </NCard>
    </div>
    <NGrid cols="2 m:3" responsive="screen" :x-gap="20" :y-gap="20">
      <NGridItem><NCard size="small"><NStatistic label="连接次数" :value="(top?.total ?? 0).toLocaleString()" /></NCard></NGridItem>
      <NGridItem><NCard size="small"><NStatistic label="主域名个数" :value="top?.domain_count ?? 0" /></NCard></NGridItem>
    </NGrid>
    <NCard title="主域名排行" size="small">
      <template #header-extra>
        <NButton size="small" :disabled="!top?.domains.length" @click="exportCSV">导出 CSV</NButton>
      </template>
      <NText v-if="top" depth="3" class="summary">
        {{ rangeText(top.from_day, top.to_day) }}，全部用户合计；点击域名查看访问过它的用户<template v-if="hidden > 0">。只列出前 {{ top.domains.length }} 个，完整列表请导出 CSV</template>。
      </NText>
      <NDataTable :columns="domainColumns" :data="top?.domains ?? []" :loading="topLoading" :row-key="(row: DomainRow) => row.domain" :pagination="{ pageSize: 20 }" :scroll-x="640" size="small" />
    </NCard>
  </NSpace>
</template>

<style scoped>
.access-filters { display: flex; flex-wrap: wrap; align-items: center; gap: 12px 16px; }
.domain-search { width: 280px; }
.summary { display: block; font-size: 12px; margin-bottom: 8px; }
:deep(.domain) { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-all; }
:deep(.bar-cell) { display: flex; align-items: center; }
:deep(.bar) { height: 8px; min-width: 2px; border-radius: 4px; background: var(--ps-accent); }
@media (max-width: 600px) {
  .domain-search { width: 100%; }
}
</style>
