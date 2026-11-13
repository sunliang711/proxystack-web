<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton, NInput, NPopconfirm, NSpace, NText, useMessage } from 'naive-ui'
import { useManualIcons, type IconKind } from '../composables/useManualIcons'

/** 为一个主域名或服务设置手动图标：上传图片、从网址获取、删除。 */
const props = defineProps<{ kind: IconKind; name: string }>()
const emit = defineEmits<{ done: [] }>()
const message = useMessage()
const icons = useManualIcons()
const manual = computed(() => icons.has(props.kind, props.name))
const url = ref('')
const busy = ref<'' | 'upload' | 'fetch' | 'delete'>('')
const input = ref<HTMLInputElement | null>(null)
const label = computed(() => (props.kind === 'service' ? `服务 ${props.name}` : props.name))

async function onFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (input.value) input.value.value = ''
  if (!file) return
  if (file.size > 100 * 1024) {
    message.error('图片不能超过 100 KB')
    return
  }
  busy.value = 'upload'
  const error = await icons.upload(props.kind, props.name, file)
  busy.value = ''
  if (error) {
    message.error(error)
    return
  }
  message.success(`已设置 ${label.value} 的图标`)
  emit('done')
}

async function fetchURL() {
  if (!url.value.trim()) return
  busy.value = 'fetch'
  const error = await icons.fetchFromURL(props.kind, props.name, url.value.trim())
  busy.value = ''
  if (error) {
    message.error(error)
    return
  }
  url.value = ''
  message.success(`已设置 ${label.value} 的图标`)
  emit('done')
}

async function remove() {
  busy.value = 'delete'
  const error = await icons.remove([{ kind: props.kind, name: props.name }])
  busy.value = ''
  if (error) {
    message.error(error)
    return
  }
  message.success(props.kind === 'service' ? `已删除 ${label.value} 的手动图标，改用代表域名的图标` : `已删除 ${label.value} 的手动图标，恢复自动获取`)
  emit('done')
}
</script>

<template>
  <NSpace vertical :size="10" class="icon-actions">
    <NText depth="3" class="hint">
      {{ label }} 的图标<template v-if="manual">（当前为手动设置）</template>。支持 PNG、GIF、JPEG、WebP、ICO 和 SVG，不超过 100 KB；手动图标优先于自动获取。
    </NText>
    <input ref="input" type="file" accept="image/png,image/gif,image/jpeg,image/webp,image/x-icon,image/vnd.microsoft.icon,image/svg+xml,.ico,.svg" hidden @change="onFile">
    <NButton size="small" :loading="busy === 'upload'" :disabled="!!busy" @click="input?.click()">上传图片</NButton>
    <NSpace :size="6" :wrap="false">
      <NInput v-model:value="url" size="small" placeholder="https://example.com/logo.png" clearable @keyup.enter="fetchURL" />
      <NButton size="small" :loading="busy === 'fetch'" :disabled="!!busy || !url.trim()" @click="fetchURL">从网址获取</NButton>
    </NSpace>
    <NPopconfirm v-if="manual" @positive-click="remove">
      <template #trigger><NButton size="small" type="error" secondary :loading="busy === 'delete'" :disabled="!!busy">删除手动图标</NButton></template>
      {{ kind === 'service' ? '删除后改用代表域名的图标。' : '删除后恢复自动获取的图标。' }}确定删除吗？
    </NPopconfirm>
  </NSpace>
</template>

<style scoped>
.icon-actions { width: 320px; max-width: calc(100vw - 48px); }
.hint { font-size: 12px; }
</style>
