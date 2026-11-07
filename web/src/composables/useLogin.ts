import { nextTick, reactive, ref, useTemplateRef } from 'vue'
import type { NInput } from 'naive-ui'
import { api, fieldErrors, problemMessage, type Schemas } from '../api/client'

/**
 * 登录表单：账号密码，账号开启两步验证时再补充验证码。登录页和重新登录对话框共用。
 * submit 成功时返回会话，失败时在 error 中给出原因并返回 null。验证码输入框在模板中用 ref="codeInput" 标记。
 */
export function useLogin(username = 'admin') {
  const form = reactive({ username, password: '', totp_code: '' })
  // 密码正确而账号开启了两步验证时，后端要求补充验证码。
  const needCode = ref(false)
  const codeInput = useTemplateRef<InstanceType<typeof NInput>>('codeInput')
  const loading = ref(false)
  const error = ref('')

  async function submit(): Promise<Schemas['SessionBody'] | null> {
    loading.value = true
    error.value = ''
    const body = { username: form.username, password: form.password, ...(needCode.value ? { totp_code: form.totp_code } : {}) }
    const { data, error: problem, response } = await api.POST('/api/auth/login', { body })
    loading.value = false
    if (data) return data
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
    return null
  }

  /** 从验证码步骤退回账号密码步骤。 */
  function back() {
    needCode.value = false
    form.totp_code = ''
    error.value = ''
  }

  const canSubmit = () => !!form.username && !!form.password && (!needCode.value || form.totp_code.length === 6)

  return { form, needCode, loading, error, submit, back, canSubmit }
}
