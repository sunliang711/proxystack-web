<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NCollapse, NCollapseItem, NDrawer, NDrawerContent, NEmpty, NPopconfirm, NSpace, NSpin, NTag } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { refreshPending } from '../runtime'
import DiffView from './DiffView.vue'
import TaskDialog from './TaskDialog.vue'

const show = defineModel<boolean>('show', { required: true })
const pending = ref<Schemas['PendingView'] | null>(null)
const loading = ref(false)
const error = ref('')
const taskId = ref<number | null>(null)

async function load() {
  loading.value = true
  const { data } = await api.GET('/api/runtime/pending')
  loading.value = false
  pending.value = data ?? null
  refreshPending()
}
watch(show, (open) => open && load())

const byStack = computed(() => {
  const groups: Record<string, Schemas['Change'][]> = {}
  for (const change of pending.value?.changes ?? []) (groups[change.stack] ||= []).push(change)
  return groups
})
const stackCount = computed(() => Object.keys(byStack.value).length)

const kindLabels: Record<string, [string, 'success' | 'warning' | 'error']> = {
  create: ['新增', 'success'],
  update: ['更新', 'warning'],
  delete: ['删除', 'error'],
}

async function apply(stacks: string[]) {
  error.value = ''
  const { data, error: problem } = await api.POST('/api/runtime/apply', { body: { stacks } })
  if (problem) {
    error.value = problemMessage(problem, '应用失败')
    return
  }
  taskId.value = data!.task_id
}
</script>

<template>
  <NDrawer v-model:show="show" :width="760">
    <NDrawerContent title="待应用变更" closable>
      <NSpin :show="loading">
        <NAlert v-if="pending?.error" type="error" title="当前配置无法生成" style="margin-bottom: 12px">{{ pending.error }}</NAlert>
        <NAlert v-if="error" type="error" style="margin-bottom: 12px">{{ error }}</NAlert>
        <NEmpty v-if="pending && !pending.changes.length && !pending.error" description="生成物与当前配置一致，没有待应用的变更" />
        <template v-for="(changes, stack) in byStack" :key="stack">
          <NSpace justify="space-between" align="center" style="margin: 12px 0 6px">
            <strong>{{ stack }}</strong>
            <NButton size="small" @click="apply([String(stack)])">只应用此 stack</NButton>
          </NSpace>
          <NCollapse>
            <NCollapseItem v-for="change in changes" :key="change.component" :name="change.component">
              <template #header>
                <NSpace align="center" :size="6">
                  <NTag size="small" :type="kindLabels[change.kind][1]">{{ kindLabels[change.kind][0] }}</NTag>
                  <span>{{ change.component }}</span>
                </NSpace>
              </template>
              <DiffView :diff="change.diff" />
            </NCollapseItem>
          </NCollapse>
        </template>
      </NSpin>
      <template #footer>
        <NSpace>
          <NButton @click="load">刷新</NButton>
          <NPopconfirm v-if="pending?.changes.length" @positive-click="apply([])">
            <template #trigger>
              <NButton type="primary">应用全部</NButton>
            </template>
            将应用 {{ pending.changes.length }} 项变更，涉及 {{ stackCount }} 个 stack，并可能重启相关服务。确定继续？
          </NPopconfirm>
          <NButton v-else type="primary" disabled>应用全部</NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>
  <TaskDialog :task-id="taskId" @close="taskId = null" @finished="load" />
</template>
