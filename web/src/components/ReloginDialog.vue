<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NForm, NFormItem, NInput, NModal, useMessage } from 'naive-ui'
import { applySession, clearSession, session } from '../session'
import { useLogin } from '../composables/useLogin'
import { connectEvents, disconnectEvents, refreshPending } from '../runtime'

// 会话失效或即将到期时在当前页面重新登录，不离开页面，未保存的修改保留在表单里。
const router = useRouter()
const message = useMessage()
const { form, needCode, loading, error, submit: login, back, canSubmit } = useLogin(session.admin?.username)
// 对话框关闭动画期间 session.reauth 已清空，标题和说明沿用打开时的原因，不在淡出时闪变。
const reason = ref<'expired' | 'renew'>('expired')
const expired = computed(() => reason.value === 'expired')
const show = computed({
  get: () => !!session.reauth,
  set: (value) => {
    // 已失效时只能重新登录或换账号，不能直接关掉。
    if (!value && session.reauth === 'renew') session.reauth = ''
  },
})

watch(() => session.reauth, (value) => {
  if (!value) return
  reason.value = value
  form.username = session.admin?.username ?? form.username
  form.password = ''
  back()
})

async function submit() {
  const wasExpired = expired.value
  const data = await login()
  if (!data) return
  applySession(data)
  // 事件流在会话失效时已被服务器拒绝，用新会话重新连接。
  disconnectEvents()
  connectEvents()
  void refreshPending()
  if (data.admin.must_change_password) {
    void router.replace({ name: 'change-password' })
    return
  }
  message.success(wasExpired ? '已重新登录。失效时没有完成的操作请再执行一次' : '已重新登录，会话有效期从现在起重新计算')
}

/** 放弃当前页面，去登录页换个账号登录，登录后回到这里。 */
function switchAccount() {
  const redirect = router.currentRoute.value.fullPath
  clearSession()
  void router.replace({ name: 'login', query: redirect !== '/' ? { redirect } : {} })
}
</script>

<template>
  <NModal
    v-model:show="show"
    preset="card"
    :title="expired ? '登录已失效' : '重新登录'"
    :closable="!expired"
    :mask-closable="!expired"
    :close-on-esc="!expired"
    style="width: 420px; max-width: calc(100vw - 32px)"
  >
    <NAlert :type="expired ? 'warning' : 'info'" :bordered="false" style="margin-bottom: 16px">
      <template v-if="expired">会话已失效，需要重新登录。登录后留在当前页面，未保存的修改不会丢失。</template>
      <template v-else>重新登录后，会话有效期从现在起重新计算：24 小时无操作或登录满 7 天后失效。</template>
    </NAlert>
    <NForm :model="form" @submit.prevent="submit">
      <NFormItem label="用户名" path="username">
        <NInput :value="form.username" disabled />
      </NFormItem>
      <NFormItem label="密码" path="password">
        <NInput v-model:value="form.password" type="password" show-password-on="click" autocomplete="current-password" :disabled="needCode" />
      </NFormItem>
      <NFormItem v-if="needCode" label="两步验证码" path="totp_code" feedback="打开验证器应用，输入 6 位验证码">
        <NInput ref="codeInput" v-model:value="form.totp_code" inputmode="numeric" autocomplete="one-time-code" :maxlength="6" placeholder="000000" />
      </NFormItem>
      <NAlert v-if="error" type="error" :show-icon="false" style="margin-bottom: 16px">{{ error }}</NAlert>
      <NButton type="primary" block attr-type="submit" :loading="loading" :disabled="!canSubmit()">登录</NButton>
      <NButton v-if="needCode" quaternary block style="margin-top: 8px" @click="back">返回</NButton>
      <NButton v-else-if="expired" quaternary block style="margin-top: 8px" @click="switchAccount">换个账号登录</NButton>
    </NForm>
  </NModal>
</template>
