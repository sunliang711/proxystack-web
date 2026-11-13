<script setup lang="ts">
import { computed, h, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NCard, NDataTable, NGrid, NGridItem, NInput, NSpace, NStatistic, NSwitch, NTag, NText, useMessage, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, saveBlob, type Schemas } from '../../api/client'
import AccessRangePicker, { type AccessBounds, type AccessPeriod } from '../../components/AccessRangePicker.vue'
import AccessStatusNotice from '../../components/AccessStatusNotice.vue'
import DomainIcon from '../../components/DomainIcon.vue'
import IconMenu from '../../components/IconMenu.vue'
import EntityLink from '../../components/EntityLink.vue'
import { recentDayRange, type TrafficRange } from '../../util/traffic-range'
import { useBlockDomain } from '../../composables/useBlockDomain'
import { accessRanks, accessRows, iconFallbacks, isServiceRow, setListed, type AccessRow } from '../../util/access-rows'

type DomainRow = AccessRow
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
const domain = ref(typeof linked.a_domain === 'string' && typeof linked.a_service !== 'string' ? linked.a_domain : '')
// 正在查看用户的服务（与 domain 二选一）。
const service = ref(typeof linked.a_service === 'string' ? linked.a_service : '')
const search = ref(domain.value)
const bounds = ref<AccessBounds | null>(null)
/** 按服务归类：同一服务的主域名合并为一行，默认开启。 */
const group = ref(linked.a_group !== '0')

watch([period, custom, domain, service, group], () => {
  const ours = {
    a_period: period.value === 'today' ? undefined : period.value,
    a_from: period.value === 'custom' ? custom.value[0].slice(0, 10) : undefined,
    a_to: period.value === 'custom' ? custom.value[1].slice(0, 10) : undefined,
    a_domain: domain.value || undefined,
    a_service: service.value || undefined,
    a_group: group.value ? undefined : '0',
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
  const { data, error } = await api.GET('/api/access/domains', { params: { query: { ...bounds.value, limit: 0, group: group.value ? 'service' : 'domain' } } })
  if (id !== topVersion) return
  topLoading.value = false
  top.value = data ?? null
  if (!data) message.error(problemMessage(error, '访问统计查询失败'))
}
watch([bounds, group], loadTop)

const users = ref<Schemas['AccessDomainUsers'] | null>(null)
const usersLoading = ref(false)
let usersVersion = 0
async function loadUsers() {
  const id = ++usersVersion
  if (!bounds.value || (!domain.value && !service.value)) {
    users.value = null
    return
  }
  usersLoading.value = true
  const target = service.value ? { service: service.value } : { domain: domain.value }
  const { data, error } = await api.GET('/api/access/domain-users', { params: { query: { ...bounds.value, ...target } } })
  if (id !== usersVersion) return
  usersLoading.value = false
  users.value = data ?? null
  if (!data) message.error(problemMessage(error, '查询失败'))
}
watch([bounds, domain, service], loadUsers)

const usersCard = ref<HTMLElement | null>(null)
function pick(value: string) {
  // 换查询对象时先清掉上一个结果，避免短暂显示上一个对象的说明。
  if (value.trim() !== domain.value || service.value) users.value = null
  search.value = value
  service.value = ''
  domain.value = value.trim()
  if (domain.value) void nextTick(() => usersCard.value?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }))
}
function pickService(name: string) {
  if (name !== service.value) users.value = null
  search.value = ''
  domain.value = ''
  service.value = name
  void nextTick(() => usersCard.value?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }))
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

const { busy, setBlocked } = useBlockDomain()
async function toggleBlock(row: DomainRow) {
  const listed = await setBlocked(row.domain, 0, row.listed !== 'global')
  if (listed !== null && top.value) setListed(top.value.domains, row.domain, listed)
}

// 服务的图标备选：排行里有该服务行时按访问次数取成员，否则用查询结果里的成员（按名称排序）。
const usersIconFallbacks = computed(() => {
  if (!service.value) return []
  const row = top.value?.domains.find((item) => item.service === service.value && isServiceRow(item))
  return row ? iconFallbacks(row) : (users.value?.members ?? [])
})
const rangeText = (from?: string, to?: string) => (!from ? '' : from === to ? from : `${from} ～ ${to}`)
const rows = computed(() => accessRows(top.value?.domains ?? []))
const ranks = computed(() => accessRanks(rows.value))
const peak = computed(() => Math.max(1, ...rows.value.map((row) => row.count + row.blocked)))
const hidden = computed(() => (top.value ? top.value.rows - top.value.domains.length : 0))
const domainColumns = computed<DataTableColumns<DomainRow>>(() => [
  { title: '名次', key: 'rank', width: 84, className: 'rank', render: (row) => ranks.value.get(row.key) ?? '' },
  {
    title: group.value ? '服务 / 主域名' : '主域名', key: 'domain', minWidth: 220,
    render: (row) => isServiceRow(row)
      ? h(NSpace, { size: 6, align: 'center', wrap: false }, () => [
        h(IconMenu, { domain: row.domain, service: row.service, fallbacks: iconFallbacks(row) }),
        h(NButton, { text: true, strong: true, type: row.service === service.value ? 'primary' : 'default', onClick: () => pickService(row.service ?? '') }, () => row.service),
        h(NTag, { size: 'small', bordered: false }, () => `${row.children?.length ?? 0} 个域名`),
      ])
      : h(NSpace, { size: 6, align: 'center', wrap: false }, () => [
        h(IconMenu, { domain: row.domain }),
        h(NButton, { text: true, type: !service.value && row.domain === users.value?.domain ? 'primary' : 'default', class: 'domain', onClick: () => pick(row.domain) }, () => row.domain),
        // 一级行显示所属服务；服务内的主域名不重复显示。
        row.service && ranks.value.has(row.key) ? h(NTag, { size: 'small', bordered: false }, () => row.service) : null,
        row.listed === 'global' ? h(NTag, { size: 'small', type: 'error', bordered: false }, () => '已拉黑') : null,
      ]),
  },
  { title: '放行次数', key: 'count', minWidth: 100, render: (row) => row.count.toLocaleString() },
  { title: '拦截次数', key: 'blocked', minWidth: 100, render: (row) => (row.blocked ? h(NText, { type: 'error' }, () => row.blocked.toLocaleString()) : '0') },
  { title: '用户数', key: 'users', minWidth: 80, render: (row) => row.users ?? 0 },
  {
    title: '占比', key: 'bar', minWidth: 140,
    render: (row) => h('div', { class: 'bar-cell' }, [
      h('span', { class: 'bar', style: { width: `${(row.count / peak.value) * 80}%` } }),
      row.blocked ? h('span', { class: 'bar blocked', style: { width: `${(row.blocked / peak.value) * 80}%` } }) : null,
    ]),
  },
  {
    title: '全局黑名单', key: 'action', width: 100, fixed: 'right',
    render: (row) => isServiceRow(row) ? h(NText, { depth: 3 }, () => '—') : h(NButton, { size: 'tiny', secondary: true, type: row.listed === 'global' ? 'default' : 'error', loading: busy.value === row.domain, onClick: () => toggleBlock(row) },
      () => (row.listed === 'global' ? '移出' : '拉黑')),
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
  { title: '放行次数', key: 'count', minWidth: 100, render: (row) => row.count.toLocaleString() },
  { title: '拦截次数', key: 'blocked', minWidth: 100, render: (row) => (row.blocked ? h(NText, { type: 'error' }, () => row.blocked.toLocaleString()) : '0') },
  { title: '有访问的天数', key: 'days', minWidth: 110 },
]
</script>

<template>
  <NSpace vertical :size="20">
    <AccessStatusNotice />
    <NCard size="small">
      <div class="access-filters">
        <AccessRangePicker v-model:period="period" v-model:custom="custom" :timezone="timezone" @change="(value) => (bounds = value)" />
        <label class="group-switch"><NSwitch v-model:value="group" size="small" /> 按服务归类</label>
        <NInput v-model:value="search" size="small" clearable placeholder="输入域名查看访问过它的用户" class="domain-search" @keyup.enter="pick(search)" @clear="pick('')" />
        <NButton size="small" type="primary" :disabled="!search.trim()" @click="pick(search)">查询</NButton>
      </div>
    </NCard>
    <div v-if="domain || service" ref="usersCard">
    <NCard size="small">
      <template #header>
        <span v-if="users && users.domain" class="users-title"><DomainIcon :domain="users.domain" :service="service" :fallbacks="usersIconFallbacks" :size="18" />访问 {{ service || users.domain }} 的用户</span>
        <template v-else>访问{{ service ? ` ${service} ` : '该域名' }}的用户</template>
      </template>
      <template #header-extra><NButton size="small" quaternary @click="pick('')">关闭</NButton></template>
      <NText v-if="users" depth="3" class="summary">
        {{ rangeText(users.from_day, users.to_day) }}<template v-if="service">；包括 {{ users.members?.length ? users.members.join('、') : '（范围内没有访问记录）' }}</template><template v-else-if="users.domain !== domain">；输入的 {{ domain }} 已合并为主域名 {{ users.domain }}</template><template v-if="!service && users.service">；属于服务 <NButton text type="primary" size="small" @click="pickService(users.service)">{{ users.service }}</NButton></template>。
      </NText>
      <NDataTable :columns="userColumns" :data="users?.users ?? []" :loading="usersLoading" :row-key="(row: UserRow) => row.user_id" :pagination="{ pageSize: 20 }" :scroll-x="480" size="small" />
    </NCard>
    </div>
    <NGrid cols="2 m:3" responsive="screen" :x-gap="20" :y-gap="20">
      <NGridItem><NCard size="small"><NStatistic label="放行次数" :value="(top?.total ?? 0).toLocaleString()" /></NCard></NGridItem>
      <NGridItem><NCard size="small"><NStatistic label="拦截次数" :value="(top?.blocked ?? 0).toLocaleString()" /></NCard></NGridItem>
      <NGridItem><NCard size="small"><NStatistic label="主域名个数" :value="top?.domain_count ?? 0" /></NCard></NGridItem>
    </NGrid>
    <NCard :title="group ? '服务与主域名排行' : '主域名排行'" size="small">
      <template #header-extra>
        <NButton size="small" :disabled="!top?.domains.length" @click="exportCSV">导出 CSV</NButton>
      </template>
      <NText v-if="top" depth="3" class="summary">
        {{ rangeText(top.from_day, top.to_day) }}，全部用户合计；点击服务或域名查看访问过它的用户<template v-if="group">，展开服务查看各主域名</template>。「拉黑」把主域名及其全部子域名加入全局域名黑名单，应用配置后生效<template v-if="hidden > 0">。只列出前 {{ top.domains.length }} 行，完整列表请导出 CSV</template>。
      </NText>
      <NDataTable :columns="domainColumns" :data="rows" :loading="topLoading" :row-key="(row: DomainRow) => row.key" :indent="20" :pagination="{ pageSize: 20 }" :scroll-x="900" size="small" />
    </NCard>
  </NSpace>
</template>

<style scoped>
.access-filters { display: flex; flex-wrap: wrap; align-items: center; gap: 12px 16px; }
.domain-search { width: 280px; }
.group-switch { display: inline-flex; align-items: center; gap: 6px; font-size: 13px; cursor: pointer; white-space: nowrap; }
.users-title { display: inline-flex; align-items: center; gap: 8px; }
.summary { display: block; font-size: 12px; margin-bottom: 8px; }
:deep(.rank) { white-space: nowrap; }
:deep(.domain) { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-all; }
:deep(.bar-cell) { display: flex; align-items: center; }
:deep(.bar) { height: 8px; min-width: 2px; border-radius: 4px; background: var(--ps-accent); }
:deep(.bar.blocked) { margin-left: 2px; background: var(--ps-danger); }
@media (max-width: 600px) {
  .domain-search { width: 100%; }
}
</style>
