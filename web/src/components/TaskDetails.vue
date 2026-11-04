<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { NAlert, NButton, NCheckbox, NDescriptions, NDescriptionsItem, NEmpty, NSpace, NSpin, NTag, NText } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { usePolling } from '../composables/usePolling'
import { onJobTaskEvent, onTaskEvent } from '../runtime'
import { taskCenter, taskFailed, type TaskSource } from '../tasks'
import { formatTime } from '../util/format'
import { taskActor, taskDuration, taskStatus } from '../util/task'

const props = defineProps<{ source: TaskSource; id: number }>()
const follow = ref(true)
const log = ref<HTMLPreElement>()
const now = ref(Date.now())
const { data: task, loading, error, refresh } = usePolling<Schemas['CenterTask']>(() => [props.source, props.id], async (signal) => {
  const { data, error } = await api.GET('/api/task-center/{source}/{id}', { params: { path: { source: props.source, id: props.id } }, signal })
  if (!data) throw new Error(problemMessage(error, '任务详情加载失败，记录可能已被清理'))
  return data
}, 5000)
watch(task, (value) => { if (value && taskFailed(value)) taskCenter.markRead(value) })
watch(() => task.value?.log.join('\n'), async () => {
  await nextTick()
  if (follow.value && log.value) log.value.scrollTop = log.value.scrollHeight
})
let queued: ReturnType<typeof setTimeout> | undefined
function update(source: TaskSource, id: number) {
  if (source !== props.source || id !== props.id || queued) return
  queued = setTimeout(() => { queued = undefined; void refresh() }, 300)
}
const stopTask = onTaskEvent((event) => update('task', event.id))
const stopJob = onJobTaskEvent((event) => update('job', event.id))
const clock = setInterval(() => { now.value = Date.now() }, 1000)
onBeforeUnmount(() => { clearInterval(clock); clearTimeout(queued); stopTask(); stopJob() })
</script>

<template>
  <NSpace vertical :size="16">
    <NAlert v-if="error" type="warning" :bordered="false">{{ error }}<template v-if="task">；当前显示上次成功读取的数据</template></NAlert>
    <template v-if="task">
      <NSpace justify="space-between" align="center">
        <NText strong class="task-title">{{ task.title }}</NText>
        <NTag round size="small" :type="taskStatus[task.status].type">{{ taskStatus[task.status].label }}</NTag>
      </NSpace>
      <NDescriptions :column="1" label-placement="left" size="small">
        <NDescriptionsItem label="来源">{{ task.source === 'job' ? '定时任务' : '一次性任务' }} · #{{ task.id }}</NDescriptionsItem>
        <NDescriptionsItem label="执行者">{{ taskActor(task) }}</NDescriptionsItem>
        <NDescriptionsItem label="开始时间">{{ formatTime(task.started_at) }}</NDescriptionsItem>
        <NDescriptionsItem v-if="task.status !== 'running'" label="结束时间">{{ formatTime(task.finished_at) }}</NDescriptionsItem>
        <NDescriptionsItem label="耗时">{{ taskDuration(task, now) }}</NDescriptionsItem>
      </NDescriptions>
      <NAlert v-if="task.error" type="error" :bordered="false" style="white-space: pre-wrap; overflow-wrap: anywhere">{{ task.error }}</NAlert>
      <NSpace justify="space-between" align="center">
        <NSpace align="center" :size="8"><NText strong>执行日志</NText><NSpin v-if="task.status === 'running'" :size="14" /></NSpace>
        <NSpace align="center"><NCheckbox v-model:checked="follow">跟随日志</NCheckbox><NButton size="tiny" :loading="loading" @click="refresh">刷新</NButton></NSpace>
      </NSpace>
      <pre ref="log" class="task-log" tabindex="0" aria-label="任务执行日志">{{ task.log.join('\n') || (task.status === 'running' ? '任务执行中，等待日志输出…' : '没有日志输出') }}</pre>
      <NText v-if="task.status === 'running'" depth="3">任务在服务器后台执行，关闭此处或切换页面不会中止任务。</NText>
    </template>
    <NEmpty v-else :description="loading ? '正在读取任务' : '暂无任务详情'" />
  </NSpace>
</template>

<style scoped>
.task-title { font-size: 17px; overflow-wrap: anywhere; }
.task-log { margin: 0; padding: 16px; max-height: 440px; min-height: 140px; overflow: auto; border: 1px solid var(--ps-border); border-radius: 10px; background: var(--ps-code-bg, rgba(0, 0, 0, 0.12)); font: 12px/1.8 ui-monospace, SFMono-Regular, monospace; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
