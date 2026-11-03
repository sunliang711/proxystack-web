<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  NAlert, NButton, NCard, NDataTable, NDescriptions, NDescriptionsItem, NDrawer, NDrawerContent, NForm, NFormItem,
  NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NStep, NSteps, NSwitch, NTag, NText, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { api, MASKED, problemMessage } from '../api/client'
import CopyableCode from '../components/CopyableCode.vue'
import { formatTime } from '../util/format'

interface SSHIdentity {
  configured: boolean
  public_key: string
  fingerprint: string
  created_at: string
}

interface PublishTarget {
  id?: number
  name: string
  ssh_host: string
  ssh_port: number
  ssh_user: string
  host_key_algorithm: string
  host_key_fingerprint: string
  remote_port: number
  source: string
  secret: string
  enabled: boolean
  last_test_at?: string
  last_test_error?: string
  last_publish_at?: string
  last_sequence?: number
  last_publish_error?: string
  deploy_state?: string
  current_version?: string
  previous_version?: string
  last_deploy_at?: string
  last_deploy_error?: string
}

interface ProvisioningResult {
  target: PublishTarget
  secret: string
  authorized_keys_entry?: string
  publish_token_command?: string
  sshd_config?: string
}

interface TestStage {
  name: string
  status: string
  duration_ms?: number
  message?: string
}

interface TestResult {
  stages: TestStage[]
  status?: string
  users?: number
  nodes?: number
  sources?: Array<{ name: string; sequence: number; error?: string }>
}

const message = useMessage()
const router = useRouter()
const identity = ref<SSHIdentity | null>(null)
const targets = ref<PublishTarget[]>([])
const currentSource = ref('main')
const loading = ref(false)
const generating = ref(false)
const saving = ref(false)
const testing = ref<number | null>(null)
const inspecting = ref<number | null>(null)
const deploying = ref<number | null>(null)
const rollingBack = ref<number | null>(null)

async function load() {
  loading.value = true
  const [identityResult, targetsResult, statusResult] = await Promise.all([
    api.GET('/api/sub-publish/identity'),
    api.GET('/api/sub-publish/targets'),
    api.GET('/api/sub-bundle'),
  ])
  loading.value = false
  if (identityResult.error) message.error(problemMessage(identityResult.error, '读取 SSH 密钥失败'))
  if (targetsResult.error) message.error(problemMessage(targetsResult.error, '读取订阅端失败'))
  if (statusResult.error) message.error(problemMessage(statusResult.error, '读取订阅来源失败'))
  identity.value = identityResult.data ?? null
  targets.value = targetsResult.data ?? []
  currentSource.value = statusResult.data?.source ?? 'main'
}
onMounted(load)

async function generateIdentity() {
  generating.value = true
  const { data, error } = await api.POST('/api/sub-publish/identity')
  generating.value = false
  if (error || !data) {
    message.error(problemMessage(error, '生成 SSH 密钥失败'))
    return
  }
  identity.value = data
  message.success('SSH 密钥已生成')
}

const editing = ref<PublishTarget | null>(null)
const draft = computed(() => editing.value as PublishTarget)
function open(target?: PublishTarget) {
  editing.value = target
    ? { ...target, source: currentSource.value }
    : {
        name: '', ssh_host: '', ssh_port: 22, ssh_user: 'proxystack-tunnel', host_key_algorithm: 'ssh-ed25519',
        host_key_fingerprint: '', remote_port: 9444, source: currentSource.value, secret: '', enabled: true,
      }
}

const provisioning = ref<ProvisioningResult | null>(null)
function sshUserSetupCommand(sshUser: string) {
  if (!/^[A-Za-z_][A-Za-z0-9_-]{0,31}$/.test(sshUser)) return '# 请先填写合法的 SSH 用户名'
  const sshDir = sshUser === 'root' ? '/root/.ssh' : `/home/${sshUser}/.ssh`
  return `id -u ${sshUser} >/dev/null 2>&1 || sudo useradd --create-home --shell /bin/bash ${sshUser}\nsudo install -d -m 700 -o ${sshUser} -g ${sshUser} ${sshDir}\nsudo touch ${sshDir}/authorized_keys\nsudo chown ${sshUser}:${sshUser} ${sshDir}/authorized_keys\nsudo chmod 600 ${sshDir}/authorized_keys`
}
function sudoSetupCommand(sshUser: string) {
  if (sshUser === 'root') return '# root 用户无需配置 sudo'
  if (!/^[A-Za-z_][A-Za-z0-9_-]{0,31}$/.test(sshUser)) return '# 请先填写合法的 SSH 用户名'
  return `echo '${sshUser} ALL=(root) NOPASSWD: /bin/true, /usr/bin/test, /tmp/proxystack-web-deploy-*, /opt/proxystack-web-sub/bin/proxystack-web' | sudo tee /etc/sudoers.d/proxystack-web-sub-deploy >/dev/null\nsudo chmod 440 /etc/sudoers.d/proxystack-web-sub-deploy\nsudo visudo -cf /etc/sudoers.d/proxystack-web-sub-deploy\nsudo -u ${sshUser} sudo -n /bin/true`
}
function provisioningResult(data: any): ProvisioningResult {
  const target = data.target ?? data
  return {
    target,
    secret: data.secret ?? '',
    authorized_keys_entry: data.authorized_keys_entry,
    publish_token_command: data.publish_token_command,
    sshd_config: data.sshd_config,
  }
}

async function save() {
  saving.value = true
  const id = draft.value.id
  const body = {
    name: draft.value.name,
    ssh_host: draft.value.ssh_host,
    ssh_port: draft.value.ssh_port,
    ssh_user: draft.value.ssh_user,
    host_key_algorithm: draft.value.host_key_algorithm,
    host_key_fingerprint: draft.value.host_key_fingerprint,
    remote_port: draft.value.remote_port,
    source: currentSource.value,
    enabled: draft.value.enabled,
  }
  const result = id
    ? await api.PUT('/api/sub-publish/targets/{id}', { params: { path: { id } }, body })
    : await api.POST('/api/sub-publish/targets', { body })
  saving.value = false
  if (result.error || !result.data) {
    message.error(problemMessage(result.error, '保存订阅端失败'))
    return
  }
  const created = !id
  if (created) provisioning.value = provisioningResult(result.data)
  editing.value = null
  message.success('订阅端配置已保存')
  await load()
}

async function remove(target: PublishTarget) {
  const { error } = await api.DELETE('/api/sub-publish/targets/{id}', { params: { path: { id: target.id! } } })
  if (error) {
    message.error(problemMessage(error, '删除订阅端失败'))
    return
  }
  message.success('订阅端已删除')
  await load()
}

async function rotateSecret(target: PublishTarget) {
  const { data, error } = await api.POST('/api/sub-publish/targets/{id}/rotate-secret', { params: { path: { id: target.id! } } })
  if (error || !data) {
    message.error(problemMessage(error, '轮换发布 Secret 失败'))
    return
  }
  provisioning.value = provisioningResult(data)
  message.success('管理端已改用新 Secret，请立即更新订阅端')
  await load()
}

const viewingSetup = ref<number | null>(null)
async function viewSetup(target: PublishTarget) {
  viewingSetup.value = target.id ?? null
  const { data, error } = await api.GET('/api/sub-publish/targets/{id}/setup', { params: { path: { id: target.id! } } })
  viewingSetup.value = null
  if (error || !data) {
    message.error(problemMessage(error, '读取订阅端完整配置失败'))
    return
  }
  provisioning.value = provisioningResult(data)
}

const testResult = ref<{ target: PublishTarget; result: TestResult } | null>(null)
async function test(target: PublishTarget) {
  testing.value = target.id ?? null
  const { data, error } = await api.POST('/api/sub-publish/targets/{id}/test', { params: { path: { id: target.id! } } })
  testing.value = null
  if (error || !data) {
    message.error(problemMessage(error, '连接测试失败'))
    return
  }
  testResult.value = { target, result: data }
  await load()
}

async function inspectDeploy(target: PublishTarget) {
  inspecting.value = target.id ?? null
  const { data, error } = await api.POST('/api/sub-publish/targets/{id}/deploy/inspect', { params: { path: { id: target.id! } } })
  inspecting.value = null
  if (error || !data) {
    message.error(problemMessage(error, '部署环境检测失败'))
    await load()
    return
  }
  message.success(`部署环境正常：${data.os}/${data.arch}，状态 ${data.state}`)
  await load()
}

async function deploy(target: PublishTarget) {
  deploying.value = target.id ?? null
  const { data, error } = await api.POST('/api/sub-publish/targets/{id}/deploy', {
    params: { path: { id: target.id! } }, body: { version: '' },
  })
  deploying.value = null
  if (error || !data) {
    message.error(problemMessage(error, '提交部署任务失败'))
    return
  }
  message.success(`部署任务 #${data.task_id} 已提交`)
  void router.push({ name: 'task-detail', params: { source: 'task', id: data.task_id } })
}

async function rollbackDeploy(target: PublishTarget) {
  rollingBack.value = target.id ?? null
  const { data, error } = await api.POST('/api/sub-publish/targets/{id}/deploy/rollback', { params: { path: { id: target.id! } } })
  rollingBack.value = null
  if (error || !data) {
    message.error(problemMessage(error, '提交回滚任务失败'))
    return
  }
  message.success(`回滚任务 #${data.task_id} 已提交`)
  void router.push({ name: 'task-detail', params: { source: 'task', id: data.task_id } })
}

const instructions = ref<PublishTarget | null>(null)
const authorizedKeys = (target: PublishTarget) =>
  `restrict,port-forwarding,permitopen="127.0.0.1:${target.remote_port}" ${identity.value?.public_key ?? ''} proxystack-web`
const tokenCommand = (target: PublishTarget) =>
  `sudo proxystack-web sub --dir /opt/proxystack-web-sub source set-publish-token ${target.source}`
const sshdConfig = (target: PublishTarget) => `Match User ${target.ssh_user}\n    AllowTcpForwarding local\n    PermitOpen 127.0.0.1:${target.remote_port}\n    PermitTTY no\n    X11Forwarding no\n    AllowAgentForwarding no\nMatch all`
const sshdConfigSetupCommand = (target: PublishTarget) =>
  `sudo install -d -m 755 /etc/ssh/sshd_config.d\nsudo tee /etc/ssh/sshd_config.d/proxystack-web-sub.conf >/dev/null <<'EOF'\n${sshdConfig(target)}\nEOF\nsudo sshd -t && sudo systemctl reload ssh`

function statusTime(value?: string) {
  return value && !value.startsWith('0001') && !value.startsWith('1970') ? formatTime(value) : '从未执行'
}

function sourceSequence(target: PublishTarget, result: TestResult) {
  return result.sources?.find((source) => source.name === target.source)?.sequence
}

function workflowState(row: PublishTarget) {
  if (!row.deploy_state) return { label: '第 1 步：检测部署', type: 'warning' as const, detail: '先确认 SSH、系统和 sudo' }
  if (row.deploy_state !== 'installed') return { label: '第 2 步：部署', type: 'warning' as const, detail: `当前状态：${row.deploy_state}` }
  const testedAfterDeploy = row.last_test_at && (!row.last_deploy_at || Date.parse(row.last_test_at) >= Date.parse(row.last_deploy_at))
  if (!testedAfterDeploy || row.last_test_error) return { label: '第 3 步：测试', type: 'warning' as const, detail: '部署完成后验证转发和 Secret' }
  return { label: '第 5 步：签发订阅包', type: 'success' as const, detail: '第 4 步 Secret 已配置，可前往发布页' }
}

const columns: DataTableColumns<PublishTarget> = [
  { title: '名称', key: 'name', minWidth: 120 },
  { title: 'SSH 地址', key: 'ssh_host', minWidth: 190, render: (row) => `${row.ssh_user}@${row.ssh_host}:${row.ssh_port}` },
  { title: '来源', key: 'source', width: 110 },
  { title: '启用', key: 'enabled', width: 70, render: (row) => (row.enabled ? '是' : '否') },
  {
    title: '接入流程', key: 'workflow', minWidth: 210,
    render: (row) => {
      const state = workflowState(row)
      return h(NSpace, { vertical: true, size: 2 }, () => [
        h(NTag, { size: 'small', type: state.type }, () => state.label),
        h(NText, { depth: 3 }, () => state.detail),
      ])
    },
  },
  {
    title: '最近测试', key: 'last_test_at', minWidth: 170,
    render: (row) => h(NSpace, { vertical: true, size: 2 }, () => [
      statusTime(row.last_test_at),
      row.last_test_error
        ? h(NText, { type: 'error' }, () => row.last_test_error)
        : row.last_test_at ? h(NTag, { size: 'small', type: 'success' }, () => '通过') : null,
    ]),
  },
  {
    title: '部署状态', key: 'deploy_state', minWidth: 180,
    render: (row) => h(NSpace, { vertical: true, size: 2 }, () => [
      row.deploy_state ? `${row.deploy_state}${row.current_version ? ` · ${row.current_version}` : ''}` : '尚未检测',
      row.previous_version ? h(NText, { depth: 3 }, () => `可回滚：${row.previous_version}`) : null,
      row.last_deploy_error ? h(NText, { type: 'error' }, () => row.last_deploy_error) : null,
    ]),
  },
  {
    title: '最近发布', key: 'last_publish_at', minWidth: 170,
    render: (row) => h(NSpace, { vertical: true, size: 2 }, () => [
      row.last_sequence ? `${statusTime(row.last_publish_at)} · #${row.last_sequence}` : '从未发布',
      row.last_publish_error
        ? h(NText, { type: 'error' }, () => row.last_publish_error)
        : row.last_sequence ? h(NTag, { size: 'small', type: 'success' }, () => '成功') : null,
    ]),
  },
  {
    title: '操作', key: 'actions', width: 660, fixed: 'right',
    render: (row) => h(NSpace, { size: 4 }, () => [
      h(NButton, { size: 'small', loading: inspecting.value === row.id, onClick: () => inspectDeploy(row) }, () => '检测部署'),
      h(NPopconfirm, { onPositiveClick: () => deploy(row) }, {
        trigger: () => h(NButton, { size: 'small', type: 'primary', loading: deploying.value === row.id }, () => row.deploy_state === 'installed' ? '更新' : '部署'),
        default: () => `将下载最新稳定版并通过 SSH ${row.deploy_state === 'installed' ? '更新' : '部署'}订阅端，继续？`,
      }),
      h(NButton, { size: 'small', disabled: row.deploy_state !== 'installed', loading: testing.value === row.id, onClick: () => test(row), title: '先完成订阅端部署' }, () => '测试'),
      h(NPopconfirm, { onPositiveClick: () => rollbackDeploy(row) }, {
        trigger: () => h(NButton, { size: 'small', disabled: !row.previous_version, loading: rollingBack.value === row.id }, () => '回滚'),
        default: () => `将订阅端回滚到 ${row.previous_version || '上一版本'}，继续？`,
      }),
      h(NPopconfirm, { onPositiveClick: () => viewSetup(row) }, {
        trigger: () => h(NButton, { size: 'small', loading: viewingSetup.value === row.id }, () => '配置 Secret / 完整配置'),
        default: () => '完整配置包含明文发布 Secret，查看操作会记录审计日志。继续？',
      }),
      h(NButton, { size: 'small', onClick: () => (instructions.value = row) }, () => '配置说明'),
      h(NButton, { size: 'small', onClick: () => open(row) }, () => '编辑'),
      h(NPopconfirm, { onPositiveClick: () => rotateSecret(row) }, {
        trigger: () => h(NButton, { size: 'small', quaternary: true }, () => '轮换 Secret'),
        default: () => '管理端将立即改用新 Secret；订阅端更新前在线发布会失败，更新后旧 Secret 才会失效。继续？',
      }),
      h(NPopconfirm, { onPositiveClick: () => remove(row) }, {
        trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '删除'),
        default: () => '删除这个订阅端配置？',
      }),
    ]),
  },
]

const algorithmOptions = [
  { label: 'Ed25519', value: 'ssh-ed25519' },
  { label: 'ECDSA P-256', value: 'ecdsa-sha2-nistp256' },
  { label: 'RSA SHA-256/512', value: 'ssh-rsa' },
]

function hostKeyCommand(algorithm: string) {
  const publicKeyFile = {
    'ssh-ed25519': '/etc/ssh/ssh_host_ed25519_key.pub',
    'ecdsa-sha2-nistp256': '/etc/ssh/ssh_host_ecdsa_key.pub',
    'ssh-rsa': '/etc/ssh/ssh_host_rsa_key.pub',
  }[algorithm] ?? '/etc/ssh/ssh_host_ed25519_key.pub'
  return `sudo ssh-keygen -lf ${publicKeyFile} -E sha256`
}
</script>

<template>
  <NSpace vertical :size="20">
    <NCard title="管理端 SSH 密钥">
      <NSpace v-if="identity?.configured" vertical :size="12">
        <NDescriptions :column="1" label-placement="left" bordered size="small">
          <NDescriptionsItem label="公钥指纹"><NTag size="small">{{ identity.fingerprint }}</NTag></NDescriptionsItem>
          <NDescriptionsItem label="生成时间">{{ statusTime(identity.created_at) }}</NDescriptionsItem>
        </NDescriptions>
        <CopyableCode label="公钥" :value="identity.public_key" />
        <NAlert type="warning" :bordered="false">
          私钥只保存在管理端，不会通过 API 或页面返回。所有目标共用这一个公钥，需要手动配置到每台订阅端。
        </NAlert>
      </NSpace>
      <NSpace v-else vertical>
        <NAlert type="info" :bordered="false">在线发布需要一套管理端 SSH Ed25519 密钥。生成后只能查看公钥，不能取回私钥。</NAlert>
        <NButton type="primary" :loading="generating" @click="generateIdentity">生成 SSH 密钥</NButton>
      </NSpace>
    </NCard>

    <NCard title="订阅端接入流程">
      <NSpace vertical :size="8">
        <NText strong>1 检测部署 → 2 部署 → 3 测试 → 4 配置发布 Secret → 5 签发订阅包</NText>
        <NText depth="3">通过 SSH 安装或更新时，第 4 步会由管理端在部署完成后自动执行；离线安装时，先安装二进制，再从“配置 Secret / 完整配置”查看并执行命令。</NText>
        <NText depth="3">只有部署完成且部署后的测试通过，目标才可在“发布订阅包”页面选择并签发。</NText>
      </NSpace>
    </NCard>

    <NCard title="订阅端目标" class="table-panel">
      <template #header-extra>
        <NButton type="primary" :disabled="!identity?.configured" @click="open()">添加订阅端</NButton>
      </template>
      <NAlert type="info" :bordered="false" style="margin-bottom: 12px">
        管理端按需建立 SSH 正向转发，远端主机固定为 127.0.0.1。每个订阅端使用独立发布 Secret，测试会依次检查 SSH、转发、订阅端健康状态和发布鉴权。
      </NAlert>
      <NDataTable :loading="loading" :columns="columns" :data="targets" :scroll-x="1710" size="small" />
    </NCard>
  </NSpace>

  <NDrawer :show="!!editing" :width="560" @update:show="(value) => !value && (editing = null)">
    <NDrawerContent v-if="editing" :title="editing.name || '新的订阅端'" closable>
      <NForm label-placement="left" label-width="120">
        <NFormItem label="名称"><NInput v-model:value="draft.name" /></NFormItem>
        <NFormItem label="SSH 主机"><NInput v-model:value="draft.ssh_host" placeholder="sub.example.com" /></NFormItem>
        <NFormItem label="SSH 端口"><NInputNumber v-model:value="draft.ssh_port" :min="1" :max="65535" :show-button="false" /></NFormItem>
        <NFormItem label="SSH 用户"><NInput v-model:value="draft.ssh_user" /></NFormItem>
        <NFormItem v-if="!draft.id" label="SSH 用户创建">
          <NSpace vertical :size="8" style="width: 100%">
            <NText depth="3">需要在订阅端主机上创建此专用 SSH 用户。该用户与运行订阅服务的 <NText code>proxystack-web-sub</NText> 用户不同：</NText>
            <CopyableCode :value="sshUserSetupCommand(draft.ssh_user)" />
            <NText depth="3">非 root 用户还要允许部署命令使用 <NText code>sudo -n</NText>：</NText>
            <CopyableCode :value="sudoSetupCommand(draft.ssh_user)" />
            <NText depth="3">如果用户已存在，第一行会跳过创建；后续命令会初始化 <NText code>~/.ssh/authorized_keys</NText> 及所需权限。保存后再把管理端生成的完整公钥配置追加到该文件。</NText>
          </NSpace>
        </NFormItem>
        <NFormItem label="Host Key 算法"><NSelect v-model:value="draft.host_key_algorithm" :options="algorithmOptions" /></NFormItem>
        <NFormItem label="Host Key 指纹">
          <NInput v-model:value="draft.host_key_fingerprint" placeholder="SHA256:..." />
        </NFormItem>
        <NFormItem label="指纹获取方式">
          <NSpace vertical :size="8" style="width: 100%">
            <NText depth="3">这是订阅端 SSH 服务的 Host Key，不是管理端生成的登录公钥。在订阅端主机本机执行：</NText>
            <CopyableCode :value="hostKeyCommand(draft.host_key_algorithm)" />
            <NText depth="3">从输出中只复制完整的 <NText code>SHA256:...</NText>；例如输出末尾为 <NText code>(ED25519)</NText> 时，算法应选择 Ed25519。</NText>
          </NSpace>
        </NFormItem>
        <NFormItem label="远端 HTTP 地址">
          <NInput :value="`127.0.0.1:${draft.remote_port}`" readonly />
        </NFormItem>
        <NFormItem label="远端端口"><NInputNumber v-model:value="draft.remote_port" :min="1" :max="65535" :show-button="false" /></NFormItem>
        <NFormItem label="来源名"><NInput :value="currentSource" readonly /></NFormItem>
        <NFormItem v-if="draft.id" label="发布 Secret">
          <NInput :value="draft.secret || MASKED" readonly />
        </NFormItem>
        <NFormItem label="启用"><NSwitch v-model:value="draft.enabled" /></NFormItem>
      </NForm>
      <NAlert type="warning" :bordered="false">
        SSH Host Key 指纹必须通过独立可信渠道核对。部署允许该 SSH 密钥上传并以 root 执行订阅端二进制，等同部署级 root 权限，只能用于受控的专用账号。来源名必须与当前管理端来源“{{ currentSource }}”一致。
      </NAlert>
      <template #footer>
        <NSpace>
          <NButton @click="editing = null">取消</NButton>
          <NButton type="primary" :loading="saving" @click="save">保存</NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>

  <NModal :show="!!provisioning" preset="card" title="订阅端完整配置" style="width: 820px; max-width: calc(100vw - 32px)" @update:show="(value) => !value && (provisioning = null)">
    <NSpace v-if="provisioning" vertical :size="16">
      <NAlert type="warning" :bordered="false">
        此窗口包含明文发布 Secret。关闭后页面会清除本次显示内容，可从目标列表的“完整配置”再次查看；每次查看都会记录审计日志。
      </NAlert>
      <NSteps vertical :current="1" status="process">
        <NStep title="安装管理端 SSH 公钥">
          <CopyableCode :value="provisioning.authorized_keys_entry || authorizedKeys(provisioning.target)" />
          <NText depth="3">把这一整行追加到 SSH 用户的 <NText code>~/.ssh/authorized_keys</NText>，目录权限设为 700，文件权限设为 600。</NText>
        </NStep>
        <NStep title="限制 SSH 用户的转发范围">
          <NSpace vertical :size="8">
            <NText depth="3">推荐写入订阅端的 <NText code>/etc/ssh/sshd_config.d/proxystack-web-sub.conf</NText>：</NText>
            <CopyableCode label="写入 sshd_config.d 并 reload" :value="sshdConfigSetupCommand(provisioning.target)" />
            <CopyableCode label="sshd 限制片段" :value="provisioning.sshd_config || sshdConfig(provisioning.target)" />
            <NText depth="3">命令会先执行 <NText code>sshd -t</NText>，校验通过后再 reload SSH 服务；如果系统服务名是 <NText code>sshd</NText>，将最后的 <NText code>ssh</NText> 替换为 <NText code>sshd</NText>。</NText>
          </NSpace>
        </NStep>
        <NStep title="允许非交互部署">
          <CopyableCode :value="sudoSetupCommand(provisioning.target.ssh_user)" />
          <NText depth="3">部署检测要求 <NText code>sudo -n /bin/true</NText> 成功。root 用户不需要此步骤。</NText>
        </NStep>
        <NStep title="配置发布 Secret">
          <NSpace vertical :size="8">
            <CopyableCode label="发布 Secret" :value="provisioning.secret" />
            <NText depth="3">首次通过 SSH 安装或更新时，管理端会在部署完成后自动配置这个 Secret，不要在订阅端尚未安装二进制时执行下面的命令。</NText>
            <CopyableCode label="安装完成后手工执行，按提示粘贴 Secret" :value="provisioning.publish_token_command || tokenCommand(provisioning.target)" />
            <NText depth="3">如果使用离线方式安装，先完成订阅端安装，再执行此命令；之后在线发布和连接测试会使用同一个 Secret。</NText>
          </NSpace>
        </NStep>
        <NStep title="核对 Host Key 并测试">
          <NText>在订阅端本机执行 <NText code>ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256</NText>，通过独立渠道核对配置的指纹，再回到目标列表点击“测试”。其他 Host Key 算法请改用对应的公钥文件。</NText>
        </NStep>
      </NSteps>
    </NSpace>
  </NModal>

  <NModal :show="!!instructions" preset="card" title="订阅端配置说明" style="width: 820px; max-width: calc(100vw - 32px)" @update:show="(value) => !value && (instructions = null)">
    <NSpace v-if="instructions" vertical :size="14">
      <CopyableCode label="authorized_keys" :value="authorizedKeys(instructions)" />
      <CopyableCode label="写入 /etc/ssh/sshd_config.d 并 reload" :value="sshdConfigSetupCommand(instructions)" />
      <CopyableCode label="sshd_config 限制" :value="sshdConfig(instructions)" />
      <CopyableCode label="sudo -n 配置" :value="sudoSetupCommand(instructions.ssh_user)" />
      <NText depth="3">首次通过 SSH 安装或更新时，管理端会在部署完成后自动配置发布 Secret；离线安装或手工重置时，请先完成订阅端安装，再执行：</NText>
      <CopyableCode label="安装完成后配置发布 Secret" :value="tokenCommand(instructions)" />
      <NAlert type="info" :bordered="false">此说明不包含明文发布 Secret；需要重新配置时，请从目标列表打开“完整配置”。</NAlert>
    </NSpace>
  </NModal>

  <NModal :show="!!testResult" preset="card" :title="`${testResult?.target.name ?? ''}：连接测试`" style="width: 680px; max-width: calc(100vw - 32px)" @update:show="(value) => !value && (testResult = null)">
    <NSpace v-if="testResult" vertical :size="14">
      <NDescriptions :column="4" bordered size="small">
        <NDescriptionsItem label="状态">{{ testResult.result.status || '-' }}</NDescriptionsItem>
        <NDescriptionsItem label="当前序号">{{ sourceSequence(testResult.target, testResult.result) ? `#${sourceSequence(testResult.target, testResult.result)}` : '-' }}</NDescriptionsItem>
        <NDescriptionsItem label="用户">{{ testResult.result.users ?? 0 }}</NDescriptionsItem>
        <NDescriptionsItem label="节点">{{ testResult.result.nodes ?? 0 }}</NDescriptionsItem>
      </NDescriptions>
      <NSteps vertical>
        <NStep
          v-for="stage in testResult.result.stages"
          :key="stage.name"
          :title="stage.name"
          :status="stage.status === 'succeeded' || stage.status === 'success' ? 'finish' : stage.status === 'failed' ? 'error' : 'process'"
          :description="`${stage.message || (stage.status === 'succeeded' || stage.status === 'success' ? '正常' : stage.status)}${stage.duration_ms !== undefined ? ` · ${stage.duration_ms}ms` : ''}`"
        />
      </NSteps>
    </NSpace>
  </NModal>
</template>
