<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import YAML from 'yaml'
import {
  NAlert, NButton, NCard, NCode, NCollapse, NCollapseItem, NDataTable, NDrawer, NDrawerContent, NDropdown, NDynamicInput, NEmpty, NForm,
  NFormItem, NGrid, NGridItem, NInput, NInputNumber, NModal, NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NSwitch, NTag,
  NText, useDialog, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, emptyErrors, fieldState, MASKED, splitProblem, type FormErrors, type Schemas } from '../api/client'
import CodeEditor from '../components/CodeEditor.vue'
import ErrorList from '../components/ErrorList.vue'
import EntityLink from '../components/EntityLink.vue'
import ExportDialog from '../components/ExportDialog.vue'
import ImportDialog from '../components/ImportDialog.vue'
import SecretInput from '../components/SecretInput.vue'
import { randomString, randomUUID, shadowsocksKey } from '../util/generate'
import { nodeSamples } from '../util/nodeSamples'
import { protocolLabels, regions, ssMethods } from '../util/options'

type Node = Schemas['ManualNode']

/** initialUser 让其他页面（例如用户详情）打开时直接筛选到某个用户；count 通知外层页面节点总数。 */
const props = defineProps<{ initialUser?: string }>()
const emit = defineEmits<{ count: [value: number] }>()
const message = useMessage()
const dialog = useDialog()
const nodes = ref<Node[]>([])
const users = ref<Schemas['User'][]>([])
const filterUser = ref<string | null>(props.initialUser || null)
watch(() => nodes.value.length, (value) => emit('count', value))
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
  { title: '用户', key: 'user', width: 100, render: (row) => h(EntityLink, { kind: 'user', name: row.user }) },
  {
    title: '名称',
    key: 'remark',
    render: (row) => h(NSpace, { size: 4 }, () => [row.remark, rowErrors(row._index) ? h(NTag, { size: 'small', type: 'error' }, () => '有错误') : null]),
  },
  { title: '模式', key: 'mode', width: 90, render: (row) => (row.mode === 'direct' ? '原生' : protocolLabels[row.protocol] ?? row.protocol) },
  { title: '服务器', key: 'server', render: (row) => (row.mode === 'direct' ? configValue(row, 'server') : `${row.server}:${row.port}`) },
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
  editing.value = { index, draft, yaml: draft.config ? YAML.stringify(draft.config) : DIRECT_SKELETON, yamlError: '' }
}

const DIRECT_SKELETON = 'type: vless\nserver: \nport: 443\n'

const modeHints: Record<string, string> = {
  standard: '用表单填写 VMess、Shadowsocks、SOCKS5、HTTP 节点，三种订阅格式都能输出',
  direct: '直接写 mihomo 的 proxy 字段，可以用 VLESS、Trojan、Hysteria2 等表单没有的协议',
}

const sampleOptions = nodeSamples.map((sample) => ({
  key: sample.key,
  label: sample.surge ? sample.label : `${sample.label}（Surge 订阅跳过）`,
}))

// Clash 配置与这里写法的对照示例。
const clashExample = `proxies:
  - name: 东京-01
    type: trojan
    server: proxy.example.com
    port: 443
    password: REPLACE_WITH_PASSWORD
    udp: true
    sni: proxy.example.com`
const directExample = `type: trojan
server: proxy.example.com
port: 443
password: REPLACE_WITH_PASSWORD
udp: true
sni: proxy.example.com`

/** 用示例替换编辑器内容；已经写过内容时先确认。 */
function fillSample(key: string) {
  const sample = nodeSamples.find((item) => item.key === key)
  if (!sample || !editing.value) return
  const current = editing.value.yaml.trim()
  if (!current || current === DIRECT_SKELETON.trim()) {
    onYAML(sample.yaml)
    return
  }
  dialog.warning({
    title: '替换当前内容？',
    content: `用「${sample.label}」示例覆盖编辑器里已有的内容。`,
    positiveText: '替换',
    negativeText: '取消',
    onPositiveClick: () => onYAML(sample.yaml),
  })
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
  if (await save(next, '已批量设置服务器')) batchServer.value = null
}
function cloneChecked() {
  const copies = nodes.value
    .filter((node) => checked.value.includes(node.id!))
    .map((node) => ({ ...JSON.parse(JSON.stringify(node)), id: undefined, tag: '', remark: `${node.remark} copy` }))
  save([...nodes.value, ...copies], `已克隆 ${copies.length} 个节点`)
}
</script>

<template>
  <NCard title="节点列表" class="table-panel">
    <template #header-extra>
      <NSpace justify="end">
        <NSelect v-model:value="filterUser" :options="users.map((u) => ({ label: u.name, value: u.name }))" clearable placeholder="全部用户" style="width: 160px" />
        <NButton @click="showImport = true">导入</NButton>
        <NButton type="primary" :disabled="!users.length" @click="open(nodes.length)">新建节点</NButton>
      </NSpace>
    </template>
    <NCollapse style="margin-bottom: 12px">
      <NCollapseItem title="填写说明与示例" name="help">
        <div class="help">
          <p><NText strong>两种模式。</NText>{{ modeHints.standard }}；Mihomo 原生节点则{{ modeHints.direct }}。</p>
          <p>
            <NText strong>原生节点的写法。</NText>填一个 YAML 映射，相当于 Clash 配置 <NText code>proxies</NText> 列表中的一项，去掉开头的
            <NText code>- </NText>。必须包含 <NText code>type</NText>、<NText code>server</NText>、<NText code>port</NText>；
            <NText code>name</NText> 不用写，订阅里统一使用「名称」栏的值。
          </p>
          <p>
            <NText strong>Surge 兼容。</NText>Surge 订阅只输出 vmess（tcp 或 ws）、ss、socks5、http 类型的节点，其他类型只出现在 Clash 和 Premium Clash 订阅里。
          </p>
          <p>
            <NText strong>保存与生效。</NText>节点 tag 全局唯一，同一用户下名称不能重复。改动在「发布订阅包」重新发布并导入订阅端后，客户端才能拉到。
          </p>
          <NGrid cols="1 m:2" responsive="screen" :x-gap="12" :y-gap="8">
            <NGridItem>
              <NText depth="3" class="example-title">Clash 配置里的节点</NText>
              <NCode :code="clashExample" class="example" />
            </NGridItem>
            <NGridItem>
              <NText depth="3" class="example-title">填到原生节点里的内容（「名称」栏填 东京-01）</NText>
              <NCode :code="directExample" class="example" />
            </NGridItem>
          </NGrid>
          <p style="margin-bottom: 0">
            <NText depth="3">编辑原生节点时，可以用编辑器上方的「填入示例」直接套用 VLESS + Reality、VLESS + WS、Trojan、Hysteria2、VMess、Shadowsocks 2022 的模板。</NText>
          </p>
        </div>
      </NCollapseItem>
    </NCollapse>
    <ErrorList :errors="errors.global" />
    <NSpace v-if="checked.length" style="margin-bottom: 8px" align="center">
      <span>已选 {{ checked.length }} 个</span>
      <NButton size="small" @click="batchServer = ''">批量设置服务器</NButton>
      <NButton size="small" @click="cloneChecked">克隆</NButton>
      <NButton size="small" @click="showExport = true">导出所选</NButton>
      <NPopconfirm @positive-click="save(nodes.filter((n) => !checked.includes(n.id!)), '已删除')">
        <template #trigger><NButton size="small" type="error" ghost>批量删除</NButton></template>
        删除选中的 {{ checked.length }} 个节点？
      </NPopconfirm>
    </NSpace>
    <NDataTable v-model:checked-row-keys="checked" :columns="columns" :data="rows" :scroll-x="900" :row-key="(row: Node) => row.id ?? -1" size="small">
      <template #empty>
        <NEmpty v-if="!users.length" description="手工节点要归属到用户，请先创建用户">
          <template #extra><RouterLink :to="{ name: 'users' }">前往用户页</RouterLink></template>
        </NEmpty>
        <NEmpty v-else-if="nodes.length" description="所选用户没有手工节点" />
        <NEmpty v-else description="还没有手工节点。添加的节点会进入所属用户的订阅">
          <template #extra><NButton size="small" type="primary" @click="open(nodes.length)">新建第一个节点</NButton></template>
        </NEmpty>
      </template>
    </NDataTable>
  </NCard>

  <NModal :show="batchServer !== null" preset="card" title="批量设置服务器" style="max-width: 420px" @update:show="(v) => !v && (batchServer = null)">
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
          <NSpace vertical style="width: 100%">
            <NSelect v-model:value="editing.draft.user" :options="users.map((u) => ({ label: u.name, value: u.name }))" filterable />
            <EntityLink v-if="editing.draft.user" kind="user" :name="editing.draft.user" label="查看用户 ↗" target="_blank" rel="noopener" />
          </NSpace>
        </NFormItem>
        <NFormItem label="模式" v-bind="f('mode', modeHints[editing.draft.mode])">
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
          <NFormItem label="服务器" required v-bind="f('server', '域名或 IP')">
            <NInput v-model:value="editing.draft.server" />
          </NFormItem>
          <NFormItem label="端口" required v-bind="f('port')">
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
              <NSpace align="center" justify="space-between" style="margin-bottom: 6px">
                <NText depth="3">Clash 节点去掉 name 和开头的 "- " 即可</NText>
                <NDropdown :options="sampleOptions" trigger="click" @select="fillSample">
                  <NButton size="small">填入示例</NButton>
                </NDropdown>
              </NSpace>
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

<style scoped>
.help {
  font-size: 13px;
  line-height: 1.8;
}
.help p {
  margin: 0 0 8px;
}
.example-title {
  display: block;
  margin-bottom: 4px;
  font-size: 12px;
}
.example {
  display: block;
  padding: 10px 12px;
  border-radius: 6px;
  background: rgba(128, 128, 128, 0.08);
  font-size: 12px;
  white-space: pre;
  overflow-x: auto;
}
</style>
