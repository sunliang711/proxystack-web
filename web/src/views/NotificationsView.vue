<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { NButton, NCard, NDataTable, NRadioButton, NRadioGroup, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { formatTime } from '../util/format'
import EntityLink from '../components/EntityLink.vue'

type Item = Schemas['Notification']

const message = useMessage()
const items = ref<Item[]>([])
const loading = ref(true)
const status = ref<'all' | Item['status']>('all')

async function load() {
  loading.value = true
  const { data } = await api.GET('/api/notifications', { params: { query: { limit: 500 } } })
  items.value = data ?? []
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

const rows = computed(() => (status.value === 'all' ? items.value : items.value.filter((item) => item.status === status.value)))
const columns: DataTableColumns<Item> = [
  { title: '时间', key: 'at', width: 180, render: (row) => formatTime(row.at) },
  { title: '事件', key: 'label' },
  { title: '用户', key: 'user_name', render: (row) => h(EntityLink, { kind: 'user', id: row.user_id, label: row.user_name }) },
  {
    title: '状态', key: 'status', width: 100,
    render: (row) => h(NTag, { size: 'small', type: statusView[row.status].type }, { default: () => statusView[row.status].label }),
  },
  { title: '尝试次数', key: 'attempts', width: 90 },
  { title: '错误', key: 'error', ellipsis: { tooltip: true } },
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
    <NDataTable :columns="columns" :data="rows" :loading="loading" :row-key="(row: Item) => row.id" :scroll-x="960" :pagination="{ pageSize: 50 }" />
  </NCard>
</template>
