<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  NAlert, NButton, NCard, NCollapse, NCollapseItem, NDescriptions, NDescriptionsItem, NForm, NFormItem, NInput, NInputNumber, NModal,
  NPopconfirm, NProgress, NRadioButton, NRadioGroup, NSelect, NSpace, NSpin, NTag, NText,
} from 'naive-ui'
import { RouterLink, useRouter } from 'vue-router'
import { api, problemMessage, type Schemas } from '../api/client'
import UpgradeProgress from '../components/UpgradeProgress.vue'
import { useBackgroundTask } from '../composables/useBackgroundTask'
import { useTaskProgress } from '../composables/useTaskProgress'
import { taskCenter } from '../tasks'
import { upgradeStage, VersionCheckTimeoutError, waitForVersion, type UpgradeStage } from '../util/self-update'
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

/** 预约等待期间每秒刷新倒计时，每 5 秒重新读取预约状态。 */
const now = ref(Date.now())
let tickTimer: number | undefined
let scheduleTimer: number | undefined

onMounted(async () => {
  const { data } = await api.GET('/api/system/info')
  info.value = data ?? null
  if (data) listen.value = { listen: data.listen, port: data.port }
  await loadUpgradeSchedule()
  // 打开页面即检查一次新版本；预约进行中时以预约状态为准，不重复检查。
  if (!scheduleActive.value) void checkUpdate()
  scheduleTimer = window.setInterval(() => {
    if (scheduleActive.value) void loadUpgradeSchedule()
  }, 5000)
  tickTimer = window.setInterval(() => { now.value = Date.now() }, 1000)
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

// ---------- 检查新版本 ----------

const update = ref<Schemas['SelfVersion'] | null>(null)
const checkError = ref('')
const checking = ref(false)
const checkedAt = ref<Date | null>(null)

async function checkUpdate() {
  checking.value = true
  checkError.value = ''
  // 重新检查时，上一次预约的结果已经过时，一并清除。
  if (scheduleFinished.value) await clearUpgradeSchedule()
  const { data, error } = await api.GET('/api/system/update')
  checking.value = false
  checkedAt.value = new Date()
  if (error) {
    checkError.value = problemMessage(error, '检查失败') + (error.errors?.map((e) => `：${e.message}`).join('') ?? '')
    return
  }
  update.value = data ?? null
}

// ---------- 立即更新 ----------

type UpdatePhase = 'idle' | 'submitting' | 'unknown' | 'updating' | 'restarting' | 'verifying' | 'complete' | 'failed' | 'timeout'
const updatePhase = ref<UpdatePhase>('idle')
const updateError = ref('')
const updateTarget = ref('')
const updateTaskID = ref<number | null>(null)
const updateTask = useTaskProgress(updateTaskID)
const updateBusy = computed(() => !['idle', 'failed', 'timeout'].includes(updatePhase.value))
const updateModalVisible = computed(() => updatePhase.value !== 'idle')
const updateModalClosable = computed(() => ['failed', 'timeout', 'unknown'].includes(updatePhase.value))
/** 失败时停留在最后到达的阶段，便于看出卡在哪一步。 */
const lastStage = ref<UpgradeStage>('resolve')
const liveStage = computed<UpgradeStage>(() => {
  switch (updatePhase.value) {
    case 'updating': return upgradeStage(updateTask.progress.value?.stage) ?? 'resolve'
    case 'restarting': return 'restart'
    case 'verifying': return 'confirm'
    case 'complete': return 'done'
    case 'timeout': return 'confirm'
    default: return 'resolve'
  }
})
watch(liveStage, (stage) => { if (updatePhase.value !== 'failed') lastStage.value = stage })
const updateStage = computed(() => (updatePhase.value === 'failed' ? lastStage.value : liveStage.value))
const updateModalTitle = computed(() => ({
  idle: '',
  submitting: `更新到 ${updateTarget.value}`,
  unknown: '更新状态待确认',
  updating: `更新到 ${updateTarget.value}`,
  restarting: `更新到 ${updateTarget.value}`,
  verifying: `更新到 ${updateTarget.value}`,
  complete: '更新完成',
  failed: '更新失败',
  timeout: '确认更新超时',
}[updatePhase.value]))

let updateController: AbortController | null = null
let submitController: AbortController | null = null
let reloadTimer: number | undefined
const trackTask = useBackgroundTask((status) => {
  if (updatePhase.value === 'complete') return
  if (status !== 'succeeded') {
    stopUpdateMonitor()
    updateError.value = '更新任务失败，请查看任务日志'
    updatePhase.value = 'failed'
  } else if (updatePhase.value === 'updating') {
    updatePhase.value = 'restarting'
    startUpdateMonitor(updateTarget.value)
  }
})

async function runUpdate() {
  if (!update.value?.update_available || updateBusy.value) return
  if (scheduleActive.value) {
    checkError.value = '请先取消正在进行的预约升级'
    return
  }
  if (info.value?.dev_mode) {
    checkError.value = '开发模式不能自动重启管理服务，请在正式部署环境中执行更新'
    return
  }
  updateError.value = ''
  updateTarget.value = update.value.latest
  updateTaskID.value = null
  lastStage.value = 'resolve'
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
    reloadTimer = window.setTimeout(() => window.location.reload(), 1200)
  }).catch((cause) => {
    if (controller.signal.aborted || updateController !== controller) return
    updateController = null
    updateError.value = cause instanceof VersionCheckTimeoutError
      ? `等待 ${targetVersion} 启动超时，请检查服务状态后手动刷新页面`
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

// ---------- 预约升级 ----------

const upgradeSchedule = ref<Schemas['SelfUpgradeSchedule'] | null>(null)
const scheduleBusy = ref(false)
const scheduleError = ref('')
/** 服务重启期间读不到预约状态。 */
const scheduleUnreachable = ref(false)
let scheduleReloadTimer: number | undefined
const scheduleActive = computed(() => upgradeSchedule.value?.status === 'waiting' || upgradeSchedule.value?.status === 'updating')
/** 预约已结束（成功、超时、失败或取消），结果只供查看，可以关闭。 */
const scheduleFinished = computed(() => !!upgradeSchedule.value && !scheduleActive.value && upgradeSchedule.value.status !== 'idle')
const scheduleTaskID = computed(() => (upgradeSchedule.value?.status === 'updating' ? upgradeSchedule.value.task_id ?? null : null))
const scheduleTask = useTaskProgress(scheduleTaskID)
const scheduleStage = computed<UpgradeStage>(() => (scheduleUnreachable.value ? 'restart' : upgradeStage(scheduleTask.progress.value?.stage) ?? 'resolve'))
const scheduleView = computed(() => {
  const state = upgradeSchedule.value
  if (!state) return { type: 'info' as const, label: '' }
  const latest = state.target === 'latest'
  switch (state.status) {
    case 'waiting': return { type: 'info' as const, label: latest ? '等待新版本发布' : `等待 ${state.target} 发布` }
    case 'updating': return { type: 'info' as const, label: `正在升级到 ${state.resolved_version || state.target}` }
    case 'succeeded': return { type: 'success' as const, label: `已升级到 ${state.resolved_version || state.target}` }
    case 'timeout': return latest ? { type: 'info' as const, label: '等待结束，没有发布新版本' } : { type: 'warning' as const, label: `等待结束，${state.target} 没有发布` }
    case 'cancelled': return { type: 'default' as const, label: '已取消' }
    default: return { type: 'error' as const, label: '升级失败' }
  }
})
const time = (value?: string) => (value ? Date.parse(value) : NaN)
/** 等待时长已过去的比例。 */
const waitPercent = computed(() => {
  const state = upgradeSchedule.value
  if (!state) return 0
  const start = time(state.started_at)
  const end = time(state.deadline_at)
  if (!(end > start)) return 0
  return Math.min(100, Math.max(0, Math.round(((now.value - start) / (end - start)) * 100)))
})
function duration(seconds: number) {
  if (seconds >= 3600) return `${Math.floor(seconds / 3600)} 小时${seconds % 3600 >= 60 ? ` ${Math.floor((seconds % 3600) / 60)} 分钟` : ''}`
  if (seconds >= 60) return `${Math.floor(seconds / 60)} 分钟${seconds % 60 ? ` ${seconds % 60} 秒` : ''}`
  return `${seconds} 秒`
}
const remaining = computed(() => duration(Math.max(0, Math.round((time(upgradeSchedule.value?.deadline_at) - now.value) / 1000))))
const nextCheckIn = computed(() => {
  const next = time(upgradeSchedule.value?.next_check_at)
  if (!(next > 0)) return ''
  const seconds = Math.round((next - now.value) / 1000)
  return seconds > 0 ? `${seconds} 秒后` : '马上'
})
const hasTime = (value?: string) => !!value && !value.startsWith('0001')

async function loadUpgradeSchedule() {
  const { data } = await api.GET('/api/system/upgrade-schedule').catch(() => ({ data: undefined }))
  scheduleUnreachable.value = !data && upgradeSchedule.value?.status === 'updating'
  if (!data) return
  const wasActive = scheduleActive.value
  upgradeSchedule.value = data
  if (wasActive && data.status === 'succeeded' && scheduleReloadTimer === undefined) {
    scheduleReloadTimer = window.setTimeout(() => window.location.reload(), 1200)
  }
}

/** 预约表单：目标版本、等待时长和检查间隔都给出常用选项。 */
const scheduleModalVisible = ref(false)
const scheduleMode = ref<'latest' | 'version'>('latest')
const scheduleVersion = ref('')
const durationOptions = [
  { label: '5 分钟', value: 300 },
  { label: '30 分钟', value: 1800 },
  { label: '1 小时', value: 3600 },
  { label: '6 小时', value: 21600 },
  { label: '24 小时', value: 86400 },
  { label: '自定义', value: 0 },
]
const intervalOptions = [
  { label: '10 秒', value: 10 },
  { label: '20 秒', value: 20 },
  { label: '1 分钟', value: 60 },
  { label: '5 分钟', value: 300 },
]
const scheduleDuration = ref(1800)
const customMinutes = ref(10)
const scheduleInterval = ref(20)
const versionPattern = /^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$/
const versionValid = computed(() => scheduleMode.value === 'latest' || versionPattern.test(scheduleVersion.value.trim()))
// 目标版本的示例取当前版本号；开发构建的版本号不是语义化版本时退回固定示例。
const versionExample = computed(() => (info.value?.version && versionPattern.test(info.value.version) ? info.value.version : 'v0.6.1'))
const timeoutSeconds = computed(() => (scheduleDuration.value || Math.round((customMinutes.value || 1) * 60)))
const scheduleSummary = computed(() => {
  const deadline = new Date(Date.now() + timeoutSeconds.value * 1000).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' })
  const what = scheduleMode.value === 'latest' ? '是否发布了比当前更新的正式版' : ` ${scheduleVersion.value.trim() || '目标版本'} 是否已发布`
  return `${duration(timeoutSeconds.value)}内（约到 ${deadline}）每 ${duration(scheduleInterval.value)}检查一次${what}，发现后自动下载、校验并升级；到时仍没有则结束预约。`
})

function openScheduleModal() {
  scheduleError.value = ''
  if (update.value?.update_available && !scheduleVersion.value) scheduleVersion.value = update.value.latest
  scheduleModalVisible.value = true
}

async function scheduleUpgrade() {
  if (!versionValid.value) return
  scheduleBusy.value = true
  scheduleError.value = ''
  const { data, error } = await api.POST('/api/system/upgrade-schedule', {
    body: {
      target: scheduleMode.value === 'latest' ? 'latest' : scheduleVersion.value.trim(),
      timeout_seconds: timeoutSeconds.value,
      interval_seconds: scheduleInterval.value,
    },
  })
  scheduleBusy.value = false
  if (error || !data) {
    scheduleError.value = problemMessage(error, '预约升级失败') + (error?.errors?.map((e) => `：${e.message}`).join('') ?? '')
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
    checkError.value = problemMessage(error, '取消预约升级失败')
    return
  }
  upgradeSchedule.value = data
}

/** 清除已结束预约的结果；失败时保留原样，不打断其他操作。 */
async function clearUpgradeSchedule() {
  const { data } = await api.DELETE('/api/system/upgrade-schedule')
  if (data) upgradeSchedule.value = data
}

onBeforeUnmount(() => {
  submitController?.abort()
  submitController = null
  stopUpdateMonitor()
  if (reloadTimer !== undefined) window.clearTimeout(reloadTimer)
  if (scheduleReloadTimer !== undefined) window.clearTimeout(scheduleReloadTimer)
  if (scheduleTimer !== undefined) window.clearInterval(scheduleTimer)
  if (tickTimer !== undefined) window.clearInterval(tickTimer)
})
</script>

<template>
  <NCard title="系统信息">
    <NSpin :show="!info">
      <NDescriptions v-if="info" label-placement="left" :column="1" bordered>
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
  </NCard>

  <NCard title="版本与升级" style="margin-top: 20px">
    <NSpace vertical :size="16">
      <div class="versions">
        <div class="version-box">
          <NText depth="3" class="version-label">当前版本</NText>
          <div class="version-value">
            <strong>{{ info?.version ?? '…' }}</strong>
            <NTag v-if="info?.dev_mode" size="small" type="warning" :bordered="false">开发模式</NTag>
          </div>
        </div>
        <div class="version-arrow" aria-hidden="true">→</div>
        <div class="version-box">
          <NText depth="3" class="version-label">最新版本</NText>
          <div class="version-value">
            <template v-if="checking"><NSpin :size="14" /><NText depth="3">正在检查…</NText></template>
            <template v-else-if="checkError && !update"><NText type="error">检查失败</NText></template>
            <template v-else-if="update">
              <strong>{{ update.latest }}</strong>
              <NTag v-if="update.update_available" size="small" type="success" :bordered="false">有新版本</NTag>
              <NTag v-else size="small" :bordered="false">已是最新</NTag>
            </template>
            <NText v-else depth="3">未检查</NText>
          </div>
          <NText v-if="checkedAt && !checking" depth="3" class="version-label">检查于 {{ checkedAt.toLocaleTimeString('zh-CN', { hour12: false }) }}</NText>
        </div>
      </div>

      <NSpace align="center">
        <NPopconfirm v-if="update?.update_available" @positive-click="runUpdate">
          <template #trigger>
            <NButton type="primary" :loading="updateBusy" :disabled="updateBusy || info?.dev_mode || scheduleActive">立即更新到 {{ update.latest }}</NButton>
          </template>
          下载并校验 {{ update.latest }} 后替换并重启管理服务，页面会短暂断开，代理流量不受影响。继续？
        </NPopconfirm>
        <NButton :loading="checking" :disabled="updateBusy || scheduleActive" @click="checkUpdate">重新检查</NButton>
        <NButton :disabled="updateBusy || info?.dev_mode || scheduleActive" @click="openScheduleModal">预约升级</NButton>
      </NSpace>

      <NAlert v-if="checkError" type="error" :bordered="false" closable @close="checkError = ''">{{ checkError }}</NAlert>
      <NAlert v-if="info?.dev_mode" type="warning" :bordered="false">开发模式不能自动重启管理服务，「立即更新」和「预约升级」在正式部署环境中可用。</NAlert>

      <NCollapse v-if="update?.update_available && update.notes" :default-expanded-names="['notes']">
        <NCollapseItem :title="`${update.latest} 更新说明`" name="notes">
          <pre class="notes">{{ update.notes }}</pre>
        </NCollapseItem>
      </NCollapse>

      <div v-if="upgradeSchedule && upgradeSchedule.status !== 'idle'" class="schedule" :class="`schedule-${scheduleView.type}`">
        <div class="schedule-head">
          <NText strong>预约升级</NText>
          <NTag size="small" :type="scheduleView.type" :bordered="false">{{ scheduleView.label }}</NTag>
          <span class="schedule-actions">
            <NButton v-if="upgradeSchedule.status === 'waiting'" size="small" :loading="scheduleBusy" @click="cancelUpgradeSchedule">取消预约</NButton>
            <NButton v-if="upgradeSchedule.task_id" size="small" text type="primary" @click="taskCenter.open({ source: 'task', id: upgradeSchedule.task_id })">查看任务</NButton>
            <NButton v-if="scheduleFinished" size="small" text @click="clearUpgradeSchedule">清除</NButton>
          </span>
        </div>

        <template v-if="upgradeSchedule.status === 'waiting'">
          <NProgress type="line" :percentage="waitPercent" :show-indicator="false" :height="6" processing />
          <div class="schedule-meta">
            <span>目标：{{ upgradeSchedule.target === 'latest' ? '最新正式版' : upgradeSchedule.target }}</span>
            <span>剩余 {{ remaining }}（截止 {{ formatTime(upgradeSchedule.deadline_at) }}）</span>
            <span>每 {{ duration(upgradeSchedule.interval_seconds) }}检查一次<template v-if="nextCheckIn">，下次 {{ nextCheckIn }}</template></span>
            <span v-if="hasTime(upgradeSchedule.last_check_at)">上次检查 {{ formatTime(upgradeSchedule.last_check_at) }}</span>
          </div>
          <NText v-if="upgradeSchedule.resolved_version && upgradeSchedule.target === 'latest'" depth="3" class="schedule-note">
            目前最新为 {{ upgradeSchedule.resolved_version }}，不比当前版本新，继续等待。
          </NText>
          <NText depth="3" class="schedule-note">管理服务在后台检查，不需要保持页面打开。</NText>
        </template>

        <template v-else-if="upgradeSchedule.status === 'updating'">
          <UpgradeProgress :stage="scheduleStage" :progress="scheduleTask.progress.value" :speed="scheduleTask.speed.value" :target="upgradeSchedule.resolved_version" />
          <NText v-if="scheduleUnreachable" depth="3" class="schedule-note">管理服务正在重启，恢复后页面自动刷新。</NText>
        </template>

        <template v-else>
          <div class="schedule-meta">
            <span>目标：{{ upgradeSchedule.target === 'latest' ? '最新正式版' : upgradeSchedule.target }}</span>
            <span v-if="hasTime(upgradeSchedule.finished_at)">结束于 {{ formatTime(upgradeSchedule.finished_at) }}</span>
          </div>
        </template>
        <NText v-if="upgradeSchedule.last_error" type="error" class="schedule-note">{{ upgradeSchedule.last_error }}</NText>
      </div>

      <NText depth="3" class="footnote">
        更新只替换管理服务本身，xray 和 mihomo 由独立的服务承载，代理流量不受影响。下载经由「系统设置 → 下载代理」，替换前会核对 sha256 并试运行新程序，保留上一版本。
      </NText>
    </NSpace>
  </NCard>

  <NModal v-model:show="scheduleModalVisible" preset="card" title="预约升级" style="width: 560px; max-width: calc(100vw - 32px)">
    <NForm label-placement="left" label-width="90">
      <NFormItem label="目标版本" :feedback="scheduleMode === 'version' && !versionValid ? `格式如 ${versionExample}` : ''" :validation-status="scheduleMode === 'version' && !versionValid ? 'error' : undefined">
        <NSpace vertical style="width: 100%">
          <NRadioGroup v-model:value="scheduleMode">
            <NRadioButton value="latest">最新正式版</NRadioButton>
            <NRadioButton value="version">指定版本</NRadioButton>
          </NRadioGroup>
          <NInput v-if="scheduleMode === 'version'" v-model:value="scheduleVersion" :placeholder="`例如 ${versionExample}`" />
        </NSpace>
      </NFormItem>
      <NFormItem label="等待时长">
        <NSpace vertical style="width: 100%">
          <NRadioGroup v-model:value="scheduleDuration" size="small">
            <NRadioButton v-for="option in durationOptions" :key="option.value" :value="option.value">{{ option.label }}</NRadioButton>
          </NRadioGroup>
          <NSpace v-if="!scheduleDuration" align="center">
            <NInputNumber v-model:value="customMinutes" :min="1" :max="1440" style="width: 140px" />
            <NText depth="3">分钟（最长 24 小时）</NText>
          </NSpace>
        </NSpace>
      </NFormItem>
      <NFormItem label="检查间隔">
        <NRadioGroup v-model:value="scheduleInterval" size="small">
          <NRadioButton v-for="option in intervalOptions" :key="option.value" :value="option.value">{{ option.label }}</NRadioButton>
        </NRadioGroup>
      </NFormItem>
    </NForm>
    <NAlert type="info" :bordered="false">{{ scheduleSummary }}管理服务在后台检查，不需要保持页面打开，服务重启后预约继续。</NAlert>
    <NAlert v-if="scheduleError" type="error" :bordered="false" style="margin-top: 12px">{{ scheduleError }}</NAlert>
    <template #footer>
      <NSpace justify="end">
        <NButton @click="scheduleModalVisible = false">取消</NButton>
        <NButton type="primary" :loading="scheduleBusy" :disabled="!versionValid" @click="scheduleUpgrade">开始预约</NButton>
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
    style="width: 460px; max-width: calc(100vw - 32px)"
    @update:show="(show) => !show && closeUpdateModal()"
  >
    <NAlert v-if="updatePhase === 'unknown'" type="warning" :bordered="false">
      没有收到提交结果，可能是网络中断。请在后台任务中确认是否已创建自更新任务，再决定是否重试。
    </NAlert>
    <template v-else>
      <UpgradeProgress
        :stage="updateStage"
        :failed="updatePhase === 'failed' || updatePhase === 'timeout'"
        :progress="updateTask.progress.value"
        :speed="updateTask.speed.value"
        :target="updateTarget"
      />
      <NAlert v-if="updatePhase === 'complete'" type="success" :bordered="false" style="margin-top: 8px">已更新到 {{ updateTarget }}，正在刷新页面…</NAlert>
      <NAlert v-else-if="updateError" :type="updatePhase === 'timeout' ? 'warning' : 'error'" :bordered="false" style="margin-top: 8px">{{ updateError }}</NAlert>
    </template>
    <template #footer>
      <NSpace justify="end">
        <NButton v-if="updateTaskID" @click="taskCenter.open({ source: 'task', id: updateTaskID })">查看任务日志</NButton>
        <NButton v-else-if="updatePhase === 'unknown'" @click="taskCenter.open()">打开后台任务</NButton>
        <NButton v-if="updatePhase === 'unknown' || updatePhase === 'failed' || updatePhase === 'timeout'" @click="reloadPage">刷新页面</NButton>
        <NButton v-if="updateModalClosable" type="primary" @click="closeUpdateModal">关闭</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.versions { display: flex; align-items: stretch; flex-wrap: wrap; gap: 12px; }
.version-box {
  flex: 1 1 200px;
  padding: 12px 16px;
  border: 1px solid var(--ps-border);
  border-radius: 10px;
  background: var(--ps-surface-raised);
}
.version-label { display: block; font-size: 12px; }
.version-value { display: flex; align-items: center; gap: 8px; min-height: 30px; margin: 2px 0; font-size: 18px; }
.version-arrow { align-self: center; color: var(--ps-muted); font-size: 18px; }
.notes { margin: 0; white-space: pre-wrap; font-family: inherit; font-size: 13px; }
.schedule {
  padding: 14px 16px;
  border: 1px solid var(--ps-border);
  border-left-width: 3px;
  border-radius: 10px;
}
.schedule-info { border-left-color: var(--ps-accent); }
.schedule-success { border-left-color: #18a058; }
.schedule-warning { border-left-color: #f0a020; }
.schedule-error { border-left-color: var(--ps-danger); }
.schedule-head { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-bottom: 10px; }
.schedule-actions { display: inline-flex; align-items: center; gap: 12px; margin-left: auto; }
.schedule-meta { display: flex; flex-wrap: wrap; gap: 4px 18px; margin-top: 8px; font-size: 13px; color: var(--ps-muted); font-variant-numeric: tabular-nums; }
.schedule-note { display: block; margin-top: 6px; font-size: 13px; }
.footnote { font-size: 12px; }
@media (max-width: 767px) {
  .version-arrow { display: none; }
}
</style>
