<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import {
  NAlert, NButton, NDataTable, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup, NSpace, NTabPane,
  NTabs, NTag, NText, NUpload, NUploadDragger, useMessage, type DataTableColumns, type UploadFileInfo,
} from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import CodeEditor from './CodeEditor.vue'
import DangerConfirm from './DangerConfirm.vue'
import EntityLink from './EntityLink.vue'

type Entry = Schemas['ImportEntry']
type Preview = Schemas['ImportOutputBody']

const props = defineProps<{ scope: 'all' | 'stacks' | 'users'; allowCsv?: boolean }>()
const emit = defineEmits<{ done: [] }>()
const show = defineModel<boolean>('show', { required: true })
const message = useMessage()

const source = ref<'file' | 'paste'>('file')
const filename = ref('')
const content = ref('')
const pasted = ref('')
const passphrase = ref('')
const mode = ref<'merge' | 'replace'>('merge')
const conflict = ref<'skip' | 'overwrite' | 'rename'>('skip')
const ports = ref<'keep' | 'realloc-conflicts' | 'realloc-all'>('realloc-conflicts')
const excluded = ref<string[]>([])
const preview = ref<Preview | null>(null)
const error = ref('')
const busy = ref(false)

watch(show, (open) => {
  if (open) {
    filename.value = ''
    content.value = ''
    pasted.value = ''
    passphrase.value = ''
    preview.value = null
    excluded.value = []
    error.value = ''
  }
})

const text = computed(() => (source.value === 'file' ? content.value : pasted.value))
const isCSV = computed(() => props.allowCsv && (filename.value.toLowerCase().endsWith('.csv') || /^\s*name\s*,/i.test(text.value)))

async function onFile({ file }: { file: UploadFileInfo }) {
  if (!file.file) return false
  filename.value = file.name
  content.value = await file.file.text()
  preview.value = null
  return false
}

function body() {
  return {
    filename: source.value === 'file' ? filename.value : '',
    content: text.value,
    csv: !!isCSV.value,
    passphrase: passphrase.value,
    mode: mode.value,
    conflict: conflict.value,
    ports: ports.value,
    exclude: excluded.value,
  }
}

async function runPreview() {
  busy.value = true
  error.value = ''
  const { data, error: problem } = await api.POST('/api/config/import/preview', { body: body() })
  busy.value = false
  if (problem) {
    error.value = problemMessage(problem, '解析失败') + (problem.errors?.map((e) => `\n${e.message}`).join('') ?? '')
    preview.value = null
    return
  }
  preview.value = data ?? null
}

const confirmReplace = ref(false)
/** 替换全部的影响范围，数量取自预览。 */
const replaceImpacts = computed(() => {
  const count = (kind: string) => preview.value?.entries.filter((entry) => entry.kind === kind && entry.action !== 'excluded' && entry.action !== 'skip').length ?? 0
  return [
    `现有的全部用户、Stack、手工节点和远端节点源被文件内容整体替换：替换后共 ${count('user')} 个用户、${count('stack')} 个 Stack，文件中没有的会被删除。`,
    ...(count('settings') ? ['系统设置被文件中的设置覆盖。'] : []),
    '被删除的用户在应用后不能再使用代理，订阅中的节点在重新发布订阅包后去掉。',
    '执行前自动备份当前数据库，可以在「备份与还原」中找到。',
    '导入只生成新的配置修订版本，应用后才影响运行中的代理。',
  ]
})

function apply() {
  if (mode.value === 'replace') confirmReplace.value = true
  else void runApply()
}

async function runApply() {
  confirmReplace.value = false
  busy.value = true
  const { data, error: problem } = await api.POST('/api/config/import/apply', { body: body() })
  busy.value = false
  if (problem) {
    error.value = problemMessage(problem, '导入失败')
    return
  }
  if (!data?.valid) {
    preview.value = data ?? null
    return
  }
  message.success('导入完成，已生成新的配置修订版本')
  show.value = false
  emit('done')
}

const actionLabels: Record<string, [string, 'success' | 'warning' | 'default' | 'info' | 'error']> = {
  create: ['新增', 'success'],
  overwrite: ['覆盖', 'warning'],
  skip: ['跳过', 'default'],
  rename: ['重命名导入', 'info'],
  excluded: ['不导入', 'default'],
}

function toggle(key: string, include: boolean) {
  excluded.value = include ? excluded.value.filter((k) => k !== key) : [...excluded.value, key]
  runPreview()
}

const columns: DataTableColumns<Entry> = [
  {
    title: '导入',
    key: 'include',
    width: 60,
    render: (row) =>
      h('input', {
        type: 'checkbox',
        checked: !excluded.value.includes(row.key),
        onChange: (event: Event) => toggle(row.key, (event.target as HTMLInputElement).checked),
      }),
  },
  { title: '类型', key: 'kind', width: 80, render: (row) => (({ settings: '系统设置', user: '用户', stack: 'Stack', manual_node: '手工节点', remote_source: '远端节点源', templates: '订阅模板' }) as Record<string, string>)[row.kind] ?? row.kind },
  {
    title: '名称', key: 'name',
    render: (row) => {
      const name = row.kind === 'stack' || row.kind === 'user'
        ? h(EntityLink, { kind: row.kind, name: row.name, target: '_blank', rel: 'noopener' }) : row.name
      return row.new_name ? h('span', [name, ` → ${row.new_name}`]) : name
    },
  },
  {
    title: '动作',
    key: 'action',
    width: 110,
    render: (row) => {
      const [label, type] = actionLabels[row.action] ?? [row.action, 'default']
      return h(NTag, { size: 'small', type }, { default: () => label })
    },
  },
  {
    title: '说明',
    key: 'notes',
    render: (row) => {
      const notes: string[] = []
      if (row.regenerated_secrets) notes.push('凭据将重新生成，客户端需重新导入订阅')
      for (const change of row.port_changes) notes.push(`${change.field}: ${change.from} → ${change.to}`)
      return notes.join('；')
    },
  },
]
</script>

<template>
  <NModal v-model:show="show" preset="card" title="导入配置" style="max-width: 820px">
    <NTabs v-model:value="source" type="segment" size="small" class="accent-segment">
      <NTabPane name="file" tab="上传文件">
        <NUpload :max="1" :show-file-list="false" :custom-request="() => {}" @before-upload="onFile">
          <NUploadDragger>
            <NText v-if="filename">{{ filename }}（{{ content.length }} 字节），点击或拖入以更换</NText>
            <NText v-else>点击或拖入 YAML / JSON{{ allowCsv ? ' / CSV' : '' }} 文件</NText>
          </NUploadDragger>
        </NUpload>
      </NTabPane>
      <NTabPane name="paste" tab="粘贴文本">
        <CodeEditor v-model:value="pasted" language="yaml" min-height="200px" />
      </NTabPane>
    </NTabs>

    <NForm label-placement="left" label-width="100" style="margin-top: 12px">
      <NFormItem v-if="scope === 'all'" label="导入模式">
        <NRadioGroup v-model:value="mode">
          <NRadioButton value="merge">合并</NRadioButton>
          <NRadioButton value="replace">替换全部</NRadioButton>
        </NRadioGroup>
        <NText v-if="mode === 'replace'" type="error" style="margin-left: 12px; font-size: 13px">文件中没有的用户和 Stack 会被删除</NText>
      </NFormItem>
      <NFormItem label="同名冲突">
        <NRadioGroup v-model:value="conflict">
          <NRadioButton value="skip">跳过</NRadioButton>
          <NRadioButton value="overwrite">覆盖</NRadioButton>
          <NRadioButton value="rename">重命名导入</NRadioButton>
        </NRadioGroup>
      </NFormItem>
      <NFormItem label="端口">
        <NRadioGroup v-model:value="ports">
          <NRadioButton value="keep">保留文件中的端口</NRadioButton>
          <NRadioButton value="realloc-conflicts">冲突时重新分配</NRadioButton>
          <NRadioButton value="realloc-all">全部重新分配</NRadioButton>
        </NRadioGroup>
      </NFormItem>
      <NFormItem v-if="preview?.needs_passphrase || preview?.secrets === 'encrypted' || passphrase" label="口令">
        <NInput v-model:value="passphrase" type="password" show-password-on="click" placeholder="文件已加密，输入导出时设置的口令" />
      </NFormItem>
    </NForm>

    <NAlert v-if="error" type="error" :bordered="false" style="white-space: pre-wrap; margin-bottom: 12px">{{ error }}</NAlert>
    <NAlert v-if="preview?.needs_passphrase" type="info" :bordered="false" style="margin-bottom: 12px">文件已口令加密，请输入口令后重新预览。</NAlert>

    <template v-if="preview && !preview.needs_passphrase">
      <NDataTable :columns="columns" :data="preview.entries" size="small" :max-height="320" />
      <NAlert v-if="preview.issues.length" type="error" title="存在问题，修正后才能导入" style="margin-top: 12px">
        <ul style="margin: 0; padding-left: 18px">
          <li v-for="(issue, i) in preview.issues" :key="i">{{ issue.field ? `${issue.field}: ` : '' }}{{ issue.message }}</li>
        </ul>
      </NAlert>
    </template>

    <template #footer>
      <NSpace justify="end">
        <NButton @click="show = false">取消</NButton>
        <NButton :disabled="!text" :loading="busy" @click="runPreview">预览</NButton>
        <NButton :type="mode === 'replace' ? 'error' : 'primary'" :disabled="!preview?.valid" :loading="busy" @click="apply">{{ mode === 'replace' ? '替换全部' : '导入' }}</NButton>
      </NSpace>
    </template>
  </NModal>
  <DangerConfirm v-model:show="confirmReplace" title="替换全部配置" :impacts="replaceImpacts" phrase="替换全部" positive-text="替换全部" :loading="busy" @confirm="runApply" />
</template>
