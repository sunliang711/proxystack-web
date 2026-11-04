<script setup lang="ts">
import { computed, h, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { NAlert, NButton, NCard, NCheckbox, NDataTable, NEmpty, NSelect, NSpace, NTag, NText, NTooltip, type DataTableColumns } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { usePolling } from '../composables/usePolling'
import { taskCenter, taskKey, taskRoute, type CenterTask } from '../tasks'
import { formatTime } from '../util/format'
import { taskActor, taskDuration, taskStatus } from '../util/task'

type TaskKind = 'all' | 'apply' | 'backup' | 'deps' | 'self-update' | 'db_backup' | 'traffic_prune' | 'geo_update' | 'deps_check' | 'deps_upgrade' | 'remote_sync' | 'reconcile' | 'egress_check'

const status = ref<'all' | CenterTask['status']>('all')
const source = ref<'all' | CenterTask['source']>('all')
const kind = ref<TaskKind>('all')
// 出口检测、对账等例行任务每隔几分钟执行一次，默认隐藏它们成功的记录，失败和手动执行的记录照常显示。
const hideScheduledOK = ref(true)
/** 只在没有按状态或任务类型筛选时生效，按类型或状态筛选时显示完整记录。 */
const hideApplies = computed(() => status.value === 'all' && kind.value === 'all')
const page = ref(1)
const pageSize = 20
const now = ref(Date.now())
const timer = setInterval(() => { now.value = Date.now() }, 1000)
onBeforeUnmount(() => clearInterval(timer))
const { data, loading, error, refresh } = usePolling<Schemas['TaskCenterPage']>(() => [status.value, source.value, kind.value, hideScheduledOK.value, page.value], async (signal) => {
  const query = {
    source: source.value, status: status.value, kind: kind.value, hide_scheduled_ok: hideScheduledOK.value && hideApplies.value,
    limit: pageSize, offset: (page.value - 1) * pageSize,
  }
  const { data, error } = await api.GET('/api/task-center', { params: { query }, signal })
  if (!data) throw new Error(problemMessage(error, '任务列表加载失败'))
  return data
}, 5000)
const statusOptions = [{ label: '全部状态', value: 'all' }, ...Object.entries(taskStatus).map(([value, view]) => ({ label: view.label, value }))]
const sourceOptions = [{ label: '全部来源', value: 'all' }, { label: '后台作业', value: 'task' }, { label: '定时任务', value: 'job' }]
const allKindOptions = [
  { label: '全部任务', value: 'all' },
  { label: '应用配置', value: 'apply', source: 'task' },
  { label: '导出迁移包', value: 'backup', source: 'task' },
  { label: '依赖操作', value: 'deps', source: 'task' },
  { label: 'proxystack-web 更新', value: 'self-update', source: 'task' },
  { label: '数据库备份', value: 'db_backup', source: 'job' },
  { label: '流量数据清理', value: 'traffic_prune', source: 'job' },
  { label: 'geo 数据更新', value: 'geo_update', source: 'job' },
  { label: '依赖版本检查', value: 'deps_check', source: 'job' },
  { label: '依赖自动升级', value: 'deps_upgrade', source: 'job' },
  { label: '远端节点源同步', value: 'remote_sync', source: 'job' },
  { label: '对账', value: 'reconcile', source: 'job' },
  { label: 'Stack 出口检测', value: 'egress_check', source: 'job' },
]
const kindOptions = computed(() => allKindOptions.filter((option) => !option.source || source.value === 'all' || option.source === source.value))
function onSourceChange() {
  kind.value = 'all'
  page.value = 1
}
const columns: DataTableColumns<CenterTask> = [
  { title: '任务', key: 'title', width: 260, render: (task) => h('div', [
    h(RouterLink, { to: taskRoute(task) }, () => task.title),
    h('div', [h(NText, { depth: 3 }, () => `${task.source === 'job' ? '定时任务' : '后台作业'} · #${task.id}`)]),
  ]) },
  { title: '状态', key: 'status', width: 100, render: (task) => h(NTag, { round: true, size: 'small', type: taskStatus[task.status].type }, () => taskStatus[task.status].label) },
  { title: '执行者', key: 'actor', width: 110, render: taskActor },
  { title: '开始时间', key: 'started_at', width: 180, render: (task) => formatTime(task.started_at) },
  { title: '耗时', key: 'duration', width: 125, render: (task) => taskDuration(task, now.value) },
  { title: '最近输出 / 错误', key: 'log', minWidth: 260, ellipsis: { tooltip: true }, render: (task) => task.error || task.log.at(-1) || (task.status === 'running' ? '执行中，等待输出…' : '—') },
  { title: '', key: 'actions', width: 95, render: (task) => h(NButton, { size: 'tiny', onClick: () => taskCenter.open(task) }, () => (task.status === 'running' ? '查看进度' : '查看详情')) },
]
const pagination = computed(() => ({ page: page.value, pageSize, itemCount: data.value?.total ?? 0, onChange: (value: number) => { page.value = value } }))
</script>

<template>
  <NCard size="small">
    <NSpace justify="space-between" align="center" style="margin-bottom: 20px">
      <NSpace align="center">
        <NSelect v-model:value="status" :options="statusOptions" style="width: 150px" aria-label="任务状态" @update:value="page = 1" />
        <NSelect v-model:value="source" :options="sourceOptions" style="width: 150px" aria-label="任务来源" @update:value="onSourceChange" />
        <NSelect v-model:value="kind" :options="kindOptions" filterable style="width: 190px" aria-label="任务类型" @update:value="page = 1" />
        <NTooltip>
          <template #trigger>
            <NCheckbox v-model:checked="hideScheduledOK" :disabled="!hideApplies" @update:checked="page = 1">隐藏成功的计划任务</NCheckbox>
          </template>
          {{ hideApplies ? '不显示按计划自动执行且成功或跳过的定时任务，失败的和手动执行的仍然显示' : '按状态或任务类型筛选时显示全部记录' }}
        </NTooltip>
      </NSpace>
      <NSpace align="center"><NText depth="3">{{ data?.running ?? taskCenter.state.running }} 个运行中 · 每 5 秒刷新</NText><NButton size="small" :loading="loading" @click="refresh">刷新</NButton></NSpace>
    </NSpace>
    <NAlert v-if="error" type="warning" :bordered="false" style="margin-bottom: 16px">{{ error }}<template v-if="data">；当前显示上次成功读取的数据</template></NAlert>
    <NDataTable remote :columns="columns" :data="data?.items ?? []" :row-key="taskKey" :loading="loading && !data" :pagination="pagination" :scroll-x="1100" size="small">
      <template #empty>
        <NEmpty :description="hideScheduledOK && hideApplies ? '没有需要关注的任务。成功的计划任务已隐藏，取消勾选「隐藏成功的计划任务」即可查看' : '没有符合条件的任务'" />
      </template>
    </NDataTable>
  </NCard>
</template>
