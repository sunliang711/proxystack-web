<script setup lang="ts">
import { computed, h, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { NAlert, NButton, NCard, NDataTable, NSelect, NSpace, NTag, NText, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { usePolling } from '../composables/usePolling'
import { taskCenter, taskKey, taskRoute, type CenterTask } from '../tasks'
import { formatTime } from '../util/format'
import { taskActor, taskDuration, taskStatus } from '../util/task'

const status = ref<'all' | CenterTask['status']>('all')
const source = ref<'all' | CenterTask['source']>('all')
const page = ref(1)
const pageSize = 20
const now = ref(Date.now())
const timer = setInterval(() => { now.value = Date.now() }, 1000)
onBeforeUnmount(() => clearInterval(timer))
const { data, loading, error, refresh } = usePolling<Schemas['TaskCenterPage']>(() => [status.value, source.value, page.value], async (signal) => {
  const { data, error } = await api.GET('/api/task-center', { params: { query: { source: source.value, status: status.value, limit: pageSize, offset: (page.value - 1) * pageSize } }, signal })
  if (!data) throw new Error(problemMessage(error, '任务列表加载失败'))
  return data
}, 5000)
const statusOptions = [{ label: '全部状态', value: 'all' }, ...Object.entries(taskStatus).map(([value, view]) => ({ label: view.label, value }))]
const sourceOptions = [{ label: '全部来源', value: 'all' }, { label: '后台作业', value: 'task' }, { label: '定时任务', value: 'job' }]
const columns: DataTableColumns<CenterTask> = [
  { title: '任务', key: 'title', minWidth: 220, render: (task) => h('div', [
    h(RouterLink, { to: taskRoute(task) }, () => task.title),
    h('div', [h(NText, { depth: 3 }, () => `${task.source === 'job' ? '定时任务' : '后台作业'} · #${task.id}`)]),
  ]) },
  { title: '状态', key: 'status', width: 100, render: (task) => h(NTag, { round: true, size: 'small', type: taskStatus[task.status].type }, () => taskStatus[task.status].label) },
  { title: '执行者', key: 'actor', width: 110, render: taskActor },
  { title: '开始时间', key: 'started_at', width: 180, render: (task) => formatTime(task.started_at) },
  { title: '耗时', key: 'duration', width: 125, render: (task) => taskDuration(task, now.value) },
  { title: '最近输出 / 错误', key: 'log', minWidth: 260, ellipsis: { tooltip: true }, render: (task) => task.error || task.log.at(-1) || (task.status === 'running' ? '执行中，等待输出…' : '—') },
  { title: '', key: 'actions', width: 95, render: (task) => h(NButton, { size: 'tiny', onClick: () => taskCenter.open(task) }, () => '查看进度') },
]
const pagination = computed(() => ({ page: page.value, pageSize, itemCount: data.value?.total ?? 0, onChange: (value: number) => { page.value = value } }))
</script>

<template>
  <NCard size="small">
    <NSpace justify="space-between" align="center" style="margin-bottom: 20px">
      <NSpace align="center">
        <NSelect v-model:value="status" :options="statusOptions" style="width: 150px" aria-label="任务状态" @update:value="page = 1" />
        <NSelect v-model:value="source" :options="sourceOptions" style="width: 150px" aria-label="任务来源" @update:value="page = 1" />
      </NSpace>
      <NSpace align="center"><NText depth="3">{{ data?.running ?? taskCenter.state.running }} 个运行中 · 每 5 秒刷新</NText><NButton size="small" :loading="loading" @click="refresh">刷新</NButton></NSpace>
    </NSpace>
    <NAlert v-if="error" type="warning" :bordered="false" style="margin-bottom: 16px">{{ error }}<template v-if="data">；当前显示上次成功读取的数据</template></NAlert>
    <NDataTable remote :columns="columns" :data="data?.items ?? []" :row-key="taskKey" :loading="loading && !data" :pagination="pagination" :scroll-x="1100" size="small" />
  </NCard>
</template>
