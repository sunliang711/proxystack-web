<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  NBadge, NButton, NCard, NCollapse, NCollapseItem, NDatePicker, NDropdown, NEmpty, NForm, NFormItem, NGrid, NGridItem, NIcon,
  NInput, NInputNumber, NList, NListItem, NPopconfirm, NRadioButton, NRadioGroup, NSpace, NSpin, NSwitch, NTabPane, NTabs, NTag, NText,
  useDialog, useMessage,
} from 'naive-ui'
import { ChevronDownOutline } from '@vicons/ionicons5'
import { api, emptyErrors, fieldState, MASKED, splitProblem, type FormErrors, type Schemas } from '../api/client'
import ByteSizeInput from '../components/ByteSizeInput.vue'
import ErrorList from '../components/ErrorList.vue'
import SecretInput from '../components/SecretInput.vue'
import SubscriptionCard from '../components/SubscriptionCard.vue'
import TemplateInput from '../components/TemplateInput.vue'
import UsageCard from '../components/UsageCard.vue'
import { useDirty } from '../composables/useDirty'
import { usePageTitle } from '../composables/usePageTitle'
import { notifySaved } from '../runtime'
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
const stacks = ref<Schemas['StackSummary'][]>([])
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
  stacks.value = (await api.GET('/api/stacks')).data?.stacks ?? []
  markClean()
  void loadUsage()
}
onMounted(load)

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
            <span class="summary-item"><NText depth="3">今日</NText> {{ formatBytes(used(usage?.today)) }}</span>
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
            <div class="grid-2">
              <NFormItem label="名称" required v-bind="f('name', '字母或数字开头，可含 _ . -；改名不影响引用')">
                <NInput v-model:value="user.name" />
              </NFormItem>
              <NFormItem label="启用" :feedback="'切换后当场生效：通过 xray API 移除或加回该用户，配置不删除'">
                <NSwitch :value="user.enabled" :loading="pausing" @update:value="(v) => setPause(v, user!.paused_stacks ?? [])" />
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
            <div class="grid-2">
              <NFormItem label="流量额度" v-bind="f('quota_bytes', '0 表示不限')">
                <ByteSizeInput v-model:value="user.quota_bytes" />
              </NFormItem>
              <NFormItem label="到期时间" v-bind="f('expire_at', '留空表示不过期')">
                <NDatePicker v-model:value="expireAt" type="datetime" clearable />
              </NFormItem>
              <NFormItem label="重置周期" v-bind="f('reset_mode')">
                <NSpace align="center">
                  <NRadioGroup v-model:value="user.reset_mode">
                    <NRadioButton value="none">不重置</NRadioButton>
                    <NRadioButton value="monthly">每月</NRadioButton>
                  </NRadioGroup>
                  <template v-if="user.reset_mode === 'monthly'">
                    <NInputNumber v-model:value="user.reset_day" :min="1" :max="28" style="width: 100px" aria-label="重置日" />
                    <NText depth="3">日</NText>
                  </template>
                </NSpace>
              </NFormItem>
              <NFormItem label="每日用量阈值" v-bind="f('daily_limit_bytes', '0 表示不限；上行与下行合计，按设置时区的自然日统计')">
                <NSpace vertical style="width: 100%">
                  <ByteSizeInput small :value="user.daily_limit_bytes ?? 0" @update:value="(value: number) => (user!.daily_limit_bytes = value)" />
                  <NRadioGroup v-if="user.daily_limit_bytes" v-model:value="user.daily_limit_action" size="small">
                    <NRadioButton value="pause">达到后暂停并通知</NRadioButton>
                    <NRadioButton value="notify">只通知</NRadioButton>
                  </NRadioGroup>
                </NSpace>
              </NFormItem>
            </div>
          </section>
        </NTabPane>

        <NTabPane name="subscription" :tab="tabLabel('subscription', '订阅')">
          <SubscriptionCard :user-id="id" :bordered="false" class="embedded-card" />
          <section class="pane-section">
            <h3 class="pane-title">订阅设置</h3>
            <div class="grid-2">
              <NFormItem label="订阅 token" v-bind="f('sub_token', '订阅链接中的身份凭据')">
                <NSpace vertical style="width: 100%">
                  <SecretInput v-model:value="user.sub_token" :reveal="reveal" />
                  <NPopconfirm @positive-click="resetToken">
                    <template #trigger><NButton size="small">重置 token</NButton></template>
                    重置后旧的订阅链接立即失效，客户端需要重新导入。确定重置？
                  </NPopconfirm>
                </NSpace>
              </NFormItem>
              <NFormItem label="展示名模板" v-bind="f('display_template', '适用于 default 档案，优先级高于入站模板；入站中的用户单独配置仍可覆盖此模板')">
                <TemplateInput v-model:value="user.display_template" />
              </NFormItem>
            </div>
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
                <NSpace vertical style="width: 100%">
                  <SecretInput v-model:value="user.ss_password" :generate="() => randomString(24)" :reveal="reveal" />
                  <NSpace :size="4">
                    <NButton size="tiny" quaternary @click="user.ss_password = shadowsocksKey('2022-blake3-aes-128-gcm')">生成 2022 密钥（16 字节）</NButton>
                    <NButton size="tiny" quaternary @click="user.ss_password = shadowsocksKey('2022-blake3-aes-256-gcm')">生成 2022 密钥（32 字节）</NButton>
                  </NSpace>
                </NSpace>
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
              <h3 class="pane-title">其他凭据档案</h3>
              <NButton size="small" @click="addProfile">添加档案</NButton>
            </div>
            <NText depth="3" tag="p" class="pane-desc">入站引用用户时可以改选其他档案，使用不同的 UUID 或密码。</NText>
            <NEmpty v-if="!user.profiles.length" size="small" description="没有额外档案" style="margin: 12px 0" />
            <div v-for="(profile, index) in user.profiles" :key="profile.id ?? `new-${index}`" class="profile">
              <NGrid cols="1 m:2" responsive="screen" :x-gap="24">
                <NGridItem>
                  <NFormItem label="档案名" v-bind="f(`profiles[${index}].name`)">
                    <NInput v-model:value="profile.name" />
                  </NFormItem>
                </NGridItem>
                <NGridItem>
                  <NFormItem label="备注">
                    <NInput v-model:value="profile.remark" />
                  </NFormItem>
                </NGridItem>
                <NGridItem>
                  <NFormItem label="UUID" v-bind="f(`profiles[${index}].uuid`)">
                    <SecretInput v-model:value="profile.uuid" :generate="randomUUID" :reveal="reveal" />
                  </NFormItem>
                </NGridItem>
                <NGridItem>
                  <NFormItem label="SS 密码" v-bind="f(`profiles[${index}].ss_password`)">
                    <SecretInput v-model:value="profile.ss_password" :generate="() => randomString(24)" :reveal="reveal" />
                  </NFormItem>
                </NGridItem>
                <NGridItem>
                  <NFormItem label="展示名" v-bind="f(`profiles[${index}].display_template`)">
                    <TemplateInput v-model:value="profile.display_template" />
                  </NFormItem>
                </NGridItem>
                <NGridItem>
                  <NFormItem label="tag">
                    <NSpace style="width: 100%" :wrap="false">
                      <NInput v-model:value="profile.tag" />
                      <NButton type="error" quaternary @click="user.profiles.splice(index, 1)">删除档案</NButton>
                    </NSpace>
                  </NFormItem>
                </NGridItem>
              </NGrid>
            </div>
          </section>
        </NTabPane>

        <NTabPane name="access" :tab="tabLabel('access', '接入')">
          <section class="pane-section">
            <h3 class="pane-title">各 stack 暂停 <NTag size="small" type="success" :bordered="false">当场生效</NTag></h3>
            <NText depth="3" tag="p" class="pane-desc">
              只在选中的 stack 上暂停该用户，不需要保存。SOCKS5/HTTP 入站的账号由多个用户共用，暂停只把它们从该用户的订阅中去掉。
            </NText>
            <NText v-if="!user.enabled" type="warning" tag="p" class="pane-desc">用户已停用，在「基本」中启用后才能按 stack 调整。</NText>
            <NEmpty v-if="!stacks.length" size="small" description="还没有 stack" />
            <NList v-else>
              <NListItem v-for="stack in stacks" :key="stack.id">
                <NSpace justify="space-between" align="center">
                  <RouterLink :to="{ name: 'stack', params: { id: stack.id } }">{{ stack.name }}</RouterLink>
                  <NSwitch
                    :value="!(user.paused_stacks ?? []).includes(stack.name)"
                    :disabled="!user.enabled || pausing"
                    @update:value="(v) => toggleStack(stack.name, !v)"
                  >
                    <template #checked>启用</template>
                    <template #unchecked>已暂停</template>
                  </NSwitch>
                </NSpace>
              </NListItem>
            </NList>
          </section>
          <section class="pane-section">
            <h3 class="pane-title">被以下入站引用</h3>
            <NEmpty v-if="!references.length" size="small" description="尚未被任何入站引用" />
            <NList v-else>
              <NListItem v-for="ref in references" :key="`${ref.stack}.${ref.inbound}`">
                <RouterLink :to="{ name: 'stack', params: { id: ref.stack_id }, query: { tab: 'inbounds' } }">{{ ref.stack }} · {{ ref.inbound }}</RouterLink>
                <NTag v-if="ref.profile" size="small" style="margin-left: 8px">档案 {{ ref.profile }}</NTag>
              </NListItem>
            </NList>
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
@media (max-width: 1023px) {
  .grid-2 { grid-template-columns: minmax(0, 1fr); }
}
.embedded-card :deep(> .n-card-header),
.embedded-card :deep(> .n-card__content) { padding-left: 0; padding-right: 0; }
.embedded-card { margin-bottom: 0 !important; }
.profile {
  border-top: 1px dashed rgba(128, 128, 128, 0.3);
  padding-top: 12px;
  margin-top: 12px;
}
</style>
