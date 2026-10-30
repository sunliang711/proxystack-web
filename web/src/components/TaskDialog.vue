<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { NAlert, NButton, NModal, NScrollbar, NSpace, NSpin, NTag } from 'naive-ui'
import { api, type Schemas } from '../api/client'
import { onTaskEvent } from '../runtime'

const props = defineProps<{ taskId: number | null }>()
const emit = defineEmits<{ close: []; finished: [status: string] }>()
const task = ref<Schemas['Task'] | null>(null)
const scroller = ref<InstanceType<typeof NScrollbar>>()

async function load(id: number) {
  const { data } = await api.GET('/api/tasks/{id}', { params: { path: { id } } })
  if (data && props.taskId === id) task.value = data
}

const stop = onTaskEvent((event) => {
  if (!task.value || event.id !== task.value.id) return
  if (event.line) task.value.log.push(event.line)
  if (event.status !== 'running') {
    task.value.status = event.status as Schemas['Task']['status']
    task.value.error = event.error ?? ''
    emit('finished', event.status)
  }
  nextTick(() => scroller.value?.scrollTo({ top: 1e9 }))
})
onBeforeUnmount(stop)

watch(
  () => props.taskId,
  (id) => {
    task.value = null
    if (id) load(id)
  },
  { immediate: true },
)

const statusTag = computed(() => {
  switch (task.value?.status) {
    case 'succeeded':
      return { text: '成功', type: 'success' as const }
    case 'failed':
      return { text: '失败', type: 'error' as const }
    default:
      return { text: '进行中', type: 'info' as const }
  }
})
</script>

<template>
  <NModal :show="!!taskId" preset="card" style="max-width: 760px" :mask-closable="false" @update:show="(v) => !v && emit('close')">
    <template #header>
      <NSpace align="center">
        <span>{{ task?.title ?? '后台作业' }}</span>
        <NTag v-if="task" size="small" :type="statusTag.type">{{ statusTag.text }}</NTag>
        <NSpin v-if="task?.status === 'running'" :size="14" />
      </NSpace>
    </template>
    <NScrollbar ref="scroller" style="max-height: 420px">
      <pre class="log">{{ task?.log.join('\n') || '等待输出…' }}</pre>
    </NScrollbar>
    <NAlert v-if="task?.error" type="error" :bordered="false" style="margin-top: 8px; white-space: pre-wrap">{{ task.error }}</NAlert>
    <template #footer>
      <NSpace justify="end">
        <NButton @click="emit('close')">{{ task?.status === 'running' ? '后台运行' : '关闭' }}</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.log {
  margin: 0;
  font-family: ui-monospace, monospace;
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
