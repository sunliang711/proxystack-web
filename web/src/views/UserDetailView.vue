<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  NButton, NCard, NCollapse, NCollapseItem, NDatePicker, NEmpty, NForm, NFormItem, NGrid, NGridItem, NInput,
  NInputNumber, NList, NListItem, NPopconfirm, NRadioButton, NRadioGroup, NSpace, NSpin, NSwitch, NTag, NText,
  useMessage,
} from 'naive-ui'
import { api, emptyErrors, fieldState, MASKED, splitProblem, type FormErrors, type Schemas } from '../api/client'
import ByteSizeInput from '../components/ByteSizeInput.vue'
import ErrorList from '../components/ErrorList.vue'
import SecretInput from '../components/SecretInput.vue'
import SubscriptionCard from '../components/SubscriptionCard.vue'
import TemplateInput from '../components/TemplateInput.vue'
import UsageCard from '../components/UsageCard.vue'
import { useDirty } from '../composables/useDirty'
import { randomString, randomUUID, shadowsocksKey } from '../util/generate'

type User = Schemas['User']
type Reference = Schemas['UserReference']

const route = useRoute()
const router = useRouter()
const message = useMessage()
const id = computed(() => Number(route.params.id))
const user = ref<User | null>(null)
const references = ref<Reference[]>([])
const stacks = ref<Schemas['StackSummary'][]>([])
const pausing = ref(false)
const errors = ref<FormErrors>(emptyErrors())
const saving = ref(false)
const { dirty, markClean } = useDirty(user)

const f = (path: string, hint?: string) => fieldState(errors.value, path, hint)

async function load() {
  const { data } = await api.GET('/api/users/{id}', { params: { path: { id: id.value } } })
  if (!data) {
    message.error('用户不存在')
    router.replace({ name: 'users' })
    return
  }
  user.value = data.user
  references.value = data.references
  stacks.value = (await api.GET('/api/stacks')).data?.stacks ?? []
  markClean()
}
onMounted(load)

/** 启用开关和逐 stack 暂停当场生效并写入配置，不需要点「保存」，也不需要应用。 */
async function setPause(enabled: boolean, pausedStacks: string[]) {
  if (!user.value) return
  pausing.value = true
  const { data, error } = await api.PUT('/api/users/{id}/pause', {
    params: { path: { id: id.value } },
    body: { enabled, paused_stacks: pausedStacks },
  })
  pausing.value = false
  if (error) {
    const problem = splitProblem(error, '操作失败')
    message.error([...problem.global, ...Object.values(problem.fields)].join('；'))
    return
  }
  const wasDirty = dirty.value
  user.value.enabled = data!.user.enabled
  user.value.paused_stacks = data!.user.paused_stacks
  if (!wasDirty) markClean()
  for (const note of data!.notes) message.warning(note, { duration: 8000 })
  message.success(enabled && !pausedStacks.length ? '已恢复，当场生效' : '已暂停，当场生效')
}

function toggleStack(stack: string, paused: boolean) {
  const current = user.value?.paused_stacks ?? []
  setPause(user.value!.enabled, paused ? [...current, stack] : current.filter((name) => name !== stack))
}

/** 取回明文凭据，只替换仍是占位的字段，不覆盖已修改的内容。 */
async function reveal() {
  const { data } = await api.GET('/api/users/{id}/secrets', { params: { path: { id: id.value } } })
  if (!data || !user.value) return
  const u = user.value
  const keep = (current: string, plain: string) => (current === MASKED ? plain : current)
  const wasDirty = dirty.value
  u.uuid = keep(u.uuid, data.uuid)
  u.ss_password = keep(u.ss_password, data.ss_password)
  u.sub_token = keep(u.sub_token, data.sub_token)
  for (const profile of u.profiles) {
    const plain = data.profiles.find((p) => p.id === profile.id)
    if (plain) {
      profile.uuid = keep(profile.uuid, plain.uuid)
      profile.ss_password = keep(profile.ss_password, plain.ss_password)
    }
  }
  if (!wasDirty) markClean()
}

const expireAt = computed({
  get: () => (user.value?.expire_at ? new Date(user.value.expire_at).getTime() : null),
  set: (value: number | null) => {
    if (user.value) user.value.expire_at = value ? new Date(value).toISOString().replace(/\.\d{3}Z$/, 'Z') : ''
  },
})

function addProfile() {
  user.value?.profiles.push({ name: '', uuid: randomUUID(), ss_password: randomString(24), remark: '', display_template: '', tag: '' })
}

async function save() {
  if (!user.value) return
  saving.value = true
  const { data, error } = await api.PUT('/api/users/{id}', { params: { path: { id: id.value } }, body: user.value })
  saving.value = false
  if (error) {
    errors.value = splitProblem(error)
    return
  }
  errors.value = emptyErrors()
  user.value = data!
  markClean()
  message.success('已保存')
}

async function resetToken() {
  const { data, error } = await api.POST('/api/users/{id}/reset-token', { params: { path: { id: id.value } } })
  if (error) {
    message.error(splitProblem(error).global.join('；'))
    return
  }
  if (user.value && data) {
    user.value.sub_token = data.sub_token
    markClean()
  }
  message.success('订阅 token 已重置，旧链接立即失效')
}

async function remove() {
  const { error } = await api.DELETE('/api/users/{id}', { params: { path: { id: id.value } } })
  if (error) {
    errors.value = splitProblem(error, '删除失败')
    return
  }
  markClean()
  message.success('已删除')
  router.replace({ name: 'users' })
}
</script>

<template>
  <NSpin :show="!user">
    <NForm v-if="user" label-placement="top" class="user-form">
      <ErrorList :errors="errors.global" />
      <NCard :title="`用户 ${user.name}`" size="small" class="card user-basics">
        <template #header-extra>
          <NSpace>
            <NPopconfirm @positive-click="remove">
              <template #trigger><NButton type="error" ghost>删除</NButton></template>
              删除后，引用该用户的入站会移除这个用户。确定删除？
            </NPopconfirm>
            <NButton type="primary" :loading="saving" :disabled="!dirty" @click="save">保存</NButton>
          </NSpace>
        </template>
        <NFormItem label="名称" required v-bind="f('name', '字母或数字开头，可含 _ . -；改名不影响引用')">
          <NInput v-model:value="user.name" />
        </NFormItem>
        <NFormItem label="启用" :feedback="'切换后当场生效：通过 xray API 移除或加回该用户，配置不删除'">
          <NSwitch :value="user.enabled" :loading="pausing" @update:value="(v) => setPause(v, user!.paused_stacks ?? [])" />
        </NFormItem>
        <NFormItem label="备注" v-bind="f('remark', '仅用于管理识别，不影响订阅节点名')">
          <NInput v-model:value="user.remark" />
        </NFormItem>
        <NFormItem label="展示名模板" v-bind="f('display_template', '适用于 default 档案，优先级高于入站模板；入站中的用户单独配置仍可覆盖此模板')">
          <TemplateInput v-model:value="user.display_template" />
        </NFormItem>
        <NFormItem label="UUID" required v-bind="f('uuid')">
          <SecretInput v-model:value="user.uuid" :generate="randomUUID" :reveal="reveal" />
        </NFormItem>
        <NFormItem label="SS 密码" required v-bind="f('ss_password', '被 2022 系列 shadowsocks 入站引用时需要对应长度的 base64 密钥')">
          <NSpace vertical style="width: 100%">
            <SecretInput v-model:value="user.ss_password" :generate="() => randomString(24)" :reveal="reveal" />
            <NSpace :size="4">
              <NButton size="tiny" quaternary @click="user.ss_password = shadowsocksKey('2022-blake3-aes-128-gcm')">生成 2022 密钥（16 字节）</NButton>
              <NButton size="tiny" quaternary @click="user.ss_password = shadowsocksKey('2022-blake3-aes-256-gcm')">生成 2022 密钥（32 字节）</NButton>
            </NSpace>
          </NSpace>
        </NFormItem>
        <NCollapse style="margin-bottom: 12px">
          <NCollapseItem title="高级" name="advanced">
            <NFormItem label="tag" v-bind="f('tag')">
              <NInput v-model:value="user.tag" />
            </NFormItem>
          </NCollapseItem>
        </NCollapse>
      </NCard>

      <UsageCard :user-id="id" :daily-limit="user.daily_limit_bytes ?? 0" :quota="user.quota_bytes" />

      <NCard title="额度与到期" size="small" class="card">
        <NFormItem label="流量额度" v-bind="f('quota_bytes', '0 表示不限')">
          <ByteSizeInput v-model:value="user.quota_bytes" />
        </NFormItem>
        <NFormItem label="重置周期" v-bind="f('reset_mode')">
          <NRadioGroup v-model:value="user.reset_mode">
            <NRadioButton value="none">不重置</NRadioButton>
            <NRadioButton value="monthly">每月</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem v-if="user.reset_mode === 'monthly'" label="重置日" v-bind="f('reset_day')">
          <NInputNumber v-model:value="user.reset_day" :min="1" :max="28" />
        </NFormItem>
        <NFormItem label="每日用量阈值" v-bind="f('daily_limit_bytes', '0 表示不限；上行与下行合计，按设置时区的自然日统计')">
          <ByteSizeInput small :value="user.daily_limit_bytes ?? 0" @update:value="(value: number) => (user!.daily_limit_bytes = value)" />
        </NFormItem>
        <NFormItem v-if="user.daily_limit_bytes" label="达到阈值后" v-bind="f('daily_limit_action')">
          <NRadioGroup v-model:value="user.daily_limit_action">
            <NRadioButton value="pause">暂停并通知</NRadioButton>
            <NRadioButton value="notify">只通知</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem label="到期时间" v-bind="f('expire_at')">
          <NDatePicker v-model:value="expireAt" type="datetime" clearable />
        </NFormItem>
      </NCard>

      <NCard title="订阅" size="small" class="card">
        <NFormItem label="订阅 token">
          <NSpace vertical style="width: 100%">
            <SecretInput v-model:value="user.sub_token" :reveal="reveal" />
            <NPopconfirm @positive-click="resetToken">
              <template #trigger><NButton size="small">重置 token</NButton></template>
              重置后旧的订阅链接立即失效，客户端需要重新导入。确定重置？
            </NPopconfirm>
          </NSpace>
        </NFormItem>
      </NCard>

      <SubscriptionCard :user-id="id" />

      <NCard title="凭据档案" size="small" class="card">
        <template #header-extra>
          <NButton size="small" @click="addProfile">添加档案</NButton>
        </template>
        <NText depth="3">default 档案即用户本身；入站引用用户时可以改选其他档案，使用不同的凭据。</NText>
        <NEmpty v-if="!user.profiles.length" size="small" description="没有额外档案" style="margin: 12px 0" />
        <div v-for="(profile, index) in user.profiles" :key="profile.id ?? `new-${index}`" class="profile">
          <NGrid cols="1 m:2" responsive="screen" :x-gap="24">
            <NGridItem>
              <NFormItem label="档案名" label-width="70" v-bind="f(`profiles[${index}].name`)">
                <NInput v-model:value="profile.name" />
              </NFormItem>
            </NGridItem>
            <NGridItem>
              <NFormItem label="备注" label-width="70">
                <NInput v-model:value="profile.remark" />
              </NFormItem>
            </NGridItem>
            <NGridItem>
              <NFormItem label="UUID" label-width="70" v-bind="f(`profiles[${index}].uuid`)">
                <SecretInput v-model:value="profile.uuid" :generate="randomUUID" :reveal="reveal" />
              </NFormItem>
            </NGridItem>
            <NGridItem>
              <NFormItem label="SS 密码" label-width="70" v-bind="f(`profiles[${index}].ss_password`)">
                <SecretInput v-model:value="profile.ss_password" :generate="() => randomString(24)" :reveal="reveal" />
              </NFormItem>
            </NGridItem>
            <NGridItem>
              <NFormItem label="展示名" label-width="70" v-bind="f(`profiles[${index}].display_template`)">
                <TemplateInput v-model:value="profile.display_template" />
              </NFormItem>
            </NGridItem>
            <NGridItem>
              <NFormItem label="tag" label-width="70">
                <NSpace style="width: 100%" :wrap="false">
                  <NInput v-model:value="profile.tag" />
                  <NButton type="error" quaternary @click="user.profiles.splice(index, 1)">删除档案</NButton>
                </NSpace>
              </NFormItem>
            </NGridItem>
          </NGrid>
        </div>
      </NCard>

      <NCard title="备注" size="small" class="card">
        <NInput v-model:value="user.note" type="textarea" :autosize="{ minRows: 2 }" />
      </NCard>

      <NCard title="各 stack 暂停" size="small" class="card">
        <NText depth="3" tag="div" style="margin-bottom: 8px">
          只在选中的 stack 上暂停该用户，当场生效。SOCKS5/HTTP 入站的账号由多个用户共用，暂停只把它们从该用户的订阅中去掉。
        </NText>
        <NEmpty v-if="!stacks.length" size="small" description="还没有 stack" />
        <NList v-else>
          <NListItem v-for="stack in stacks" :key="stack.id">
            <NSpace justify="space-between" align="center">
              <RouterLink :to="{ name: 'stack', params: { id: stack.id } }">{{ stack.name }}</RouterLink>
              <NSwitch
                :value="!(user.paused_stacks ?? []).includes(stack.name)"
                :disabled="!user.enabled || pausing"
                @update:value="(v) => toggleStack(stack.name, !v)"
              >
                <template #checked>启用</template>
                <template #unchecked>已暂停</template>
              </NSwitch>
            </NSpace>
          </NListItem>
        </NList>
      </NCard>

      <NCard title="被以下入站引用" size="small" class="card">
        <NEmpty v-if="!references.length" size="small" description="尚未被任何入站引用" />
        <NList v-else>
          <NListItem v-for="ref in references" :key="`${ref.stack}.${ref.inbound}`">
            <RouterLink :to="{ name: 'stack', params: { id: ref.stack_id }, query: { tab: 'inbounds' } }">{{ ref.stack }} · {{ ref.inbound }}</RouterLink>
            <NTag v-if="ref.profile" size="small" style="margin-left: 8px">档案 {{ ref.profile }}</NTag>
          </NListItem>
        </NList>
      </NCard>
    </NForm>
  </NSpin>
</template>

<style scoped>
.user-basics :deep(.n-card-content) {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-items: start;
  column-gap: 24px;
}
.user-basics :deep(.n-collapse) { grid-column: 1 / -1; }
@media (max-width: 1023px) {
  .user-basics :deep(.n-card-content) { grid-template-columns: minmax(0, 1fr); }
}
.card {
  margin-bottom: 20px;
}
.profile {
  border-top: 1px dashed rgba(128, 128, 128, 0.3);
  padding-top: 12px;
  margin-top: 12px;
}
</style>
