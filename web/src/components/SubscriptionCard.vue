<script setup lang="ts">
import { ref, watch } from 'vue'
import { NAlert, NButton, NCard, NFormItem, NInputGroup, NInput, NModal, NQrCode, NRadioButton, NRadioGroup, NSpace, NText, useMessage } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import CodeEditor from './CodeEditor.vue'

const props = defineProps<{ userId: number }>()
const message = useMessage()
const links = ref<Schemas['SubscriptionLinks'] | null>(null)
const qr = ref<string | null>(null)
const format = ref<'clash' | 'premium' | 'surge'>('clash')
const preview = ref('')
const previewError = ref('')
const labels = { clash: 'Clash', premium: 'Premium Clash', surge: 'Surge' } as const

async function showLinks() {
  const { data, error } = await api.GET('/api/users/{id}/subscription', { params: { path: { id: props.userId } } })
  if (error) {
    message.error(problemMessage(error))
    return
  }
  links.value = data ?? null
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    message.success('已复制')
  } catch {
    message.error('复制失败：浏览器不允许访问剪贴板')
  }
}

async function loadPreview() {
  const { data, error } = await api.GET('/api/users/{id}/subscription/preview', {
    params: { path: { id: props.userId }, query: { format: format.value } },
  })
  // 有字段说明时直接显示说明（例如用户为什么没有节点），否则显示通用错误。
  previewError.value = error ? (error.errors?.map((e) => e.message).join('；') || problemMessage(error)) : ''
  preview.value = data?.text ?? ''
}
watch([format, () => props.userId], loadPreview, { immediate: true })
</script>

<template>
  <NCard title="订阅" size="small" style="margin-bottom: 12px">
    <NSpace vertical style="width: 100%">
      <NText depth="3">订阅只包含已应用的 stack 节点、该用户的手工节点和远端节点源同步来的节点；改动在下一次发布订阅包并导入订阅端后生效。</NText>
      <NButton v-if="!links" size="small" @click="showLinks">显示订阅链接</NButton>
      <template v-else>
        <NText>当前 {{ links.nodes }} 个节点</NText>
        <NAlert v-if="!links.base_url" type="warning" :bordered="false">未设置订阅端对外地址（系统设置 → 订阅），下面只显示链接路径，使用时在前面加上订阅端地址。</NAlert>
        <NFormItem v-for="(label, key) in labels" :key="key" :label="label" label-placement="left" label-width="110" :show-feedback="false">
          <NInputGroup>
            <NInput :value="links.links[key]" readonly />
            <NButton @click="copy(links.links[key])">复制</NButton>
            <NButton @click="qr = links.links[key]">二维码</NButton>
          </NInputGroup>
        </NFormItem>
      </template>
      <NRadioGroup v-model:value="format" size="small">
        <NRadioButton v-for="(label, key) in labels" :key="key" :value="key">预览 {{ label }}</NRadioButton>
      </NRadioGroup>
      <NAlert v-if="previewError" type="warning" :bordered="false">{{ previewError }}</NAlert>
      <CodeEditor v-else :value="preview" :language="format === 'surge' ? 'text' : 'yaml'" readonly min-height="240px" />
    </NSpace>
  </NCard>
  <NModal :show="!!qr" preset="card" title="订阅二维码" style="max-width: 360px" @update:show="(v) => !v && (qr = null)">
    <NSpace justify="center"><NQrCode :value="qr ?? ''" :size="260" /></NSpace>
  </NModal>
</template>
