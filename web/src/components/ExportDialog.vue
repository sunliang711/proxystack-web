<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup, NSpace, NSwitch, useMessage } from 'naive-ui'
import { api, problemMessage, saveBlob } from '../api/client'
import EntityLink from './EntityLink.vue'

const props = defineProps<{
  scope: 'all' | 'stacks' | 'users' | 'manual_nodes' | 'templates'
  stacks?: string[]
  users?: string[]
  manualNodes?: number[]
  revisionId?: number
  allowCsv?: boolean
}>()
const show = defineModel<boolean>('show', { required: true })
const message = useMessage()

const format = ref<'yaml' | 'json' | 'csv'>('yaml')
const secrets = ref<'encrypted' | 'omitted' | 'plain'>('encrypted')
const passphrase = ref('')
const confirm = ref('')
const includeUsers = ref(true)
const plainConfirmed = ref(false)
const busy = ref(false)
const error = ref('')

watch(show, (open) => {
  if (open) {
    passphrase.value = ''
    confirm.value = ''
    plainConfirmed.value = false
    error.value = ''
  }
})
watch(format, (value) => {
  if (value === 'csv' && secrets.value === 'encrypted') secrets.value = 'omitted'
})

const scopeLabel = computed(() => {
  if (props.scope === 'stacks') return `stack：${props.stacks?.join('、')}`
  if (props.scope === 'users') return `用户：${props.users?.join('、')}`
  if (props.scope === 'manual_nodes') return `手工节点：${props.manualNodes?.length ?? 0} 个`
  if (props.scope === 'templates') return '订阅模板'
  return props.revisionId ? `版本 #${props.revisionId} 的全部配置` : '全部配置'
})

const ready = computed(() => {
  if (secrets.value === 'encrypted') return passphrase.value.length >= 8 && passphrase.value === confirm.value
  if (secrets.value === 'plain') return plainConfirmed.value
  return true
})

async function download() {
  busy.value = true
  error.value = ''
  const { data, error: problem, response } = await api.POST('/api/config/export', {
    body: {
      scope: props.scope,
      stacks: props.stacks ?? [],
      users: props.users ?? [],
      manual_nodes: props.manualNodes ?? [],
      include_referenced_users: includeUsers.value,
      revision_id: props.revisionId ?? 0,
      format: format.value,
      secrets: secrets.value,
      passphrase: passphrase.value,
    },
    parseAs: 'blob',
  })
  busy.value = false
  if (problem || !data) {
    error.value = problemMessage(problem, '导出失败')
    return
  }
  const disposition = response.headers.get('Content-Disposition') ?? ''
  const filename = /filename="([^"]+)"/.exec(disposition)?.[1] ?? `proxystack-export.${format.value}`
  saveBlob(data as Blob, filename)
  message.success('已导出')
  show.value = false
}
</script>

<template>
  <NModal v-model:show="show" preset="card" title="导出配置" style="max-width: 560px">
    <NForm label-placement="left" label-width="110">
      <NFormItem label="范围">
        <NSpace v-if="scope === 'stacks' || scope === 'users'" :size="8">
          <span>{{ scope === 'stacks' ? 'Stack：' : '用户：' }}</span>
          <EntityLink v-for="name in scope === 'stacks' ? stacks : users" :key="name" :kind="scope === 'stacks' ? 'stack' : 'user'" :name="name" target="_blank" rel="noopener" />
        </NSpace>
        <template v-else>{{ scopeLabel }}</template>
      </NFormItem>
      <NFormItem v-if="scope === 'stacks'" label="附带引用的用户">
        <NSwitch v-model:value="includeUsers" />
      </NFormItem>
      <NFormItem label="格式">
        <NRadioGroup v-model:value="format">
          <NRadioButton value="yaml">YAML</NRadioButton>
          <NRadioButton value="json">JSON</NRadioButton>
          <NRadioButton v-if="allowCsv && scope === 'users'" value="csv">CSV</NRadioButton>
        </NRadioGroup>
      </NFormItem>
      <NFormItem label="凭据">
        <NRadioGroup v-model:value="secrets">
          <NRadioButton value="encrypted" :disabled="format === 'csv'">口令加密</NRadioButton>
          <NRadioButton value="omitted">不包含</NRadioButton>
          <NRadioButton value="plain">明文</NRadioButton>
        </NRadioGroup>
      </NFormItem>
      <template v-if="secrets === 'encrypted'">
        <NFormItem label="口令" :feedback="'至少 8 个字符，导入时需要输入同一口令'">
          <NInput v-model:value="passphrase" type="password" show-password-on="click" />
        </NFormItem>
        <NFormItem
          label="确认口令"
          :validation-status="confirm && confirm !== passphrase ? 'error' : undefined"
          :feedback="confirm && confirm !== passphrase ? '两次输入不一致' : undefined"
        >
          <NInput v-model:value="confirm" type="password" show-password-on="click" />
        </NFormItem>
      </template>
      <NAlert v-if="secrets === 'omitted'" type="info" :bordered="false" style="margin-bottom: 12px">
        UUID、密码、controller secret 和订阅 token 不写入文件，导入时重新生成，客户端需要重新导入订阅。
        上游节点和外部出站的凭据属于外部服务，仍原样写出。
      </NAlert>
      <NAlert v-if="secrets === 'plain'" type="warning" :bordered="false" style="margin-bottom: 12px">
        <NSpace vertical>
          <span>文件中的全部凭据都是明文，任何拿到文件的人都能使用这些代理。</span>
          <NSwitch v-model:value="plainConfirmed"><template #checked>我已了解风险</template><template #unchecked>我已了解风险</template></NSwitch>
        </NSpace>
      </NAlert>
      <NAlert v-if="error" type="error" :bordered="false" style="margin-bottom: 12px">{{ error }}</NAlert>
    </NForm>
    <template #footer>
      <NSpace justify="end">
        <NButton @click="show = false">取消</NButton>
        <NButton type="primary" :disabled="!ready" :loading="busy" @click="download">导出</NButton>
      </NSpace>
    </template>
  </NModal>
</template>
