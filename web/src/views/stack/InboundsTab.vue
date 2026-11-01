<script setup lang="ts">
import { computed, h, ref } from 'vue'
import {
  NAlert, NButton, NCollapse, NCollapseItem, NDataTable, NDrawer, NDrawerContent, NDynamicInput, NForm, NFormItem, NInput,
  NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NSwitch, NTag, NText, type DataTableColumns,
} from 'naive-ui'
import { fieldState, hasErrorsUnder, type FormErrors, type Schemas } from '../../api/client'
import PortInput from '../../components/PortInput.vue'
import SecretInput from '../../components/SecretInput.vue'
import TemplateInput from '../../components/TemplateInput.vue'
import { randomString, shadowsocksKey } from '../../util/generate'
import { listenOptions, protocolLabels, regions, ssMethods } from '../../util/options'
import { stackPorts } from './ports'

type Inbound = Schemas['Inbound']
type UserRef = Schemas['UserRef']

const props = defineProps<{
  stack: Schemas['Stack']
  users: Schemas['User'][]
  errors: FormErrors
  reveal: () => Promise<void>
}>()

const editing = ref<{ index: number; draft: Inbound } | null>(null)
const subChosen = ref(true)

const columns = computed<DataTableColumns<Inbound & { _index: number }>>(() => [
  {
    title: '名称',
    key: 'name',
    render: (row) =>
      h(NSpace, { size: 4, align: 'center' }, () => [
        row.name,
        hasErrorsUnder(props.errors, `xray.inbounds[${row._index}]`) ? h(NTag, { size: 'small', type: 'error' }, () => '有错误') : null,
      ]),
  },
  { title: '协议', key: 'protocol', render: (row) => protocolLabels[row.protocol] ?? row.protocol },
  { title: '监听', key: 'listen', render: (row) => `${row.listen}:${row.port}` },
  { title: '订阅', key: 'sub', width: 60, render: (row) => (row.sub ? '是' : '否') },
  { title: '用户', key: 'users', render: (row) => row.users.map((u) => (u.profile ? `${u.user}/${u.profile}` : u.user)).join('、') || '—' },
  {
    title: '操作',
    key: 'actions',
    width: 140,
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'small', onClick: () => open(row._index) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => props.stack.xray.inbounds.splice(row._index, 1) }, {
          trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '删除'),
          default: () => '删除这个入站？',
        }),
      ]),
  },
])

const rows = computed(() => props.stack.xray.inbounds.map((inbound, index) => ({ ...inbound, _index: index })))

function blank(): Inbound {
  return {
    name: '', protocol: 'vmess', listen: '0.0.0.0', port: 0, sub: false, udp: true, network: 'raw', ws_path: '',
    ws_headers: {}, grpc_service_name: '', method: '', password: '', auth_type: '', auth_username: '', auth_password: '',
    users: props.users.map((u) => ({ user: u.name, profile: '', remark: '', display_template: '', tag: '' })),
    server: '', remark: '', display_template: '', region: '', tag: '',
  }
}

function open(index: number) {
  const source = index < props.stack.xray.inbounds.length ? props.stack.xray.inbounds[index] : blank()
  editing.value = { index, draft: JSON.parse(JSON.stringify(source)) }
  // 新建入站必须显式选择是否进入订阅。
  subChosen.value = index < props.stack.xray.inbounds.length
}

function confirm() {
  if (!editing.value) return
  const { index, draft } = editing.value
  if (index < props.stack.xray.inbounds.length) {
    props.stack.xray.inbounds[index] = draft
  } else {
    props.stack.xray.inbounds.push(draft)
  }
  editing.value = null
}

function onProtocol(protocol: Inbound['protocol']) {
  const draft = editing.value!.draft
  draft.protocol = protocol
  if (protocol === 'http') draft.udp = false
  if (protocol === 'vmess' && !draft.network) draft.network = 'raw'
  if (protocol === 'shadowsocks') {
    draft.method ||= 'aes-256-gcm'
    draft.password ||= shadowsocksKey(draft.method)
  }
  if ((protocol === 'socks5' || protocol === 'http') && !draft.auth_type) {
    draft.auth_type = 'password'
    draft.auth_username ||= props.stack.name
    draft.auth_password ||= randomString(24)
  }
}

const prefix = computed(() => `xray.inbounds[${editing.value?.index ?? 0}]`)
const f = (path: string, hint?: string) => fieldState(props.errors, `${prefix.value}.${path}`, hint)
const otherPorts = computed(() => stackPorts(props.stack))

const userOptions = computed(() => props.users.map((u) => ({ label: u.remark ? `${u.name}（${u.remark}）` : u.name, value: u.name })))
const selectedUsers = computed({
  get: () => editing.value?.draft.users.map((u) => u.user) ?? [],
  set: (names: string[]) => {
    const draft = editing.value!.draft
    draft.users = names.map((name) => draft.users.find((u) => u.user === name) ?? { user: name, profile: '', remark: '', display_template: '', tag: '' })
  },
})
const missingUsers = computed(() => props.users.filter((user) => !selectedUsers.value.includes(user.name)))
const selectedUserCount = computed(() => new Set(selectedUsers.value).size)
function addAllUsers() {
  // 仅追加缺少的用户，保留已有凭据档案及覆盖设置。
  editing.value!.draft.users.push(...missingUsers.value.map((user) => ({ user: user.name, profile: '', remark: '', display_template: '', tag: '' })))
}
function profileOptions(ref: UserRef) {
  const user = props.users.find((u) => u.name === ref.user)
  return [{ label: 'default', value: '' }, ...(user?.profiles ?? []).map((p) => ({ label: p.name, value: p.name }))]
}

const headers = computed({
  get: () => Object.entries(editing.value?.draft.ws_headers ?? {}).map(([key, value]) => ({ key, value })),
  set: (pairs: { key: string; value: string }[]) => {
    editing.value!.draft.ws_headers = Object.fromEntries(pairs.filter((p) => p.key).map((p) => [p.key, p.value]))
  },
})

const isUserProtocol = computed(() => ['vmess', 'shadowsocks'].includes(editing.value?.draft.protocol ?? ''))
const isAuthProtocol = computed(() => ['socks5', 'http'].includes(editing.value?.draft.protocol ?? ''))
const showUsers = computed(() => isUserProtocol.value || (isAuthProtocol.value && editing.value?.draft.sub))
</script>

<template>
  <NAlert type="info" :bordered="false" title="地区与订阅用户" style="margin-bottom: 12px">
    点击入站的「编辑」可设置用户列表；将「进入订阅」设为「是」后，还可设置地区（region）。
    region 用于 Surge / Premium Clash 的地区分组（如 US 美国、DE 德国），用户列表决定哪些用户能在订阅中看到该节点。修改后请保存并应用 Stack。
  </NAlert>
  <NSpace justify="end" style="margin: 20px 0 16px">
    <NButton type="primary" @click="open(stack.xray.inbounds.length)">添加入站</NButton>
  </NSpace>
  <NDataTable :columns="columns" :data="rows" :scroll-x="940" size="small" />

  <NDrawer :show="!!editing" :width="620" @update:show="(v) => !v && (editing = null)">
    <NDrawerContent v-if="editing" :title="editing.draft.name ? `入站 ${editing.draft.name}` : '新入站'" closable>
      <NForm label-placement="left" label-width="120">
        <NFormItem label="name" required v-bind="f('name', '在本 stack 内唯一；改名会同步其他 stack 中的引用')">
          <NInput v-model:value="editing.draft.name" />
        </NFormItem>
        <NFormItem label="protocol" v-bind="f('protocol')">
          <NRadioGroup :value="editing.draft.protocol" @update:value="onProtocol">
            <NRadioButton v-for="(label, value) in protocolLabels" :key="value" :value="value">{{ label }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem label="listen" v-bind="f('listen')">
          <NSelect v-model:value="editing.draft.listen" :options="listenOptions" filterable tag />
        </NFormItem>
        <NFormItem label="port" required v-bind="f('port')">
          <PortInput v-model:value="editing.draft.port" pool="xray_inbound" :stack-id="stack.id" :exclude="otherPorts" />
        </NFormItem>
        <NFormItem label="进入订阅" required v-bind="f('sub', '必须显式选择')">
          <NRadioGroup :value="subChosen ? editing.draft.sub : null" @update:value="(v) => ((editing!.draft.sub = v), (subChosen = true))">
            <NRadioButton :value="true">是</NRadioButton>
            <NRadioButton :value="false">否</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem v-if="editing.draft.protocol !== 'http'" label="UDP" v-bind="f('udp')">
          <NSwitch v-model:value="editing.draft.udp" />
        </NFormItem>

        <template v-if="editing.draft.protocol === 'vmess'">
          <NFormItem label="network" v-bind="f('network')">
            <NRadioGroup v-model:value="editing.draft.network">
              <NRadioButton value="raw">raw (tcp)</NRadioButton>
              <NRadioButton value="ws">ws</NRadioButton>
              <NRadioButton value="grpc">grpc</NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <template v-if="editing.draft.network === 'ws'">
            <NFormItem label="ws path" v-bind="f('ws_path', '以 / 开头')">
              <NInput v-model:value="editing.draft.ws_path" placeholder="/vmess" />
            </NFormItem>
            <NFormItem label="ws headers" v-bind="f('ws_headers')">
              <NDynamicInput v-model:value="headers" preset="pair" key-placeholder="Header" value-placeholder="值" />
            </NFormItem>
          </template>
          <NFormItem v-if="editing.draft.network === 'grpc'" label="service_name" v-bind="f('grpc_service_name')">
            <NInput v-model:value="editing.draft.grpc_service_name" />
          </NFormItem>
        </template>

        <template v-if="editing.draft.protocol === 'shadowsocks'">
          <NFormItem label="method" v-bind="f('method')">
            <NSelect v-model:value="editing.draft.method" :options="ssMethods" />
          </NFormItem>
          <NFormItem label="password" required v-bind="f('password', '2022 系列需要对应长度的 base64 密钥')">
            <SecretInput v-model:value="editing.draft.password" :generate="() => shadowsocksKey(editing!.draft.method)" :reveal="reveal" />
          </NFormItem>
        </template>

        <template v-if="isAuthProtocol">
          <NFormItem label="auth type" v-bind="f('auth_type')">
            <NRadioGroup v-model:value="editing.draft.auth_type">
              <NRadioButton value="noauth">无认证</NRadioButton>
              <NRadioButton value="password">用户名密码</NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <template v-if="editing.draft.auth_type === 'password'">
            <NFormItem label="username" v-bind="f('auth_username')">
              <NInput v-model:value="editing.draft.auth_username" />
            </NFormItem>
            <NFormItem label="password" v-bind="f('auth_password')">
              <SecretInput v-model:value="editing.draft.auth_password" :generate="() => randomString(24)" :reveal="reveal" />
            </NFormItem>
          </template>
        </template>

        <template v-if="showUsers">
          <NFormItem :label="isAuthProtocol ? '订阅用户' : '用户'" v-bind="f('users')">
            <NSpace vertical :size="6" style="width: 100%">
              <NSelect v-model:value="selectedUsers" :options="userOptions" multiple filterable />
              <NSpace justify="space-between" align="center">
                <NText depth="3">已选 {{ selectedUserCount }} / 共 {{ users.length }}</NText>
                <NButton size="tiny" secondary :disabled="!missingUsers.length" @click="addAllUsers">{{ !users.length ? '暂无用户' : missingUsers.length ? '添加全部' : '已全部添加' }}</NButton>
              </NSpace>
            </NSpace>
          </NFormItem>
          <NCollapse v-if="editing.draft.users.length" class="inbound-user-overrides">
            <NCollapseItem v-for="(ref, index) in editing.draft.users" :key="ref.user" :name="ref.user">
              <template #header>
                <NSpace align="center" :size="4">
                  <span>{{ ref.user }}</span>
                  <NTag v-if="ref.profile" size="small">{{ ref.profile }}</NTag>
                  <NText v-if="f(`users[${index}].user`).feedback || f(`users[${index}].profile`).feedback" type="error">
                    {{ f(`users[${index}].user`).feedback || f(`users[${index}].profile`).feedback }}
                  </NText>
                </NSpace>
              </template>
              <NFormItem label="档案" label-width="90">
                <NSelect v-model:value="ref.profile" :options="profileOptions(ref)" />
              </NFormItem>
              <NFormItem label="remark" label-width="90">
                <NInput v-model:value="ref.remark" placeholder="覆盖此用户在当前入站的节点名" />
              </NFormItem>
              <NFormItem label="展示名模板" label-width="90" v-bind="f(`users[${index}].display_template`)">
                <TemplateInput v-model:value="ref.display_template" />
              </NFormItem>
              <NFormItem label="tag" label-width="90">
                <NInput v-model:value="ref.tag" />
              </NFormItem>
            </NCollapseItem>
          </NCollapse>
        </template>

        <template v-if="editing.draft.sub">
          <NFormItem label="server" v-bind="f('server', '留空使用系统设置中的对外地址')">
            <NInput v-model:value="editing.draft.server" placeholder="external_host" />
          </NFormItem>
          <NFormItem label="remark" v-bind="f('remark')">
            <NInput v-model:value="editing.draft.remark" />
          </NFormItem>
          <NFormItem label="展示名模板" v-bind="f('display_template')">
            <TemplateInput v-model:value="editing.draft.display_template" />
          </NFormItem>
          <NFormItem label="region" v-bind="f('region')">
            <NSelect v-model:value="editing.draft.region" :options="regions" filterable clearable />
          </NFormItem>
        </template>
        <NCollapse>
          <NCollapseItem title="高级" name="advanced">
            <NFormItem label="tag" v-bind="f('tag')">
              <NInput v-model:value="editing.draft.tag" :placeholder="`${editing.draft.protocol}:${editing.draft.port}:${editing.draft.name}`" />
            </NFormItem>
          </NCollapseItem>
        </NCollapse>
      </NForm>
      <template #footer>
        <NSpace>
          <NButton @click="editing = null">取消</NButton>
          <NButton type="primary" :disabled="!subChosen || !editing.draft.name" @click="confirm">确定</NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.inbound-user-overrides { margin: -8px 0 12px 120px; }
@media (max-width: 767px) {
  .inbound-user-overrides { margin-left: 0; }
}
</style>
