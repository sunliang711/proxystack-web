<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NQrCode, NSpace, NSteps, NStep, NTag, NText, useMessage } from 'naive-ui'
import { api, fieldErrors, problemMessage, type Schemas } from '../api/client'
import { loadSession, session } from '../session'

const message = useMessage()
const enabled = computed(() => session.admin?.totp_enabled ?? false)

// 开启流程：生成密钥 → 验证器扫码 → 输入第一个验证码确认。
const setup = ref<Schemas['TOTPSetup'] | null>(null)
const enableCode = ref('')
const disableForm = reactive({ password: '', code: '' })
const errors = ref<Record<string, string>>({})
const error = ref('')
const busy = ref(false)

// 后端字段提示是英文，这里换成面向使用者的中文说明。
const hints: Record<string, string> = {
  totp_code: '验证码错误或已使用。请确认手机时间准确，等待下一个验证码后重试',
  password: '密码错误',
}

function showProblem(problem: Parameters<typeof fieldErrors>[0]) {
  const fields = fieldErrors(problem)
  errors.value = Object.fromEntries(Object.keys(fields).map((key) => [key, hints[key] ?? fields[key]]))
  error.value = Object.keys(fields).length ? '' : problemMessage(problem)
}

function reset() {
  setup.value = null
  enableCode.value = ''
  disableForm.password = ''
  disableForm.code = ''
  errors.value = {}
  error.value = ''
}

async function begin() {
  reset()
  busy.value = true
  const { data, error: problem } = await api.POST('/api/auth/totp/setup')
  busy.value = false
  if (!data) {
    showProblem(problem)
    return
  }
  setup.value = data
}

async function enable() {
  busy.value = true
  errors.value = {}
  const { response, error: problem } = await api.POST('/api/auth/totp/enable', { body: { totp_code: enableCode.value } })
  busy.value = false
  if (!response.ok) {
    showProblem(problem)
    return
  }
  message.success('两步验证已开启，其他设备上的登录已注销')
  reset()
  await loadSession()
}

async function disable() {
  busy.value = true
  errors.value = {}
  const { response, error: problem } = await api.POST('/api/auth/totp/disable', {
    body: { password: disableForm.password, totp_code: disableForm.code },
  })
  busy.value = false
  if (!response.ok) {
    showProblem(problem)
    return
  }
  message.success('两步验证已关闭')
  reset()
  await loadSession()
}

const feedback = (key: string) => ({
  validationStatus: errors.value[key] ? ('error' as const) : undefined,
  feedback: errors.value[key],
})
</script>

<template>
  <NSpace vertical :size="20">
    <NCard title="两步验证" size="small">
      <template #header-extra>
        <NTag :type="enabled ? 'success' : 'default'" size="small">{{ enabled ? '已开启' : '未开启' }}</NTag>
      </template>
      <NText depth="3" tag="p" style="margin-top: 0">
        开启后，登录时除了密码还要输入验证器应用（如 Google Authenticator、1Password、Microsoft Authenticator）生成的 6 位验证码。
      </NText>
      <NAlert v-if="error" type="error" :bordered="false" style="margin-bottom: 12px">{{ error }}</NAlert>

      <template v-if="!enabled && !setup">
        <NButton type="primary" :loading="busy" @click="begin">开启两步验证</NButton>
      </template>

      <template v-else-if="!enabled && setup">
        <NSteps vertical :current="enableCode.length === 6 ? 2 : 1" size="small">
          <NStep title="用验证器应用扫描二维码">
            <div class="totp-setup">
              <div class="qr"><NQrCode :value="setup.uri" :size="168" :padding="8" error-correction-level="M" /></div>
              <NSpace vertical :size="4" class="setup-help">
                <NText depth="3">无法扫码时，手工输入密钥：</NText>
                <NText code class="secret">{{ setup.secret }}</NText>
                <NText depth="3" style="font-size: 12px">类型选「基于时间」，6 位，30 秒</NText>
              </NSpace>
            </div>
          </NStep>
          <NStep title="输入应用显示的验证码">
            <NForm style="max-width: 320px; margin-top: 8px" @submit.prevent="enable">
              <NFormItem :show-label="false" v-bind="feedback('totp_code')">
                <NInput v-model:value="enableCode" inputmode="numeric" autocomplete="one-time-code" :maxlength="6" placeholder="000000" />
              </NFormItem>
              <NSpace>
                <NButton type="primary" attr-type="submit" :loading="busy" :disabled="enableCode.length !== 6">确认开启</NButton>
                <NButton @click="reset">取消</NButton>
              </NSpace>
            </NForm>
          </NStep>
        </NSteps>
      </template>

      <template v-else>
        <NForm label-placement="left" label-width="90" style="max-width: 420px" @submit.prevent="disable">
          <NFormItem label="当前密码" v-bind="feedback('password')">
            <NInput v-model:value="disableForm.password" type="password" show-password-on="click" autocomplete="current-password" />
          </NFormItem>
          <NFormItem label="验证码" v-bind="feedback('totp_code')">
            <NInput v-model:value="disableForm.code" inputmode="numeric" autocomplete="one-time-code" :maxlength="6" placeholder="000000" />
          </NFormItem>
          <NFormItem :show-label="false">
            <NButton type="error" ghost attr-type="submit" :loading="busy" :disabled="!disableForm.password || disableForm.code.length !== 6">
              关闭两步验证
            </NButton>
          </NFormItem>
        </NForm>
      </template>
    </NCard>

    <NCard title="登录保护" size="small">
      <NText depth="3" tag="p" style="margin: 0 0 6px">
        同一账号连续 5 次登录失败后锁定 15 分钟；同一来源地址 15 分钟内失败 10 次后，暂停接受该地址的登录 15 分钟。验证码错误同样计入失败次数。
      </NText>
      <NText depth="3" tag="p" style="margin: 0">
        验证器设备丢失时，在服务器上执行 <NText code>sudo proxystack-web admin reset-password --disable-totp</NText>，重置密码的同时关闭两步验证。
      </NText>
    </NCard>
  </NSpace>
</template>

<style scoped>
.totp-setup {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 20px;
  margin: 8px 0 4px;
}
.setup-help { flex: 1 1 260px; min-width: 0; }
.qr {
  display: inline-flex;
  flex-shrink: 0;
  background: #fff;
  border-radius: 8px;
  line-height: 0;
}
.secret {
  font-size: 15px;
  letter-spacing: 1px;
  word-break: break-all;
}
</style>
