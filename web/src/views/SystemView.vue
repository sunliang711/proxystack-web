<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NDescriptions, NDescriptionsItem, NForm, NFormItem, NInputNumber, NPopconfirm, NSelect, NSpace, NSpin, NTag, NText,
} from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import TaskDialog from '../components/TaskDialog.vue'

const info = ref<Schemas['SystemInfo'] | null>(null)

const listen = ref({ listen: '', port: 0 })
const listenResult = ref<{ type: 'success' | 'warning' | 'error'; text: string } | null>(null)
const listenOptions = ['0.0.0.0', '127.0.0.1', '::'].map((value) => ({ label: value, value }))

onMounted(async () => {
  const { data } = await api.GET('/api/system/info')
  info.value = data ?? null
  if (data) listen.value = { listen: data.listen, port: data.port }
})

async function saveListen() {
  const { data, error } = await api.PUT('/api/system/panel', { body: listen.value })
  if (error) {
    listenResult.value = { type: 'error', text: error.errors?.[0]?.message ?? problemMessage(error, '保存失败') }
    return
  }
  listenResult.value = data?.restarting
    ? { type: 'success', text: `已保存，管理服务正在重启。端口变了的话，用 ${listen.value.port} 端口重新打开，nginx 的 proxy_pass 也要同步修改。` }
    : { type: 'warning', text: '已保存，当前模式不能自动重启，手工重启管理服务后生效。' }
}

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
      </NDescriptions>
    </NSpin>
  </NCard>
  <NCard title="面板监听" style="margin-top: 12px">
    <NSpace vertical>
      <NText depth="3">修改后管理服务会重启，xray 和 mihomo 不受影响。改错连不上时，在本机执行 proxystack-web admin set-listen 恢复。</NText>
      <NForm label-placement="left" label-width="90" style="max-width: 420px">
        <NFormItem label="监听地址"><NSelect v-model:value="listen.listen" :options="listenOptions" filterable tag /></NFormItem>
        <NFormItem label="端口"><NInputNumber v-model:value="listen.port" :min="1" :max="65535" :show-button="false" /></NFormItem>
      </NForm>
      <NPopconfirm @positive-click="saveListen">
        <template #trigger><NButton :disabled="!listen.listen || !listen.port">保存并重启</NButton></template>
        重启管理服务，页面会短暂断开。继续？
      </NPopconfirm>
      <NAlert v-if="listenResult" :type="listenResult.type" :bordered="false">{{ listenResult.text }}</NAlert>
    </NSpace>
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
