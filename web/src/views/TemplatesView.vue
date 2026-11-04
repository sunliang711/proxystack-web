<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NGrid, NGridItem, NPopconfirm, NSelect, NSpace, NTab, NTabs, NTag, NText, useMessage } from 'naive-ui'
import { api, saveBlob, splitProblem, type Schemas } from '../api/client'
import CodeEditor from '../components/CodeEditor.vue'
import EntityLink from '../components/EntityLink.vue'

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
const previewing = ref(false)
let previewVersion = 0

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
  const version = previewVersion
  previewing.value = true
  const { data, error } = await api.POST('/api/sub-templates/preview', {
    body: { format: format.value, user_id: userId.value, content: drafts.value[format.value] },
  })
  if (version !== previewVersion) return
  previewing.value = false
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
  previewVersion++
  preview.value = ''
  previewError.value = ''
  previewing.value = !!userId.value
  timer = setTimeout(runPreview, 400)
})

onUnmounted(() => {
  clearTimeout(timer)
  previewVersion++
})

function downloadPreview() {
  if (!preview.value || previewing.value || previewError.value) return
  const name = users.value.find((user) => user.id === userId.value)?.name ?? 'user'
  const extension = format.value === 'surge' ? 'conf' : 'yaml'
  const type = format.value === 'surge' ? 'text/plain;charset=utf-8' : 'application/yaml;charset=utf-8'
  saveBlob(new Blob([preview.value], { type }), `${name}-${format.value}.${extension}`)
}

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
  <NCard title="模板编辑与预览" class="templates-panel">
    <template #header-extra>
      <NSpace justify="end" align="center">
        <NText v-if="dirty" type="warning" class="header-note">未保存</NText>
        <NText v-else-if="!customized" depth="3" class="header-note">正在使用内置模板，无需保存；点「基于内置模板修改」后可编辑</NText>
        <NButton type="primary" :disabled="!dirty" :loading="saving" @click="save">保存</NButton>
      </NSpace>
    </template>
    <NTabs v-model:value="format" type="line">
      <NTab v-for="item in formats" :key="item.value" :name="item.value">{{ item.label }}</NTab>
    </NTabs>
    <NGrid v-if="view" :cols="2" :x-gap="24" :y-gap="24" responsive="screen" item-responsive>
      <NGridItem span="2 l:1">
        <NSpace align="center" style="min-height: 36px; margin: 16px 0 12px">
          <NTag :type="customized ? 'warning' : 'default'" size="small">{{ customized ? '自定义模板' : '内置模板' }}</NTag>
          <NButton v-if="!customized" size="small" @click="drafts[format] = view.builtin[format]">基于内置模板修改</NButton>
          <NPopconfirm v-else @positive-click="drafts[format] = ''">
            <template #trigger><NButton size="small">恢复内置模板</NButton></template>
            清空自定义内容，恢复使用内置模板？
          </NPopconfirm>
        </NSpace>
        <CodeEditor v-if="customized" v-model:value="drafts[format]" language="text" min-height="var(--template-editor-height)" />
        <CodeEditor v-else :value="view.builtin[format]" language="text" readonly min-height="var(--template-editor-height)" />
      </NGridItem>
      <NGridItem span="2 l:1">
        <NSpace align="center" style="min-height: 36px; margin: 16px 0 12px">
          <span>预览用户</span>
          <NSelect v-model:value="userId" :options="users.map((u) => ({ label: u.name, value: u.id! }))" style="width: 180px" />
          <EntityLink v-if="userId" kind="user" :id="userId" label="查看用户 ↗" target="_blank" rel="noopener" />
          <NButton size="small" :loading="previewing" :disabled="!preview || !!previewError || previewing" @click="downloadPreview">下载到文件</NButton>
        </NSpace>
        <NAlert v-if="previewError" type="error" :bordered="false" style="margin-bottom: 8px">{{ previewError }}</NAlert>
        <CodeEditor :value="preview" :language="format === 'surge' ? 'text' : 'yaml'" readonly min-height="var(--template-editor-height)" />
      </NGridItem>
    </NGrid>
  </NCard>
</template>

<style scoped>
.templates-panel {
  --template-editor-height: clamp(320px, 60vh, 600px);
  --code-editor-max-height: var(--template-editor-height);
}
</style>
