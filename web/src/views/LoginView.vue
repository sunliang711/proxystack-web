<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NForm, NFormItem, NInput } from 'naive-ui'
import { api, problemMessage } from '../api/client'
import { applySession } from '../session'
import AuthCard from '../components/AuthCard.vue'

const router = useRouter()
const route = useRoute()
const form = reactive({ username: 'admin', password: '' })
const loading = ref(false)
const error = ref('')

async function submit() {
  loading.value = true
  error.value = ''
  const { data, error: problem } = await api.POST('/api/auth/login', { body: form })
  loading.value = false
  if (!data) {
    error.value = problemMessage(problem, '登录失败')
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
        <NInput v-model:value="form.username" autocomplete="username" />
      </NFormItem>
      <NFormItem label="密码" path="password">
        <NInput v-model:value="form.password" type="password" show-password-on="click" autocomplete="current-password" />
      </NFormItem>
      <NAlert v-if="error" type="error" :show-icon="false" style="margin-bottom: 16px">{{ error }}</NAlert>
      <NButton type="primary" block attr-type="submit" :loading="loading" :disabled="!form.username || !form.password">
        登录
      </NButton>
    </NForm>
  </AuthCard>
</template>
