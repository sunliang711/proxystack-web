<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NForm, NFormItem, NInput, NSpace, useMessage } from 'naive-ui'
import { api, fieldErrors, problemMessage } from '../api/client'
import { loadSession, logout, session } from '../session'
import AuthCard from '../components/AuthCard.vue'
import NewPasswordFields from '../components/NewPasswordFields.vue'

const router = useRouter()
const message = useMessage()
const form = reactive({ old: '', password: '', confirm: '' })
const errors = ref<Record<string, string>>({})
const error = ref('')
const loading = ref(false)
const forced = computed(() => session.admin?.must_change_password ?? false)
const valid = computed(() => form.old && form.password && form.password === form.confirm)

async function submit() {
  loading.value = true
  errors.value = {}
  error.value = ''
  const { response, error: problem } = await api.POST('/api/auth/password', {
    body: { old_password: form.old, new_password: form.password },
  })
  loading.value = false
  if (!response.ok) {
    errors.value = fieldErrors(problem)
    if (!Object.keys(errors.value).length) {
      error.value = problemMessage(problem)
    }
    return
  }
  message.success('密码已修改')
  await loadSession()
  router.replace('/')
}

async function signOut() {
  await logout()
  router.replace({ name: 'login' })
}
</script>

<template>
  <AuthCard title="修改密码">
    <NAlert v-if="forced" type="warning" :show-icon="false" style="margin-bottom: 16px">
      首次登录或密码被重置后，需要先设置新密码。
    </NAlert>
    <NForm @submit.prevent="submit">
      <NFormItem
        label="当前密码"
        :validation-status="errors.old_password ? 'error' : undefined"
        :feedback="errors.old_password"
      >
        <NInput v-model:value="form.old" type="password" show-password-on="click" autocomplete="current-password" />
      </NFormItem>
      <NewPasswordFields v-model:password="form.password" v-model:confirm="form.confirm" :errors="errors" />
      <NAlert v-if="error" type="error" :show-icon="false" style="margin-bottom: 16px">{{ error }}</NAlert>
      <NSpace justify="end">
        <NButton v-if="forced" @click="signOut">退出登录</NButton>
        <NButton v-else @click="router.back()">取消</NButton>
        <NButton type="primary" attr-type="submit" :loading="loading" :disabled="!valid">保存</NButton>
      </NSpace>
    </NForm>
  </AuthCard>
</template>
