<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NForm, NResult } from 'naive-ui'
import { api, fieldErrors, problemMessage } from '../api/client'
import AuthCard from '../components/AuthCard.vue'
import NewPasswordFields from '../components/NewPasswordFields.vue'

const route = useRoute()
const router = useRouter()
const token = computed(() => (typeof route.query.token === 'string' ? route.query.token : ''))
const form = reactive({ password: '', confirm: '' })
const errors = ref<Record<string, string>>({})
const error = ref('')
const loading = ref(false)
const done = ref(false)
const valid = computed(() => form.password && form.password === form.confirm)

async function submit() {
  loading.value = true
  errors.value = {}
  error.value = ''
  const { response, error: problem } = await api.POST('/api/auth/reset', {
    body: { token: token.value, new_password: form.password },
  })
  loading.value = false
  if (!response.ok) {
    errors.value = fieldErrors(problem)
    if (!Object.keys(errors.value).length) {
      error.value = problemMessage(problem)
    }
    return
  }
  done.value = true
}
</script>

<template>
  <AuthCard title="重置密码">
    <NResult v-if="!token" status="warning" title="链接无效" description="请在服务器上执行 proxystack-web admin reset-link 获取新的链接。" />
    <NResult v-else-if="done" status="success" title="密码已重置" description="全部会话已失效，请使用新密码登录。">
      <template #footer>
        <NButton type="primary" @click="router.replace({ name: 'login' })">前往登录</NButton>
      </template>
    </NResult>
    <NForm v-else @submit.prevent="submit">
      <NewPasswordFields v-model:password="form.password" v-model:confirm="form.confirm" :errors="errors" />
      <NAlert v-if="error" type="error" :show-icon="false" style="margin-bottom: 16px">{{ error }}</NAlert>
      <NButton type="primary" block attr-type="submit" :loading="loading" :disabled="!valid">设置新密码</NButton>
    </NForm>
  </AuthCard>
</template>
