<script setup lang="ts">
import { ref, watch } from 'vue'
import { NInput, NText } from 'naive-ui'

/** 编辑域名黑名单：每行一条，example.com 拦截该域名及全部子域名，full:www.example.com 只拦截该域名。 */
const value = defineModel<string[]>('value', { required: true })
defineProps<{ placeholder?: string }>()

// 只去掉空白和空行，条目的规范化和校验由后端完成，错误按条目序号提示。
function parse(input: string): string[] {
  return input.split(/\r?\n/).map((line) => line.trim()).filter((line) => line !== '')
}

// 输入框保留用户正在编辑的原文（含末尾换行），只有列表从外部改变（加载、重置）时才重写。
const text = ref(value.value.join('\n'))
watch(value, (list) => {
  if (parse(text.value).join('\n') !== list.join('\n')) text.value = list.join('\n')
})
function onInput(input: string) {
  text.value = input
  value.value = parse(input)
}
</script>

<template>
  <div style="width: 100%">
    <NInput
      :value="text"
      type="textarea"
      :autosize="{ minRows: 3, maxRows: 14 }"
      :placeholder="placeholder ?? 'example.com\nfull:www.example.org'"
      class="mono-input"
      @update:value="onInput"
    />
    <NText depth="3" style="font-size: 12px">共 {{ value.length }} 条</NText>
  </div>
</template>

<style scoped>
.mono-input :deep(textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
</style>
