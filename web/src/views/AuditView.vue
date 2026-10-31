<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { NCard, NDataTable, NInput, NSelect, NSpace, type DataTableColumns } from 'naive-ui'
import { api, type Schemas } from '../api/client'
import { formatTime } from '../util/format'

type Entry = Schemas['AuditEntry']

const PAGE = 50
const items = ref<Entry[]>([])
const total = ref(0)
const page = ref(1)
const action = ref('')
const actor = ref('')
const loading = ref(true)
const actions = [
  { label: '全部动作', value: '' },
  { label: '用户暂停', value: 'user.' },
  { label: '配置', value: 'config.' },
  { label: '运行', value: 'runtime.' },
  { label: '依赖', value: 'deps.' },
  { label: '订阅发布', value: 'subscription.' },
  { label: '备份与恢复', value: 'backup.' },
  { label: '定时任务', value: 'job.' },
  { label: '登录', value: 'auth.' },
]

async function load() {
  loading.value = true
  const { data } = await api.GET('/api/audit', {
    params: { query: { action: action.value, actor: actor.value, limit: PAGE, offset: (page.value - 1) * PAGE } },
  })
  items.value = data?.items ?? []
  total.value = data?.total ?? 0
  loading.value = false
}
onMounted(load)
watch([action, actor], () => {
  if (page.value === 1) load()
  else page.value = 1
})
watch(page, load)

const columns: DataTableColumns<Entry> = [
  { title: '时间', key: 'at', width: 180, render: (row) => formatTime(row.at) },
  { title: '操作者', key: 'actor', width: 100 },
  { title: '来源 IP', key: 'source_ip', width: 130 },
  { title: '动作', key: 'action', width: 160 },
  { title: '对象', key: 'object', width: 180, ellipsis: { tooltip: true } },
  { title: '摘要', key: 'summary', minWidth: 280, ellipsis: { tooltip: true } },
]
</script>

<template>
  <NCard title="审计日志">
    <template #header-extra>
      <NSpace>
        <NSelect v-model:value="action" :options="actions" size="small" style="width: 140px" />
        <NInput v-model:value="actor" size="small" clearable placeholder="操作者" style="width: 140px" />
      </NSpace>
    </template>
    <NDataTable
      remote
      :columns="columns"
      :data="items"
      :loading="loading"
      :row-key="(row: Entry) => row.id"
      :scroll-x="1060"
      :pagination="{ page, pageSize: PAGE, itemCount: total, onUpdatePage: (value: number) => (page = value) }"
    />
  </NCard>
</template>
