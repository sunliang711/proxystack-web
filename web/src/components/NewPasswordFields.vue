<script setup lang="ts">
import { NFormItem, NInput } from 'naive-ui'

defineProps<{ errors: Record<string, string> }>()
const password = defineModel<string>('password', { required: true })
const confirm = defineModel<string>('confirm', { required: true })
</script>

<template>
  <NFormItem
    label="新密码"
    :validation-status="errors.new_password ? 'error' : undefined"
    :feedback="errors.new_password || '至少 10 个字符'"
  >
    <NInput v-model:value="password" type="password" show-password-on="click" autocomplete="new-password" />
  </NFormItem>
  <NFormItem
    label="确认新密码"
    :validation-status="confirm && confirm !== password ? 'error' : undefined"
    :feedback="confirm && confirm !== password ? '两次输入不一致' : undefined"
  >
    <NInput v-model:value="confirm" type="password" show-password-on="click" autocomplete="new-password" />
  </NFormItem>
</template>
