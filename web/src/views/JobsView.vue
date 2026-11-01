<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  NButton, NCard, NCode, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NSwitch, NTag, NText,
  useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, emptyErrors, fieldState, problemMessage, splitProblem, type FormErrors, type Schemas } from '../api/client'
import ErrorList from '../components/ErrorList.vue'
import { formatTime } from '../util/format'
import { onJobTaskEvent } from '../runtime'
import { taskCenter } from '../tasks'

type Job = Schemas['JobView']
type Run = Schemas['JobRun']

const message = useMessage()
const jobs = ref<Job[]>([])
const loading = ref(true)
const editing = ref<Job | null>(null)
const preset = ref('custom')
const preview = ref<string[]>([])
const previewError = ref('')
const errors = ref<FormErrors>(emptyErrors())
const history = ref<{ job: Job; runs: Run[] } | null>(null)

const presets = [
  { label: '每小时', value: '0 * * * *' },
  { label: '每天', value: '0 4 * * *' },
  { label: '每周', value: '0 5 * * 1' },
  { label: '每月', value: '0 4 1 * *' },
  { label: '自定义', value: 'custom' },
]
const planPresets = computed(() => editing.value?.kind === 'egress_check' ? [
  { label: '每分钟', value: '* * * * *' },
  { label: '每 2 分钟', value: '*/2 * * * *' },
  { label: '每 5 分钟', value: '*/5 * * * *' },
  ...presets,
] : presets)
const statusView: Record<Run['status'], { label: string; type: 'default' | 'info' | 'success' | 'warning' | 'error' }> = {
  running: { label: '执行中', type: 'info' },
  succeeded: { label: '成功', type: 'success' },
  failed: { label: '失败', type: 'error' },
  skipped: { label: '跳过', type: 'default' },
  timeout: { label: '超时', type: 'warning' },
}

async function load() {
  loading.value = true
  jobs.value = (await api.GET('/api/jobs')).data ?? []
  loading.value = false
}
onMounted(load)
const stopEvents = onJobTaskEvent((event) => { if (event.status !== 'running') void load() })
onBeforeUnmount(stopEvents)

async function run(job: Job) {
  const { error } = await api.POST('/api/jobs/{id}/run', { params: { path: { id: job.id } } })
  if (error) {
    message.error(problemMessage(error))
    return
  }
  message.success('已开始执行')
  void taskCenter.refresh()
  window.setTimeout(load, 1000)
}

async function toggle(job: Job, enabled: boolean) {
  const { error } = await api.PUT('/api/jobs/{id}', {
    params: { path: { id: job.id } }, body: { schedule: job.schedule, enabled, timeout: job.timeout, params: job.params },
  })
  if (error) message.error(problemMessage(error))
  load()
}

function edit(job: Job) {
  editing.value = JSON.parse(JSON.stringify(job))
  preset.value = planPresets.value.find((item) => item.value === job.schedule)?.value ?? 'custom'
  errors.value = emptyErrors()
}

watch(preset, (value) => {
  if (editing.value && value !== 'custom') editing.value.schedule = value
})
watch(() => editing.value?.schedule, async (schedule) => {
  if (!schedule) return
  const { data, error } = await api.POST('/api/jobs/preview', { body: { schedule } })
  previewError.value = error ? (error.errors?.[0]?.message ?? problemMessage(error)) : ''
  preview.value = (data ?? []).map(formatTime)
})

async function save() {
  const job = editing.value
  if (!job) return
  const { error } = await api.PUT('/api/jobs/{id}', {
    params: { path: { id: job.id } }, body: { schedule: job.schedule, enabled: job.enabled, timeout: job.timeout, params: job.params },
  })
  if (error) {
    errors.value = splitProblem(error)
    return
  }
  editing.value = null
  message.success('已保存')
  load()
}

async function showHistory(job: Job) {
  const { data } = await api.GET('/api/jobs/{id}/runs', { params: { path: { id: job.id } } })
  history.value = { job, runs: data ?? [] }
}

const columns: DataTableColumns<Job> = [
  { title: '任务', key: 'name', minWidth: 260, render: (row) => h('div', [h('div', row.name), h(NText, { depth: 3, style: 'font-size: 12px' }, { default: () => row.description })]) },
  { title: '计划', key: 'schedule', width: 130, render: (row) => h(NText, { code: true, style: 'white-space: nowrap' }, { default: () => row.schedule }) },
  { title: '下次执行', key: 'next', width: 180, render: (row) => (row.enabled && row.next_runs[0] ? formatTime(row.next_runs[0]) : '—') },
  {
    title: '最近一次', key: 'last', width: 200,
    render: (row) => {
      if (!row.last_run) return '—'
      const view = statusView[row.last_run.status]
      return h(NSpace, { size: 6, align: 'center' }, { default: () => [h(NTag, { size: 'small', type: view.type }, { default: () => view.label }), formatTime(row.last_run!.started_at)] })
    },
  },
  { title: '启用', key: 'enabled', width: 70, render: (row) => h(NSwitch, { size: 'small', value: row.enabled, onUpdateValue: (value: boolean) => toggle(row, value) }) },
  {
    title: '', key: 'actions', width: 200,
    render: (row) => h(NSpace, { size: 6 }, {
      default: () => [
        h(NButton, { size: 'tiny', onClick: () => run(row) }, { default: () => '立即执行' }),
        h(NButton, { size: 'tiny', onClick: () => edit(row) }, { default: () => '编辑' }),
        h(NButton, { size: 'tiny', onClick: () => showHistory(row) }, { default: () => '记录' }),
      ],
    }),
  },
]
</script>

<template>
  <NCard title="任务计划" class="table-panel">
    <NDataTable :columns="columns" :data="jobs" :loading="loading" :row-key="(row: Job) => row.id" :scroll-x="1040" />
  </NCard>

  <NDrawer :show="!!editing" :width="460" @update:show="(value: boolean) => !value && (editing = null)">
    <NDrawerContent v-if="editing" :title="editing.name" closable>
      <ErrorList :errors="errors.global" />
      <NForm label-placement="top">
        <NFormItem label="计划">
          <NSelect v-model:value="preset" :options="planPresets" />
        </NFormItem>
        <NFormItem v-if="preset === 'custom'" label="cron 表达式" v-bind="fieldState(errors, 'schedule', `五段式：分 时 日 月 周；默认 ${editing.default_schedule}`)">
          <NInput v-model:value="editing.schedule" />
        </NFormItem>
        <NFormItem label="未来执行时间">
          <NText v-if="previewError" type="error">{{ previewError }}</NText>
          <NSpace v-else vertical :size="2"><NText v-for="time in preview" :key="time" depth="3">{{ time }}</NText></NSpace>
        </NFormItem>
        <NFormItem label="超时（秒）" v-bind="fieldState(errors, 'timeout')">
          <NInputNumber v-model:value="editing.timeout" :min="1" />
        </NFormItem>
        <template v-if="editing.kind === 'egress_check'">
          <NFormItem label="失败后重试次数" v-bind="fieldState(errors, 'params.retry_count', '首次查询失败后，每隔 1 秒额外重试；默认重试 3 次，共查询 4 次；0 表示不重试。任一地址成功即结束本轮')">
            <NInputNumber v-model:value="editing.params.retry_count" :min="0" :max="10" />
          </NFormItem>
          <NFormItem label="每次查询超时（秒）" v-bind="fieldState(errors, 'params.probe_timeout', '每次 IPv4、IPv6 并发查询共享此超时；任务总超时需覆盖所有 Stack 的查询、重试及等待时间')">
            <NInputNumber v-model:value="editing.params.probe_timeout" :min="1" :max="60" />
          </NFormItem>
        </template>
        <NFormItem v-if="editing.has_keep" label="保留份数" v-bind="fieldState(errors, 'params.keep')">
          <NInputNumber v-model:value="editing.params.keep" :min="1" />
        </NFormItem>
      </NForm>
      <template #footer>
        <NButton type="primary" :disabled="!!previewError" @click="save">保存</NButton>
      </template>
    </NDrawerContent>
  </NDrawer>

  <NDrawer :show="!!history" :width="640" @update:show="(value: boolean) => !value && (history = null)">
    <NDrawerContent v-if="history" :title="`${history.job.name}：执行记录`" closable>
      <NText v-if="!history.runs.length" depth="3">还没有执行记录</NText>
      <div v-for="item in history.runs" :key="item.id" class="run">
        <NSpace align="center" :size="8">
          <NTag size="small" :type="statusView[item.status].type">{{ statusView[item.status].label }}</NTag>
          <NText>{{ formatTime(item.started_at) }}</NText>
          <NText depth="3">{{ item.trigger === 'manual' ? '手动' : '计划' }}</NText>
          <NButton size="tiny" @click="taskCenter.open({ source: 'job', id: item.id }); history = null">查看详情</NButton>
        </NSpace>
        <NCode v-if="item.log" :code="item.log" style="display: block; margin-top: 6px; white-space: pre-wrap" />
      </div>
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.run {
  padding: 8px 0;
  border-bottom: 1px solid var(--n-border-color, rgba(128, 128, 128, 0.2));
}
</style>
