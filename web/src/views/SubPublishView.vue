<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  NAlert, NButton, NCard, NCheckbox, NCheckboxGroup, NDescriptions, NDescriptionsItem, NPopconfirm, NRadioButton,
  NRadioGroup, NSpace, NTag, NText, useMessage,
} from 'naive-ui'
import { api, problemMessage, saveBlob, type Schemas } from '../api/client'
import CopyableCode from '../components/CopyableCode.vue'
import { formatTime } from '../util/format'

interface PublishTarget {
  id: number
  name: string
  ssh_host: string
  ssh_port: number
  source: string
  enabled: boolean
  deploy_state?: string
  last_deploy_at?: string
  last_test_at?: string
  last_test_error?: string
  last_sequence?: number
}

interface OnlinePublishResult {
  task_id: number
  sequence: number
}

const router = useRouter()
const message = useMessage()
const status = ref<Schemas['BundleStatus'] | null>(null)
const targets = ref<PublishTarget[]>([])
const selected = ref<number[]>([])
const mode = ref<'online' | 'offline'>('online')
const busy = ref(false)
const submitted = ref<OnlinePublishResult | null>(null)

async function load() {
  const [statusResult, targetsResult] = await Promise.all([
    api.GET('/api/sub-bundle'),
    api.GET('/api/sub-publish/targets'),
  ])
  if (statusResult.error) message.error(problemMessage(statusResult.error))
  if (targetsResult.error) message.error(problemMessage(targetsResult.error, '读取订阅端失败'))
  status.value = statusResult.data ?? null
  targets.value = (targetsResult.data ?? []).filter((target: PublishTarget) => target.enabled)
  selected.value = selected.value.filter((id) => targets.value.some((target) => target.id === id && targetReady(target)))
}
onMounted(load)

async function publishOffline() {
  const { data, error, response } = await api.POST('/api/sub-bundle/publish', { parseAs: 'blob' })
  if (error || !data) {
    message.error(problemMessage(error, '签发失败'))
    return
  }
  const disposition = response.headers.get('Content-Disposition') ?? ''
  saveBlob(data as Blob, /filename="([^"]+)"/.exec(disposition)?.[1] ?? 'subscription.bundle')
  message.success('已签发并下载，拷贝到订阅端导入后生效')
  await load()
}

async function publishOnline() {
  if (!selected.value.length) {
    message.warning('请至少选择一个订阅端')
    return
  }
  const { data, error } = await api.POST('/api/sub-bundle/publish-online', { body: { target_ids: selected.value } })
  if (error || !data) {
    message.error(problemMessage(error, '提交在线发布失败'))
    return
  }
  submitted.value = data
  message.success(`订阅包 #${data.sequence} 已提交到后台任务`)
  await load()
}

async function publish() {
  busy.value = true
  try {
    if (mode.value === 'online') await publishOnline()
    else await publishOffline()
  } finally {
    busy.value = false
  }
}

const installCommand = computed(() =>
  status.value
    ? `set -e
case "$(uname -m)" in x86_64|amd64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) echo "unsupported architecture" >&2; exit 1 ;; esac
BASE=https://github.com/sunliang711/proxystack-web/releases/latest/download
FILE=proxystack-web-linux-$ARCH
curl -fLO "$BASE/$FILE"
curl -fLO "$BASE/$FILE.sha256"
sha256sum -c "$FILE.sha256"
chmod 0700 "$FILE"
sudo ./$FILE sub --dir /opt/proxystack-web-sub deploy apply --sha256 "$(cut -d " " -f1 "$FILE.sha256")" --source '${status.value.source}' --public-key '${status.value.public_key}'`
    : '',
)
const rollbackCommand = 'sudo /opt/proxystack-web-sub/bin/proxystack-web sub --dir /opt/proxystack-web-sub deploy rollback'
const uninstallCommand = 'sudo /opt/proxystack-web-sub/bin/proxystack-web sub --dir /opt/proxystack-web-sub deploy uninstall'
const addSourceCommand = computed(() =>
  status.value ? `proxystack-web sub source add ${status.value.source} ${status.value.public_key}` : '',
)
/** 构建时跳过的节点数；跳过的节点不进入订阅包，其余内容照常签发。 */
const skipped = computed(() => status.value?.build_warnings.length ?? 0)
const actionLabel = computed(() => mode.value === 'online' ? '签发并在线发布' : '签发并下载')
/** 签发按钮不可用的原因；可以签发时为空。 */
const blockedReason = computed(() => {
  if (!status.value) return '正在读取订阅包状态'
  if (mode.value !== 'online') return ''
  if (!targets.value.length) return '还没有启用的订阅端，请先在「SSH 发布与部署」中添加'
  if (!targets.value.some(targetReady)) return '还没有部署并测试通过的订阅端'
  if (!selected.value.length) return '请先在上方勾选要发布的订阅端'
  return ''
})

function targetSummary(target: PublishTarget) {
  const address = `${target.ssh_host}:${target.ssh_port}`
  if (target.last_test_error) return `${address} · 最近测试失败`
  if (target.last_sequence) return `${address} · 当前 #${target.last_sequence}`
  return `${address} · 尚未发布`
}

function targetReady(target: PublishTarget) {
  if (target.deploy_state !== 'installed' || !!target.last_test_error || !target.last_test_at) return false
  return !target.last_deploy_at || Date.parse(target.last_test_at) >= Date.parse(target.last_deploy_at)
}

function targetNextStep(target: PublishTarget) {
  if (target.deploy_state !== 'installed') return target.deploy_state ? '先完成部署' : '先检测部署'
  if (!targetReady(target)) return '先完成部署后的测试'
  return '可签发'
}

function openTask() {
  if (!submitted.value) return
  void router.push({ name: 'task-detail', params: { source: 'task', id: submitted.value.task_id } })
}

function openSSHSettings() {
  void router.push({ name: 'sub-publish-settings' })
}
</script>

<template>
  <NSpace vertical :size="20">
    <NCard title="订阅包状态">
      <NSpace v-if="status" vertical>
        <NAlert v-if="status.sequence === 0" type="info" :bordered="false">还没有发布过订阅包。</NAlert>
        <NAlert v-else-if="status.pending" type="warning" :bordered="false">
          订阅内容在上次签发（#{{ status.sequence }}）之后有变化。暂停用户已经在代理端生效，订阅中的节点需要重新发布后才会去掉。
        </NAlert>
        <NAlert v-else type="success" :bordered="false">当前内容已签发为 #{{ status.sequence }}；各订阅端是否已导入以目标状态或发布任务结果为准。</NAlert>
        <NAlert v-if="skipped" type="warning" :bordered="false" title="部分内容被跳过">
          下面 {{ skipped }} 项在构建订阅时被跳过，不会进入签发的订阅包，其余节点照常签发。修复对应的配置后重新加载本页即可恢复。
        </NAlert>
        <NAlert v-for="warning in status.build_warnings" :key="warning" type="warning" :bordered="false">{{ warning }}</NAlert>
        <NDescriptions :column="1" label-placement="left" bordered size="small">
          <NDescriptionsItem label="来源名">{{ status.source }}</NDescriptionsItem>
          <NDescriptionsItem label="最近一次签发">
            <template v-if="status.sequence">#{{ status.sequence }}，{{ formatTime(status.published_at) }}，{{ status.published_by }}</template>
            <NText v-else depth="3">无</NText>
          </NDescriptionsItem>
          <NDescriptionsItem label="当前内容">{{ status.users }} 个用户，{{ status.nodes }} 个节点（不含停用用户的节点）</NDescriptionsItem>
          <NDescriptionsItem label="订阅端对外地址">
            <NText v-if="status.public_base_url">{{ status.public_base_url }}</NText>
            <NText v-else depth="3">未设置（系统设置 → 订阅）</NText>
          </NDescriptionsItem>
          <NDescriptionsItem label="公钥指纹"><NTag size="small">{{ status.fingerprint }}</NTag></NDescriptionsItem>
        </NDescriptions>
        <NText depth="3">订阅包含有全部用户的订阅 token 和节点凭据，签发与在线投递均记入审计日志。</NText>
      </NSpace>
    </NCard>

    <NCard title="发布方式">
      <NSpace vertical :size="16">
        <NRadioGroup v-model:value="mode" size="small">
          <NRadioButton value="online">SSH 在线发布</NRadioButton>
          <NRadioButton value="offline">离线签发下载</NRadioButton>
        </NRadioGroup>

        <template v-if="mode === 'online'">
          <NSpace justify="end">
            <NButton text type="primary" @click="openSSHSettings">SSH 发布与部署设置</NButton>
          </NSpace>
          <NAlert v-if="!targets.length" type="warning" :bordered="false">
            还没有启用的订阅端目标。请先到“SSH 发布与部署设置”生成 SSH 密钥并配置订阅端。
          </NAlert>
          <NAlert v-else type="info" :bordered="false">
            在线签发流程：检测部署 → 部署 → 测试 → 配置发布 Secret → 签发订阅包。SSH 部署会自动配置 Secret；只有部署完成且测试通过的目标可以选择。
          </NAlert>
          <NCheckboxGroup v-if="targets.length" v-model:value="selected">
            <NSpace vertical>
              <NCheckbox v-for="target in targets" :key="target.id" :value="target.id" :disabled="!targetReady(target)">
                <NSpace :size="8" align="center">
                  <NText strong>{{ target.name }}</NText>
                  <NText depth="3">{{ targetSummary(target) }}</NText>
                  <NTag size="small" :type="targetReady(target) ? 'success' : 'warning'">{{ targetNextStep(target) }}</NTag>
                </NSpace>
              </NCheckbox>
            </NSpace>
          </NCheckboxGroup>
          <NAlert type="info" :bordered="false">
            本次只签发一份订阅包，并向所有选中目标发送相同内容。部分目标失败时，已经成功的目标不会回滚；再次发布会生成新的序号。
          </NAlert>
          <NAlert v-if="submitted" type="success" :bordered="false">
            订阅包 #{{ submitted.sequence }} 已提交，任务编号 #{{ submitted.task_id }}。
            <NButton text type="primary" @click="openTask">查看发布详情</NButton>
          </NAlert>
        </template>

        <template v-else-if="status">
          <NAlert type="info" :bordered="false">
            离线模式会下载签名订阅包。通过可信方式把文件拷贝到订阅端，执行导入后删除传输副本。
          </NAlert>
          <CopyableCode label="订阅包验签公钥" :value="status.public_key" />
          <CopyableCode label="安装或更新订阅端" :value="installCommand" />
          <CopyableCode label="回滚到上一版本" :value="rollbackCommand" />
          <CopyableCode label="卸载并保留配置与数据" :value="uninstallCommand" />
          <CopyableCode label="已有订阅端添加本机为来源" :value="addSourceCommand" />
          <NText depth="3">
            执行 <NText code>proxystack-web sub import 文件</NText>，或把文件放进订阅端目录下的 <NText code>inbox/</NText>；
            <NText code>proxystack-web sub status</NText> 查看各来源已导入的序号。
          </NText>
        </template>

        <!-- 选好发布方式和目标后再签发，按钮不可用时就地说明原因。 -->
        <NSpace align="center" class="publish-actions">
          <NPopconfirm v-if="skipped" @positive-click="publish">
            <template #trigger>
              <NButton type="primary" :loading="busy" :disabled="!!blockedReason">{{ actionLabel }}</NButton>
            </template>
            有 {{ skipped }} 项内容在构建时被跳过，签发的订阅包里不包含它们。确定继续？
          </NPopconfirm>
          <NButton v-else type="primary" :loading="busy" :disabled="!!blockedReason" @click="publish">{{ actionLabel }}</NButton>
          <NText v-if="blockedReason" depth="3">{{ blockedReason }}</NText>
          <NText v-else-if="mode === 'online'" depth="3">将向选中的 {{ selected.length }} 个订阅端发布</NText>
        </NSpace>
      </NSpace>
    </NCard>
  </NSpace>
</template>

<style scoped>
.publish-actions { padding-top: 4px; }
</style>
