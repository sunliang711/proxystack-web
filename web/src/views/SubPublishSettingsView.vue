<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  NAlert, NButton, NCard, NDataTable, NDescriptions, NDescriptionsItem, NDrawer, NDrawerContent, NDropdown, NForm, NFormItem,
  NIcon, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NStep, NSteps, NSwitch, NTag, NText, useDialog, useMessage,
  type DataTableColumns, type DropdownOption,
} from 'naive-ui'
import { ChevronDownOutline } from '@vicons/ionicons5'
import { api, MASKED, problemMessage, saveBlob } from '../api/client'
import CopyableCode from '../components/CopyableCode.vue'
import PublishFlowBar from '../components/PublishFlowBar.vue'
import { formatTime } from '../util/format'
import { hasTime, targetNextStep, testedAfterDeploy, type FlowStepKey, type PublishTargetState } from '../util/subPublish'

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
/** 订阅内容在上次签发后有变化。 */
const bundlePending = ref(false)
/** 首次加载完成后才显示进度条，避免先闪出第 1 步。 */
const loaded = ref(false)
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
  bundlePending.value = !!statusResult.data?.pending
  loaded.value = true
}
onMounted(load)

const flowBusy = computed(() => generating.value || inspecting.value !== null || deploying.value !== null || testing.value !== null)
/** 进度条上的主操作。 */
function onFlowAction(key: FlowStepKey, state: PublishTargetState | null) {
  const target = targets.value.find((candidate) => candidate.id === state?.id)
  if (key === 'identity') void generateIdentity()
  else if (key === 'target') open()
  else if (key === 'deploy' && target) {
    if (!target.deploy_state) void inspectDeploy(target)
    else confirmThen(`部署订阅端 ${target.name}`, '将下载最新稳定版并通过 SSH 部署订阅端，继续？', () => deploy(target))
  } else if (key === 'test' && target) void test(target)
}

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
const secretSetup = ref<ProvisioningResult | null>(null)
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
  const sharedSSHTarget = targets.value.some((candidate) => candidate.id !== target.id && candidate.ssh_host === target.ssh_host && candidate.ssh_port === target.ssh_port && candidate.ssh_user === target.ssh_user)
  const { error } = await api.DELETE('/api/sub-publish/targets/{id}', { params: { path: { id: target.id! } } })
  if (error) {
    message.error(problemMessage(error, '删除订阅端失败'))
    return
  }
  const filename = `proxystack-web-sub-cleanup-${target.name.replace(/[^A-Za-z0-9._-]+/g, '-') || 'target'}.md`
  saveBlob(new Blob([cleanupGuide(target, sharedSSHTarget)], { type: 'text/markdown;charset=utf-8' }), filename)
  message.success('订阅端已删除，清理说明已下载')
  await load()
}

async function rotateSecret(target: PublishTarget) {
  const { data, error } = await api.POST('/api/sub-publish/targets/{id}/rotate-secret', { params: { path: { id: target.id! } } })
  if (error || !data) {
    message.error(problemMessage(error, '轮换发布 Secret 失败'))
    return
  }
  secretSetup.value = provisioningResult(data)
  message.success('管理端已改用新 Secret，请立即更新订阅端')
  await load()
}

function viewSetup(target: PublishTarget) {
  provisioning.value = { target, secret: '' }
}

function cleanupGuide(target: PublishTarget, sharedSSHTarget: boolean) {
  const publicKeyEntry = authorizedKeys(target)
  const sshCleanup = sharedSSHTarget
    ? `检测到同一 SSH 主机、端口和用户仍被其他订阅端目标使用。请不要删除以下共享 SSH 配置：\n\n- /etc/ssh/sshd_config.d/proxystack-web-sub.conf\n- /etc/sudoers.d/proxystack-web-sub-deploy\n- SSH 用户 ${target.ssh_user} 的其他公钥条目\n\n确认没有其他目标使用该 SSH 用户后，再按下面步骤清理。`
    : `确认没有其他目标使用该 SSH 用户后执行：\n\n1. 从 ${sshAuthorizedKeysPath(target.ssh_user)} 删除下面这一整行：\n\n   ${publicKeyEntry}\n\n2. 删除 sudoers 配置并校验：\n\n   sudo rm -f /etc/sudoers.d/proxystack-web-sub-deploy\n   sudo visudo -c\n\n3. 删除 sshd 限制配置并 reload：\n\n   sudo rm -f /etc/ssh/sshd_config.d/proxystack-web-sub.conf\n   sudo sshd -t && sudo systemctl reload ssh\n\n如果系统服务名是 sshd，将最后的 ssh 替换为 sshd。`
  return `# proxystack-web 订阅端清理说明

生成时间：${new Date().toISOString()}

## 目标

- SSH 地址：${target.ssh_user}@${target.ssh_host}:${target.ssh_port}
- 订阅端 HTTP 地址：127.0.0.1:${target.remote_port}
- 来源名：${target.source}

本文件不包含发布 Secret 或 SSH 私钥。管理端订阅端配置已经删除；以下命令需要在订阅端主机上人工确认后执行。

## 一、卸载订阅服务

保留配置和订阅数据：

    sudo /opt/proxystack-web-sub/bin/proxystack-web sub --dir /opt/proxystack-web-sub deploy uninstall

确认同时删除配置、订阅数据和服务账号：

    sudo /opt/proxystack-web-sub/bin/proxystack-web sub --dir /opt/proxystack-web-sub deploy uninstall --purge

## 二、清理 SSH 配置

${sshCleanup}

## 三、可选：删除专用 SSH 用户

仅当 ${target.ssh_user} 是本次部署专用账号、没有其他用途且没有其他目标共用时执行：

    sudo userdel -r ${target.ssh_user}
`
}

const viewingSecret = ref<number | null>(null)
async function viewSecret(target: PublishTarget) {
  viewingSecret.value = target.id ?? null
  const { data, error } = await api.GET('/api/sub-publish/targets/{id}/setup', { params: { path: { id: target.id! } } })
  viewingSecret.value = null
  if (error || !data) {
    message.error(problemMessage(error, '读取发布 Secret 失败'))
    return
  }
  secretSetup.value = provisioningResult(data)
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
const shellQuote = (value: string) => `'${value.replace(/'/g, "'\"'\"'")}'`
const sshAuthorizedKeysPath = (sshUser: string) => `${sshUser === 'root' ? '/root' : `/home/${sshUser}`}/.ssh/authorized_keys`
const authorizedKeysSetupCommand = (target: PublishTarget, entry = authorizedKeys(target)) => {
  const path = sshAuthorizedKeysPath(target.ssh_user)
  const directory = path.slice(0, path.lastIndexOf('/'))
  const user = shellQuote(target.ssh_user)
  const quotedDirectory = shellQuote(directory)
  const quotedPath = shellQuote(path)
  const quotedEntry = shellQuote(entry)
  return `set -eu
sudo install -d -m 700 -o ${user} -g ${user} ${quotedDirectory}
sudo touch ${quotedPath}
sudo grep -qxF -- ${quotedEntry} ${quotedPath} || printf '%s\\n' ${quotedEntry} | sudo tee -a ${quotedPath} >/dev/null
sudo chown ${user}:${user} ${quotedPath}
sudo chmod 600 ${quotedPath}`
}
const tokenCommand = (target: PublishTarget) =>
  `sudo proxystack-web sub --dir /opt/proxystack-web-sub source set-publish-token ${target.source}`
const sshdConfig = (target: PublishTarget) => `Match User ${target.ssh_user}\n    AllowTcpForwarding local\n    PermitOpen 127.0.0.1:${target.remote_port}\n    PermitTTY no\n    X11Forwarding no\n    AllowAgentForwarding no\nMatch all`
const sshdConfigSetupCommand = (target: PublishTarget) =>
  `sudo install -d -m 755 /etc/ssh/sshd_config.d\nsudo tee /etc/ssh/sshd_config.d/proxystack-web-sub.conf >/dev/null <<'EOF'\n${sshdConfig(target)}\nEOF\nsudo sshd -t && sudo systemctl reload ssh`

function statusTime(value?: string) {
  return hasTime(value) ? formatTime(value) : '从未执行'
}

function sourceSequence(target: PublishTarget, result: TestResult) {
  return result.sources?.find((source) => source.name === target.source)?.sequence
}

/** 接入流程的下一步，对应的按钮用主色突出；全部完成时返回 null。 */
const nextStep = (row: PublishTarget) => targetNextStep(row)

const dialog = useDialog()
function moreOptions(row: PublishTarget): DropdownOption[] {
  return [
    { key: 'rollback', label: row.previous_version ? `回滚到 ${row.previous_version}` : '回滚（没有可回滚的版本）', disabled: !row.previous_version },
    { key: 'view-secret', label: '查看 Secret' },
    { key: 'rotate-secret', label: '轮换 Secret' },
    { type: 'divider', key: 'd1' },
    { key: 'setup', label: '完整配置' },
    { key: 'instructions', label: '配置说明' },
    { type: 'divider', key: 'd2' },
    { key: 'delete', label: '删除订阅端', props: { style: 'color: var(--ps-danger)' } },
  ]
}
function confirmThen(title: string, content: string, run: () => unknown, danger = false) {
  dialog[danger ? 'error' : 'warning']({ title, content, positiveText: '继续', negativeText: '取消', onPositiveClick: () => { void run() } })
}
function onMore(row: PublishTarget, key: string) {
  if (key === 'rollback') confirmThen('回滚订阅端', `将订阅端回滚到 ${row.previous_version || '上一版本'}，继续？`, () => rollbackDeploy(row))
  else if (key === 'view-secret') confirmThen('查看发布 Secret', '将显示明文发布 Secret，查看操作会记录审计日志。继续？', () => viewSecret(row))
  else if (key === 'rotate-secret') confirmThen('轮换发布 Secret', '管理端将立即改用新 Secret；订阅端更新前在线发布会失败，更新后旧 Secret 才会失效。继续？', () => rotateSecret(row))
  else if (key === 'setup') viewSetup(row)
  else if (key === 'instructions') instructions.value = row
  else if (key === 'delete') confirmThen(`删除订阅端 ${row.name}？`, '只删除管理端保存的这个订阅端配置，不会卸载远端已部署的订阅端；删除后会下载一份远端清理说明。', () => remove(row), true)
}

function workflowState(row: PublishTarget) {
  if (!row.deploy_state) return { label: '第 1 步：检测部署', type: 'warning' as const, detail: '先确认 SSH、系统和 sudo' }
  if (row.deploy_state !== 'installed') return { label: '第 2 步：部署', type: 'warning' as const, detail: `当前状态：${row.deploy_state}` }
  if (!testedAfterDeploy(row)) return { label: '第 3 步：测试', type: 'warning' as const, detail: '部署完成后验证转发和 Secret' }
  return { label: '第 4 步：配置发布 Secret（已自动完成）', type: 'success' as const, detail: 'SSH 部署已配置 Secret，下一步：第 5 步签发订阅包' }
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
        : hasTime(row.last_test_at) ? h(NTag, { size: 'small', type: 'success' }, () => '通过') : null,
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
    title: '操作', key: 'actions', width: 380, fixed: 'right',
    render: (row) => {
      const next = nextStep(row)
      return h(NSpace, { size: 4, wrap: false }, () => [
        h(NButton, { size: 'small', type: next === 'inspect' ? 'primary' : 'default', loading: inspecting.value === row.id, onClick: () => inspectDeploy(row) }, () => '检测部署'),
        h(NPopconfirm, { onPositiveClick: () => deploy(row) }, {
          trigger: () => h(NButton, { size: 'small', type: next === 'deploy' ? 'primary' : 'default', loading: deploying.value === row.id }, () => row.deploy_state === 'installed' ? '更新' : '部署'),
          default: () => row.deploy_state === 'installed' ? '将检查最新稳定版：有新版本时通过 SSH 更新订阅端，已是最新时只同步发布 Secret。继续？' : '将下载最新稳定版并通过 SSH 部署订阅端，继续？',
        }),
        h(NButton, { size: 'small', type: next === 'test' ? 'primary' : 'default', disabled: row.deploy_state !== 'installed', loading: testing.value === row.id, onClick: () => test(row), title: row.deploy_state === 'installed' ? '' : '先完成订阅端部署' }, () => '测试'),
        h(NButton, { size: 'small', onClick: () => open(row) }, () => '编辑'),
        h(NDropdown, { trigger: 'click', placement: 'bottom-end', options: moreOptions(row), onSelect: (key: string) => onMore(row, key) }, () =>
          h(NButton, { size: 'small', iconPlacement: 'right', loading: rollingBack.value === row.id || viewingSecret.value === row.id }, {
            default: () => '更多',
            icon: () => h(NIcon, null, () => h(ChevronDownOutline)),
          })),
      ])
    },
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
    <PublishFlowBar v-if="loaded" :input="{ identity: identity?.configured ?? null, targets, bundlePending }" page="settings" :loading="flowBusy" @action="onFlowAction" />
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


    <NCard title="订阅端目标" class="table-panel">
      <template #header-extra>
        <NButton type="primary" :disabled="!identity?.configured" @click="open()">添加订阅端</NButton>
      </template>
      <NAlert type="info" :bordered="false" style="margin-bottom: 12px">
        管理端按需建立 SSH 正向转发，远端主机固定为 127.0.0.1。每个订阅端使用独立发布 Secret，测试会依次检查 SSH、转发、订阅端健康状态和发布鉴权。
        每个订阅端依次经过 1 检测部署 → 2 部署 → 3 测试 → 4 配置发布 Secret → 5 签发订阅包。通过 SSH 安装或更新时，第 4 步由管理端在部署完成后自动执行；离线安装时，先安装二进制，再从“查看 Secret”弹窗查看并执行命令。只有部署完成且部署后的测试通过，目标才可在“发布订阅包”页面选择并签发。
      </NAlert>
      <NDataTable :loading="loading" :columns="columns" :data="targets" :scroll-x="1430" size="small" />
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

  <NModal :show="!!provisioning" preset="card" title="订阅端完整配置（不含 Secret）" style="width: 820px; max-width: calc(100vw - 32px)" @update:show="(value) => !value && (provisioning = null)">
    <NSpace v-if="provisioning" vertical :size="16">
      <NAlert type="info" :bordered="false">
        此窗口只包含 SSH、公钥、转发和 sudo 配置，不显示发布 Secret。发布 Secret 请从目标列表单独点击“查看 Secret”。
      </NAlert>
      <NSteps vertical :current="1" status="process">
        <NStep title="安装管理端 SSH 公钥">
          <CopyableCode label="幂等安装公钥" :value="authorizedKeysSetupCommand(provisioning.target, provisioning.authorized_keys_entry || authorizedKeys(provisioning.target))" />
          <NText depth="3">命令会把公钥追加到 <NText code>{{ sshAuthorizedKeysPath(provisioning.target.ssh_user) }}</NText>；重复执行不会重复追加，目录权限为 700，文件权限为 600。</NText>
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
        <NStep title="核对 Host Key 并测试">
          <NText>在订阅端本机执行 <NText code>ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256</NText>，通过独立渠道核对配置的指纹，再回到目标列表点击“测试”。其他 Host Key 算法请改用对应的公钥文件。</NText>
        </NStep>
      </NSteps>
    </NSpace>
  </NModal>

  <NModal :show="!!secretSetup" preset="card" title="订阅端发布 Secret" style="width: 680px; max-width: calc(100vw - 32px)" @update:show="(value) => !value && (secretSetup = null)">
    <NSpace v-if="secretSetup" vertical :size="14">
      <NAlert type="warning" :bordered="false">
        此窗口包含明文发布 Secret。关闭后页面会清除本次显示内容；每次查看都会记录审计日志。
      </NAlert>
      <CopyableCode label="发布 Secret" :value="secretSetup.secret" />
      <NText depth="3">首次通过 SSH 安装或更新时，管理端会在部署完成后自动配置此 Secret；离线安装或手工重置时，先完成订阅端安装，再执行下面的命令并按提示粘贴 Secret。</NText>
      <CopyableCode label="安装完成后配置发布 Secret" :value="secretSetup.publish_token_command || tokenCommand(secretSetup.target)" />
    </NSpace>
  </NModal>

  <NModal :show="!!instructions" preset="card" title="订阅端配置说明" style="width: 820px; max-width: calc(100vw - 32px)" @update:show="(value) => !value && (instructions = null)">
    <NSpace v-if="instructions" vertical :size="14">
      <CopyableCode label="幂等安装公钥" :value="authorizedKeysSetupCommand(instructions)" />
      <CopyableCode label="写入 /etc/ssh/sshd_config.d 并 reload" :value="sshdConfigSetupCommand(instructions)" />
      <CopyableCode label="sshd_config 限制" :value="sshdConfig(instructions)" />
      <CopyableCode label="sudo -n 配置" :value="sudoSetupCommand(instructions.ssh_user)" />
      <NAlert type="info" :bordered="false">此说明不包含明文发布 Secret；需要查看或重新配置时，请从目标列表单独点击“查看 Secret”。</NAlert>
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
