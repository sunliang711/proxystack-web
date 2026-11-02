<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NDescriptions, NDescriptionsItem, NForm, NFormItem, NInputNumber, NPopconfirm, NSelect, NSpace, NSpin, NTag, NText, NTooltip,
} from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { useBackgroundTask } from '../composables/useBackgroundTask'
import { taskCenter } from '../tasks'
import { VersionCheckTimeoutError, waitForVersion } from '../util/self-update'

const info = ref<Schemas['SystemInfo'] | null>(null)

const listen = ref({ listen: '', port: 0 })
const listenResult = ref<{ type: 'success' | 'warning' | 'error'; text: string } | null>(null)
const listenOptions = ['0.0.0.0', '127.0.0.1', '::'].map((value) => ({ label: value, value }))

onMounted(async () => {
  const { data } = await api.GET('/api/system/info')
  info.value = data ?? null
  if (data) listen.value = { listen: data.listen, port: data.port }
})

async function saveListen() {
  const { data, error } = await api.PUT('/api/system/panel', { body: listen.value })
  if (error) {
    listenResult.value = { type: 'error', text: error.errors?.[0]?.message ?? problemMessage(error, '保存失败') }
    return
  }
  listenResult.value = data?.restarting
    ? { type: 'success', text: `已保存，管理服务正在重启。端口变了的话，用 ${listen.value.port} 端口重新打开，nginx 的 proxy_pass 也要同步修改。` }
    : { type: 'warning', text: '已保存，当前模式不能自动重启，手工重启管理服务后生效。' }
}

const update = ref<Schemas['SelfVersion'] | null>(null)
const updateError = ref('')
const checking = ref(false)
type UpdatePhase = 'idle' | 'submitting' | 'unknown' | 'updating' | 'restarting' | 'verifying' | 'complete'
const updatePhase = ref<UpdatePhase>('idle')
const updateTarget = ref('')
const updateTaskID = ref<number | null>(null)
const updateBusy = computed(() => updatePhase.value !== 'idle')
const updatePhaseText = computed(() => ({
  idle: '',
  submitting: '正在提交更新任务…',
  unknown: '提交结果未知，请先在任务中心确认是否已创建自更新任务',
  updating: `正在下载并校验 ${updateTarget.value}…`,
  restarting: '管理服务正在重启…',
  verifying: `服务已恢复，正在确认 ${updateTarget.value}…`,
  complete: '更新完成，正在刷新页面…',
}[updatePhase.value]))
let updateController: AbortController | null = null
let submitController: AbortController | null = null
let reloadTimer: number | undefined
const trackTask = useBackgroundTask((status) => {
  if (updatePhase.value === 'complete') return
  if (status !== 'succeeded') {
    stopUpdateMonitor()
    updatePhase.value = 'idle'
    updateError.value = '更新任务失败，请打开任务详情查看日志'
  } else if (status === 'succeeded' && updatePhase.value === 'updating') {
    updatePhase.value = 'restarting'
    startUpdateMonitor(updateTarget.value)
  }
})

async function checkUpdate() {
  checking.value = true
  updateError.value = ''
  const { data, error } = await api.GET('/api/system/update')
  checking.value = false
  if (error) {
    updateError.value = problemMessage(error, '检查失败') + (error.errors?.map((e) => `：${e.message}`).join('') ?? '')
    return
  }
  update.value = data ?? null
}

async function runUpdate() {
  if (!update.value?.update_available || updateBusy.value) return
  if (info.value?.dev_mode) {
    updateError.value = '开发模式不能自动重启管理服务，请在正式部署环境中执行更新'
    return
  }
  updateError.value = ''
  updateTarget.value = update.value.latest
  updateTaskID.value = null
  updatePhase.value = 'submitting'
  const controller = new AbortController()
  submitController = controller
  try {
    const { data, error } = await api.POST('/api/system/update', { signal: controller.signal })
    if (submitController !== controller) return
    submitController = null
    if (error) {
      updatePhase.value = 'idle'
      updateError.value = problemMessage(error, '更新失败')
      return
    }
    updateTaskID.value = data!.task_id
    updatePhase.value = 'updating'
    trackTask(data!.task_id)
  } catch {
    if (controller.signal.aborted || submitController !== controller) return
    submitController = null
    updatePhase.value = 'unknown'
    return
  }
}

function stopUpdateMonitor() {
  updateController?.abort()
  updateController = null
}

function startUpdateMonitor(targetVersion: string) {
  stopUpdateMonitor()
  const controller = new AbortController()
  updateController = controller
  void waitForVersion({
    targetVersion,
    signal: controller.signal,
    probe: async (signal) => {
      const { data } = await api.GET('/api/system/info', { signal })
      if (!data) throw new Error('system info unavailable')
      return data.version
    },
    onReachable: () => {
      if (updatePhase.value === 'restarting') updatePhase.value = 'verifying'
    },
    onUnavailable: () => {
      if (updatePhase.value !== 'complete') updatePhase.value = 'restarting'
    },
  }).then(() => {
    if (updateController !== controller) return
    updateController = null
    updatePhase.value = 'complete'
    reloadTimer = window.setTimeout(() => window.location.reload(), 800)
  }).catch((cause) => {
    if (controller.signal.aborted || updateController !== controller) return
    updateController = null
    updatePhase.value = 'idle'
    updateError.value = cause instanceof VersionCheckTimeoutError
      ? `自动确认 ${targetVersion} 超时，请检查服务状态后手动刷新页面`
      : '检查更新结果失败，请手动刷新页面'
  })
}

onBeforeUnmount(() => {
  submitController?.abort()
  submitController = null
  stopUpdateMonitor()
  if (reloadTimer !== undefined) window.clearTimeout(reloadTimer)
})
</script>

<template>
  <NCard title="系统信息">
    <NSpin :show="!info">
      <NDescriptions v-if="info" label-placement="left" :column="1" bordered>
        <NDescriptionsItem label="版本">
          {{ info.version }}
          <NTag v-if="info.dev_mode" size="small" type="warning" style="margin-left: 8px">开发模式</NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="提交">{{ info.commit }}</NDescriptionsItem>
        <NDescriptionsItem label="构建时间">{{ info.build_date }}</NDescriptionsItem>
        <NDescriptionsItem label="运行环境">{{ info.go_version }} · {{ info.platform }}</NDescriptionsItem>
        <NDescriptionsItem label="面板地址">{{ info.panel_url }}</NDescriptionsItem>
      </NDescriptions>
    </NSpin>
    <NSpace vertical style="margin-top: 20px">
      <NSpace>
        <NTooltip placement="top-start" style="max-width: min(360px, calc(100vw - 32px))">
          <template #trigger><NButton :loading="checking" :disabled="updateBusy" @click="checkUpdate">检查新版本</NButton></template>
          更新只替换管理服务本身；xray 和 mihomo 由独立的服务承载，代理流量不受影响。下载走系统设置中的下载代理。
        </NTooltip>
        <NPopconfirm v-if="update?.update_available" @positive-click="runUpdate">
          <template #trigger><NButton type="primary" :loading="updateBusy" :disabled="updateBusy || info?.dev_mode">更新到 {{ update.latest }}</NButton></template>
          下载并校验新版本后替换并重启管理服务，页面会短暂断开。继续？
        </NPopconfirm>
      </NSpace>
      <NAlert v-if="updateBusy" :type="updatePhase === 'complete' ? 'success' : updatePhase === 'unknown' ? 'warning' : 'info'" :bordered="false">
        <NSpace align="center">
          <NSpin v-if="updatePhase !== 'complete' && updatePhase !== 'unknown'" size="small" />
          <NText>{{ updatePhaseText }}</NText>
          <NButton v-if="updateTaskID" text size="small" @click="taskCenter.open({ source: 'task', id: updateTaskID })">查看任务</NButton>
          <NButton v-else-if="updatePhase === 'unknown'" text size="small" @click="taskCenter.open()">打开任务中心</NButton>
        </NSpace>
      </NAlert>
      <NAlert v-if="update && !update.update_available" type="success" :bordered="false">当前已是最新版本（{{ update.current }}）</NAlert>
      <NAlert v-if="info?.dev_mode && update?.update_available" type="warning" :bordered="false">开发模式不能自动重启管理服务，请在正式部署环境中执行更新。</NAlert>
      <NAlert v-if="update?.update_available && update.notes" type="info" :title="`${update.latest} 更新说明`" :bordered="false">
        <pre style="white-space: pre-wrap; font-family: inherit; margin: 0">{{ update.notes }}</pre>
      </NAlert>
      <NAlert v-if="updateError" type="error" :bordered="false">{{ updateError }}</NAlert>
    </NSpace>
  </NCard>
  <NCard title="面板监听" style="margin-top: 20px">
    <NSpace vertical>
      <NText depth="3">修改后管理服务会重启，xray 和 mihomo 不受影响。改错连不上时，在本机执行 proxystack-web admin set-listen 恢复。</NText>
      <NForm label-placement="left" label-width="90" style="max-width: 420px">
        <NFormItem label="监听地址"><NSelect v-model:value="listen.listen" :options="listenOptions" filterable tag /></NFormItem>
        <NFormItem label="端口"><NInputNumber v-model:value="listen.port" :min="1" :max="65535" :show-button="false" /></NFormItem>
      </NForm>
      <NPopconfirm @positive-click="saveListen">
        <template #trigger><NButton :disabled="!listen.listen || !listen.port">保存并重启</NButton></template>
        重启管理服务，页面会短暂断开。继续？
      </NPopconfirm>
      <NAlert v-if="listenResult" :type="listenResult.type" :bordered="false">{{ listenResult.text }}</NAlert>
    </NSpace>
  </NCard>
</template>
