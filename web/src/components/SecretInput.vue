<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NInput, NInputGroup, useMessage } from 'naive-ui'
import { MASKED } from '../api/client'

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

async function copy() {
  try {
    await navigator.clipboard.writeText(value.value)
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
      :type="plain ? 'text' : 'password'"
      show-password-on="click"
      :placeholder="placeholder"
      :input-props="{ autocomplete: 'off', spellcheck: false }"
      :readonly="masked"
    />
    <NButton v-if="masked && props.reveal" @click="props.reveal">显示</NButton>
    <NButton :disabled="masked || !value" @click="copy">复制</NButton>
    <NButton v-if="props.generate" @click="value = props.generate()">生成</NButton>
  </NInputGroup>
</template>
