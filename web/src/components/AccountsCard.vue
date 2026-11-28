<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NModal, NSelect, NSpace, NTable, NTag, NText, useMessage } from 'naive-ui'
import { api, fieldErrors, problemMessage, type ProblemDetail, type Schemas } from '../api/client'
import { clearSession, loadSession, session } from '../session'
import { formatTime } from '../util/format'

type Account = Schemas['AccountView']
type Action = 'create' | 'rename' | 'role' | 'reset' | 'disable' | 'enable'
const emit = defineEmits<{ changed: [] }>()
const router = useRouter()
const message = useMessage()
const accounts = ref<Account[]>([])
const loading = ref(false)
const error = ref('')
const shown = ref(false)
const busy = ref(false)
const action = ref<Action>('create')
const target = ref<Account | null>(null)
const form = reactive({ username: '', password: '', confirm: '', role: 'viewer' as 'admin' | 'viewer', currentPassword: '' })
const errors = ref<Record<string, string>>({})
const formError = ref('')
const roles = [{ label: '只读运维', value: 'viewer' }, { label: '管理员', value: 'admin' }]
const enabledAdmins = computed(() => accounts.value.filter((item) => item.role === 'admin' && !item.disabled).length)
const labels: Record<Action, string> = { create: '新增账号', rename: '修改用户名', role: '修改角色', reset: '重置密码', disable: '停用账号', enable: '启用账号' }
const title = computed(() => `${labels[action.value]}${target.value ? `：${target.value.username}` : ''}`)
const needsPassword = computed(() => action.value === 'create' || action.value === 'reset')
const valid = computed(() => form.currentPassword && (!needsPassword.value || (form.password && form.password === form.confirm)) && (!['create', 'rename'].includes(action.value) || form.username))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const { data, error: problem } = await api.GET('/api/admins')
    if (data) accounts.value = data.admins
    else error.value = problemMessage(problem, '加载管理账号失败')
  } catch {
    error.value = '加载管理账号失败，请检查网络后重试'
  } finally {
    loading.value = false
  }
}

function open(next: Action, account: Account | null = null) {
  action.value = next
  target.value = account
  Object.assign(form, { username: account?.username ?? '', password: '', confirm: '', currentPassword: '', role: account?.role === 'admin' ? 'viewer' : 'admin' })
  if (next === 'create') form.role = 'viewer'
  errors.value = {}
  formError.value = ''
  shown.value = true
}

function close() {
  if (busy.value) return
  shown.value = false
  form.password = ''
  form.confirm = ''
  form.currentPassword = ''
}

async function submit() {
  if (busy.value || !valid.value) return
  busy.value = true
  errors.value = {}
  formError.value = ''
  const selected = action.value
  const id = target.value?.id ?? 0
  try {
    let response: Response
    let problem: ProblemDetail | undefined
    const params = { path: { id } }
    const current_password = form.currentPassword
    if (selected === 'create') {
      ({ response, error: problem } = await api.POST('/api/admins', { body: { current_password, username: form.username, password: form.password, role: form.role } }))
    } else if (selected === 'rename') {
      ({ response, error: problem } = await api.PUT('/api/admins/{id}', { params, body: { current_password, username: form.username } }))
    } else if (selected === 'role') {
      ({ response, error: problem } = await api.PUT('/api/admins/{id}/role', { params, body: { current_password, role: form.role } }))
    } else if (selected === 'reset') {
      ({ response, error: problem } = await api.POST('/api/admins/{id}/reset-password', { params, body: { current_password, new_password: form.password } }))
    } else if (selected === 'disable') {
      ({ response, error: problem } = await api.POST('/api/admins/{id}/disable', { params, body: { current_password } }))
    } else {
      ({ response, error: problem } = await api.POST('/api/admins/{id}/enable', { params, body: { current_password } }))
    }
    if (!response.ok) {
      errors.value = fieldErrors(problem)
      if (errors.value.current_password) errors.value.current_password = '当前密码验证失败'
      formError.value = Object.keys(errors.value).length ? '' : problemMessage(problem)
      return
    }
    shown.value = false
    form.password = ''
    form.confirm = ''
    form.currentPassword = ''
    message.success(`${labels[selected]}成功`)
    if (id === session.admin?.id && selected === 'role' && form.role !== target.value?.role) {
      clearSession()
      await router.replace({ name: 'login' })
      return
    }
    if (id === session.admin?.id) await loadSession()
    await load()
    emit('changed')
  } catch {
    formError.value = '操作失败，请检查网络后重试'
  } finally {
    busy.value = false
  }
}

const lastAdmin = (item: Account) => item.role === 'admin' && !item.disabled && enabledAdmins.value <= 1
const feedback = (field: string) => ({ validationStatus: errors.value[field] ? 'error' as const : undefined, feedback: errors.value[field] })
onMounted(load)
</script>

<template>
  <NCard id="accounts" title="管理员与只读账号" size="small">
    <template #header-extra>
      <NSpace :size="8">
        <NButton size="small" :loading="loading" :disabled="busy" @click="load">刷新</NButton>
        <NButton size="small" type="primary" :disabled="loading || busy" @click="open('create')">新增账号</NButton>
      </NSpace>
    </template>
    <NText depth="3" tag="p" style="margin-top: 0">管理员可管理全部配置和账号；只读运维可查看状态与脱敏配置，并管理自己的密码、两步验证和会话。账号管理操作需验证你的当前密码。</NText>
    <NAlert v-if="error" type="error" :bordered="false" style="margin-bottom: 12px">{{ error }}</NAlert>
    <div class="accounts-table">
      <NTable size="small" :single-line="false">
        <thead><tr><th>用户名</th><th>角色</th><th>状态</th><th>最近登录</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="item in accounts" :key="item.id">
            <td><NText strong>{{ item.username }}</NText><NTag v-if="item.id === session.admin?.id" size="small" style="margin-left: 8px">当前账号</NTag></td>
            <td><NTag :type="item.role === 'admin' ? 'info' : 'default'" size="small">{{ item.role === 'admin' ? '管理员' : '只读运维' }}</NTag></td>
            <td><NTag :type="item.disabled ? 'error' : 'success'" size="small">{{ item.disabled ? '已停用' : '启用' }}</NTag></td>
            <td>{{ Date.parse(item.last_login_at) > 0 ? formatTime(item.last_login_at) : '从未登录' }}</td>
            <td>
              <NSpace :size="6" :wrap="false">
                <NButton size="tiny" :disabled="busy || loading" @click="open('rename', item)">改名</NButton>
                <NButton size="tiny" :disabled="busy || loading || lastAdmin(item)" :title="lastAdmin(item) ? '不能降级最后一个启用的管理员' : undefined" @click="open('role', item)">{{ item.role === 'admin' ? '设为只读' : '设为管理员' }}</NButton>
                <NButton size="tiny" :disabled="busy || loading" @click="open('reset', item)">重置密码</NButton>
                <NButton size="tiny" :type="item.disabled ? 'primary' : 'error'" ghost :disabled="busy || loading || (!item.disabled && (item.id === session.admin?.id || lastAdmin(item)))" @click="open(item.disabled ? 'enable' : 'disable', item)">{{ item.disabled ? '启用' : '停用' }}</NButton>
              </NSpace>
            </td>
          </tr>
          <tr v-if="!accounts.length"><td colspan="5">{{ loading ? '正在加载…' : '暂无管理账号' }}</td></tr>
        </tbody>
      </NTable>
    </div>
  </NCard>

  <NModal :show="shown" preset="card" :title="title" :mask-closable="!busy" :close-on-esc="!busy" :closable="!busy" style="width: min(560px, calc(100vw - 32px))" @update:show="close">
    <NForm label-placement="top" @submit.prevent="submit">
      <NAlert v-if="action === 'create' || (action === 'reset' && target?.id !== session.admin?.id)" type="info" :bordered="false" style="margin-bottom: 16px">账号首次登录或使用重置后的密码登录时，必须先设置新密码。</NAlert>
      <NAlert v-if="action === 'role' || action === 'disable' || action === 'reset'" type="warning" :bordered="false" style="margin-bottom: 16px">
        <template v-if="action === 'role'">切换角色后，该账号的全部会话将被注销，需要重新登录。</template>
        <template v-else-if="action === 'disable'">停用后，该账号无法登录，现有会话和未使用的重置链接立即失效。</template>
        <template v-else>重置后，该账号的其他会话及未使用的重置链接失效；重置自己的密码时保留当前会话。</template>
      </NAlert>
      <NFormItem v-if="action === 'create' || action === 'rename'" label="用户名" v-bind="feedback('username')">
        <NInput v-model:value="form.username" :disabled="busy" :maxlength="64" autocomplete="off" placeholder="3～64 位字母、数字、点、下划线或连字符" />
      </NFormItem>
      <NFormItem v-if="action === 'create' || action === 'role'" label="角色" v-bind="feedback('role')">
        <NSelect v-model:value="form.role" :options="roles" :disabled="busy" />
      </NFormItem>
      <template v-if="needsPassword">
        <NFormItem :label="action === 'create' ? '初始密码' : '新密码'" v-bind="feedback(action === 'create' ? 'password' : 'new_password')">
          <NInput v-model:value="form.password" :disabled="busy" type="password" show-password-on="click" autocomplete="new-password" placeholder="至少 10 个字符" />
        </NFormItem>
        <NFormItem label="确认密码" :validation-status="form.confirm && form.password !== form.confirm ? 'error' : undefined" :feedback="form.confirm && form.password !== form.confirm ? '两次输入不一致' : undefined">
          <NInput v-model:value="form.confirm" :disabled="busy" type="password" show-password-on="click" autocomplete="new-password" />
        </NFormItem>
      </template>
      <NFormItem label="你的当前密码" v-bind="feedback('current_password')">
        <NInput v-model:value="form.currentPassword" :disabled="busy" type="password" show-password-on="click" autocomplete="current-password" />
      </NFormItem>
      <NAlert v-if="formError" type="error" :bordered="false" style="margin-bottom: 16px">{{ formError }}</NAlert>
      <NSpace justify="end">
        <NButton :disabled="busy" @click="close">取消</NButton>
        <NButton :type="action === 'disable' ? 'error' : 'primary'" attr-type="submit" :loading="busy" :disabled="!valid">确认{{ labels[action] }}</NButton>
      </NSpace>
    </NForm>
  </NModal>
</template>

<style scoped>
.accounts-table { overflow-x: auto; }
.accounts-table :deep(table) { min-width: 840px; }
.accounts-table :deep(th) { white-space: nowrap; }
</style>
