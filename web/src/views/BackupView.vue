<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NCheckbox, NDataTable, NDescriptions, NDescriptionsItem, NForm, NFormItem, NInput, NModal, NPopconfirm, NRadioButton, NRadioGroup,
  NSpace, NSwitch, NTag, NText, NTooltip, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, getCSRFToken, handleUnauthorizedResponse, panelBase, problemMessage, type Schemas } from '../api/client'
import DangerConfirm from '../components/DangerConfirm.vue'
import ExportDialog from '../components/ExportDialog.vue'
import ImportDialog from '../components/ImportDialog.vue'
import { useBackgroundTask } from '../composables/useBackgroundTask'
import { formatBytes, formatTime } from '../util/format'

type BackupFile = Schemas['BackupFile']

const message = useMessage()
const showExport = ref(false)
const showImport = ref(false)
const files = ref<BackupFile[]>([])
const trackTask = useBackgroundTask(load)

async function load() {
  files.value = (await api.GET('/api/backup/files')).data ?? []
}
onMounted(load)

async function remove(name: string) {
  const { error } = await api.DELETE('/api/backup/files/{name}', { params: { path: { name } } })
  if (error) message.error(problemMessage(error))
  load()
}

const packing = ref<{ snapshot: string; plain: boolean; confirmed: boolean; passphrase: string; confirm: string } | null>(null)
const openPacking = (snapshot = '') => {
  packing.value = { snapshot, plain: false, confirmed: false, passphrase: '', confirm: '' }
}
const packReady = computed(() => {
  const form = packing.value
  if (!form) return false
  return form.plain ? form.confirmed : form.passphrase.length >= 8 && form.passphrase === form.confirm
})

async function startPacking() {
  const form = packing.value
  if (!form) return
  const { data, error } = await api.POST('/api/backup/export', {
    body: { passphrase: form.passphrase, plain: form.plain, snapshot: form.snapshot },
  })
  if (error) {
    message.error(problemMessage(error))
    return
  }
  packing.value = null
  trackTask(data!.task_id)
}

const columns: DataTableColumns<BackupFile> = [
  { title: '文件', key: 'name' },
  { title: '类型', key: 'kind', width: 100, render: (row) => h(NTag, { size: 'small' }, { default: () => (row.kind === 'package' ? '迁移包' : '数据库快照') }) },
  { title: '大小', key: 'size', width: 110, render: (row) => formatBytes(row.size) },
  { title: '时间', key: 'modified_at', width: 180, render: (row) => formatTime(row.modified_at) },
  {
    title: '', key: 'actions', width: 250,
    render: (row) => h(NSpace, { size: 6 }, {
      default: () => [
        h(NPopconfirm, { positiveText: '下载', onPositiveClick: () => download(row.name) }, {
          trigger: () => h(NButton, { size: 'tiny' }, { default: () => '下载' }),
          default: () => h('div', { style: 'max-width: 300px' }, '该文件包含全部凭据、订阅 token 和管理员账号，下载会记入审计日志。请保存在可信位置，用完及时删除副本。'),
        }),
        row.kind === 'snapshot'
          ? h(NTooltip, null, {
              trigger: () => h(NButton, { size: 'tiny', onClick: () => openPacking(row.name) }, { default: () => '转为迁移包' }),
              default: () => '用这个数据库快照生成整机迁移包，可选口令加密，用于迁移到另一台机器',
            })
          : null,
        h(NPopconfirm, { onPositiveClick: () => remove(row.name) }, {
          trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, { default: () => '删除' }),
          default: () => `删除 ${row.name}？删除后无法恢复。`,
        }),
      ],
    }),
  },
]

/** 备份文件含全部凭据，下载前在按钮旁确认。 */
function download(name: string) {
  window.location.href = `${panelBase}api/backup/files/${encodeURIComponent(name)}/download`
}

/** 恢复流程：上传 → 预览和选项 → 执行。 */
const upload = ref<{ file: File | null; passphrase: string }>({ file: null, passphrase: '' })
const uploading = ref(false)
const preview = ref<Schemas['RestorePreview'] | null>(null)
const restoreError = ref('')
const options = ref({ external_host: '', keep_panel: true, keep_admins: false, start_proxies: true })
const restoring = ref(false)
const restarting = ref(false)
const fileInput = ref<HTMLInputElement>()

async function uploadPackage() {
  const file = upload.value.file
  if (!file) return
  uploading.value = true
  restoreError.value = ''
  const form = new FormData()
  form.append('file', file)
  form.append('passphrase', upload.value.passphrase)
  const response = await fetch(`${panelBase}api/backup/restore/upload`, {
    method: 'POST', body: form, headers: { 'X-CSRF-Token': getCSRFToken() }, credentials: 'same-origin',
  })
  uploading.value = false
  if (handleUnauthorizedResponse(response)) return
  const body = await response.json()
  if (!response.ok) {
    restoreError.value = body.errors?.[0]?.message ?? problemMessage(body, '上传失败')
    return
  }
  preview.value = body as Schemas['RestorePreview']
  options.value.external_host = preview.value.external_host
}

const confirmRestore = ref(false)
/** 按预览和所选项列出恢复的影响范围。 */
const restoreImpacts = computed(() => {
  const value = preview.value
  if (!value) return []
  const opts = options.value
  return [
    `当前数据库被整体替换为迁移包内容：恢复后为 ${value.counts.users} 个用户、${value.counts.stacks} 个 Stack、${value.counts.manual_nodes} 个手工节点，本机现有数据不再保留。`,
    '全部凭据、订阅 token、流量历史和审计日志换成迁移包中的内容，本机现有的订阅链接失效。',
    opts.keep_admins ? '管理员账号保留本机的。' : `管理员账号换成迁移包中的，之后用源主机 ${value.hostname} 的管理员账号登录。`,
    opts.keep_panel ? '面板监听地址和端口保留本机设置。' : '面板监听地址和端口换成迁移包中的，可能需要用新的地址打开面板。',
    `管理服务重启，全部登录会话失效；恢复后${opts.start_proxies ? '启动迁移包中的代理' : '代理保持停止'}。`,
    ...(value.port_conflicts.length ? [`有 ${value.port_conflicts.length} 处端口冲突，恢复后需要在面板中调整。`] : []),
    '执行前自动备份当前数据库。',
  ]
})

async function execute() {
  confirmRestore.value = false
  restoring.value = true
  const { error } = await api.POST('/api/backup/restore/execute', { body: options.value })
  restoring.value = false
  if (error) {
    restoreError.value = error.errors?.[0]?.message ?? problemMessage(error)
    return
  }
  preview.value = null
  restarting.value = true
  waitForRestart()
}

/** 管理服务重启后回到登录页。选择使用迁移包的面板设置时监听地址可能会变，这里等不到，提示手工打开。 */
function waitForRestart() {
  const deadline = Date.now() + 60_000
  const poll = async () => {
    if (Date.now() > deadline) return
    try {
      const response = await fetch(`${panelBase}api/auth/session`)
      if (response.status < 500) {
        window.location.href = `${panelBase}login`
        return
      }
    } catch {
      // 服务还在重启。
    }
    window.setTimeout(poll, 1500)
  }
  window.setTimeout(poll, 3000)
}
</script>

<template>
  <NSpace vertical :size="20">
    <NCard title="整机迁移" size="small">
      <NText depth="3" tag="p">
        迁移包包含数据库中的全部数据：配置、凭据、订阅 token、流量历史、审计日志和管理员账号。不含 xray、mihomo 和 geo 文件，恢复后按源实例的版本重新安装。
      </NText>
      <NButton type="primary" @click="openPacking()">导出迁移包</NButton>
    </NCard>
    <NCard title="导出全部配置" size="small">
      <NText depth="3" tag="p">
        导出系统设置（不含面板监听和管理员）、全部用户和 stack，可在另一台实例导入。流量数据、审计日志和管理员账号不随配置导出。
      </NText>
      <NButton @click="showExport = true">导出</NButton>
    </NCard>
    <NCard title="备份文件" size="small">
      <NAlert type="warning" :bordered="false" style="margin-bottom: 12px">这些文件包含全部凭据、订阅 token 和管理员账号，下载记入审计日志。下载后请保存在可信位置，用完及时删除副本。</NAlert>
      <NDataTable :columns="columns" :data="files" :row-key="(row: BackupFile) => row.name" :scroll-x="760" size="small" :pagination="{ pageSize: 10 }" />
    </NCard>
    <div class="danger-zone-label">以下操作会替换现有数据</div>
    <NCard title="导入配置" size="small" class="danger-card">
      <template #header-extra><NTag size="small" type="warning" :bordered="false">可能替换现有配置</NTag></template>
      <NText depth="3" tag="p">
        支持合并或替换全部。导入只写入配置并生成修订版本，不会自动应用；替换全部前会自动备份数据库。
      </NText>
      <NButton @click="showImport = true">导入</NButton>
    </NCard>
    <NCard title="恢复迁移包" size="small" class="danger-card">
      <template #header-extra><NTag size="small" type="error" :bordered="false">替换整个实例</NTag></template>
      <NAlert v-if="restarting" type="info" :bordered="false">正在重启管理服务，完成后跳转到登录页。如果迁移包改变了面板的监听地址或端口，请用新的地址打开。</NAlert>
      <template v-else>
        <NText depth="3" tag="p">恢复会整体替换当前实例的数据库，执行前自动备份当前数据库，然后重启管理服务。</NText>
        <NAlert v-if="restoreError" type="error" :bordered="false" style="margin-bottom: 12px">{{ restoreError }}</NAlert>
        <NForm v-if="!preview" label-placement="left" label-width="90" style="max-width: 520px">
          <NFormItem label="迁移包">
            <input ref="fileInput" type="file" accept=".pswbak" @change="upload.file = fileInput?.files?.[0] ?? null" />
          </NFormItem>
          <NFormItem label="口令">
            <NInput v-model:value="upload.passphrase" type="password" show-password-on="click" placeholder="未加密的迁移包留空" />
          </NFormItem>
          <NButton :disabled="!upload.file" :loading="uploading" @click="uploadPackage">上传并校验</NButton>
        </NForm>
        <NSpace v-else vertical>
          <NDescriptions :column="2" label-placement="left" bordered size="small">
            <NDescriptionsItem label="源主机">{{ preview.hostname }}</NDescriptionsItem>
            <NDescriptionsItem label="导出时间">{{ formatTime(preview.exported_at) }}</NDescriptionsItem>
            <NDescriptionsItem label="程序版本">{{ preview.app_version }}</NDescriptionsItem>
            <NDescriptionsItem label="用户 / stack">{{ preview.counts.users }} / {{ preview.counts.stacks }}</NDescriptionsItem>
            <NDescriptionsItem label="手工节点 / 修订版本">{{ preview.counts.manual_nodes }} / {{ preview.counts.revisions }}</NDescriptionsItem>
            <NDescriptionsItem label="流量明细">{{ preview.counts.traffic }} 条</NDescriptionsItem>
            <NDescriptionsItem label="流量时间范围">
              <template v-if="preview.counts.traffic">{{ formatTime(preview.traffic_from) }} 至 {{ formatTime(preview.traffic_to) }}</template>
              <template v-else>—</template>
            </NDescriptionsItem>
          </NDescriptions>
          <NAlert v-if="preview.port_conflicts.length" type="warning" :bordered="false" title="端口冲突">
            <div v-for="conflict in preview.port_conflicts" :key="conflict">{{ conflict }}</div>
            可以先恢复，再在 Web 端调整端口。
          </NAlert>
          <NForm label-placement="left" label-width="130" style="max-width: 560px">
            <NFormItem label="external_host">
              <NInput v-model:value="options.external_host" />
            </NFormItem>
            <NFormItem label="面板设置">
              <NRadioGroup v-model:value="options.keep_panel">
                <NRadioButton :value="true">保留本机</NRadioButton>
                <NRadioButton :value="false">使用迁移包</NRadioButton>
              </NRadioGroup>
            </NFormItem>
            <NFormItem label="管理员账号">
              <NRadioGroup v-model:value="options.keep_admins">
                <NRadioButton :value="false">使用迁移包</NRadioButton>
                <NRadioButton :value="true">保留本机</NRadioButton>
              </NRadioGroup>
            </NFormItem>
            <NFormItem label="恢复后启动代理">
              <NSwitch v-model:value="options.start_proxies" />
            </NFormItem>
          </NForm>
          <NSpace>
            <NButton type="error" :loading="restoring" @click="confirmRestore = true">执行恢复</NButton>
            <NButton @click="preview = null">取消</NButton>
          </NSpace>
        </NSpace>
      </template>
    </NCard>
  </NSpace>
  <ExportDialog v-model:show="showExport" scope="all" />
  <ImportDialog v-model:show="showImport" scope="all" />
  <DangerConfirm v-model:show="confirmRestore" title="恢复迁移包" :impacts="restoreImpacts" phrase="恢复" positive-text="执行恢复" :loading="restoring" @confirm="execute" />

  <NModal :show="!!packing" preset="card" title="导出迁移包" style="max-width: 500px" @update:show="(value: boolean) => !value && (packing = null)">
    <NForm v-if="packing" label-placement="left" label-width="100">
      <NFormItem v-if="packing.snapshot" label="使用快照"><NText>{{ packing.snapshot }}</NText></NFormItem>
      <NFormItem label="加密">
        <NRadioGroup v-model:value="packing.plain">
          <NRadioButton :value="false">口令加密</NRadioButton>
          <NRadioButton :value="true">不加密</NRadioButton>
        </NRadioGroup>
      </NFormItem>
      <template v-if="!packing.plain">
        <NFormItem label="口令"><NInput v-model:value="packing.passphrase" type="password" show-password-on="click" placeholder="至少 8 个字符" /></NFormItem>
        <NFormItem label="确认口令"><NInput v-model:value="packing.confirm" type="password" show-password-on="click" /></NFormItem>
      </template>
      <NAlert v-else type="warning" :bordered="false" style="margin-bottom: 12px">
        <NCheckbox v-model:checked="packing.confirmed">我知道迁移包会包含全部明文凭据</NCheckbox>
      </NAlert>
    </NForm>
    <template #footer>
      <NButton type="primary" :disabled="!packReady" @click="startPacking">开始导出</NButton>
    </template>
  </NModal>
</template>

<style scoped>
.danger-zone-label { margin: 4px 0 -8px; font-size: 13px; font-weight: 600; color: var(--ps-danger); }
/* 组件库的边框样式在运行时注入，优先级与此相同，需要 !important 覆盖。 */
.danger-card { border-color: color-mix(in srgb, var(--ps-danger) 45%, var(--ps-border)) !important; }
</style>
