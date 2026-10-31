<script setup lang="ts">
import { nextTick, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NForm, NFormItem, NInput } from 'naive-ui'
import { api, fieldErrors, problemMessage } from '../api/client'
import { applySession } from '../session'
import AuthCard from '../components/AuthCard.vue'

const router = useRouter()
const route = useRoute()
const form = reactive({ username: 'admin', password: '', totp_code: '' })
// 密码正确而账号开启了两步验证时，后端要求补充验证码。
const needCode = ref(false)
const codeInput = ref<InstanceType<typeof NInput> | null>(null)
const loading = ref(false)
const error = ref('')

async function submit() {
  loading.value = true
  error.value = ''
  const body = { username: form.username, password: form.password, ...(needCode.value ? { totp_code: form.totp_code } : {}) }
  const { data, error: problem, response } = await api.POST('/api/auth/login', { body })
  loading.value = false
  if (!data) {
    if (response.status === 429) {
      const seconds = Number(response.headers.get('Retry-After') ?? 0)
      error.value = `失败次数过多，请 ${Math.ceil(seconds / 60) || 1} 分钟后再试`
    } else if (response.status === 423) {
      error.value = '连续登录失败，账号已临时锁定，请 15 分钟后再试'
    } else if (fieldErrors(problem).totp_code) {
      error.value = needCode.value ? '验证码错误或已使用，请等待下一个验证码' : ''
      needCode.value = true
      form.totp_code = ''
      await nextTick()
      codeInput.value?.focus()
    } else {
      error.value = problemMessage(problem, '登录失败')
    }
    return
  }
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
        :disabled="!form.username || !form.password || (needCode && form.totp_code.length !== 6)"
      >
        登录
      </NButton>
      <NButton v-if="needCode" quaternary block style="margin-top: 8px" @click="(needCode = false), (form.totp_code = ''), (error = '')">返回</NButton>
    </NForm>
  </AuthCard>
</template>
