<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NForm, NFormItem, NInput } from 'naive-ui'
import { applySession } from '../session'
import { useLogin } from '../composables/useLogin'
import AuthCard from '../components/AuthCard.vue'

const router = useRouter()
const route = useRoute()
const { form, needCode, loading, error, submit: login, back, canSubmit } = useLogin()
if (route.query.expired === '1') error.value = '登录已过期，请重新登录'

async function submit() {
  const data = await login()
  if (!data) return
  applySession(data)
  const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
  router.replace(data.admin.must_change_password ? { name: 'change-password' } : redirect)
}
</script>

<template>
  <AuthCard title="登录">
    <NForm :model="form" @submit.prevent="submit">
      <NFormItem label="用户名" path="username">
        <NInput v-model:value="form.username" autocomplete="username" :disabled="needCode" />
      </NFormItem>
      <NFormItem label="密码" path="password">
        <NInput v-model:value="form.password" type="password" show-password-on="click" autocomplete="current-password" :disabled="needCode" />
      </NFormItem>
      <NFormItem v-if="needCode" label="两步验证码" path="totp_code" feedback="打开验证器应用，输入 6 位验证码">
        <NInput
          ref="codeInput"
          v-model:value="form.totp_code"
          inputmode="numeric"
          autocomplete="one-time-code"
          :maxlength="6"
          placeholder="000000"
        />
      </NFormItem>
      <NAlert v-if="error" type="error" :show-icon="false" style="margin-bottom: 16px">{{ error }}</NAlert>
      <NButton
        type="primary"
        block
        attr-type="submit"
        :loading="loading"
        :disabled="!canSubmit()"
      >
        登录
      </NButton>
      <NButton v-if="needCode" quaternary block style="margin-top: 8px" @click="back">返回</NButton>
    </NForm>
  </AuthCard>
</template>
