<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { NButton, NCard, NDataTable, NSpace, NTag, NText, type DataTableColumns } from 'naive-ui'
import { api, type Schemas } from '../api/client'
import EntityLink from '../components/EntityLink.vue'

type Check = Schemas['HealthCheck']

const checks = ref<Check[]>([])
const loading = ref(true)
const view: Record<Check['status'], { label: string; type: 'success' | 'warning' | 'error' }> = {
  ok: { label: '通过', type: 'success' }, warn: { label: '警告', type: 'warning' }, fail: { label: '失败', type: 'error' },
}

async function load() {
  loading.value = true
  checks.value = (await api.GET('/api/health-checks')).data ?? []
  loading.value = false
}
onMounted(load)

const summary = computed(() => {
  const count = (status: Check['status']) => checks.value.filter((check) => check.status === status).length
  return `${count('ok')} 项通过，${count('warn')} 项警告，${count('fail')} 项失败`
})
/** 有问题的排在前面，同一状态内保持分组顺序。 */
const rows = computed(() => {
  const order = { fail: 0, warn: 1, ok: 2 }
  return [...checks.value].sort((a, b) => order[a.status] - order[b.status])
})
const columns: DataTableColumns<Check> = [
  { title: '状态', key: 'status', width: 80, render: (row) => h(NTag, { size: 'small', type: view[row.status].type }, { default: () => view[row.status].label }) },
  { title: '分组', key: 'group', width: 80 },
  {
    title: '检查项', key: 'title', width: 220,
    render: (row) => {
      const match = /^(HandlerService|采集)：(.+)$/.exec(row.title)
      return match ? h(EntityLink, { kind: 'stack', name: match[2], label: row.title }) : row.title
    },
  },
  { title: '结果', key: 'detail' },
  { title: '处理建议', key: 'hint' },
]
</script>

<template>
  <NCard title="检查结果" class="table-panel">
    <template #header-extra>
      <NSpace align="center" justify="space-between">
        <NText v-if="!loading" depth="3" class="header-note">{{ summary }}</NText>
        <NButton size="small" :loading="loading" @click="load">重新检查</NButton>
      </NSpace>
    </template>
    <NDataTable :columns="columns" :data="rows" :loading="loading" :row-key="(row: Check) => row.group + row.title" :scroll-x="1100" />
  </NCard>
</template>
