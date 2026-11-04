<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NAlert, NBadge, NButton, NCard, NDropdown, NDynamicTags, NForm, NFormItem, NIcon, NInput, NModal, NRadioButton, NRadioGroup,
  NSelect, NSpace, NSpin, NSwitch, NTabPane, NTabs, NText, useDialog, useMessage,
} from 'naive-ui'
import { ChevronDownOutline } from '@vicons/ionicons5'
import {
  api, emptyErrors, fieldState, MASKED, splitProblem, type FormErrors, type Schemas,
} from '../../api/client'
import ErrorList from '../../components/ErrorList.vue'
import EntityLink from '../../components/EntityLink.vue'
import ByteSizeInput from '../../components/ByteSizeInput.vue'
import PortInput from '../../components/PortInput.vue'
import SecretInput from '../../components/SecretInput.vue'
import TriStateSelect from '../../components/TriStateSelect.vue'
import { useDirty } from '../../composables/useDirty'
import { usePageTitle } from '../../composables/usePageTitle'
import { randomString } from '../../util/generate'
import { mihomoLogLevels, xrayLogLevels } from '../../util/options'
import GroupsTab from './GroupsTab.vue'
import InboundsTab from './InboundsTab.vue'
import ListenerForm from './ListenerForm.vue'
import PreviewTab from './PreviewTab.vue'
import RulesTab from './RulesTab.vue'
import UpstreamsTab from './UpstreamsTab.vue'
import LogsTab from './LogsTab.vue'
import DiagnosticsTab from './DiagnosticsTab.vue'
import FlowTab from './FlowTab.vue'
import UsageTab from './UsageTab.vue'
import { useBackgroundTask } from '../../composables/useBackgroundTask'
import UnitStatus from '../../components/UnitStatus.vue'
import { refreshPending } from '../../runtime'
import { stackPorts } from './ports'

type Stack = Schemas['Stack']

const route = useRoute()
const router = useRouter()
const message = useMessage()
const dialog = useDialog()
const id = computed(() => Number(route.params.id))
const stack = ref<Stack | null>(null)
const summaries = ref<Schemas['StackSummary'][]>([])
const users = ref<Schemas['User'][]>([])
const errors = ref<FormErrors>(emptyErrors())
const saving = ref(false)
const tab = ref(String(route.query.tab ?? 'basic'))
/** 每次保存后递增，生成预览据此重新加载。 */
const savedVersion = ref(0)
const { dirty, markClean } = useDirty(stack)
/** 已保存的名称，用作页面标题，编辑名称输入框时不跟着变。 */
const savedName = ref('')
usePageTitle(() => savedName.value)

const f = (path: string, hint?: string) => fieldState(errors.value, path, hint)

async function load() {
  const [stackResult, listResult, userResult] = await Promise.all([
    api.GET('/api/stacks/{id}', { params: { path: { id: id.value } } }),
    api.GET('/api/stacks'),
    api.GET('/api/users'),
  ])
  if (!stackResult.data) {
    message.error('stack 不存在')
    router.replace({ name: 'stacks' })
    return
  }
  stack.value = stackResult.data
  savedName.value = stackResult.data.name
  summaries.value = listResult.data?.stacks ?? []
  users.value = userResult.data?.users ?? []
  errors.value = emptyErrors()
  markClean()
}
onMounted(load)
watch(id, load)

/** 取回明文凭据，只替换仍是占位的字段。 */
async function reveal() {
  const { data } = await api.GET('/api/stacks/{id}/secrets', { params: { path: { id: id.value } } })
  const s = stack.value
  if (!data || !s) return
  const wasDirty = dirty.value
  const keep = (current: string, plain: string) => (current === MASKED ? plain : current)
  s.mihomo.controller_secret = keep(s.mihomo.controller_secret, data.mihomo.controller_secret)
  s.xray.outbound.password = keep(s.xray.outbound.password, data.xray.outbound.password)
  for (const inbound of s.xray.inbounds) {
    const plain = data.xray.inbounds.find((i) => i.id === inbound.id)
    if (plain) {
      inbound.password = keep(inbound.password, plain.password)
      inbound.auth_password = keep(inbound.auth_password, plain.auth_password)
    }
  }
  for (const [listener, plain] of [[s.mihomo.socks, data.mihomo.socks], [s.mihomo.http, data.mihomo.http]] as const) {
    for (const account of listener.accounts) {
      const match = plain.accounts.find((a) => a.username === account.username)
      if (match) account.password = keep(account.password, match.password)
    }
  }
  if (!wasDirty) markClean()
}

const trackTask = useBackgroundTask(refreshPending)

/** 保存后只应用本 stack。 */
async function saveAndApply() {
  if (dirty.value && !(await save())) return
  const { data, error } = await api.POST('/api/runtime/apply', { body: { stacks: [stack.value!.name] } })
  if (error) {
    message.error(splitProblem(error, '应用失败').global.join('；'))
    return
  }
  trackTask(data!.task_id)
}

async function save(): Promise<boolean> {
  if (!stack.value) return false
  saving.value = true
  const { data, error } = await api.PUT('/api/stacks/{id}', { params: { path: { id: id.value } }, body: stack.value })
  saving.value = false
  if (error) {
    errors.value = splitProblem(error)
    const first = Object.keys(errors.value.fields)[0]
    if (first) tab.value = tabFor(first)
    return false
  }
  stack.value = data!
  savedName.value = data!.name
  errors.value = emptyErrors()
  markClean()
  savedVersion.value++
  refreshPending()
  message.success('已保存，配置变更将在下次应用时生效')
  return true
}

function tabFor(path: string): string {
  if (path.startsWith('direct_users') || path === 'direct_suffix') return 'upstreams'
  if (path.startsWith('xray.inbounds')) return 'inbounds'
  if (path.startsWith('xray.outbound')) return 'outbound'
  if (path.startsWith('xray')) return 'xray'
  if (path.startsWith('mihomo.upstreams')) return 'upstreams'
  if (path.startsWith('mihomo.groups')) return 'groups'
  if (path.startsWith('mihomo.rules')) return 'rules'
  if (path.startsWith('mihomo')) return 'mihomo'
  return 'basic'
}

const tabErrors = computed(() => {
  const out: Record<string, boolean> = {}
  for (const path of Object.keys(errors.value.fields)) out[tabFor(path)] = true
  return out
})

const tabLabel = (name: string, label: string) => () =>
  tabErrors.value[name] ? h(NBadge, { dot: true, type: 'error', offset: [6, 0] }, () => label) : label

// 标签页分为两级：先选 Xray / Mihomo 等分组，再选分组内的具体页面。
const tabGroups = computed(() => [
  { name: 'general', label: '基本', tabs: ['basic', ...(stack.value?.role === 'auto' ? ['members'] : [])] },
  { name: 'xray', label: 'Xray', tabs: ['inbounds', 'outbound', 'xray'] },
  { name: 'mihomo', label: 'Mihomo', tabs: ['upstreams', 'groups', 'rules', 'mihomo'] },
  { name: 'observe', label: '运行观察', tabs: ['flow', 'usage', 'diagnostics', 'logs', 'preview'] },
])
const groupOf = (name: string) => tabGroups.value.find((g) => g.tabs.includes(name))?.name ?? 'general'
const lastTab: Record<string, string> = {}
watch(tab, (name) => { lastTab[groupOf(name)] = name }, { immediate: true })
const group = computed({
  get: () => groupOf(tab.value),
  set: (name: string) => { tab.value = lastTab[name] ?? tabGroups.value.find((g) => g.name === name)!.tabs[0]! },
})
const groupLabel = (name: string, label: string) => () => {
  const tabs = tabGroups.value.find((g) => g.name === name)?.tabs ?? []
  return tabs.some((t) => tabErrors.value[t]) ? h(NBadge, { dot: true, type: 'error', offset: [6, 0] }, () => label) : label
}

/** 按配置结构判断是否为聚合 stack：出站交给本 stack 的 Mihomo，上游全部引用其他 stack 的入站。 */
const relaysOnly = computed(() => {
  const s = stack.value
  if (!s || s.xray.outbound.type !== 'mihomo' || !s.mihomo.enabled || !s.mihomo.upstreams.length) return false
  return s.mihomo.upstreams.every((upstream) => upstream.type === 'xray-socks5')
})
/** 角色是 auto，但上游里混有外部节点。 */
const mixedAuto = computed(() => stack.value?.role === 'auto' && (stack.value?.mihomo.upstreams ?? []).some((upstream) => upstream.type !== 'xray-socks5'))

const moreOptions = [
  { key: 'clone', label: '克隆' },
  { type: 'divider', key: 'divider' },
  { key: 'delete', label: '删除 stack', props: { style: 'color: var(--ps-danger)' } },
]
function onMore(key: string) {
  if (key === 'clone') {
    cloning.value = true
    cloneName.value = `${stack.value!.name}-copy`
    cloneErrors.value = emptyErrors()
  } else if (key === 'delete') {
    dialog.warning({
      title: `删除 stack ${savedName.value}？`,
      content: '删除 stack 的全部配置。被其他 stack 引用时无法删除。',
      positiveText: '删除',
      negativeText: '取消',
      onPositiveClick: remove,
    })
  }
}

const ports = computed(() => (stack.value ? stackPorts(stack.value) : []))

// 克隆与删除
const cloning = ref(false)
const cloneName = ref('')
const cloneErrors = ref<FormErrors>(emptyErrors())

async function clone() {
  const { data, error } = await api.POST('/api/stacks/{id}/clone', { params: { path: { id: id.value } }, body: { name: cloneName.value } })
  if (error) {
    cloneErrors.value = splitProblem(error, '克隆失败')
    return
  }
  cloning.value = false
  message.success('已克隆，端口已重新分配')
  router.push({ name: 'stack', params: { id: data!.id } })
}

async function remove() {
  const { error } = await api.DELETE('/api/stacks/{id}', { params: { path: { id: id.value } } })
  if (error) {
    errors.value = splitProblem(error, '删除失败')
    return
  }
  markClean()
  message.success('已删除')
  router.replace({ name: 'stacks' })
}

// 成员
const members = ref<string[]>([])
watch(stack, (s) => {
  if (s && !dirty.value) {
    members.value = [...new Set(s.mihomo.upstreams.filter((u) => u.type === 'xray-socks5').map((u) => u.stack))].sort()
  }
}, { immediate: true })
const memberOptions = computed(() =>
  summaries.value
    .filter((s) => s.id !== id.value && s.role === 'edge' && s.inbounds.some((i) => i.name === 'relay' && i.protocol === 'socks5'))
    .map((s) => ({ label: s.name, value: s.name })),
)

async function saveMembers() {
  const run = async () => {
    const { data, error } = await api.PUT('/api/stacks/{id}/members', { params: { path: { id: id.value } }, body: { members: members.value } })
    if (error) {
      errors.value = splitProblem(error, '保存成员失败')
      return
    }
    stack.value = data!
    errors.value = emptyErrors()
    markClean()
    savedVersion.value++
    message.success('成员已更新，上游和代理组已同步')
  }
  if (dirty.value) {
    dialog.warning({
      title: '放弃未保存的修改？',
      content: '保存成员会重新载入 stack，其他标签页中未保存的修改将丢失。',
      positiveText: '继续',
      negativeText: '取消',
      onPositiveClick: run,
    })
  } else {
    run()
  }
}
</script>

<template>
  <NSpin :show="!stack">
    <template v-if="stack">
      <NCard size="small" class="stack-toolbar">
        <NSpace justify="space-between" align="center">
          <NSpace align="center">
            <UnitStatus :stack="stack.name" actions prominent />
            <NText v-if="dirty" type="warning">有未保存的修改</NText>
          </NSpace>
          <NSpace>
            <NDropdown :options="moreOptions" trigger="click" placement="bottom-end" @select="onMore">
              <NButton icon-placement="right">
                <template #icon><NIcon><ChevronDownOutline /></NIcon></template>
                更多
              </NButton>
            </NDropdown>
            <NButton :disabled="!dirty" :loading="saving" @click="save">保存</NButton>
            <NButton type="primary" :loading="saving" @click="saveAndApply">{{ dirty ? '保存并应用' : '应用' }}</NButton>
          </NSpace>
        </NSpace>
      </NCard>
      <ErrorList :errors="errors.global" />

      <NTabs v-model:value="group" type="line" animated class="detail-tabs">
        <NTabPane name="general" :tab="groupLabel('general', '基本')">
          <NTabs v-model:value="tab" type="segment" size="small" class="sub-tabs" :class="{ single: stack.role !== 'auto' }">
            <NTabPane name="basic" :tab="tabLabel('basic', '基本')">
              <NForm label-placement="left" label-width="110" style="max-width: 720px">
                <NFormItem label="名称" required v-bind="f('name', '改名会同步更新其他 stack 中的引用')">
                  <NInput v-model:value="stack.name" />
                </NFormItem>
                <NFormItem label="启用" v-bind="f('enabled', '关闭时不生成配置、不参与对账')">
                  <NSwitch v-model:value="stack.enabled" />
                </NFormItem>
                <NFormItem label="期望运行" v-bind="f('desired_running', '关闭时仍生成配置，但对账会让 xray / mihomo 保持停止')">
                  <NSwitch v-model:value="stack.desired_running" />
                </NFormItem>
                <NFormItem label="每日流量阈值" v-bind="f('daily_limit_bytes', '按系统设置时区统计上行和下行总量；0 表示无限制')">
                  <ByteSizeInput small :value="stack.daily_limit_bytes ?? 0" @update:value="(value: number) => (stack!.daily_limit_bytes = value)" />
                </NFormItem>
                <NFormItem label="角色" v-bind="f('role', 'edge：独立出口，流量经本 stack 的上游出去；auto：聚合多个 edge stack 的 relay 入站，按测速或负载均衡挑选出口')">
                  <NSpace vertical style="width: 100%">
                    <NRadioGroup v-model:value="stack.role">
                      <NRadioButton value="edge">edge</NRadioButton>
                      <NRadioButton value="auto">auto</NRadioButton>
                    </NRadioGroup>
                    <NAlert v-if="relaysOnly && stack.role === 'edge'" type="warning" :bordered="false">
                      所有上游都引用其他 stack 的入站，按结构这是聚合 stack，但角色是 edge。改为 auto 后才能在「成员」页管理成员，流量拓扑也会按聚合 stack 显示。
                      <NButton size="small" type="warning" secondary style="margin-left: 8px" @click="stack.role = 'auto'">改为 auto</NButton>
                    </NAlert>
                    <NAlert v-else-if="mixedAuto" type="warning" :bordered="false">
                      角色是 auto，但上游里混有外部节点。流量合计会计入这个 stack，经其他 stack relay 转发的部分会重复计算；建议聚合 stack 只引用其他 stack。
                    </NAlert>
                    <NText v-if="relaysOnly" depth="3" style="font-size: 12px">
                      这个 stack 只经其他 stack 的 relay 出网，流量已在被引用的 stack 计入，多个 stack 加总时不再计入它。
                    </NText>
                  </NSpace>
                </NFormItem>
                <NFormItem label="标签" v-bind="f('labels')">
                  <NDynamicTags v-model:value="stack.labels" />
                </NFormItem>
              </NForm>
            </NTabPane>
            <NTabPane v-if="stack.role === 'auto'" name="members" tab="成员">
              <NForm label-placement="top" style="max-width: 640px">
                <NFormItem label="成员 stack" :feedback="'只列出带 relay SOCKS5 入站的 edge stack；保存后同步上游和代理组成员'">
                  <NSpace vertical style="width: 100%">
                    <NSelect v-model:value="members" :options="memberOptions" multiple filterable />
                    <NSpace :size="8"><EntityLink v-for="name in members" :key="name" kind="stack" :name="name" target="_blank" rel="noopener" /></NSpace>
                  </NSpace>
                </NFormItem>
                <NButton type="primary" @click="saveMembers">保存成员</NButton>
              </NForm>
            </NTabPane>
          </NTabs>
        </NTabPane>
        <NTabPane name="xray" :tab="groupLabel('xray', 'Xray')">
          <NTabs v-model:value="tab" type="segment" size="small" class="sub-tabs">
            <NTabPane name="inbounds" :tab="tabLabel('inbounds', '入站')">
              <InboundsTab :stack="stack" :users="users" :errors="errors" :reveal="reveal" />
            </NTabPane>
            <NTabPane name="outbound" :tab="tabLabel('outbound', '出站')">
              <NForm label-placement="left" label-width="110" style="max-width: 720px">
                <NFormItem label="类型" v-bind="f('xray.outbound.type')">
                  <NRadioGroup v-model:value="stack.xray.outbound.type">
                    <NRadioButton value="mihomo">本 stack Mihomo</NRadioButton>
                    <NRadioButton value="socks5">外部 SOCKS5</NRadioButton>
                    <NRadioButton value="http">外部 HTTP</NRadioButton>
                    <NRadioButton value="direct">直连</NRadioButton>
                  </NRadioGroup>
                </NFormItem>
                <NFormItem v-if="stack.xray.outbound.type === 'mihomo'" label="目标监听">
                  <NText>本 stack 的 Mihomo SOCKS 监听（{{ stack.mihomo.socks.listen }}:{{ stack.mihomo.socks.port }}）</NText>
                </NFormItem>
                <template v-if="stack.xray.outbound.type === 'socks5' || stack.xray.outbound.type === 'http'">
                  <NFormItem label="server" required v-bind="f('xray.outbound.server')">
                    <NInput v-model:value="stack.xray.outbound.server" />
                  </NFormItem>
                  <NFormItem label="port" required v-bind="f('xray.outbound.port')">
                    <NInput :value="String(stack.xray.outbound.port || '')" @update:value="(v) => (stack!.xray.outbound.port = Number(v) || 0)" />
                  </NFormItem>
                  <NFormItem label="username" v-bind="f('xray.outbound.username')">
                    <NInput v-model:value="stack.xray.outbound.username" />
                  </NFormItem>
                  <NFormItem label="password" v-bind="f('xray.outbound.password')">
                    <SecretInput v-model:value="stack.xray.outbound.password" :reveal="reveal" />
                  </NFormItem>
                  <NFormItem label="私网目标直连" v-bind="f('xray.outbound.private_direct')">
                    <NSwitch v-model:value="stack.xray.outbound.private_direct" />
                  </NFormItem>
                </template>
                <NText v-if="stack.xray.outbound.type !== 'mihomo' && stack.mihomo.enabled" depth="3">
                  出站不经过 Mihomo 时，可以在「Mihomo」标签关闭本 stack 的 Mihomo。
                </NText>
              </NForm>
            </NTabPane>
            <NTabPane name="xray" :tab="tabLabel('xray', '设置')">
              <NForm label-placement="left" label-width="110" style="max-width: 720px">
                <NFormItem label="启用" v-bind="f('xray.enabled')">
                  <NSwitch v-model:value="stack.xray.enabled" />
                </NFormItem>
                <NFormItem label="日志级别" v-bind="f('xray.loglevel')">
                  <NSelect
                    v-model:value="stack.xray.loglevel"
                    :options="[{ label: '继承全局', value: 'inherit' }, ...xrayLogLevels.map((v) => ({ label: v, value: v }))]"
                  />
                </NFormItem>
                <NFormItem label="记录访问日志" v-bind="f('xray.access_log', '记录客户端请求日志，独立于错误日志级别；日志级别为 none 时关闭全部日志。')">
                  <TriStateSelect :value="stack.xray.access_log ?? 'inherit'" @update:value="(value) => (stack!.xray.access_log = value as Stack['xray']['access_log'])" />
                </NFormItem>
                <NFormItem label="API" v-bind="f('xray.api')"><TriStateSelect v-model:value="stack.xray.api" /></NFormItem>
                <NFormItem label="stats" v-bind="f('xray.stats')"><TriStateSelect v-model:value="stack.xray.stats" /></NFormItem>
                <NFormItem label="policy" v-bind="f('xray.policy')"><TriStateSelect v-model:value="stack.xray.policy" /></NFormItem>
                <NFormItem label="API 端口" v-bind="f('xray.api_port', '监听 127.0.0.1')">
                  <PortInput v-model:value="stack.xray.api_port" pool="xray_api" :stack-id="stack.id" :exclude="ports" />
                </NFormItem>
              </NForm>
            </NTabPane>
          </NTabs>
        </NTabPane>
        <NTabPane name="mihomo" :tab="groupLabel('mihomo', 'Mihomo')">
          <NTabs v-model:value="tab" type="segment" size="small" class="sub-tabs">
            <NTabPane name="upstreams" :tab="tabLabel('upstreams', '上游')">
              <UpstreamsTab :stack="stack" :summaries="summaries" :users="users" :errors="errors" />
            </NTabPane>
            <NTabPane name="groups" :tab="tabLabel('groups', '代理组')">
              <GroupsTab :stack="stack" :errors="errors" />
            </NTabPane>
            <NTabPane name="rules" :tab="tabLabel('rules', '规则')">
              <RulesTab :stack="stack" :errors="errors" />
            </NTabPane>
            <NTabPane name="mihomo" :tab="tabLabel('mihomo', '设置与监听')">
              <NForm label-placement="left" label-width="130" style="max-width: 760px">
                <NFormItem label="启用" v-bind="f('mihomo.enabled')">
                  <NSwitch v-model:value="stack.mihomo.enabled" />
                </NFormItem>
                <NFormItem label="模式" v-bind="f('mihomo.mode')">
                  <NRadioGroup v-model:value="stack.mihomo.mode">
                    <NRadioButton value="inherit">继承全局</NRadioButton>
                    <NRadioButton value="Rule">Rule</NRadioButton>
                    <NRadioButton value="Global">Global</NRadioButton>
                    <NRadioButton value="Direct">Direct</NRadioButton>
                  </NRadioGroup>
                </NFormItem>
                <NFormItem label="日志级别" v-bind="f('mihomo.loglevel')">
                  <NSelect
                    v-model:value="stack.mihomo.loglevel"
                    :options="[{ label: '继承全局', value: 'inherit' }, ...mihomoLogLevels.map((v) => ({ label: v, value: v }))]"
                  />
                </NFormItem>
                <NFormItem label="controller 端口" v-bind="f('mihomo.controller_port', '监听 127.0.0.1')">
                  <PortInput v-model:value="stack.mihomo.controller_port" pool="mihomo_controller" :stack-id="stack.id" :exclude="ports" />
                </NFormItem>
                <NFormItem label="controller secret" required v-bind="f('mihomo.controller_secret')">
                  <SecretInput v-model:value="stack.mihomo.controller_secret" :generate="() => randomString(32)" :reveal="reveal" />
                </NFormItem>
                <ListenerForm title="SOCKS 监听" kind="socks" :listener="stack.mihomo.socks" :errors="errors" prefix="mihomo.socks" :stack-id="stack.id" :ports="ports" :reveal="reveal" />
                <ListenerForm title="HTTP 监听" kind="http" :listener="stack.mihomo.http" :errors="errors" prefix="mihomo.http" :stack-id="stack.id" :ports="ports" :reveal="reveal" />
              </NForm>
            </NTabPane>
          </NTabs>
        </NTabPane>
        <NTabPane name="observe" :tab="groupLabel('observe', '运行观察')">
          <NTabs v-model:value="tab" type="segment" size="small" class="sub-tabs">
            <NTabPane name="flow" tab="流量拓扑">
              <FlowTab :stack="stack" :summaries="summaries" />
            </NTabPane>
            <NTabPane name="usage" tab="用量统计" display-directive="if">
              <UsageTab :key="id" :stack-id="id" :users="users" />
            </NTabPane>
            <NTabPane name="diagnostics" tab="诊断" display-directive="if">
              <DiagnosticsTab :stack-id="id" />
            </NTabPane>
            <NTabPane name="logs" tab="日志" display-directive="if">
              <LogsTab :stack="stack.name" />
            </NTabPane>
            <NTabPane name="preview" tab="生成预览">
              <PreviewTab :stack-id="stack.id!" :dirty="dirty" :version="savedVersion" />
            </NTabPane>
          </NTabs>
        </NTabPane>
      </NTabs>
    </template>
  </NSpin>


  <NModal v-model:show="cloning" preset="card" title="克隆 stack" style="max-width: 480px">
    <ErrorList :errors="cloneErrors.global" title="克隆失败" />
    <NForm label-placement="left" label-width="80">
      <NFormItem label="新名称" v-bind="fieldState(cloneErrors, 'name')">
        <NInput v-model:value="cloneName" />
      </NFormItem>
    </NForm>
    <NText depth="3">复制全部子项并重新分配端口，只改写指向源 stack 自身资源的引用。克隆的是已保存的配置。</NText>
    <template #footer>
      <NSpace justify="end">
        <NButton @click="cloning = false">取消</NButton>
        <NButton type="primary" :disabled="!cloneName" @click="clone">克隆</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.sub-tabs > :deep(.n-tabs-nav) {
  max-width: 560px;
  margin-bottom: 16px;
}
.sub-tabs.single > :deep(.n-tabs-nav) {
  display: none;
}
/* 分组切换时子标签所在面板先隐藏再显示，组件库的滑块量不到宽度，直接给选中的子标签上色。 */
.sub-tabs > :deep(.n-tabs-nav .n-tabs-capsule) {
  display: none;
}
.sub-tabs > :deep(.n-tabs-nav .n-tabs-tab--active) {
  border-radius: 6px;
  background: var(--ps-accent-soft);
  box-shadow: inset 0 0 0 1px var(--ps-accent);
  color: var(--ps-accent);
  font-weight: 600;
}
.stack-toolbar {
  position: sticky;
  top: 0;
  z-index: 10;
  margin-bottom: 20px;
  background: var(--n-color);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
}

@media (max-width: 767px) {
  .stack-toolbar :deep(.n-card-content) {
    padding: 12px;
  }

  .stack-toolbar :deep(.n-space) {
    flex-wrap: wrap;
  }
}
</style>
