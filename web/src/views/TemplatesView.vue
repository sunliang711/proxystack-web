<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NGrid, NGridItem, NPopconfirm, NSelect, NSpace, NTab, NTabs, NTag, NText, useMessage } from 'naive-ui'
import { api, splitProblem, type Schemas } from '../api/client'
import CodeEditor from '../components/CodeEditor.vue'

type Format = 'clash' | 'premium' | 'surge'
const message = useMessage()
const format = ref<Format>('clash')
// 只切换模板，不需要标签页内容区，用 NTab 让标签文字作为插槽渲染。
const formats: { value: Format; label: string }[] = [
  { value: 'clash', label: 'Clash' },
  { value: 'premium', label: 'Premium Clash' },
  { value: 'surge', label: 'Surge' },
]
const view = ref<Schemas['TemplatesView'] | null>(null)
const drafts = ref<Record<Format, string>>({ clash: '', premium: '', surge: '' })
const users = ref<Schemas['User'][]>([])
const userId = ref<number | null>(null)
const preview = ref('')
const previewError = ref('')
const saving = ref(false)

async function load() {
  const [templates, userList] = await Promise.all([api.GET('/api/sub-templates'), api.GET('/api/users')])
  view.value = templates.data ?? null
  if (view.value) drafts.value = { ...view.value.current }
  users.value = userList.data?.users ?? []
  userId.value ??= users.value[0]?.id ?? null
}
onMounted(load)

const customized = computed(() => !!drafts.value[format.value])
const dirty = computed(() => !!view.value && JSON.stringify(drafts.value) !== JSON.stringify(view.value.current))

async function runPreview() {
  if (!userId.value) return
  const { data, error } = await api.POST('/api/sub-templates/preview', {
    body: { format: format.value, user_id: userId.value, content: drafts.value[format.value] },
  })
  if (error) {
    preview.value = ''
    previewError.value = [...splitProblem(error).global, ...Object.values(splitProblem(error).fields)].join('；')
    return
  }
  previewError.value = ''
  preview.value = data!.text
}
let timer: ReturnType<typeof setTimeout> | undefined
watch([format, userId, () => drafts.value[format.value]], () => {
  clearTimeout(timer)
  timer = setTimeout(runPreview, 400)
})

async function save() {
  saving.value = true
  const { error } = await api.PUT('/api/sub-templates', { body: drafts.value })
  saving.value = false
  if (error) {
    message.error([...splitProblem(error).global, ...Object.values(splitProblem(error).fields)].join('；'))
    return
  }
  message.success('已保存，订阅立即使用新模板')
  load()
}
</script>

<template>
  <NCard>
    <template #header>
      <NSpace justify="end" align="center">
        <NText v-if="dirty" type="warning" class="header-note">未保存</NText>
        <NButton type="primary" :disabled="!dirty" :loading="saving" @click="save">保存</NButton>
      </NSpace>
    </template>
    <NTabs v-model:value="format" type="line">
      <NTab v-for="item in formats" :key="item.value" :name="item.value">{{ item.label }}</NTab>
    </NTabs>
    <NGrid v-if="view" :cols="2" :x-gap="12" responsive="screen" item-responsive>
      <NGridItem span="2 l:1">
        <NSpace align="center" style="margin-bottom: 8px">
          <NTag :type="customized ? 'warning' : 'default'" size="small">{{ customized ? '自定义模板' : '内置模板' }}</NTag>
          <NButton v-if="!customized" size="small" @click="drafts[format] = view.builtin[format]">基于内置模板修改</NButton>
          <NPopconfirm v-else @positive-click="drafts[format] = ''">
            <template #trigger><NButton size="small">恢复内置模板</NButton></template>
            清空自定义内容，恢复使用内置模板？
          </NPopconfirm>
        </NSpace>
        <CodeEditor v-if="customized" v-model:value="drafts[format]" language="text" min-height="520px" />
        <CodeEditor v-else :value="view.builtin[format]" language="text" readonly min-height="520px" />
      </NGridItem>
      <NGridItem span="2 l:1">
        <NSpace align="center" style="margin-bottom: 8px">
          <span>预览用户</span>
          <NSelect v-model:value="userId" :options="users.map((u) => ({ label: u.name, value: u.id! }))" style="width: 180px" />
        </NSpace>
        <NAlert v-if="previewError" type="error" :bordered="false" style="margin-bottom: 8px">{{ previewError }}</NAlert>
        <CodeEditor :value="preview" :language="format === 'surge' ? 'text' : 'yaml'" readonly min-height="520px" />
      </NGridItem>
    </NGrid>
  </NCard>
</template>
