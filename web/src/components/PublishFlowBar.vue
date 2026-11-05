<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NCard, NStep, NSteps, NText } from 'naive-ui'
import { useMobile } from '../composables/useMobile'
import { publishFlow, type FlowInput, type FlowStepKey, type PublishTargetState } from '../util/subPublish'

/**
 * 在线发布的整体进度条，「SSH 发布与部署」和「发布订阅包」两页共用。
 * 当前步骤只给一个主操作：属于本页的操作交给页面处理，属于另一页的跳转过去。
 */
const props = defineProps<{ input: FlowInput; page: 'settings' | 'publish'; loading?: boolean }>()
const emit = defineEmits<{ action: [key: FlowStepKey, target: PublishTargetState | null] }>()

const router = useRouter()
const mobile = useMobile()
const flow = computed(() => publishFlow(props.input))
const currentIndex = computed(() => {
  const index = flow.value.steps.findIndex((step) => step.key === flow.value.current?.key)
  return index < 0 ? flow.value.steps.length + 1 : index + 1
})

const stepPage: Record<FlowStepKey, 'settings' | 'publish'> = {
  identity: 'settings', target: 'settings', deploy: 'settings', test: 'settings', publish: 'publish',
}

const action = computed(() => {
  const step = flow.value.current
  if (!step) return null
  const target = flow.value.target
  const name = target ? ` ${target.name}` : ''
  const labels: Record<FlowStepKey, string> = {
    identity: '生成 SSH 密钥',
    target: '添加订阅端',
    deploy: target?.deploy_state ? `部署${name}` : `检测部署${name}`,
    test: `测试${name}`,
    publish: '签发并发布',
  }
  const local = stepPage[step.key] === props.page
  // 发布页的签发按钮就在下方，进度条不再重复一个。
  if (local && step.key === 'publish') return null
  return { label: local ? labels[step.key] : `去${props.page === 'settings' ? '发布订阅包' : '「SSH 发布与部署」'}：${labels[step.key]}`, local, step }
})

function run() {
  const current = action.value
  if (!current) return
  if (current.local) emit('action', current.step.key, flow.value.target)
  else void router.push({ name: stepPage[current.step.key] === 'settings' ? 'sub-publish-settings' : 'sub-publish' })
}
</script>

<template>
  <NCard size="small" title="在线发布进度">
    <NSteps :current="currentIndex" :status="flow.current?.status === 'error' ? 'error' : 'process'" size="small" :vertical="mobile">
      <NStep v-for="step in flow.steps" :key="step.key" :title="step.title" :description="step.detail" />
    </NSteps>
    <NAlert v-if="!flow.current" type="success" :bordered="false" class="flow-next">全部完成。订阅内容变化后，在「发布订阅包」重新签发即可。</NAlert>
    <div v-else class="flow-next">
      <NText :type="flow.current.status === 'error' ? 'error' : undefined">
        下一步：{{ flow.current.title }}（{{ flow.current.detail }}）
      </NText>
      <NButton v-if="action" size="small" type="primary" :loading="loading" @click="run">{{ action.label }}</NButton>
      <NText v-else-if="flow.current.key === 'publish'" depth="3">在下方选择订阅端后点击「签发并在线发布」。</NText>
    </div>
  </NCard>
</template>

<style scoped>
.flow-next { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; margin-top: 16px; }
</style>
