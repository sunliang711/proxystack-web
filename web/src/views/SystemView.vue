<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NDescriptions, NDescriptionsItem, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NSpin, NTag, NText, NTooltip,
} from 'naive-ui'
import { RouterLink, useRouter } from 'vue-router'
import { api, problemMessage, type Schemas } from '../api/client'
import { useBackgroundTask } from '../composables/useBackgroundTask'
import { taskCenter } from '../tasks'
import { VersionCheckTimeoutError, waitForVersion } from '../util/self-update'
import { formatTime } from '../util/format'

const router = useRouter()
/** 面板地址以某个页面路径结尾时返回该路径，例如误把订阅模板页的地址填成了面板地址。 */
const panelURLPage = computed(() => {
  const raw = info.value?.panel_url
  if (!raw) return ''
  let last = ''
  try { last = new URL(raw).pathname.replace(/\/+$/, '').split('/').pop() ?? '' } catch { return '' }
  const pages = new Set(router.getRoutes().map((route) => route.path.split('/')[1]).filter(Boolean))
  return last && pages.has(last) ? last : ''
})

const info = ref<Schemas['SystemInfo'] | null>(null)

const listen = ref({ listen: '', port: 0 })
const listenResult = ref<{ type: 'success' | 'warning' | 'error'; text: string } | null>(null)
const listenOptions = ['0.0.0.0', '127.0.0.1', '::'].map((value) => ({ label: value, value }))

onMounted(async () => {
  const { data } = await api.GET('/api/system/info')
  info.value = data ?? null
  if (data) listen.value = { listen: data.listen, port: data.port }
  await loadUpgradeSchedule()
  scheduleTimer = window.setInterval(() => {
    if (scheduleActive.value) void loadUpgradeSchedule()
  }, 5000)
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
type UpdatePhase = 'idle' | 'submitting' | 'unknown' | 'updating' | 'restarting' | 'verifying' | 'complete' | 'failed' | 'timeout'
const updatePhase = ref<UpdatePhase>('idle')
const updateTarget = ref('')
const updateTaskID = ref<number | null>(null)
const upgradeSchedule = ref<Schemas['SelfUpgradeSchedule'] | null>(null)
const scheduleModalVisible = ref(false)
const scheduleTarget = ref('latest')
const scheduleTimeoutSeconds = ref(300)
const scheduleIntervalSeconds = ref(20)
const scheduleBusy = ref(false)
let scheduleTimer: number | undefined
let scheduleReloadTimer: number | undefined
const scheduleActive = computed(() => upgradeSchedule.value?.status === 'waiting' || upgradeSchedule.value?.status === 'updating')
/** 预约已结束（成功、超时、失败或取消），结果只供查看，可以关闭。 */
const scheduleFinished = computed(() => !!upgradeSchedule.value && !scheduleActive.value && upgradeSchedule.value.status !== 'idle')
const scheduleView = computed(() => {
  const state = upgradeSchedule.value
  if (!state) return { type: 'info' as const, label: '' }
  const latest = state.target === 'latest'
  switch (state.status) {
    case 'waiting': return { type: 'info' as const, label: latest ? '等待新版本发布' : '等待目标版本发布' }
    case 'updating': return { type: 'info' as const, label: '正在升级' }
    case 'succeeded': return { type: 'success' as const, label: '已完成' }
    case 'timeout': return latest ? { type: 'info' as const, label: '等待结束，没有新版本' } : { type: 'error' as const, label: '已超时' }
    case 'cancelled': return { type: 'default' as const, label: '已取消' }
    default: return { type: 'error' as const, label: '失败' }
  }
})
const updateBusy = computed(() => !['idle', 'failed', 'timeout'].includes(updatePhase.value))
const updateModalVisible = computed(() => updatePhase.value !== 'idle')
const updateModalClosable = computed(() => updatePhase.value === 'failed' || updatePhase.value === 'timeout')
const updateSpinning = computed(() => ['submitting', 'updating', 'restarting', 'verifying'].includes(updatePhase.value))
const updateModalType = computed(() => {
  if (updatePhase.value === 'complete') return 'success'
  if (updatePhase.value === 'failed') return 'error'
  if (updatePhase.value === 'unknown' || updatePhase.value === 'timeout') return 'warning'
  return 'info'
})
const updateModalTitle = computed(() => ({
  idle: '',
  submitting: '正在更新管理服务',
  unknown: '更新状态待确认',
  updating: '正在更新管理服务',
  restarting: '正在重启管理服务',
  verifying: '正在确认新版本',
  complete: '更新完成',
  failed: '更新失败',
  timeout: '确认更新超时',
}[updatePhase.value]))
const updatePhaseText = computed(() => ({
  idle: '',
  submitting: '正在提交更新任务…',
  unknown: '提交结果未知，请先在后台任务中确认是否已创建自更新任务',
  updating: `正在下载并校验 ${updateTarget.value}…`,
  restarting: '管理服务正在重启…',
  verifying: `服务已恢复，正在确认 ${updateTarget.value}…`,
  complete: '更新完成，正在刷新页面…',
  failed: updateError.value || '更新失败，请查看任务日志',
  timeout: updateError.value || `自动确认 ${updateTarget.value} 超时，请检查服务状态`,
}[updatePhase.value]))
let updateController: AbortController | null = null
let submitController: AbortController | null = null
let reloadTimer: number | undefined
const trackTask = useBackgroundTask((status) => {
  if (updatePhase.value === 'complete') return
  if (status !== 'succeeded') {
    stopUpdateMonitor()
    updateError.value = '更新任务失败，请打开任务详情查看日志'
    updatePhase.value = 'failed'
  } else if (status === 'succeeded' && updatePhase.value === 'updating') {
    updatePhase.value = 'restarting'
    startUpdateMonitor(updateTarget.value)
  }
})

async function checkUpdate() {
  checking.value = true
  updateError.value = ''
  // 重新检查时，上一次预约的结果已经过时，一并清除。
  if (scheduleFinished.value) await clearUpgradeSchedule()
  const { data, error } = await api.GET('/api/system/update')
  checking.value = false
  if (error) {
    updateError.value = problemMessage(error, '检查失败') + (error.errors?.map((e) => `：${e.message}`).join('') ?? '')
    return
  }
  update.value = data ?? null
}

async function loadUpgradeSchedule() {
  const { data } = await api.GET('/api/system/upgrade-schedule')
  if (!data) return
  const wasActive = scheduleActive.value
  upgradeSchedule.value = data
  if (wasActive && data.status === 'succeeded' && scheduleReloadTimer === undefined) {
    scheduleReloadTimer = window.setTimeout(() => window.location.reload(), 800)
  }
}

async function scheduleUpgrade() {
  scheduleBusy.value = true
  const { data, error } = await api.POST('/api/system/upgrade-schedule', {
    body: {
      target: scheduleTarget.value.trim() || 'latest',
      timeout_seconds: scheduleTimeoutSeconds.value,
      interval_seconds: scheduleIntervalSeconds.value,
    },
  })
  scheduleBusy.value = false
  if (error || !data) {
    updateError.value = problemMessage(error, '预约升级失败')
    return
  }
  upgradeSchedule.value = data
  scheduleModalVisible.value = false
}

async function cancelUpgradeSchedule() {
  scheduleBusy.value = true
  const { data, error } = await api.DELETE('/api/system/upgrade-schedule')
  scheduleBusy.value = false
  if (error || !data) {
    updateError.value = problemMessage(error, '取消预约升级失败')
    return
  }
  upgradeSchedule.value = data
}

/** 清除已结束预约的结果；失败时保留原样，不打断其他操作。 */
async function clearUpgradeSchedule() {
  const { data } = await api.DELETE('/api/system/upgrade-schedule')
  if (data) upgradeSchedule.value = data
}

async function runUpdate() {
  if (!update.value?.update_available || updateBusy.value) return
  if (scheduleActive.value) {
    updateError.value = '请先取消正在等待的预约升级'
    return
  }
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
      updateError.value = problemMessage(error, '更新失败')
      updatePhase.value = 'failed'
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
    updateError.value = cause instanceof VersionCheckTimeoutError
      ? `自动确认 ${targetVersion} 超时，请检查服务状态后手动刷新页面`
      : '检查更新结果失败，请手动刷新页面'
    updatePhase.value = cause instanceof VersionCheckTimeoutError ? 'timeout' : 'failed'
  })
}

function closeUpdateModal() {
  if (!updateModalClosable.value) return
  updatePhase.value = 'idle'
  updateError.value = ''
}

function reloadPage() {
  window.location.reload()
}

onBeforeUnmount(() => {
  submitController?.abort()
  submitController = null
  stopUpdateMonitor()
  if (reloadTimer !== undefined) window.clearTimeout(reloadTimer)
  if (scheduleReloadTimer !== undefined) window.clearTimeout(scheduleReloadTimer)
  if (scheduleTimer !== undefined) window.clearInterval(scheduleTimer)
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
      <NAlert v-if="panelURLPage" type="warning" :bordered="false" style="margin-top: 12px">
        面板地址以页面路径 /{{ panelURLPage }}/ 结尾，通知邮件里的链接会打不开。请到
        <RouterLink :to="{ name: 'settings', query: { section: 'basic' } }">系统设置 → 基本</RouterLink>
        把它改成面板首页地址。
      </NAlert>
    </NSpin>
    <NSpace vertical style="margin-top: 20px">
      <NSpace>
        <NTooltip placement="top-start" style="max-width: min(360px, calc(100vw - 32px))">
          <template #trigger><NButton :loading="checking" :disabled="updateBusy || scheduleActive" @click="checkUpdate">检查新版本</NButton></template>
          更新只替换管理服务本身；xray 和 mihomo 由独立的服务承载，代理流量不受影响。下载走系统设置中的下载代理。
        </NTooltip>
        <NButton :disabled="updateBusy || info?.dev_mode || scheduleActive" @click="scheduleModalVisible = true">预约升级</NButton>
        <NPopconfirm v-if="update?.update_available" @positive-click="runUpdate">
          <template #trigger><NButton type="primary" :loading="updateBusy" :disabled="updateBusy || info?.dev_mode || scheduleActive">更新到 {{ update.latest }}</NButton></template>
          下载并校验新版本后替换并重启管理服务，页面会短暂断开。继续？
        </NPopconfirm>
      </NSpace>
      <NAlert
        v-if="upgradeSchedule && upgradeSchedule.status !== 'idle'"
        :type="scheduleView.type"
        :bordered="false"
        :closable="scheduleFinished"
        @close="clearUpgradeSchedule"
      >
        <NSpace vertical :size="4">
          <NText>预约升级：{{ upgradeSchedule.target }} · {{ scheduleView.label }}</NText>
          <NText v-if="upgradeSchedule.status === 'waiting'" depth="3">
            截止 {{ formatTime(upgradeSchedule.deadline_at) }}，每 {{ upgradeSchedule.interval_seconds }} 秒检查一次；{{ upgradeSchedule.target === 'latest' ? '期间发布比当前更新的版本就自动升级。' : '目标版本发布后自动升级。' }}
          </NText>
          <NText v-if="upgradeSchedule.resolved_version && upgradeSchedule.status === 'waiting' && upgradeSchedule.target === 'latest'" depth="3">
            目前最新版本 {{ upgradeSchedule.resolved_version }}，不比当前版本新，继续等待。
          </NText>
          <NText v-else-if="upgradeSchedule.resolved_version" depth="3">目标版本：{{ upgradeSchedule.resolved_version }}</NText>
          <NText v-if="upgradeSchedule.last_error" depth="3">{{ upgradeSchedule.last_error }}</NText>
          <NButton v-if="scheduleActive" size="small" :loading="scheduleBusy" @click="cancelUpgradeSchedule">取消预约</NButton>
        </NSpace>
      </NAlert>
      <NAlert v-if="update && !update.update_available" type="success" :bordered="false">当前已是最新版本（{{ update.current }}）</NAlert>
      <NAlert v-if="info?.dev_mode && update?.update_available" type="warning" :bordered="false">开发模式不能自动重启管理服务，请在正式部署环境中执行更新。</NAlert>
      <NAlert v-if="update?.update_available && update.notes" type="info" :title="`${update.latest} 更新说明`" :bordered="false">
        <pre style="white-space: pre-wrap; font-family: inherit; margin: 0">{{ update.notes }}</pre>
      </NAlert>
      <NAlert v-if="updateError && updatePhase === 'idle'" type="error" :bordered="false">{{ updateError }}</NAlert>
    </NSpace>
  </NCard>
  <NModal v-model:show="scheduleModalVisible" preset="card" title="预约升级" style="width: 560px; max-width: calc(100vw - 32px)">
    <NForm label-placement="left" label-width="120">
      <NFormItem label="目标版本">
        <NSpace vertical style="width: 100%">
          <NSelect v-model:value="scheduleTarget" :options="[{ label: 'latest（最新正式版）', value: 'latest' }]" />
          <NInput v-model:value="scheduleTarget" placeholder="latest 或 v0.3.7" />
        </NSpace>
      </NFormItem>
      <NFormItem label="等待时长">
        <NInputNumber v-model:value="scheduleTimeoutSeconds" :min="30" :max="86400" :step="30" />
        <NText depth="3" style="margin-left: 8px">秒</NText>
      </NFormItem>
      <NFormItem label="检查间隔">
        <NInputNumber v-model:value="scheduleIntervalSeconds" :min="5" :max="3600" :step="5" />
        <NText depth="3" style="margin-left: 8px">秒</NText>
      </NFormItem>
    </NForm>
    <NAlert type="info" :bordered="false">
      管理服务会在后台持续检查，不需要保持页面打开。找到目标版本后会自动下载、校验、替换并重启管理服务；预约状态会在服务重启后继续恢复。
      目标为 latest 时，在整个等待时长内只要发布了比当前更新的版本就升级，等满时长仍没有新版本则结束预约。
    </NAlert>
    <template #footer>
      <NSpace justify="end">
        <NButton @click="scheduleModalVisible = false">取消</NButton>
        <NButton type="primary" :loading="scheduleBusy" @click="scheduleUpgrade">开始预约</NButton>
      </NSpace>
    </template>
  </NModal>
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
  <NModal
    :show="updateModalVisible"
    preset="card"
    aria-label="更新管理服务"
    :title="updateModalTitle"
    :closable="updateModalClosable"
    :mask-closable="false"
    :close-on-esc="false"
    style="width: 400px; max-width: calc(100vw - 32px)"
    @update:show="(show) => !show && closeUpdateModal()"
  >
    <NSpace vertical align="center" :size="20" style="padding: 12px 0 4px; text-align: center">
      <NSpin v-if="updateSpinning" size="large" />
      <NAlert :type="updateModalType" :bordered="false" style="width: 100%">
        {{ updatePhaseText }}
      </NAlert>
      <NText v-if="updateTarget && updatePhase !== 'unknown'" depth="3">目标版本：{{ updateTarget }}</NText>
    </NSpace>
    <template #footer>
      <NSpace justify="end">
        <NButton v-if="updateTaskID" @click="taskCenter.open({ source: 'task', id: updateTaskID })">查看任务</NButton>
        <NButton v-else-if="updatePhase === 'unknown'" @click="taskCenter.open()">打开后台任务</NButton>
        <NButton v-if="updatePhase === 'unknown' || updatePhase === 'failed' || updatePhase === 'timeout'" @click="reloadPage">刷新页面</NButton>
        <NButton v-if="updateModalClosable" type="primary" @click="closeUpdateModal">关闭</NButton>
      </NSpace>
    </template>
  </NModal>
</template>
