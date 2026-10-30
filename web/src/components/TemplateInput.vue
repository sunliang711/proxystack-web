<script setup lang="ts">
import { NInput, NSpace, NTag } from 'naive-ui'
import { templateFunctions, templateVariables } from '../util/options'

defineProps<{ placeholder?: string }>()
const value = defineModel<string>('value', { required: true })

function insert(text: string) {
  value.value = (value.value ?? '') + text
}
</script>

<template>
  <div style="width: 100%">
    <NInput v-model:value="value" :placeholder="placeholder ?? '例如 {{ .stack }} {{ .remark }}'" />
    <NSpace :size="4" style="margin-top: 4px">
      <NTag v-for="v in templateVariables" :key="v" size="small" checkable @click="insert(`{{ ${v} }}`)">{{ v }}</NTag>
      <NTag v-for="f in templateFunctions" :key="f" size="small" type="info" checkable @click="insert(`{{ ${f} .remark }}`)">{{ f }}</NTag>
    </NSpace>
  </div>
</template>
