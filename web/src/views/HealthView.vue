<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NDataTable, NSpace, NTag, NText, type DataTableColumns } from 'naive-ui'
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

const count = (status: Check['status']) => checks.value.filter((check) => check.status === status).length
const summary = computed(() => `${count('ok')} 项通过，${count('warn')} 项警告，${count('fail')} 项失败`)
const problems = computed(() => count('warn') + count('fail'))
/** 通过的检查项默认收起，只列出需要处理的；点击后展开全部。 */
const showPassed = ref(false)
/** 有问题的排在前面，同一状态内保持分组顺序。 */
const rows = computed(() => {
  const order = { fail: 0, warn: 1, ok: 2 }
  return [...checks.value]
    .filter((check) => showPassed.value || check.status !== 'ok')
    .sort((a, b) => order[a.status] - order[b.status])
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
    <template v-if="!loading">
      <NAlert v-if="!problems" type="success" :bordered="false" style="margin-bottom: 12px">全部 {{ checks.length }} 项检查通过，没有需要处理的事项。</NAlert>
      <NAlert v-else :type="count('fail') ? 'error' : 'warning'" :bordered="false" style="margin-bottom: 12px">
        {{ problems }} 项需要处理，请按下方的处理建议操作。
      </NAlert>
    </template>
    <NDataTable v-if="rows.length || loading" :columns="columns" :data="rows" :loading="loading" :row-key="(row: Check) => row.group + row.title" :scroll-x="1100" />
    <NButton v-if="!loading && count('ok')" text type="primary" style="margin-top: 12px" @click="showPassed = !showPassed">
      {{ showPassed ? '收起通过的检查项' : `显示通过的 ${count('ok')} 项` }}
    </NButton>
  </NCard>
</template>
