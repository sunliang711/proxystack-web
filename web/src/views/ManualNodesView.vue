<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import YAML from 'yaml'
import {
  NAlert, NButton, NCard, NDataTable, NDrawer, NDrawerContent, NDynamicInput, NForm, NFormItem, NInput, NInputNumber,
  NModal, NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, emptyErrors, fieldState, MASKED, splitProblem, type FormErrors, type Schemas } from '../api/client'
import CodeEditor from '../components/CodeEditor.vue'
import ErrorList from '../components/ErrorList.vue'
import ExportDialog from '../components/ExportDialog.vue'
import ImportDialog from '../components/ImportDialog.vue'
import SecretInput from '../components/SecretInput.vue'
import { randomString, randomUUID, shadowsocksKey } from '../util/generate'
import { protocolLabels, regions, ssMethods } from '../util/options'

type Node = Schemas['ManualNode']

const message = useMessage()
const nodes = ref<Node[]>([])
const users = ref<Schemas['User'][]>([])
const filterUser = ref<string | null>(null)
const checked = ref<number[]>([])
const errors = ref<FormErrors>(emptyErrors())
const saving = ref(false)
const showImport = ref(false)
const showExport = ref(false)

async function load() {
  const [nodeResult, userResult] = await Promise.all([api.GET('/api/manual-nodes'), api.GET('/api/users')])
  nodes.value = nodeResult.data ?? []
  users.value = userResult.data?.users ?? []
}
onMounted(load)

async function reveal() {
  const { data } = await api.GET('/api/manual-nodes/secrets')
  for (const node of nodes.value) {
    const plain = data?.find((n) => n.id === node.id)
    if (!plain) continue
    for (const key of ['uuid', 'password', 'auth_password'] as const) {
      if (node[key] === MASKED) node[key] = plain[key]
    }
    const draft = editing.value?.draft
    if (draft && draft.id === node.id) {
      for (const key of ['uuid', 'password', 'auth_password'] as const) {
        if (draft[key] === MASKED) draft[key] = plain[key]
      }
    }
  }
}

/** 整体保存节点列表；失败时把错误定位到节点。 */
async function save(next: Node[], success: string): Promise<boolean> {
  saving.value = true
  const { data, error } = await api.PUT('/api/manual-nodes', { body: next })
  saving.value = false
  if (error) {
    errors.value = splitProblem(error)
    return false
  }
  errors.value = emptyErrors()
  nodes.value = data ?? []
  checked.value = []
  message.success(success)
  return true
}

const rows = computed(() =>
  nodes.value.map((node, index) => ({ ...node, _index: index })).filter((node) => !filterUser.value || node.user === filterUser.value),
)
const rowErrors = (index: number) => Object.keys(errors.value.fields).some((path) => path.startsWith(`[${index}]`))

const columns: DataTableColumns<Node & { _index: number }> = [
  { type: 'selection' },
  { title: '用户', key: 'user', width: 100 },
  {
    title: '名称',
    key: 'remark',
    render: (row) => h(NSpace, { size: 4 }, () => [row.remark, rowErrors(row._index) ? h(NTag, { size: 'small', type: 'error' }, () => '有错误') : null]),
  },
  { title: '模式', key: 'mode', width: 90, render: (row) => (row.mode === 'direct' ? '原生' : protocolLabels[row.protocol] ?? row.protocol) },
  { title: 'server', key: 'server', render: (row) => (row.mode === 'direct' ? configValue(row, 'server') : `${row.server}:${row.port}`) },
  { title: '地区', key: 'region', width: 70 },
  {
    title: '操作',
    key: 'actions',
    width: 130,
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'small', onClick: () => open(row._index) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => save(nodes.value.filter((_, i) => i !== row._index), '已删除') }, {
          trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '删除'),
          default: () => '删除这个节点？',
        }),
      ]),
  },
]

function configValue(node: Node, key: string): string {
  const config = (node.config ?? {}) as Record<string, unknown>
  return `${config[key] ?? ''}:${config.port ?? ''}`
}

// 编辑抽屉
const editing = ref<{ index: number; draft: Node; yaml: string; yamlError: string } | null>(null)

function blank(): Node {
  return {
    user: filterUser.value ?? users.value[0]?.name ?? '', mode: 'standard', protocol: 'vmess', server: '', port: 443, tag: '',
    remark: '', region: '', udp: true, uuid: randomUUID(), network: 'ws', ws_path: '/', ws_headers: {}, grpc_service_name: '',
    method: 'aes-256-gcm', password: '', auth_type: 'noauth', auth_username: '', auth_password: '', config: null,
  }
}

function open(index: number) {
  const draft: Node = JSON.parse(JSON.stringify(index < nodes.value.length ? nodes.value[index] : blank()))
  editing.value = { index, draft, yaml: draft.config ? YAML.stringify(draft.config) : 'type: vless\nserver: \nport: 443\n', yamlError: '' }
}

function onYAML(text: string) {
  if (!editing.value) return
  editing.value.yaml = text
  try {
    const parsed = YAML.parse(text)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('必须是映射')
    editing.value.draft.config = parsed
    editing.value.yamlError = ''
  } catch (error) {
    editing.value.yamlError = (error as Error).message
  }
}

async function confirm() {
  if (!editing.value) return
  const { index, draft } = editing.value
  if (draft.mode === 'standard') draft.config = null
  const next = [...nodes.value]
  if (index < next.length) next[index] = draft
  else next.push(draft)
  if (await save(next, '已保存')) editing.value = null
}

const prefix = computed(() => `[${editing.value?.index ?? 0}]`)
const f = (path: string, hint?: string) => fieldState(errors.value, `${prefix.value}.${path}`, hint)
const headers = computed({
  get: () => Object.entries(editing.value?.draft.ws_headers ?? {}).map(([key, value]) => ({ key, value })),
  set: (pairs: { key: string; value: string }[]) => {
    editing.value!.draft.ws_headers = Object.fromEntries(pairs.filter((p) => p.key).map((p) => [p.key, p.value]))
  },
})

// 批量操作
const batchServer = ref<string | null>(null)
async function setServer() {
  const next = nodes.value.map((node) => (checked.value.includes(node.id!) && node.mode === 'standard' ? { ...node, server: batchServer.value! } : node))
  if (await save(next, '已批量设置 server')) batchServer.value = null
}
function cloneChecked() {
  const copies = nodes.value
    .filter((node) => checked.value.includes(node.id!))
    .map((node) => ({ ...JSON.parse(JSON.stringify(node)), id: undefined, tag: '', remark: `${node.remark} copy` }))
  save([...nodes.value, ...copies], `已克隆 ${copies.length} 个节点`)
}
</script>

<template>
  <NCard>
    <template #header>
      <NSpace justify="end">
        <NSelect v-model:value="filterUser" :options="users.map((u) => ({ label: u.name, value: u.name }))" clearable placeholder="全部用户" style="width: 160px" />
        <NButton @click="showImport = true">导入</NButton>
        <NButton type="primary" :disabled="!users.length" @click="open(nodes.length)">添加节点</NButton>
      </NSpace>
    </template>
    <ErrorList :errors="errors.global" />
    <NSpace v-if="checked.length" style="margin-bottom: 8px" align="center">
      <span>已选 {{ checked.length }} 个</span>
      <NButton size="small" @click="batchServer = ''">批量设置 server</NButton>
      <NButton size="small" @click="cloneChecked">克隆</NButton>
      <NButton size="small" @click="showExport = true">导出所选</NButton>
      <NPopconfirm @positive-click="save(nodes.filter((n) => !checked.includes(n.id!)), '已删除')">
        <template #trigger><NButton size="small" type="error" ghost>批量删除</NButton></template>
        删除选中的 {{ checked.length }} 个节点？
      </NPopconfirm>
    </NSpace>
    <NDataTable v-model:checked-row-keys="checked" :columns="columns" :data="rows" :row-key="(row: Node) => row.id ?? -1" size="small" />
  </NCard>

  <NModal :show="batchServer !== null" preset="card" title="批量设置 server" style="max-width: 420px" @update:show="(v) => !v && (batchServer = null)">
    <NInput v-model:value="batchServer" placeholder="域名或 IP" />
    <template #footer>
      <NSpace justify="end">
        <NButton @click="batchServer = null">取消</NButton>
        <NButton type="primary" :disabled="!batchServer" :loading="saving" @click="setServer">应用到选中的标准节点</NButton>
      </NSpace>
    </template>
  </NModal>

  <NDrawer :show="!!editing" :width="620" @update:show="(v) => !v && (editing = null)">
    <NDrawerContent v-if="editing" :title="editing.draft.remark || '新节点'" closable>
      <NForm label-placement="left" label-width="120">
        <NFormItem label="所属用户" v-bind="f('user')">
          <NSelect v-model:value="editing.draft.user" :options="users.map((u) => ({ label: u.name, value: u.name }))" filterable />
        </NFormItem>
        <NFormItem label="模式" v-bind="f('mode')">
          <NRadioGroup v-model:value="editing.draft.mode">
            <NRadioButton value="standard">标准节点</NRadioButton>
            <NRadioButton value="direct">Mihomo 原生节点</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem label="名称" required v-bind="f('remark', '订阅中显示的代理名，同一用户下不能重复')">
          <NInput v-model:value="editing.draft.remark" />
        </NFormItem>
        <NFormItem label="地区" v-bind="f('region')">
          <NSelect v-model:value="editing.draft.region" :options="regions" filterable clearable />
        </NFormItem>
        <template v-if="editing.draft.mode === 'standard'">
          <NFormItem label="协议" v-bind="f('protocol')">
            <NRadioGroup v-model:value="editing.draft.protocol">
              <NRadioButton v-for="(label, value) in protocolLabels" :key="value" :value="value">{{ label }}</NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <NFormItem label="server" required v-bind="f('server')">
            <NInput v-model:value="editing.draft.server" />
          </NFormItem>
          <NFormItem label="port" required v-bind="f('port')">
            <NInputNumber v-model:value="editing.draft.port" :min="1" :max="65535" :show-button="false" />
          </NFormItem>
          <NFormItem v-if="editing.draft.protocol !== 'http'" label="UDP">
            <NSwitch v-model:value="editing.draft.udp" />
          </NFormItem>
          <template v-if="editing.draft.protocol === 'vmess'">
            <NFormItem label="uuid" v-bind="f('uuid')">
              <SecretInput v-model:value="editing.draft.uuid" :generate="randomUUID" :reveal="reveal" />
            </NFormItem>
            <NFormItem label="network">
              <NRadioGroup v-model:value="editing.draft.network">
                <NRadioButton value="raw">raw (tcp)</NRadioButton>
                <NRadioButton value="ws">ws</NRadioButton>
                <NRadioButton value="grpc">grpc</NRadioButton>
              </NRadioGroup>
            </NFormItem>
            <template v-if="editing.draft.network === 'ws'">
              <NFormItem label="ws path"><NInput v-model:value="editing.draft.ws_path" /></NFormItem>
              <NFormItem label="ws headers"><NDynamicInput v-model:value="headers" preset="pair" key-placeholder="Header" value-placeholder="值" /></NFormItem>
            </template>
            <NFormItem v-if="editing.draft.network === 'grpc'" label="service_name"><NInput v-model:value="editing.draft.grpc_service_name" /></NFormItem>
          </template>
          <template v-if="editing.draft.protocol === 'shadowsocks'">
            <NFormItem label="method"><NSelect v-model:value="editing.draft.method" :options="ssMethods" /></NFormItem>
            <NFormItem label="password" v-bind="f('password')">
              <SecretInput v-model:value="editing.draft.password" :generate="() => shadowsocksKey(editing!.draft.method)" :reveal="reveal" />
            </NFormItem>
          </template>
          <template v-if="editing.draft.protocol === 'socks5' || editing.draft.protocol === 'http'">
            <NFormItem label="认证">
              <NRadioGroup v-model:value="editing.draft.auth_type">
                <NRadioButton value="noauth">无认证</NRadioButton>
                <NRadioButton value="password">用户名密码</NRadioButton>
              </NRadioGroup>
            </NFormItem>
            <template v-if="editing.draft.auth_type === 'password'">
              <NFormItem label="username"><NInput v-model:value="editing.draft.auth_username" /></NFormItem>
              <NFormItem label="password"><SecretInput v-model:value="editing.draft.auth_password" :generate="() => randomString(24)" :reveal="reveal" /></NFormItem>
            </template>
          </template>
          <NFormItem label="tag" v-bind="f('tag', '留空自动生成；全局唯一')">
            <NInput v-model:value="editing.draft.tag" />
          </NFormItem>
        </template>
        <template v-else>
          <NFormItem label="节点字段" v-bind="f('config', '按 mihomo proxy 字段原样输出到 Clash；Surge 按兼容规则映射，不支持的类型跳过')">
            <div style="width: 100%">
              <CodeEditor :value="editing.yaml" language="yaml" min-height="240px" @update:value="onYAML" />
              <NAlert v-if="editing.yamlError" type="error" :bordered="false" style="margin-top: 6px">{{ editing.yamlError }}</NAlert>
            </div>
          </NFormItem>
        </template>
      </NForm>
      <template #footer>
        <NSpace>
          <NButton @click="editing = null">取消</NButton>
          <NButton type="primary" :loading="saving" :disabled="!editing.draft.remark || !!editing.yamlError" @click="confirm">保存</NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>

  <ImportDialog v-model:show="showImport" scope="users" @done="load" />
  <ExportDialog v-model:show="showExport" scope="manual_nodes" :manual-nodes="checked" />
</template>
