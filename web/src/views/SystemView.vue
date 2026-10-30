<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NDescriptions, NDescriptionsItem, NPopconfirm, NSpace, NSpin, NTag, NText } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import TaskDialog from '../components/TaskDialog.vue'

const info = ref<Schemas['SystemInfo'] | null>(null)

onMounted(async () => {
  const { data } = await api.GET('/api/system/info')
  info.value = data ?? null
})

const update = ref<Schemas['SelfVersion'] | null>(null)
const updateError = ref('')
const checking = ref(false)
const taskId = ref<number | null>(null)

async function checkUpdate() {
  checking.value = true
  updateError.value = ''
  const { data, error } = await api.GET('/api/system/update')
  checking.value = false
  if (error) {
    updateError.value = problemMessage(error, '检查失败') + (error.errors?.map((e) => `：${e.message}`).join('') ?? '')
    return
  }
  update.value = data ?? null
}

async function runUpdate() {
  const { data, error } = await api.POST('/api/system/update')
  if (error) {
    updateError.value = problemMessage(error, '更新失败')
    return
  }
  taskId.value = data!.task_id
}

const tlsLabels: Record<string, string> = {
  'self-signed': '自签证书',
  acme: 'ACME',
  proxy: '反向代理',
}
</script>

<template>
  <NCard title="系统信息">
    <NSpin :show="!info">
      <NDescriptions v-if="info" label-placement="left" :column="1" bordered>
        <NDescriptionsItem label="版本">
          {{ info.version }}
          <NTag v-if="info.dev_mode" size="small" type="warning" style="margin-left: 8px">开发模式</NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="提交">{{ info.commit }}</NDescriptionsItem>
        <NDescriptionsItem label="构建时间">{{ info.build_date }}</NDescriptionsItem>
        <NDescriptionsItem label="运行环境">{{ info.go_version }} · {{ info.platform }}</NDescriptionsItem>
        <NDescriptionsItem label="面板地址">{{ info.panel_url }}</NDescriptionsItem>
        <NDescriptionsItem label="TLS">{{ tlsLabels[info.tls_mode] ?? info.tls_mode }}</NDescriptionsItem>
        <NDescriptionsItem v-if="info.fingerprint" label="证书指纹">
          <NText code style="word-break: break-all">{{ info.fingerprint }}</NText>
        </NDescriptionsItem>
      </NDescriptions>
    </NSpin>
  </NCard>
  <NCard title="管理服务更新" style="margin-top: 12px">
    <NSpace vertical>
      <NText depth="3">更新只替换管理服务本身；xray 和 mihomo 由独立的服务承载，代理流量不受影响。下载走系统设置中的下载代理。</NText>
      <NSpace>
        <NButton :loading="checking" @click="checkUpdate">检查新版本</NButton>
        <NPopconfirm v-if="update?.update_available" @positive-click="runUpdate">
          <template #trigger><NButton type="primary">更新到 {{ update.latest }}</NButton></template>
          下载并校验新版本后替换并重启管理服务，页面会短暂断开。继续？
        </NPopconfirm>
      </NSpace>
      <NAlert v-if="update && !update.update_available" type="success" :bordered="false">当前已是最新版本（{{ update.current }}）</NAlert>
      <NAlert v-if="update?.update_available && update.notes" type="info" :title="`${update.latest} 更新说明`" :bordered="false">
        <pre style="white-space: pre-wrap; font-family: inherit; margin: 0">{{ update.notes }}</pre>
      </NAlert>
      <NAlert v-if="updateError" type="error" :bordered="false">{{ updateError }}</NAlert>
    </NSpace>
  </NCard>
  <TaskDialog :task-id="taskId" @close="taskId = null" />
</template>
