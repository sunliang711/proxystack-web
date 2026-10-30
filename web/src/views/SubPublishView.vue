<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NDescriptions, NDescriptionsItem, NInputGroup, NInput, NSpace, NTag, NText, useMessage } from 'naive-ui'
import { api, problemMessage, saveBlob, type Schemas } from '../api/client'
import { formatTime } from '../util/format'

const message = useMessage()
const status = ref<Schemas['BundleStatus'] | null>(null)
const busy = ref(false)

async function load() {
  const { data, error } = await api.GET('/api/sub-bundle')
  if (error) message.error(problemMessage(error))
  status.value = data ?? null
}
onMounted(load)

async function publish() {
  busy.value = true
  const { data, error, response } = await api.POST('/api/sub-bundle/publish', { parseAs: 'blob' })
  busy.value = false
  if (error || !data) {
    message.error(problemMessage(error, '签发失败'))
    return
  }
  const disposition = response.headers.get('Content-Disposition') ?? ''
  saveBlob(data as Blob, /filename="([^"]+)"/.exec(disposition)?.[1] ?? 'subscription.bundle')
  message.success('已签发并下载，拷贝到订阅端导入后生效')
  load()
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    message.success('已复制')
  } catch {
    message.error('复制失败：浏览器不允许访问剪贴板')
  }
}

const installCommand = computed(() =>
  status.value ? `bash install.sh --role sub --source ${status.value.source} --public-key ${status.value.public_key}` : '',
)
const addSourceCommand = computed(() =>
  status.value ? `proxystack-web sub source add ${status.value.source} ${status.value.public_key}` : '',
)
</script>

<template>
  <NSpace vertical :size="12">
    <NCard title="发布订阅包">
      <template #header-extra>
        <NButton type="primary" :loading="busy" :disabled="!status" @click="publish">签发并下载</NButton>
      </template>
      <NSpace v-if="status" vertical>
        <NAlert v-if="status.sequence === 0" type="info" :bordered="false">还没有发布过订阅包。</NAlert>
        <NAlert v-else-if="status.pending" type="warning" :bordered="false">
          订阅内容在上次发布（#{{ status.sequence }}）之后有变化，订阅端仍在提供旧内容。暂停用户已经在代理端生效，订阅中的节点要等下次发布导入后才会去掉。
        </NAlert>
        <NAlert v-else type="success" :bordered="false">订阅端导入 #{{ status.sequence }} 后即为最新内容。</NAlert>
        <NAlert v-for="warning in status.build_warnings" :key="warning" type="warning" :bordered="false">{{ warning }}</NAlert>
        <NDescriptions :column="1" label-placement="left" bordered size="small">
          <NDescriptionsItem label="来源名">{{ status.source }}</NDescriptionsItem>
          <NDescriptionsItem label="最近一次发布">
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
        <NText depth="3">订阅包含有全部用户的订阅 token 和节点凭据，签发记入审计日志；请通过可信的方式拷贝，导入后删除。</NText>
      </NSpace>
    </NCard>

    <NCard v-if="status" title="订阅端操作" size="small">
      <NSpace vertical>
        <NText>订阅端与管理端不需要网络连通，订阅包通过手工拷贝送达。订阅端用下面的公钥验签，包被修改过或不是本机签发的都会被拒收，序号不大于已导入序号的旧包也会被拒收。</NText>
        <NText strong>公钥</NText>
        <NInputGroup>
          <NInput :value="status.public_key" readonly />
          <NButton @click="copy(status.public_key)">复制</NButton>
        </NInputGroup>
        <NText strong>安装订阅端（在订阅机上执行）</NText>
        <NInputGroup>
          <NInput :value="installCommand" readonly />
          <NButton @click="copy(installCommand)">复制</NButton>
        </NInputGroup>
        <NText strong>已有订阅端添加本机为来源</NText>
        <NText depth="3">作为主来源时追加 --primary；主来源提供用户、token、模板和设置，其他来源只提供节点，按用户名合并。</NText>
        <NInputGroup>
          <NInput :value="addSourceCommand" readonly />
          <NButton @click="copy(addSourceCommand)">复制</NButton>
        </NInputGroup>
        <NText strong>导入订阅包</NText>
        <NText depth="3">
          执行 <NText code>proxystack-web sub import 文件</NText>，或把文件放进订阅端目录下的 <NText code>inbox/</NText>，几秒内自动导入；被拒收的文件移到 <NText code>inbox/rejected/</NText> 并附带原因。
          <NText code>proxystack-web sub status</NText> 查看各来源已导入的序号。
        </NText>
      </NSpace>
    </NCard>
  </NSpace>
</template>
