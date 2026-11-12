<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { NAlert, NButton, NCard, NDataTable, NRadioButton, NRadioGroup, NTag, NText, useMessage, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { formatTime } from '../util/format'
import EntityLink from '../components/EntityLink.vue'

type Item = Schemas['Notification']

const message = useMessage()
const items = ref<Item[]>([])
const loading = ref(true)
const status = ref<'all' | Item['status']>('all')
/** 邮件通知总开关；读取失败时为 null，不显示提示。 */
const notifyEnabled = ref<boolean | null>(null)

async function load() {
  loading.value = true
  const [{ data }, { data: settings }] = await Promise.all([
    api.GET('/api/notifications', { params: { query: { limit: 500 } } }),
    api.GET('/api/config/settings'),
  ])
  items.value = data ?? []
  notifyEnabled.value = settings ? settings.notify.enabled : null
  loading.value = false
}
onMounted(load)

async function resend(id: number) {
  const { error } = await api.POST('/api/notifications/{id}/resend', { params: { path: { id } } })
  if (error) {
    message.error(problemMessage(error))
    return
  }
  message.success('已放回发送队列')
  load()
}

const statusView: Record<Item['status'], { label: string; type: 'default' | 'info' | 'success' | 'error' }> = {
  pending: { label: '等待发送', type: 'info' },
  sent: { label: '已发送', type: 'success' },
  failed: { label: '发送失败', type: 'error' },
  skipped: { label: '未发送', type: 'default' },
}

/** 说明列：失败时显示错误，未发送时显示记录时写明的原因；早期记录没有写原因，只能给出两种可能。 */
function note(row: Item): string {
  if (row.error) return row.error
  if (row.status === 'skipped') return '邮件通知未开启或未订阅此事件，只记录不发送'
  return ''
}

const rows = computed(() => (status.value === 'all' ? items.value : items.value.filter((item) => item.status === status.value)))
const columns: DataTableColumns<Item> = [
  { title: '时间', key: 'at', width: 180, render: (row) => formatTime(row.at) },
  { title: '事件', key: 'label' },
  { title: '关联对象', key: 'user_name', render: (row) => row.stack_id
    ? h(EntityLink, { kind: 'stack', id: row.stack_id, label: row.stack_name })
    : h(EntityLink, { kind: 'user', id: row.user_id, label: row.user_name }) },
  {
    title: '状态', key: 'status', width: 100,
    render: (row) => h(NTag, { size: 'small', type: statusView[row.status].type }, { default: () => statusView[row.status].label }),
  },
  { title: '尝试次数', key: 'attempts', width: 90 },
  {
    title: '说明', key: 'error', ellipsis: { tooltip: true },
    render: (row) => (row.status === 'skipped' ? h(NText, { depth: 3 }, () => note(row)) : note(row)),
  },
  {
    title: '', key: 'actions', width: 80,
    render: (row) => (row.status === 'failed'
      ? h(NButton, { size: 'tiny', onClick: () => resend(row.id) }, { default: () => '重发' })
      : null),
  },
]
</script>

<template>
  <NCard title="发送记录" class="table-panel">
    <template #header-extra>
      <NRadioGroup v-model:value="status" size="small">
        <NRadioButton value="all">全部</NRadioButton>
        <NRadioButton value="failed">发送失败</NRadioButton>
        <NRadioButton value="pending">等待发送</NRadioButton>
        <NRadioButton value="sent">已发送</NRadioButton>
        <NRadioButton value="skipped">未发送</NRadioButton>
      </NRadioGroup>
    </template>
    <NAlert v-if="notifyEnabled === false" type="info" :bordered="false" style="margin-bottom: 12px">
      邮件通知未开启，事件只记录、不发送。可以在
      <RouterLink :to="{ name: 'settings', query: { section: 'notify' } }">系统设置 → 通知</RouterLink>
      中开启并选择要发送的事件。
    </NAlert>
    <NDataTable :columns="columns" :data="rows" :loading="loading" :row-key="(row: Item) => row.id" :scroll-x="960" :pagination="{ pageSize: 50 }" />
  </NCard>
</template>
