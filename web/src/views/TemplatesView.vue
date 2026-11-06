<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  NAlert, NButton, NCard, NCollapse, NCollapseItem, NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NTab, NTabs, NTag, NText, useMessage,
} from 'naive-ui'
import { api, saveBlob, splitProblem, type Schemas } from '../api/client'
import CodeEditor from '../components/CodeEditor.vue'
import EntityLink from '../components/EntityLink.vue'
import { copyText } from '../util/clipboard'

type Format = 'clash' | 'premium' | 'surge'
const message = useMessage()
const format = ref<Format>('clash')
const formats: { value: Format; label: string; file: string }[] = [
  { value: 'clash', label: 'Clash', file: 'YAML' },
  { value: 'premium', label: 'Premium Clash', file: 'YAML' },
  { value: 'surge', label: 'Surge', file: 'conf' },
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

const customized = (item: Format) => !!drafts.value[item]
const changed = (item: Format) => !!view.value && drafts.value[item] !== view.value.current[item]
const dirtyFormats = computed(() => formats.filter((item) => changed(item.value)))
const dirty = computed(() => dirtyFormats.value.length > 0)
const userOptions = computed(() => users.value.map((user) => ({ label: user.remark ? `${user.name}（${user.remark}）` : user.name, value: user.id! })))
const editorLanguage = computed(() => (format.value === 'surge' ? 'text' : 'yaml'))
const lineCount = (text: string) => (text ? text.split('\n').length : 0)

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

function downloadPreview() {
  if (!preview.value || previewing.value || previewError.value) return
  const name = users.value.find((user) => user.id === userId.value)?.name ?? 'user'
  const extension = format.value === 'surge' ? 'conf' : 'yaml'
  const type = format.value === 'surge' ? 'text/plain;charset=utf-8' : 'application/yaml;charset=utf-8'
  saveBlob(new Blob([preview.value], { type }), `${name}-${format.value}.${extension}`)
}

async function save() {
  if (!dirty.value || saving.value) return
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
function discard() {
  if (view.value) drafts.value[format.value] = view.value.current[format.value]
}

/** Cmd/Ctrl+S 保存全部修改。 */
const shortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘S' : 'Ctrl+S'
function onKeydown(event: KeyboardEvent) {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
    event.preventDefault()
    void save()
  }
}

/** 内容区足够宽时模板与预览并排；较窄时用切换按钮在两者之间切换，避免预览被挤到页面下方。 */
const panel = ref<HTMLElement>()
const wide = ref(true)
const pane = ref<'template' | 'preview'>('template')
let observer: ResizeObserver | undefined
onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  observer = new ResizeObserver(([entry]) => { wide.value = (entry?.contentRect.width ?? 0) >= 960 })
})
// 内容区在模板加载后才渲染，出现后再开始观察宽度。
watch(panel, (element, previous) => {
  if (previous) observer?.unobserve(previous)
  if (element) observer?.observe(element)
})
onUnmounted(() => {
  clearTimeout(timer)
  previewVersion++
  window.removeEventListener('keydown', onKeydown)
  observer?.disconnect()
})

/** 模板使用 Jinja 语法（{{ 变量 }}、{% for %}、{% if %}），可用的变量如下。 */
const variables: { name: string; desc: string }[] = [
  { name: 'user', desc: '用户名' },
  { name: 'generated_at', desc: '订阅生成时间' },
  { name: 'proxies | yaml_block', desc: '全部节点的 Clash proxies 列表，已按 YAML 缩进' },
  { name: 'proxy_names', desc: '全部节点名称，可用 for 循环逐个输出' },
  { name: 'proxy_groups | yaml_block', desc: '默认的 Clash 代理组' },
  { name: 'nodes', desc: '节点原始数据（名称、协议、服务器、端口、地区等）' },
  { name: 'surge_proxy_lines', desc: 'Surge [Proxy] 段的节点行' },
  { name: 'surge_proxy_names', desc: 'Surge 节点名称' },
  { name: 'surge_region_groups', desc: '按地区分组的节点，每组含 name、icon_url、proxy_names' },
  { name: 'clash_rules / surge_rules', desc: '默认分流规则' },
  { name: 'test_url', desc: '测速地址' },
  { name: 'managed_config_url', desc: 'Surge 托管配置地址（为空时不输出托管配置行）' },
]
const filters = '过滤器：yaml_block 把列表或对象输出为缩进正确的 YAML，tojson 输出带引号的 JSON 字符串（适合节点名称）。'
async function copyVariable(name: string) {
  const expression = name.split(' / ')[0]!
  await copyText(`{{ ${expression} }}`)
  message.success(`已复制 {{ ${expression} }}`)
}
</script>

<template>
  <NCard class="templates-panel" content-style="padding-top: 0">
    <template #header>
      <NTabs v-model:value="format" type="line" class="format-tabs">
        <NTab v-for="item in formats" :key="item.value" :name="item.value">
          <span class="format-tab">
            {{ item.label }}
            <span v-if="changed(item.value)" class="dot dirty" title="有未保存的修改" />
            <NTag v-else-if="view && customized(item.value)" size="tiny" :bordered="false" type="warning">自定义</NTag>
          </span>
        </NTab>
      </NTabs>
    </template>
    <template #header-extra>
      <NSpace align="center" :size="12" :wrap="false">
        <NText v-if="dirty" type="warning" class="header-note">{{ dirtyFormats.map((item) => item.label).join('、') }} 未保存</NText>
        <NButton type="primary" :disabled="!dirty" :loading="saving" @click="save">保存<span class="shortcut">{{ shortcut }}</span></NButton>
      </NSpace>
    </template>

    <div v-if="view" ref="panel" class="templates-body">
      <div class="toolbar">
        <NSpace align="center" :size="8">
          <NTag :type="customized(format) ? 'warning' : 'default'" size="small" :bordered="false">{{ customized(format) ? '自定义模板' : '内置模板' }}</NTag>
          <NText depth="3" class="toolbar-note">{{ customized(format) ? '订阅按下方内容生成' : '内置模板只读；需要调整时基于它创建自定义模板' }}</NText>
        </NSpace>
        <NSpace align="center" :size="8">
          <NButton v-if="changed(format)" size="small" quaternary @click="discard">撤销本页修改</NButton>
          <NButton v-if="!customized(format)" size="small" type="primary" secondary @click="drafts[format] = view.builtin[format]">基于内置模板修改</NButton>
          <NPopconfirm v-else @positive-click="drafts[format] = ''">
            <template #trigger><NButton size="small">恢复内置模板</NButton></template>
            清空自定义内容，恢复使用内置模板？保存后生效。
          </NPopconfirm>
          <NRadioGroup v-if="!wide" v-model:value="pane" size="small" aria-label="显示内容">
            <NRadioButton value="template">模板</NRadioButton>
            <NRadioButton value="preview">预览</NRadioButton>
          </NRadioGroup>
        </NSpace>
      </div>

      <NCollapse class="syntax-help">
        <NCollapseItem title="模板语法与可用变量" name="help">
          <NText depth="3" class="help-intro">模板使用 Jinja 语法：<code v-pre>{{ 变量 }}</code> 输出变量，<code v-pre>{% for x in 列表 %}…{% endfor %}</code> 循环，<code v-pre>{% if 条件 %}…{% endif %}</code> 判断。点击变量名复制。</NText>
          <div class="variable-grid">
            <button v-for="item in variables" :key="item.name" type="button" class="variable" @click="copyVariable(item.name)">
              <code>{{ item.name }}</code>
              <span>{{ item.desc }}</span>
            </button>
          </div>
          <NText depth="3" class="help-intro">{{ filters }}</NText>
        </NCollapseItem>
      </NCollapse>

      <div class="editors" :class="{ stacked: !wide }">
        <section v-show="wide || pane === 'template'" class="editor-pane">
          <div class="pane-head">
            <NText strong>模板</NText>
            <NText depth="3" class="pane-meta">{{ lineCount(customized(format) ? drafts[format] : view.builtin[format]) }} 行 · {{ formats.find((item) => item.value === format)!.file }}</NText>
          </div>
          <CodeEditor v-if="customized(format)" v-model:value="drafts[format]" :language="editorLanguage" min-height="var(--template-editor-height)" />
          <CodeEditor v-else :value="view.builtin[format]" :language="editorLanguage" readonly min-height="var(--template-editor-height)" />
        </section>
        <section v-show="wide || pane === 'preview'" class="editor-pane">
          <div class="pane-head">
            <NText strong>预览</NText>
            <NSelect v-model:value="userId" :options="userOptions" filterable size="small" placeholder="选择用户" class="user-select" />
            <EntityLink v-if="userId" kind="user" :id="userId" label="↗" />
            <NText depth="3" class="pane-meta">{{ previewing ? '正在生成…' : previewError ? '生成失败' : `${lineCount(preview)} 行` }}</NText>
            <NButton size="small" :disabled="!preview || !!previewError || previewing" @click="downloadPreview">下载</NButton>
          </div>
          <NAlert v-if="previewError" type="error" :bordered="false" class="preview-error">{{ previewError }}</NAlert>
          <NAlert v-else-if="!users.length" type="info" :bordered="false" class="preview-error">还没有用户，创建用户后可以预览订阅内容。</NAlert>
          <CodeEditor :value="preview" :language="editorLanguage" readonly min-height="var(--template-editor-height)" />
        </section>
      </div>
      <NText depth="3" class="footer-note">预览随编辑自动刷新，使用所选用户的节点按当前编辑内容生成，未保存的修改不会影响已发出的订阅。</NText>
    </div>
  </NCard>
</template>

<style scoped>
.templates-panel {
  --template-editor-height: clamp(320px, calc(100vh - 380px), 720px);
  --code-editor-max-height: var(--template-editor-height);
}
.format-tabs { margin-bottom: -1px; }
.format-tab { display: inline-flex; align-items: center; gap: 6px; }
.dot { width: 7px; height: 7px; border-radius: 50%; display: inline-block; }
.dot.dirty { background: #eda100; }
.header-note { font-size: 13px; white-space: nowrap; }
.shortcut { margin-left: 6px; font-size: 11px; opacity: 0.7; }
.templates-body { display: grid; gap: 12px; padding-top: 16px; }
.toolbar { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 16px; }
.toolbar-note { font-size: 12px; }
.syntax-help :deep(.n-collapse-item__header) { font-size: 13px; }
.help-intro { display: block; font-size: 12px; margin-bottom: 8px; }
.help-intro code { font-size: 12px; padding: 0 4px; border-radius: 4px; background: var(--ps-code-bg); }
.variable-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 6px; margin-bottom: 8px; }
.variable {
  display: grid; gap: 2px; text-align: left; padding: 6px 10px; cursor: pointer; font: inherit; color: inherit;
  border: 1px solid var(--ps-border); border-radius: 8px; background: transparent;
}
.variable:hover { border-color: var(--ps-accent); background: var(--ps-accent-soft); }
.variable code { font-size: 12px; color: var(--ps-accent); }
.variable span { font-size: 12px; color: var(--ps-muted); }
.editors { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 16px; }
.editors.stacked { grid-template-columns: minmax(0, 1fr); }
.editor-pane { display: grid; gap: 8px; min-width: 0; align-content: start; }
.pane-head { display: flex; align-items: center; gap: 8px; min-height: 30px; }
.pane-head > * { flex-shrink: 0; }
.pane-meta { font-size: 12px; margin-left: auto; white-space: nowrap; }
.user-select { flex: 0 1 180px; min-width: 110px; }
.preview-error { margin: 0; }
.footer-note { font-size: 12px; }
@media (max-width: 600px) {
  .shortcut { display: none; }
  .pane-head { flex-wrap: wrap; }
  .user-select { flex: 1 1 100%; }
}
</style>
