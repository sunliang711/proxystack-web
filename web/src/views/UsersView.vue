<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import {
  NButton, NCard, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NModal, NPopconfirm, NSelect, NSpace, NTag, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { api, emptyErrors, fieldState, problemMessage, splitProblem, type FormErrors, type Schemas } from '../api/client'
import ErrorList from '../components/ErrorList.vue'
import ExportDialog from '../components/ExportDialog.vue'
import ImportDialog from '../components/ImportDialog.vue'
import SubscriptionCard from '../components/SubscriptionCard.vue'
import { formatBytes } from '../util/format'

type User = Schemas['User']
type Usage = Schemas['UserUsage']

const pauseLabels = { daily: '每日阈值', quota: '额度', expired: '到期' } as const

const router = useRouter()
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
onMounted(load)

const autoPauses = (user: User) => usage.value[user.id ?? 0]?.auto_pauses ?? []
const rows = computed(() => users.value.filter((user) => {
  if (filter.value === 'auto') return autoPauses(user).length > 0
  if (filter.value === 'paused') return !user.enabled || autoPauses(user).length > 0
  return true
}))
const used = (bytes?: { up: number; down: number }) => formatBytes((bytes?.up ?? 0) + (bytes?.down ?? 0))

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

const columns: DataTableColumns<User> = [
  { type: 'selection' },
  {
    title: '名称',
    key: 'name',
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
  { title: '备注', key: 'remark' },
  { title: '今日用量', key: 'today', render: (row) => used(usage.value[row.id ?? 0]?.today) },
  { title: '本周期用量', key: 'period', render: (row) => used(usage.value[row.id ?? 0]?.period) },
  { title: '额度', key: 'quota_bytes', width: 90, render: (row) => (row.quota_bytes ? formatBytes(row.quota_bytes) : '不限') },
  { title: '重置', key: 'reset_mode', width: 100, render: (row) => (row.reset_mode === 'monthly' ? `每月 ${row.reset_day} 日` : '不重置') },
  { title: '到期', key: 'expire_at', render: (row) => (row.expire_at ? new Date(row.expire_at).toLocaleString() : '—') },
  { title: '档案', key: 'profiles', width: 60, align: 'center', render: (row) => String(row.profiles.length + 1) },
  {
    title: '订阅',
    key: 'subscription',
    width: 100,
    render: (row) => h(NButton, { text: true, type: 'primary', size: 'small', 'aria-label': `查看 ${row.name} 的订阅`, onClick: () => { subscriptionUser.value = row } }, { default: () => '查看链接' }),
  },
]

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
        <NButton type="primary" @click="(creating = true), (draft = { name: '', remark: '' }), (errors = emptyErrors())">新建用户</NButton>
      </NSpace>
    </template>
    <NDataTable
      v-model:checked-row-keys="checked"
      :columns="columns"
      :data="rows"
      :loading="loading"
      :row-key="rowKey"
      :scroll-x="1100"
      :pagination="{ pageSize: 50 }"
    />
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
