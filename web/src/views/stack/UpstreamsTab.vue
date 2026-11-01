<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import YAML from 'yaml'
import {
  NAlert, NButton, NCascader, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber,
  NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NSwitch, NTabPane, NTabs, NTag, NText, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { fieldState, hasErrorsUnder, type FormErrors, type Schemas } from '../../api/client'
import CodeEditor from '../../components/CodeEditor.vue'
import EntityLink from '../../components/EntityLink.vue'
import { ssMethods } from '../../util/options'

type Upstream = Schemas['Upstream']
type RawConfig = Record<string, unknown>

const props = defineProps<{ stack: Schemas['Stack']; summaries: Schemas['StackSummary'][]; errors: FormErrors }>()
const message = useMessage()

const editing = ref<{ index: number; draft: Upstream; config: RawConfig } | null>(null)
const yamlText = ref('')
const yamlError = ref('')
const editorTab = ref<'form' | 'yaml'>('form')

const rows = computed(() => props.stack.mihomo.upstreams.map((upstream, index) => ({ ...upstream, _index: index })))

const columns = computed<DataTableColumns<Upstream & { _index: number }>>(() => [
  {
    title: '名称',
    key: 'name',
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        row.name,
        hasErrorsUnder(props.errors, `mihomo.upstreams[${row._index}]`) ? h(NTag, { size: 'small', type: 'error' }, () => '有错误') : null,
      ]),
  },
  { title: '类型', key: 'type', render: (row) => (row.type === 'raw' ? '外部节点' : '引用 stack 入站') },
  {
    title: '目标',
    key: 'target',
    render: (row) => {
      if (row.type === 'xray-socks5') return h(EntityLink, { kind: 'stack', name: row.stack, label: `${row.stack}.${row.inbound}`, tab: 'inbounds', target: '_blank', rel: 'noopener' })
      const config = (row.config ?? {}) as RawConfig
      return `${config.type ?? '?'} ${config.server ?? ''}:${config.port ?? ''}`
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 140,
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'small', onClick: () => open(row._index) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => remove(row._index) }, {
          trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '删除'),
          default: () => '删除上游，并从代理组中移除？',
        }),
      ]),
  },
])

function remove(index: number) {
  const name = props.stack.mihomo.upstreams[index].name
  props.stack.mihomo.upstreams.splice(index, 1)
  for (const group of props.stack.mihomo.groups) {
    group.proxies = group.proxies.filter((proxy) => proxy !== name)
  }
}

function open(index: number) {
  const source: Upstream =
    index < props.stack.mihomo.upstreams.length
      ? props.stack.mihomo.upstreams[index]
      : { name: '', type: 'raw', stack: '', inbound: '', config: { type: 'vmess', server: '', port: 443, uuid: '', network: 'ws', tls: true } }
  const draft: Upstream = JSON.parse(JSON.stringify(source))
  editing.value = { index, draft, config: (draft.config ?? {}) as RawConfig }
  yamlText.value = YAML.stringify(editing.value.config)
  yamlError.value = ''
  editorTab.value = 'form'
}

function confirm() {
  if (!editing.value) return
  const { index, draft, config } = editing.value
  draft.config = draft.type === 'raw' ? config : null
  if (draft.type === 'raw') {
    draft.stack = ''
    draft.inbound = ''
  }
  if (index < props.stack.mihomo.upstreams.length) {
    const oldName = props.stack.mihomo.upstreams[index].name
    props.stack.mihomo.upstreams[index] = draft
    if (oldName && oldName !== draft.name) renameTarget(oldName, draft.name)
  } else {
    props.stack.mihomo.upstreams.push(draft)
    const joined = joinGroups(draft.name)
    if (joined.length) message.success(`已加入代理组 ${joined.join('、')}，保存后生效`)
  }
  editing.value = null
}

const BUILTINS = ['DIRECT', 'REJECT']
const AUTO_TYPES = ['url-test', 'load-balance', 'fallback']

/**
 * 新增的上游自动加入代理组，返回加入的组名。有自动测速或负载均衡组时加入这些组，以及引用它们的 select 组；
 * 否则加入兜底规则指向的 select 组（默认是 AllProxy）。select 组里排在 DIRECT、REJECT 之前，
 * 原先只有 DIRECT 时新上游成为默认选择。
 */
function joinGroups(name: string): string[] {
  const groups = props.stack.mihomo.groups
  const autoGroups = new Set(groups.filter((group) => AUTO_TYPES.includes(group.type)).map((group) => group.name))
  let targets = groups.filter(
    (group) => autoGroups.has(group.name) || (group.type === 'select' && group.proxies.some((proxy) => autoGroups.has(proxy))),
  )
  if (!targets.length) {
    const fallback =
      groups.find((group) => group.type === 'select' && group.name === props.stack.mihomo.rules.final) ??
      groups.find((group) => group.type === 'select')
    targets = fallback ? [fallback] : []
  }
  for (const group of targets) {
    if (group.proxies.includes(name)) continue
    const position = group.type === 'select' ? group.proxies.findIndex((proxy) => BUILTINS.includes(proxy)) : -1
    if (position < 0) group.proxies.push(name)
    else group.proxies.splice(position, 0, name)
  }
  return targets.map((group) => group.name)
}

/** 上游改名时同步本 stack 内的组成员和规则目标。 */
function renameTarget(oldName: string, newName: string) {
  for (const group of props.stack.mihomo.groups) {
    group.proxies = group.proxies.map((p) => (p === oldName ? newName : p))
  }
  const rules = props.stack.mihomo.rules
  if (rules.final === oldName) rules.final = newName
  for (const rule of rules.extra) if (rule.target === oldName) rule.target = newName
}

// 结构化字段与 YAML 文本双向同步：表单改动时重写 YAML，YAML 改动且可解析时替换配置对象。
watch(
  () => editing.value?.config,
  (config) => {
    if (config && editorTab.value === 'form') yamlText.value = YAML.stringify(config)
  },
  { deep: true },
)
watch(yamlText, (text) => {
  if (!editing.value || editorTab.value !== 'yaml') return
  try {
    const parsed = YAML.parse(text)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      yamlError.value = '必须是映射'
      return
    }
    editing.value.config = parsed as RawConfig
    yamlError.value = ''
  } catch (error) {
    yamlError.value = (error as Error).message
  }
})

const protocols = ['vmess', 'shadowsocks', 'socks5', 'http']
const protocol = computed({
  get: () => {
    const type = String(editing.value?.config.type ?? '')
    return protocols.includes(type) ? type : 'other'
  },
  set: (value: string) => {
    if (editing.value && value !== 'other') editing.value.config.type = value
  },
})

function field<T>(key: string) {
  return computed<T | null>({
    get: () => (editing.value?.config[key] ?? null) as T | null,
    set: (value) => {
      if (!editing.value) return
      if (value === null || value === '') {
        delete editing.value.config[key]
      } else {
        editing.value.config[key] = value
      }
    },
  })
}
const server = field<string>('server')
const port = field<number>('port')
const uuid = field<string>('uuid')
const network = field<string>('network')
const cipher = field<string>('cipher')
const password = field<string>('password')
const username = field<string>('username')
const tls = field<boolean>('tls')
const skipCert = field<boolean>('skip-cert-verify')
const servername = field<string>('servername')
const udp = field<boolean>('udp')

function nested(parent: string, key: string) {
  return computed<string>({
    get: () => {
      const value = editing.value?.config[parent] as Record<string, unknown> | undefined
      return String(value?.[key] ?? '')
    },
    set: (next) => {
      if (!editing.value) return
      const current = { ...((editing.value.config[parent] as Record<string, unknown>) ?? {}) }
      if (next) current[key] = next
      else delete current[key]
      if (Object.keys(current).length) editing.value.config[parent] = current
      else delete editing.value.config[parent]
    },
  })
}
const wsPath = nested('ws-opts', 'path')
const grpcService = nested('grpc-opts', 'grpc-service-name')

const cascaderOptions = computed(() =>
  props.summaries
    .filter((s) => s.inbounds.some((i) => i.protocol === 'socks5'))
    .map((s) => ({
      label: s.name,
      value: s.name,
      children: s.inbounds.filter((i) => i.protocol === 'socks5').map((i) => ({ label: `${i.name}（${i.listen}:${i.port}）`, value: `${s.name}\u0000${i.name}` })),
    })),
)
const refValue = computed({
  get: () => (editing.value?.draft.stack ? `${editing.value.draft.stack}\u0000${editing.value.draft.inbound}` : null),
  set: (value: string | null) => {
    if (!editing.value) return
    const [stack, inbound] = (value ?? '').split('\u0000')
    editing.value.draft.stack = stack ?? ''
    editing.value.draft.inbound = inbound ?? ''
  },
})

const prefix = computed(() => `mihomo.upstreams[${editing.value?.index ?? 0}]`)
const f = (path: string, hint?: string) => fieldState(props.errors, `${prefix.value}.${path}`, hint)
</script>

<template>
  <NSpace justify="space-between" align="center" style="margin-bottom: 16px">
    <NText depth="3">新增的上游会自动加入代理组，排在 DIRECT 之前；顺序和成员可以在「代理组」标签页调整。</NText>
    <NButton type="primary" @click="open(stack.mihomo.upstreams.length)">添加上游</NButton>
  </NSpace>
  <NDataTable :columns="columns" :data="rows" :scroll-x="840" size="small" />

  <NDrawer :show="!!editing" :width="640" @update:show="(v) => !v && (editing = null)">
    <NDrawerContent v-if="editing" :title="editing.draft.name ? `上游 ${editing.draft.name}` : '新上游'" closable>
      <NForm label-placement="left" label-width="130">
        <NFormItem label="name" required v-bind="f('name', '在本 stack 的上游和代理组中唯一；改名会同步代理组和规则')">
          <NInput v-model:value="editing.draft.name" />
        </NFormItem>
        <NFormItem label="type" v-bind="f('type')">
          <NRadioGroup v-model:value="editing.draft.type">
            <NRadioButton value="raw">外部节点</NRadioButton>
            <NRadioButton value="xray-socks5">引用其他 stack 的 SOCKS5 入站</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem v-if="editing.draft.type === 'xray-socks5'" label="引用" v-bind="f('inbound')">
          <NSpace vertical style="width: 100%">
            <NCascader v-model:value="refValue" :options="cascaderOptions" check-strategy="child" filterable />
            <EntityLink v-if="editing.draft.stack" kind="stack" :name="editing.draft.stack" label="查看目标入站 ↗" tab="inbounds" target="_blank" rel="noopener" />
          </NSpace>
        </NFormItem>
        <template v-else>
          <NFormItem label="节点协议">
            <NSelect v-model:value="protocol" :options="[...protocols.map((p) => ({ label: p, value: p })), { label: '其他（只能编辑 YAML）', value: 'other' }]" />
          </NFormItem>
          <NTabs v-model:value="editorTab" type="segment" size="small" style="margin-bottom: 12px">
            <NTabPane name="form" tab="表单" :disabled="protocol === 'other'">
              <NFormItem label="server" v-bind="f('config')"><NInput v-model:value="server" /></NFormItem>
              <NFormItem label="port"><NInputNumber v-model:value="port" :min="1" :max="65535" /></NFormItem>
              <template v-if="protocol === 'vmess'">
                <NFormItem label="uuid"><NInput v-model:value="uuid" /></NFormItem>
                <NFormItem label="network">
                  <NSelect v-model:value="network" :options="['tcp', 'ws', 'grpc', 'h2', 'http'].map((v) => ({ label: v, value: v }))" />
                </NFormItem>
                <NFormItem v-if="network === 'ws'" label="ws path"><NInput v-model:value="wsPath" /></NFormItem>
                <NFormItem v-if="network === 'grpc'" label="grpc service"><NInput v-model:value="grpcService" /></NFormItem>
              </template>
              <template v-if="protocol === 'shadowsocks'">
                <NFormItem label="cipher"><NSelect v-model:value="cipher" :options="ssMethods" filterable tag /></NFormItem>
                <NFormItem label="password"><NInput v-model:value="password" type="password" show-password-on="click" /></NFormItem>
              </template>
              <template v-if="protocol === 'socks5' || protocol === 'http'">
                <NFormItem label="username"><NInput v-model:value="username" /></NFormItem>
                <NFormItem label="password"><NInput v-model:value="password" type="password" show-password-on="click" /></NFormItem>
              </template>
              <template v-if="protocol !== 'shadowsocks'">
                <NFormItem label="tls"><NSwitch :value="!!tls" @update:value="(v) => (tls = v || null)" /></NFormItem>
                <NFormItem v-if="tls" label="servername"><NInput v-model:value="servername" /></NFormItem>
                <NFormItem v-if="tls" label="skip-cert-verify"><NSwitch :value="!!skipCert" @update:value="(v) => (skipCert = v || null)" /></NFormItem>
              </template>
              <NFormItem label="udp"><NSwitch :value="!!udp" @update:value="(v) => (udp = v || null)" /></NFormItem>
            </NTabPane>
            <NTabPane name="yaml" tab="YAML">
              <CodeEditor v-model:value="yamlText" language="yaml" min-height="260px" />
              <NAlert v-if="yamlError" type="error" :bordered="false" style="margin-top: 8px">{{ yamlError }}</NAlert>
            </NTabPane>
          </NTabs>
          <NAlert v-if="f('config').feedback" type="error" :bordered="false">{{ f('config').feedback }}</NAlert>
        </template>
      </NForm>
      <template #footer>
        <NSpace>
          <NButton @click="editing = null">取消</NButton>
          <NButton type="primary" :disabled="!editing.draft.name || !!yamlError" @click="confirm">确定</NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>
