<script setup lang="ts">
import { ref, watch } from 'vue'
import { NAlert, NButton, NCard, NCollapse, NCollapseItem, NFormItem, NInputGroup, NInput, NModal, NQrCode, NRadioButton, NRadioGroup, NSpace, NSpin, NText, useMessage } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import CodeEditor from './CodeEditor.vue'
import { copyText } from '../util/clipboard'

/** linksOnly 只显示链接；bare 去掉卡片的标题、边框和内边距，用于嵌入已有分区。 */
const props = defineProps<{ userId: number; linksOnly?: boolean; bare?: boolean }>()
const message = useMessage()
const links = ref<Schemas['SubscriptionLinks'] | null>(null)
const qr = ref<string | null>(null)
const linksLoading = ref(false)
const expanded = ref<string[]>([])
const previewLoading = ref(false)
const format = ref<'clash' | 'premium' | 'surge'>('clash')
const preview = ref('')
const previewError = ref('')
const labels = { clash: 'Clash', premium: 'Premium Clash', surge: 'Surge' } as const

async function showLinks() {
  const userId = props.userId
  linksLoading.value = true
  try {
    const { data, error } = await api.GET('/api/users/{id}/subscription', { params: { path: { id: userId } } })
    if (userId !== props.userId) return
    if (error) {
      message.error(problemMessage(error))
      return
    }
    links.value = data ?? null
  } catch {
    if (userId === props.userId) message.error('订阅链接加载失败，请重试')
  } finally {
    if (userId === props.userId) linksLoading.value = false
  }
}

async function copy(text: string) {
  try {
    await copyText(text)
    message.success('已复制')
  } catch {
    message.error('复制失败：浏览器不允许访问剪贴板')
  }
}

async function loadPreview(signal: AbortSignal) {
  previewLoading.value = true
  previewError.value = ''
  preview.value = ''
  try {
    const { data, error } = await api.GET('/api/users/{id}/subscription/preview', {
      params: { path: { id: props.userId }, query: { format: format.value } }, signal,
    })
    if (signal.aborted) return
    // 有字段说明时直接显示说明（例如用户为什么没有节点），否则显示通用错误。
    previewError.value = error ? (error.errors?.map((e) => e.message).join('；') || problemMessage(error)) : ''
    preview.value = data?.text ?? ''
  } catch {
    if (!signal.aborted) previewError.value = '订阅预览加载失败，请收起后重新展开'
  } finally {
    if (!signal.aborted) previewLoading.value = false
  }
}
watch(() => props.userId, () => {
  links.value = null
  qr.value = null
  expanded.value = []
  if (props.linksOnly) showLinks()
}, { immediate: true })
watch([format, () => props.userId, expanded], (_, __, onCleanup) => {
  if (props.linksOnly || !expanded.value.includes('preview')) return
  // 默认折叠且不请求预览；收起或切换格式时取消旧请求，避免覆盖新结果。
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  loadPreview(controller.signal)
})
</script>

<template>
  <NCard :title="linksOnly || bare ? undefined : '订阅'" size="small" :bordered="!linksOnly && !bare" :content-style="linksOnly || bare ? 'padding: 0' : undefined" :style="linksOnly || bare ? undefined : 'margin-bottom: 20px'">
    <NSpace vertical style="width: 100%">
      <NText depth="3">订阅只包含已应用的 stack 节点、该用户的手工节点和远端节点源同步来的节点；改动在下一次发布订阅包并导入订阅端后生效。</NText>
      <NButton v-if="!links" size="small" :loading="linksLoading" @click="showLinks">显示订阅链接</NButton>
      <template v-else>
        <NText>当前 {{ links.nodes }} 个节点</NText>
        <NAlert v-if="links.no_nodes_reason" type="warning" :bordered="false" title="订阅端会对该用户返回 not_found">{{ links.no_nodes_reason }}</NAlert>
        <NAlert v-if="!links.base_url" type="warning" :bordered="false">未设置订阅端对外地址（系统设置 → 订阅），下面只显示链接路径，使用时在前面加上订阅端地址。</NAlert>
        <NFormItem v-for="(label, key) in labels" :key="key" :label="label" label-placement="left" label-width="110" :show-feedback="false">
          <NInputGroup>
            <NInput :value="links.links[key]" :input-props="{ 'aria-label': `${label} 订阅链接` }" readonly />
            <NButton :aria-label="`复制 ${label} 订阅链接`" @click="copy(links.links[key])">复制</NButton>
            <NButton :aria-label="`显示 ${label} 订阅二维码`" @click="qr = links.links[key]">二维码</NButton>
          </NInputGroup>
        </NFormItem>
      </template>
      <NCollapse v-if="!linksOnly" v-model:expanded-names="expanded">
        <NCollapseItem title="订阅预览" name="preview">
          <NSpace vertical>
            <NRadioGroup v-model:value="format" size="small">
              <NRadioButton v-for="(label, key) in labels" :key="key" :value="key">预览 {{ label }}</NRadioButton>
            </NRadioGroup>
            <NSpin v-if="previewLoading" size="small" description="正在加载预览" />
            <NAlert v-else-if="previewError" type="warning" :bordered="false">{{ previewError }}</NAlert>
            <CodeEditor v-else :value="preview" :language="format === 'surge' ? 'text' : 'yaml'" readonly min-height="240px" />
          </NSpace>
        </NCollapseItem>
      </NCollapse>
    </NSpace>
  </NCard>
  <NModal :show="!!qr" preset="card" title="订阅二维码" style="max-width: 360px" @update:show="(v) => !v && (qr = null)">
    <NSpace justify="center"><NQrCode :value="qr ?? ''" :size="260" /></NSpace>
  </NModal>
</template>
