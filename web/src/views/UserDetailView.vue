<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  NAlert, NBadge, NButton, NCard, NCollapse, NCollapseItem, NDatePicker, NDropdown, NEmpty, NForm, NFormItem, NIcon,
  NInput, NInputNumber, NPopconfirm, NRadioButton, NRadioGroup, NSpace, NSpin, NSwitch, NTabPane, NTabs, NTag, NText,
  useDialog, useMessage,
} from 'naive-ui'
import { ChevronDownOutline } from '@vicons/ionicons5'
import { api, emptyErrors, fieldState, MASKED, splitProblem, type FormErrors, type Schemas } from '../api/client'
import ByteSizeInput from '../components/ByteSizeInput.vue'
import ErrorList from '../components/ErrorList.vue'
import SecretInput from '../components/SecretInput.vue'
import SubscriptionCard from '../components/SubscriptionCard.vue'
import SubscriptionGapAlert from '../components/SubscriptionGapAlert.vue'
import TemplateInput from '../components/TemplateInput.vue'
import UsageCard from '../components/UsageCard.vue'
import { useDirty } from '../composables/useDirty'
import { usePageTitle } from '../composables/usePageTitle'
import { notifySaved, runtime } from '../runtime'
import { formatBytes } from '../util/format'
import { randomString, randomUUID, shadowsocksKey } from '../util/generate'
import { userTabFor, userTabs, type UserTab } from '../util/user-tabs'

type User = Schemas['User']
type Reference = Schemas['UserReference']

const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const id = computed(() => Number(route.params.id))
const user = ref<User | null>(null)
const references = ref<Reference[]>([])
/** 订阅节点情况：没有节点的原因和尚未应用的 stack，用于「接入」「订阅」两页的提示。 */
const subscription = ref<Schemas['UserSubscriptionStatus'] | null>(null)
const stacks = ref<Schemas['StackSummary'][]>([])
/** 归属该用户的手工节点数，显示在「订阅」标签页。 */
const manualNodes = ref(0)
const pausing = ref(false)
const errors = ref<FormErrors>(emptyErrors())
const saving = ref(false)
const { dirty, markClean, patchBaseline, saved } = useDirty(user)
/** 已保存的名称，用作页面标题，编辑名称输入框时不跟着变。 */
const savedName = ref('')
usePageTitle(() => savedName.value)

const f = (path: string, hint?: string) => fieldState(errors.value, path, hint)

// 页面分为 5 个标签页，当前标签页记在地址栏里，刷新或分享链接后回到原处。
const tab = ref<UserTab>(userTabs.some((t) => t.name === route.query.tab) ? (route.query.tab as UserTab) : 'basic')
watch(tab, (name) => {
  if (route.query.tab !== name) void router.replace({ query: { ...route.query, tab: name === 'basic' ? undefined : name } })
})
const tabErrors = computed(() => new Set(Object.keys(errors.value.fields).map(userTabFor)))
/** 各标签页内是否有未保存的修改；当场生效的启用开关和逐 stack 暂停不算。 */
const tabDirty = computed(() => {
  const out = new Set<UserTab>()
  const current = user.value
  const base = saved.value
  if (!current || !base) return out
  for (const t of userTabs) {
    if (t.fields.some((key) => JSON.stringify(current[key]) !== JSON.stringify(base[key]))) out.add(t.name)
  }
  return out
})
const tabLabel = (name: UserTab, label: string) => () => {
  if (tabErrors.value.has(name)) return h(NBadge, { dot: true, type: 'error', offset: [6, 0] }, () => label)
  if (tabDirty.value.has(name)) return h(NBadge, { dot: true, type: 'warning', offset: [6, 0] }, () => label)
  return label
}

/** 顶部摘要用的用量：打开页面时读取一次，「用量」标签页打开期间随其刷新。 */
const usage = ref<Schemas['UserUsage'] | null>(null)
async function loadUsage() {
  const { data } = await api.GET('/api/users/{id}/usage', { params: { path: { id: id.value } } })
  if (data) usage.value = data
}
const pauseLabels = { daily: '每日阈值', quota: '额度', expired: '到期' } as const
const statusView = computed(() => {
  if (!user.value?.enabled) return { type: 'default' as const, text: '停用' }
  const pauses = usage.value?.auto_pauses ?? []
  if (pauses.length) return { type: 'warning' as const, text: `${pauses.map((p) => pauseLabels[p.reason]).join('、')}暂停` }
  const paused = user.value.paused_stacks?.length ?? 0
  return paused ? { type: 'warning' as const, text: `启用 · ${paused} 个 stack 暂停` } : { type: 'success' as const, text: '启用' }
})
const used = (bytes?: { up: number; down: number }) => (bytes ? bytes.up + bytes.down : 0)
const quotaText = computed(() => {
  const u = user.value
  if (!u) return ''
  const period = used(usage.value?.period)
  if (!u.quota_bytes) return `${u.reset_mode === 'monthly' ? '本周期' : '累计'} ${formatBytes(period)} · 不限额度`
  return `${formatBytes(period)} / ${formatBytes(u.quota_bytes)}（${Math.min(999, Math.round((period / u.quota_bytes) * 100))}%）`
})
const dailyText = computed(() => {
  const limit = user.value?.daily_limit_bytes ?? 0
  const today = used(usage.value?.today)
  return limit ? `${formatBytes(today)} / ${formatBytes(limit)}` : formatBytes(today)
})
/** 额度和阈值下方显示的已用量，帮助判断设置是否合适。 */
const periodUsedText = computed(() => `${new Date(usage.value?.period_start ?? 0).getFullYear() > 1 ? '本周期' : '累计'}已用 ${formatBytes(used(usage.value?.period))}`)
const expireText = computed(() => {
  const value = saved.value?.expire_at
  if (!value) return '不过期'
  const at = new Date(value)
  return at.getTime() < Date.now() ? `已于 ${at.toLocaleDateString()} 到期` : `${at.toLocaleDateString()} 到期`
})

async function load() {
  const { data } = await api.GET('/api/users/{id}', { params: { path: { id: id.value } } })
  if (!data) {
    message.error('用户不存在')
    router.replace({ name: 'users' })
    return
  }
  user.value = data.user
  savedName.value = data.user.name
  references.value = data.references
  subscription.value = data.subscription ?? null
  const [stackResult, nodeResult] = await Promise.all([api.GET('/api/stacks'), api.GET('/api/manual-nodes')])
  stacks.value = stackResult.data?.stacks ?? []
  manualNodes.value = (nodeResult.data ?? []).filter((node) => node.user === data.user.name).length
  markClean()
  void loadUsage()
}
onMounted(load)

/** 只刷新入站引用和订阅节点情况，不动表单；应用完成或保存后调用。 */
async function refreshSubscription() {
  const { data } = await api.GET('/api/users/{id}', { params: { path: { id: id.value } } })
  if (!data || data.user.id !== id.value) return
  references.value = data.references
  subscription.value = data.subscription ?? null
}
// 待应用变更随应用任务完成而变化，这时已应用的版本也变了，重新读取提示。
watch(() => [runtime.pendingCount, runtime.pendingStacks.join(',')], () => { if (user.value) void refreshSubscription() })
const unapplied = computed(() => new Set(subscription.value?.unapplied_stacks ?? []))

/** 启用开关和逐 stack 暂停当场生效并写入配置，不需要点「保存」，也不需要应用。 */
async function setPause(enabled: boolean, pausedStacks: string[]) {
  if (!user.value) return
  pausing.value = true
  const { data, error } = await api.PUT('/api/users/{id}/pause', {
    params: { path: { id: id.value } },
    body: { enabled, paused_stacks: pausedStacks },
  })
  pausing.value = false
  if (error) {
    const problem = splitProblem(error, '操作失败')
    message.error([...problem.global, ...Object.values(problem.fields)].join('；'))
    return
  }
  user.value.enabled = data!.user.enabled
  user.value.paused_stacks = data!.user.paused_stacks
  patchBaseline((base) => {
    base.enabled = data!.user.enabled
    base.paused_stacks = data!.user.paused_stacks
  })
  void loadUsage()
  for (const note of data!.notes) message.warning(note, { duration: 8000 })
  message.success(enabled && !pausedStacks.length ? '已恢复，当场生效' : '已暂停，当场生效')
}

function toggleStack(stack: string, paused: boolean) {
  const current = user.value?.paused_stacks ?? []
  setPause(user.value!.enabled, paused ? [...current, stack] : current.filter((name) => name !== stack))
}

/** 加入或移出 stack：修改该 stack 的入站，需要应用后生效。 */
const joining = ref(false)
async function setMember(stack: Schemas['StackSummary'], member: boolean) {
  if (!user.value) return
  joining.value = true
  const { data, error } = await api.PUT('/api/users/{id}/stacks/{stack_id}', {
    params: { path: { id: id.value, stack_id: stack.id! } }, body: { member },
  })
  joining.value = false
  if (error) {
    const problem = splitProblem(error, '操作失败')
    message.error([...problem.global, ...Object.values(problem.fields)].join('；'))
    return
  }
  user.value.paused_stacks = data!.paused_stacks
  patchBaseline((base) => { base.paused_stacks = data!.paused_stacks })
  await refreshSubscription()
  void notifySaved(message)
}

type AccessState = 'out' | 'on' | 'paused'
const rowState = (row: { refs: Reference[]; paused: boolean }): AccessState => (!row.refs.length ? 'out' : row.paused ? 'paused' : 'on')
function changeAccess(row: { stack: Schemas['StackSummary']; refs: Reference[]; paused: boolean }, next: AccessState) {
  const current = rowState(row)
  if (next === current) return
  if (next === 'out') {
    dialog.warning({
      title: `把 ${savedName.value} 移出 ${row.stack.name}？`,
      content: `将从该 stack 的 ${new Set(row.refs.map((item) => item.inbound)).size} 个入站中移除该用户，应用后生效。`,
      positiveText: '移出',
      negativeText: '取消',
      onPositiveClick: () => setMember(row.stack, false),
    })
    return
  }
  if (current === 'out') {
    void setMember(row.stack, true)
    return
  }
  toggleStack(row.stack.name, next === 'paused')
}

/** 取回明文凭据，只替换仍是占位的字段，不覆盖已修改的内容。 */
async function reveal() {
  const { data } = await api.GET('/api/users/{id}/secrets', { params: { path: { id: id.value } } })
  if (!data || !user.value) return
  const keep = (current: string, plain: string) => (current === MASKED ? plain : current)
  // 当前值和基准快照里仍是占位的字段一并换成明文，取回明文本身不算修改。
  const fill = (u: User) => {
    u.uuid = keep(u.uuid, data.uuid)
    u.ss_password = keep(u.ss_password, data.ss_password)
    u.sub_token = keep(u.sub_token, data.sub_token)
    for (const profile of u.profiles) {
      const plain = data.profiles.find((p) => p.id === profile.id)
      if (plain) {
        profile.uuid = keep(profile.uuid, plain.uuid)
        profile.ss_password = keep(profile.ss_password, plain.ss_password)
      }
    }
  }
  fill(user.value)
  patchBaseline(fill)
}

const expireAt = computed({
  get: () => (user.value?.expire_at ? new Date(user.value.expire_at).getTime() : null),
  set: (value: number | null) => {
    if (user.value) user.value.expire_at = value ? new Date(value).toISOString().replace(/\.\d{3}Z$/, 'Z') : ''
  },
})

/** 到期时间快捷延长：从当前到期时间和现在两者中较晚的一个开始计算。 */
function extendExpire(months: number) {
  if (!user.value) return
  const current = user.value.expire_at ? new Date(user.value.expire_at) : null
  const base = current && current.getTime() > Date.now() ? current : new Date()
  const next = new Date(base)
  next.setMonth(next.getMonth() + months)
  expireAt.value = next.getTime()
}

/** 「接入」标签页：每个 stack 一行，列出引用该用户的入站和逐 stack 暂停开关。 */
const accessRows = computed(() => stacks.value.map((stack) => ({
  stack,
  refs: references.value.filter((item) => item.stack_id === stack.id),
  paused: (user.value?.paused_stacks ?? []).includes(stack.name),
})))
const joinedStacks = computed(() => accessRows.value.filter((row) => row.refs.length).length)

function addProfile() {
  user.value?.profiles.push({ name: '', uuid: randomUUID(), ss_password: randomString(24), remark: '', display_template: '', tag: '' })
}

async function save() {
  if (!user.value) return
  saving.value = true
  const { data, error } = await api.PUT('/api/users/{id}', { params: { path: { id: id.value } }, body: user.value })
  saving.value = false
  if (error) {
    errors.value = splitProblem(error)
    // 出错的字段不在当前标签页时切过去。
    const first = Object.keys(errors.value.fields)[0]
    if (first && !tabErrors.value.has(tab.value)) tab.value = userTabFor(first)
    return
  }
  errors.value = emptyErrors()
  user.value = data!
  savedName.value = data!.name
  markClean()
  void refreshSubscription()
  void notifySaved(message)
}

async function resetToken() {
  const { data, error } = await api.POST('/api/users/{id}/reset-token', { params: { path: { id: id.value } } })
  if (error) {
    message.error(splitProblem(error).global.join('；'))
    return
  }
  if (user.value && data) {
    user.value.sub_token = data.sub_token
    patchBaseline((base) => { base.sub_token = data.sub_token })
  }
  message.success('订阅 token 已重置，旧链接立即失效')
}

const moreOptions = [{ key: 'delete', label: '删除用户', props: { style: 'color: var(--ps-danger)' } }]
function onMore(key: string) {
  if (key !== 'delete') return
  dialog.warning({
    title: `删除用户 ${savedName.value}？`,
    content: '删除后，引用该用户的入站会移除这个用户。',
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: remove,
  })
}

async function remove() {
  const { error } = await api.DELETE('/api/users/{id}', { params: { path: { id: id.value } } })
  if (error) {
    errors.value = splitProblem(error, '删除失败')
    return
  }
  markClean()
  message.success('已删除')
  router.replace({ name: 'users' })
}
</script>

<template>
  <NSpin :show="!user">
    <NForm v-if="user" label-placement="top" class="user-form">
      <!-- 顶部摘要在所有标签页都显示：状态、今日用量、额度、到期和保存。 -->
      <NCard size="small" class="user-toolbar">
        <div class="toolbar">
          <div class="summary">
            <NTag size="small" :type="statusView.type" :bordered="false">{{ statusView.text }}</NTag>
            <span class="summary-item"><NText depth="3">今日</NText> {{ dailyText }}</span>
            <span class="summary-item"><NText depth="3">额度</NText> {{ quotaText }}</span>
            <span class="summary-item"><NText depth="3">到期</NText> {{ expireText }}</span>
          </div>
          <NSpace align="center" :size="12" class="toolbar-actions">
            <NText v-if="dirty" type="warning">有未保存的修改</NText>
            <NDropdown :options="moreOptions" trigger="click" placement="bottom-end" @select="onMore">
              <NButton icon-placement="right">
                <template #icon><NIcon><ChevronDownOutline /></NIcon></template>
                更多
              </NButton>
            </NDropdown>
            <NButton type="primary" :loading="saving" :disabled="!dirty" @click="save">保存</NButton>
          </NSpace>
        </div>
      </NCard>
      <ErrorList :errors="errors.global" />

      <NTabs v-model:value="tab" type="line" animated class="detail-tabs">
        <NTabPane name="basic" :tab="tabLabel('basic', '基本')">
          <section class="pane-section">
            <div class="status-row">
              <NSwitch :value="user.enabled" :loading="pausing" aria-label="启用用户" @update:value="(v) => setPause(v, user!.paused_stacks ?? [])" />
              <div>
                <div class="status-title">
                  <NText strong>{{ user.enabled ? '已启用' : '已停用' }}</NText>
                  <NTag size="small" type="success" :bordered="false">当场生效</NTag>
                </div>
                <NText depth="3" class="status-desc">切换后立即通过 xray API 移除或加回该用户，配置保留，不需要保存和应用。只在部分 stack 上暂停请到「接入」设置。</NText>
              </div>
            </div>
          </section>
          <section class="pane-section">
            <h3 class="pane-title">基本信息</h3>
            <div class="grid-2">
              <NFormItem label="名称" required v-bind="f('name', '字母或数字开头，可含 _ . -；改名不影响引用')">
                <NInput v-model:value="user.name" />
              </NFormItem>
              <NFormItem label="备注" v-bind="f('remark', '仅用于管理识别，不影响订阅节点名')">
                <NInput v-model:value="user.remark" />
              </NFormItem>
            </div>
            <NFormItem label="说明" v-bind="f('note', '内部记录，例如联系方式、付费情况')">
              <NInput v-model:value="user.note" type="textarea" :autosize="{ minRows: 2 }" />
            </NFormItem>
          </section>
          <section class="pane-section">
            <h3 class="pane-title">额度与到期</h3>
            <NText depth="3" tag="p" class="pane-desc">额度、每日阈值和到期都按全部 stack 的合计判定，超出后按设置自动暂停并发送邮件通知。</NText>
            <div class="grid-2">
              <NFormItem label="流量额度" v-bind="f('quota_bytes', '0 表示不限')">
                <div class="field-stack">
                  <ByteSizeInput v-model:value="user.quota_bytes" />
                  <NText depth="3" class="field-meta">{{ periodUsedText }}</NText>
                </div>
              </NFormItem>
              <NFormItem label="重置周期" v-bind="f('reset_mode')">
                <div class="field-stack">
                  <NSpace align="center" :size="8">
                    <NRadioGroup v-model:value="user.reset_mode">
                      <NRadioButton value="none">不重置</NRadioButton>
                      <NRadioButton value="monthly">每月</NRadioButton>
                    </NRadioGroup>
                    <template v-if="user.reset_mode === 'monthly'">
                      <NInputNumber v-model:value="user.reset_day" :min="1" :max="28" style="width: 96px" aria-label="重置日" />
                      <NText depth="3">日</NText>
                    </template>
                  </NSpace>
                  <NText depth="3" class="field-meta">{{ user.reset_mode === 'monthly' ? `每月 ${user.reset_day} 日 00:00 清零本周期用量` : '额度按累计用量计算，不清零' }}</NText>
                </div>
              </NFormItem>
              <NFormItem label="每日用量阈值" v-bind="f('daily_limit_bytes', '0 表示不限；上行与下行合计，按设置时区的自然日统计')">
                <div class="field-stack">
                  <ByteSizeInput small :value="user.daily_limit_bytes ?? 0" @update:value="(value: number) => (user!.daily_limit_bytes = value)" />
                  <NRadioGroup v-if="user.daily_limit_bytes" v-model:value="user.daily_limit_action" size="small">
                    <NRadioButton value="pause">达到后暂停并通知</NRadioButton>
                    <NRadioButton value="notify">只通知</NRadioButton>
                  </NRadioGroup>
                  <NText depth="3" class="field-meta">今日已用 {{ formatBytes(used(usage?.today)) }}</NText>
                </div>
              </NFormItem>
              <NFormItem label="到期时间" v-bind="f('expire_at', '留空表示不过期')">
                <div class="field-stack">
                  <NDatePicker v-model:value="expireAt" type="datetime" clearable style="width: 100%" />
                  <NSpace :size="4">
                    <NButton size="tiny" secondary @click="extendExpire(1)">+1 个月</NButton>
                    <NButton size="tiny" secondary @click="extendExpire(3)">+3 个月</NButton>
                    <NButton size="tiny" secondary @click="extendExpire(12)">+1 年</NButton>
                    <NButton v-if="user.expire_at" size="tiny" quaternary @click="expireAt = null">设为不过期</NButton>
                  </NSpace>
                  <NText depth="3" class="field-meta">延长从当前到期时间和现在两者中较晚的一个开始计算</NText>
                </div>
              </NFormItem>
            </div>
          </section>
        </NTabPane>

        <NTabPane name="subscription" :tab="tabLabel('subscription', '订阅')">
          <section class="pane-section">
            <h3 class="pane-title">订阅链接</h3>
            <SubscriptionGapAlert :status="subscription" />
            <SubscriptionCard :user-id="id" bare />
          </section>
          <section class="pane-section">
            <h3 class="pane-title">订阅设置</h3>
            <div class="grid-2">
              <NFormItem label="订阅 token" v-bind="f('sub_token', '订阅链接中的身份凭据')">
                <div class="field-stack">
                  <SecretInput v-model:value="user.sub_token" :reveal="reveal" />
                  <NPopconfirm @positive-click="resetToken">
                    <template #trigger><NButton size="small" style="align-self: flex-start">重置 token</NButton></template>
                    重置后旧的订阅链接立即失效，客户端需要重新导入。确定重置？
                  </NPopconfirm>
                </div>
              </NFormItem>
              <NFormItem label="展示名模板" v-bind="f('display_template', '适用于 default 档案，优先级高于入站模板；入站中的用户单独配置仍可覆盖此模板')">
                <TemplateInput v-model:value="user.display_template" />
              </NFormItem>
            </div>
          </section>
          <section class="pane-section">
            <h3 class="pane-title">节点来源</h3>
            <ul class="source-list">
              <li><NText>本机 stack 节点</NText><NText depth="3">：入站引用该用户的 stack 共 {{ joinedStacks }} 个，应用后其节点进入订阅<template v-if="unapplied.size">，其中 {{ unapplied.size }} 个尚未应用</template></NText></li>
              <li>
                <NText>手工节点</NText><NText depth="3">：{{ manualNodes ? `${manualNodes} 个` : '没有' }}</NText>
                <RouterLink :to="{ name: 'extra-nodes', query: { user: savedName } }" class="source-link">{{ manualNodes ? '查看' : '添加' }}</RouterLink>
              </li>
              <li><NText>远端节点源</NText><NText depth="3">：按用户名分给同名用户</NText><RouterLink :to="{ name: 'extra-nodes', query: { tab: 'remote' } }" class="source-link">查看</RouterLink></li>
            </ul>
          </section>
        </NTabPane>

        <NTabPane name="credentials" :tab="tabLabel('credentials', '凭据')">
          <section class="pane-section">
            <h3 class="pane-title">default 档案</h3>
            <NText depth="3" tag="p" class="pane-desc">default 档案即用户本身；入站引用用户时默认使用它。</NText>
            <div class="grid-2">
              <NFormItem label="UUID" required v-bind="f('uuid')">
                <SecretInput v-model:value="user.uuid" :generate="randomUUID" :reveal="reveal" />
              </NFormItem>
              <NFormItem label="SS 密码" required v-bind="f('ss_password', '被 2022 系列 shadowsocks 入站引用时需要对应长度的 base64 密钥')">
                <div class="field-stack">
                  <SecretInput v-model:value="user.ss_password" :generate="() => randomString(24)" :reveal="reveal" />
                  <NSpace :size="4">
                    <NButton size="tiny" secondary @click="user.ss_password = shadowsocksKey('2022-blake3-aes-128-gcm')">生成 2022 密钥（16 字节）</NButton>
                    <NButton size="tiny" secondary @click="user.ss_password = shadowsocksKey('2022-blake3-aes-256-gcm')">生成 2022 密钥（32 字节）</NButton>
                  </NSpace>
                </div>
              </NFormItem>
            </div>
            <NCollapse>
              <NCollapseItem title="高级" name="advanced">
                <NFormItem label="tag" v-bind="f('tag')" style="max-width: 420px">
                  <NInput v-model:value="user.tag" />
                </NFormItem>
              </NCollapseItem>
            </NCollapse>
          </section>
          <section class="pane-section">
            <div class="pane-head">
              <h3 class="pane-title">其他凭据档案 <NTag v-if="user.profiles.length" size="small" :bordered="false">{{ user.profiles.length }}</NTag></h3>
              <NButton size="small" @click="addProfile">添加档案</NButton>
            </div>
            <NText depth="3" tag="p" class="pane-desc">入站引用用户时可以改选其他档案，使用不同的 UUID 或密码，例如为不同设备分别发放凭据。</NText>
            <NEmpty v-if="!user.profiles.length" size="small" description="没有额外档案" class="profiles-empty">
              <template #extra><NButton size="small" @click="addProfile">添加档案</NButton></template>
            </NEmpty>
            <div class="profiles">
              <div v-for="(profile, index) in user.profiles" :key="profile.id ?? `new-${index}`" class="profile-card">
                <div class="profile-head">
                  <NText strong>{{ profile.name || '未命名档案' }}</NText>
                  <NText v-if="profile.remark" depth="3" class="profile-remark">{{ profile.remark }}</NText>
                  <NTag v-if="!profile.id" size="small" type="warning" :bordered="false">未保存</NTag>
                  <NPopconfirm @positive-click="user.profiles.splice(index, 1)">
                    <template #trigger><NButton size="tiny" type="error" quaternary class="profile-delete">删除</NButton></template>
                    删除档案「{{ profile.name || '未命名档案' }}」？引用它的入站需要改选其他档案，保存后生效。
                  </NPopconfirm>
                </div>
                <div class="grid-2">
                  <NFormItem label="档案名" v-bind="f(`profiles[${index}].name`)">
                    <NInput v-model:value="profile.name" />
                  </NFormItem>
                  <NFormItem label="备注">
                    <NInput v-model:value="profile.remark" />
                  </NFormItem>
                  <NFormItem label="UUID" v-bind="f(`profiles[${index}].uuid`)">
                    <SecretInput v-model:value="profile.uuid" :generate="randomUUID" :reveal="reveal" />
                  </NFormItem>
                  <NFormItem label="SS 密码" v-bind="f(`profiles[${index}].ss_password`)">
                    <SecretInput v-model:value="profile.ss_password" :generate="() => randomString(24)" :reveal="reveal" />
                  </NFormItem>
                  <NFormItem label="展示名" v-bind="f(`profiles[${index}].display_template`)">
                    <TemplateInput v-model:value="profile.display_template" />
                  </NFormItem>
                  <NFormItem label="tag">
                    <NInput v-model:value="profile.tag" />
                  </NFormItem>
                </div>
              </div>
            </div>
          </section>
        </NTabPane>

        <NTabPane name="access" :tab="tabLabel('access', '接入')">
          <section class="pane-section">
            <h3 class="pane-title">各 stack 接入</h3>
            <NText depth="3" tag="p" class="pane-desc">
              每个 stack 一行，右侧选择该用户在这个 stack 上的状态。「加入」把该用户（default 档案）加进这个 stack 全部进入订阅的入站，「未加入」从这个 stack 的全部入站中移除该用户，两者都需要应用后生效；「启用」和「暂停」当场生效，不需要保存和应用。需要使用其他档案或只加入部分入站时，到 stack 的「入站」中编辑。SOCKS5/HTTP 入站的账号由多个用户共用，暂停只把它们从该用户的订阅中去掉。
            </NText>
            <SubscriptionGapAlert :status="subscription" />
            <NAlert v-if="!user.enabled" type="warning" :bordered="false" style="margin-bottom: 12px">用户已停用，在「基本」中启用后才能按 stack 启用或暂停；加入和移出不受影响。</NAlert>
            <NEmpty v-if="!stacks.length" size="small" description="还没有 stack" />
            <div v-else class="access-list">
              <div class="access-row access-header">
                <span>Stack</span><span>引用该用户的入站</span><span>状态</span>
              </div>
              <div v-for="row in accessRows" :key="row.stack.id" class="access-row" :class="{ paused: row.paused }">
                <div class="access-stack">
                  <RouterLink :to="{ name: 'stack', params: { id: row.stack.id } }">{{ row.stack.name }}</RouterLink>
                  <NTag size="tiny" :bordered="false" :type="row.stack.role === 'auto' ? 'info' : 'default'">{{ row.stack.role }}</NTag>
                </div>
                <div class="access-refs">
                  <template v-if="row.refs.length">
                    <RouterLink v-for="item in row.refs" :key="item.inbound" :to="{ name: 'stack', params: { id: item.stack_id }, query: { tab: 'inbounds' } }">
                      <NTag size="small" :bordered="false">{{ item.inbound }}<template v-if="item.profile"> · 档案 {{ item.profile }}</template></NTag>
                    </RouterLink>
                  </template>
                  <NText v-else depth="3" class="access-none">未加入</NText>
                  <NTag v-if="unapplied.has(row.stack.name)" size="small" type="warning" :bordered="false" title="入站已引用该用户，但这个 stack 还没有重新应用，节点暂时不在订阅中">未应用</NTag>
                </div>
                <NRadioGroup
                  size="small" :value="rowState(row)" :disabled="pausing || joining" :aria-label="`该用户在 ${row.stack.name} 上的状态`"
                  @update:value="(v: AccessState) => changeAccess(row, v)"
                >
                  <NRadioButton value="out">未加入</NRadioButton>
                  <NRadioButton value="on" :disabled="!!row.refs.length && !user.enabled">{{ row.refs.length ? '启用' : '加入' }}</NRadioButton>
                  <NRadioButton value="paused" :disabled="!row.refs.length || !user.enabled">暂停</NRadioButton>
                </NRadioGroup>
              </div>
            </div>
          </section>
        </NTabPane>

        <!-- 只在打开时挂载，用量每 10 秒的刷新随之开始和停止。 -->
        <NTabPane name="usage" :tab="tabLabel('usage', '用量')" display-directive="if">
          <UsageCard :user-id="id" :daily-limit="user.daily_limit_bytes ?? 0" :quota="user.quota_bytes" :bordered="false" class="embedded-card" @update="(data: Schemas['UserUsage']) => (usage = data)" />
        </NTabPane>
      </NTabs>
    </NForm>
  </NSpin>
</template>

<style scoped>
.user-toolbar {
  position: sticky;
  top: 0;
  z-index: 10;
  margin-bottom: 20px;
  background: var(--n-color);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
}
.toolbar { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px 20px; }
.summary { display: flex; align-items: center; flex-wrap: wrap; gap: 6px 20px; font-variant-numeric: tabular-nums; }
.summary-item { white-space: nowrap; }
.toolbar-actions { margin-left: auto; }
.pane-section + .pane-section { margin-top: 20px; padding-top: 20px; border-top: 1px solid var(--ps-border); }
.pane-title { display: flex; align-items: center; gap: 8px; margin: 0 0 12px; font-size: 15px; font-weight: 600; }
.pane-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 4px; }
.pane-head .pane-title { margin: 0; }
.pane-desc { margin: 0 0 12px; font-size: 13px; }
.grid-2 { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); align-items: start; column-gap: 24px; }
.field-stack { display: flex; flex-direction: column; gap: 6px; width: 100%; }
.field-meta { font-size: 12px; }
.status-row { display: flex; align-items: flex-start; gap: 14px; }
.status-row > .n-switch { margin-top: 2px; }
.status-title { display: flex; align-items: center; gap: 8px; }
.status-desc { display: block; margin-top: 4px; font-size: 13px; }
.source-list { margin: 0; padding-left: 18px; font-size: 14px; line-height: 2; }
.source-link { margin-left: 8px; font-size: 13px; }
.profiles { display: grid; gap: 12px; }
.profiles-empty { margin: 12px 0; }
.profile-card { border: 1px solid var(--ps-border); border-radius: 10px; padding: 12px 16px 0; background: var(--ps-surface-raised); }
.profile-head { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; min-height: 28px; }
.profile-remark { font-size: 13px; }
.profile-delete { margin-left: auto; }
.access-list { border: 1px solid var(--ps-border); border-radius: 10px; overflow: hidden; }
.access-row { display: grid; grid-template-columns: minmax(140px, 1fr) minmax(0, 2fr) auto; align-items: center; gap: 12px; padding: 10px 16px; }
.access-row + .access-row { border-top: 1px solid var(--ps-border); }
.access-header { font-size: 12px; font-weight: 600; color: var(--ps-muted); background: var(--ps-surface-raised); padding-top: 8px; padding-bottom: 8px; }
.access-row.paused .access-stack, .access-row.paused .access-refs { opacity: 0.55; }
.access-stack { display: flex; align-items: center; gap: 6px; min-width: 0; }
.access-refs { display: flex; flex-wrap: wrap; gap: 4px 6px; min-width: 0; }
.access-none { font-size: 13px; }
@media (max-width: 1023px) {
  .grid-2 { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 640px) {
  .access-header { display: none; }
  .access-row { grid-template-columns: minmax(0, 1fr) auto; }
  .access-refs { grid-column: 1 / -1; grid-row: 2; }
}
.embedded-card :deep(> .n-card-header),
.embedded-card :deep(> .n-card__content) { padding-left: 0; padding-right: 0; }
.embedded-card { margin-bottom: 0 !important; }
</style>
