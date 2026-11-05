<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  NButton, NCard, NDataTable, NDrawer, NDrawerContent, NEmpty, NForm, NFormItem, NInput, NModal, NPopconfirm, NSelect, NSpace, NTag, NText, NTooltip, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { api, emptyErrors, fieldState, problemMessage, splitProblem, type FormErrors, type Schemas } from '../api/client'
import ErrorList from '../components/ErrorList.vue'
import ExportDialog from '../components/ExportDialog.vue'
import ImportDialog from '../components/ImportDialog.vue'
import SubscriptionCard from '../components/SubscriptionCard.vue'
import { useMobile } from '../composables/useMobile'
import { formatBytes } from '../util/format'
import { tipTitle } from '../util/table'

type User = Schemas['User']
type Usage = Schemas['UserUsage']

const pauseLabels = { daily: '每日阈值', quota: '额度', expired: '到期' } as const

const router = useRouter()
const route = useRoute()
const mobile = useMobile()
const message = useMessage()
const users = ref<User[]>([])
const usage = ref<Record<number, Usage>>({})
const filter = ref<'all' | 'paused' | 'auto'>('all')
const loading = ref(true)
const checked = ref<string[]>([])
const creating = ref(false)
const draft = ref({ name: '', remark: '' })
const errors = ref<FormErrors>(emptyErrors())
const showImport = ref(false)
const showExport = ref(false)
const subscriptionUser = ref<User | null>(null)

async function load() {
  loading.value = true
  const [{ data }, { data: usageData }] = await Promise.all([api.GET('/api/users'), api.GET('/api/traffic/users')])
  users.value = data?.users ?? []
  usage.value = Object.fromEntries((usageData ?? []).map((item) => [item.user_id, item]))
  loading.value = false
}
onMounted(() => {
  load()
  // 初始化清单等入口用 ?create=1 直接打开新建抽屉。
  if (route.query.create) {
    openCreate()
    router.replace({ query: { ...route.query, create: undefined } })
  }
})

function openCreate() {
  draft.value = { name: '', remark: '' }
  errors.value = emptyErrors()
  creating.value = true
}

const autoPauses = (user: User) => usage.value[user.id ?? 0]?.auto_pauses ?? []
const rows = computed(() => users.value.filter((user) => {
  if (filter.value === 'auto') return autoPauses(user).length > 0
  if (filter.value === 'paused') return !user.enabled || autoPauses(user).length > 0
  return true
}))
const sum = (bytes?: { up: number; down: number }) => (bytes?.up ?? 0) + (bytes?.down ?? 0)
const used = (bytes?: { up: number; down: number }) => formatBytes(sum(bytes))
const todayOf = (user: User) => sum(usage.value[user.id ?? 0]?.today)
const periodOf = (user: User) => sum(usage.value[user.id ?? 0]?.period)
/** 限额单元格：限额值下方显示已用百分比，80% 起标黄，达到限额标红。 */
function limitCell(limit: number, usedBytes: number, label: string) {
  if (!limit) return '不限'
  const percent = Math.round((usedBytes / limit) * 100)
  return h('div', [
    h('div', formatBytes(limit)),
    h(NText, { depth: percent >= 80 ? undefined : 3, type: percent >= 100 ? 'error' : percent >= 80 ? 'warning' : 'default', style: 'font-size: 12px' }, () => `${label} ${percent}%`),
  ])
}
const usageTitle = computed(() => {
  if (!rows.value.length) return '用量'
  if (rows.value.every((user) => user.reset_mode !== 'monthly')) return '累计用量'
  if (rows.value.every((user) => user.reset_mode === 'monthly')) return '本周期用量'
  return '周期 / 累计用量'
})

/** 批量切换手动暂停开关，逐 stack 的暂停保持不变。 */
async function setEnabled(enabled: boolean) {
  const targets = users.value.filter((user) => checked.value.includes(user.name))
  const succeeded: string[] = []
  const failed: string[] = []
  for (const user of targets) {
    const { error } = await api.PUT('/api/users/{id}/pause', {
      params: { path: { id: user.id! } }, body: { enabled, paused_stacks: user.paused_stacks ?? [] },
    })
    if (error) {
      failed.push(`${user.name}：${problemMessage(error)}`)
    } else {
      succeeded.push(user.name)
    }
  }
  if (failed.length) {
    message.warning(`已处理 ${succeeded.length}/${targets.length} 个用户；失败：${failed.join('；')}`)
  } else {
    message.success(enabled ? '已恢复所选用户' : '已暂停所选用户')
  }
  load()
}

/** 窄屏只显示这些列，其余字段在用户详情页查看。 */
const mobileKeys = new Set(['name', 'enabled', 'today', 'period', 'quota_bytes', 'expire_at', 'subscription'])

const allColumns: DataTableColumns<User> = [
  { type: 'selection', fixed: 'left' },
  {
    title: '名称',
    key: 'name',
    width: 120,
    fixed: 'left',
    ellipsis: { tooltip: true },
    render: (row) => h(RouterLink, { to: { name: 'user', params: { id: row.id } } }, { default: () => row.name }),
  },
  {
    title: '状态',
    key: 'enabled',
    width: 100,
    render: (row) => {
      const pauses = autoPauses(row)
      if (row.enabled && pauses.length) {
        return h(NTag, { size: 'small', type: 'warning' }, { default: () => pauses.map((pause) => pauseLabels[pause.reason]).join('、') + '暂停' })
      }
      return h(NTag, { size: 'small', type: row.enabled ? 'success' : 'default' }, { default: () => (row.enabled ? '启用' : '停用') })
    },
  },
  { title: '今日用量', key: 'today', width: 120, sorter: (a, b) => todayOf(a) - todayOf(b), render: (row) => used(usage.value[row.id ?? 0]?.today) },
  { title: () => usageTitle.value, key: 'period', width: 150, sorter: (a, b) => periodOf(a) - periodOf(b), render: (row) => h(NTooltip, null, {
    trigger: () => h('span', { tabindex: 0 }, used(usage.value[row.id ?? 0]?.period)),
    default: () => row.reset_mode === 'monthly' ? `本周期用量，每月 ${row.reset_day} 日重置` : '累计用量：不按周期重置，包含历史流量',
  }) },
  { title: tipTitle('每日阈值', '当日用量（上行与下行合计，全部 stack）达到阈值后，按用户详情中的设置暂停或只通知；暂停在次日自动恢复。'), key: 'daily_limit_bytes', width: 110, render: (row) => limitCell(row.daily_limit_bytes ?? 0, todayOf(row), '今日') },
  { title: tipTitle('额度', '流量总额度。用量达到额度后自动暂停；到重置日或调高额度后自动恢复。'), key: 'quota_bytes', width: 110, sorter: (a, b) => (a.quota_bytes ? periodOf(a) / a.quota_bytes : -1) - (b.quota_bytes ? periodOf(b) / b.quota_bytes : -1), render: (row) => limitCell(row.quota_bytes, periodOf(row), '已用') },
  { title: '额度重置', key: 'reset_mode', width: 100, render: (row) => (row.reset_mode === 'monthly' ? `每月 ${row.reset_day} 日` : '不重置') },
  { title: '到期', key: 'expire_at', render: (row) => (row.expire_at ? new Date(row.expire_at).toLocaleString() : '—') },
  { title: tipTitle('凭据档案', '该用户的凭据档案数量，含用户本身的 default 档案。入站引用用户时可以改选其他档案，使用不同的 UUID 或密码。'), key: 'profiles', width: 100, align: 'center', render: (row) => String(row.profiles.length + 1) },
  {
    title: '订阅',
    key: 'subscription',
    width: 100,
    render: (row) => h(NButton, { text: true, type: 'primary', size: 'small', 'aria-label': `查看 ${row.name} 的订阅`, onClick: () => { subscriptionUser.value = row } }, { default: () => '查看链接' }),
  },
  { title: '备注', key: 'remark' },
]
const columns = computed(() => mobile.value ? allColumns.filter((column) => column.type === 'selection' || ('key' in column && mobileKeys.has(String(column.key)))) : allColumns)

const rowKey = (row: User) => row.name
const selectedNames = computed(() => checked.value)

async function create() {
  const body: User = {
    name: draft.value.name, remark: draft.value.remark, enabled: true, uuid: '', ss_password: '', display_template: '',
    tag: '', sub_token: '', quota_bytes: 0, reset_mode: 'none', reset_day: 0, expire_at: '', note: '', profiles: [],
  }
  const { data, error } = await api.POST('/api/users', { body })
  if (error) {
    errors.value = splitProblem(error)
    return
  }
  creating.value = false
  message.success('已创建，UUID、密码和订阅 token 已随机生成')
  router.push({ name: 'user', params: { id: data!.id } })
}
</script>

<template>
  <NCard title="用户列表" class="table-panel">
    <template #header-extra>
      <NSpace justify="end">
        <NSelect
          v-model:value="filter"
          :options="[{ label: '全部用户', value: 'all' }, { label: '已暂停', value: 'paused' }, { label: '自动暂停', value: 'auto' }]"
          style="width: 120px"
        />
        <NPopconfirm @positive-click="setEnabled(false)">
          <template #trigger><NButton :disabled="!selectedNames.length">暂停所选</NButton></template>
          在全部 stack 上暂停所选的 {{ selectedNames.length }} 个用户？
        </NPopconfirm>
        <NButton :disabled="!selectedNames.length" @click="setEnabled(true)">恢复所选</NButton>
        <NButton @click="showImport = true">导入</NButton>
        <NButton :disabled="!selectedNames.length" @click="showExport = true">导出所选</NButton>
        <NButton type="primary" @click="openCreate">新建用户</NButton>
      </NSpace>
    </template>
    <NDataTable
      v-model:checked-row-keys="checked"
      :columns="columns"
      :data="rows"
      :loading="loading"
      :row-key="rowKey"
      :scroll-x="mobile ? 760 : 1200"
      :pagination="{ pageSize: 50 }"
    >
      <template #empty>
        <div v-if="loading" />
        <NEmpty v-else-if="filter === 'all'" description="还没有用户。Stack 的入站引用用户，先创建一个用户。">
          <template #extra><NButton size="small" type="primary" @click="openCreate">创建第一个用户</NButton></template>
        </NEmpty>
        <NEmpty v-else description="没有符合条件的用户" />
      </template>
    </NDataTable>
    <NText v-if="mobile && rows.length" depth="3" class="mobile-table-note">窄屏只显示主要字段，左右滑动查看更多；完整信息在用户详情页。</NText>
  </NCard>

  <NDrawer v-model:show="creating" :width="420">
    <NDrawerContent title="新建用户" closable>
      <ErrorList :errors="errors.global" />
      <NForm label-placement="top">
        <NFormItem label="名称" required v-bind="fieldState(errors, 'name', '字母或数字开头，可含 _ . -')">
          <NInput v-model:value="draft.name" />
        </NFormItem>
        <NFormItem label="备注" feedback="仅用于管理识别，不影响订阅节点名">
          <NInput v-model:value="draft.remark" />
        </NFormItem>
      </NForm>
      <NText depth="3" tag="p" style="font-size: 12px; margin: 0">
        UUID、密码和订阅 token 会随机生成。创建后自动进入用户详情页，可以在那里设置流量额度、每日阈值和到期时间。
      </NText>
      <template #footer>
        <NButton type="primary" :disabled="!draft.name" @click="create">创建</NButton>
      </template>
    </NDrawerContent>
  </NDrawer>

  <NModal :show="!!subscriptionUser" preset="card" :title="`${subscriptionUser?.name ?? ''} 的订阅`" style="width: 780px; max-width: calc(100vw - 32px)" @update:show="(show) => !show && (subscriptionUser = null)">
    <SubscriptionCard v-if="subscriptionUser" :key="subscriptionUser.id" :user-id="subscriptionUser.id!" links-only />
  </NModal>

  <ImportDialog v-model:show="showImport" scope="users" allow-csv @done="load" />
  <ExportDialog v-model:show="showExport" scope="users" :users="selectedNames" allow-csv />
</template>
