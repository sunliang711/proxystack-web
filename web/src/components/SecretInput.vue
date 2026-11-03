<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { NButton, NInput, NInputGroup, useMessage } from 'naive-ui'
import { MASKED } from '../api/client'
import { copyText } from '../util/clipboard'

const props = defineProps<{
  /** 生成新值；不提供时不显示「生成」按钮。 */
  generate?: () => string
  /** 值为遮蔽占位时，点击「显示」调用它取回明文。 */
  reveal?: () => Promise<void> | void
  placeholder?: string
  plain?: boolean
}>()
const value = defineModel<string>('value', { required: true })
const message = useMessage()
const masked = computed(() => value.value === MASKED)
const visible = ref(false)
const revealing = ref(false)

watch(masked, (isMasked) => { if (isMasked) visible.value = false })

async function toggleVisibility() {
  if (revealing.value) return
  if (visible.value) {
    visible.value = false
    return
  }
  revealing.value = true
  try {
    if (masked.value) {
      if (!props.reveal) return
      await props.reveal()
      // 等待父组件回填明文，只展开当前字段，其他凭据继续遮蔽。
      await nextTick()
      if (masked.value) {
        message.error('获取明文失败，请重试')
        return
      }
    }
    visible.value = true
  } catch {
    message.error('获取明文失败，请重试')
  } finally {
    revealing.value = false
  }
}

async function copy() {
  try {
    await copyText(value.value)
    message.success('已复制')
  } catch {
    message.error('复制失败：浏览器不允许访问剪贴板')
  }
}
</script>

<template>
  <NInputGroup>
    <NInput
      v-model:value="value"
      :type="plain || visible ? 'text' : 'password'"
      :placeholder="placeholder"
      :input-props="{ autocomplete: 'off', spellcheck: false }"
      :readonly="masked"
    />
    <NButton v-if="!plain || masked" :loading="revealing" :disabled="masked && !props.reveal" :aria-pressed="visible" @click="toggleVisibility">{{ visible ? '隐藏' : '显示' }}</NButton>
    <NButton :disabled="masked || !value" @click="copy">复制</NButton>
    <NButton v-if="props.generate" @click="value = props.generate()">生成</NButton>
  </NInputGroup>
</template>
