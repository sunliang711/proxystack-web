<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  NAlert, NButton, NCard, NDataTable, NDescriptions, NDescriptionsItem, NDrawer, NDrawerContent, NForm, NFormItem,
  NGrid, NGridItem, NInput, NModal, NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NSpin, NSwitch, NTag, NText,
  NUpload, NUploadDragger, useMessage, type DataTableColumns, type UploadFileInfo,
} from 'naive-ui'
import { api, getCSRFToken, handleUnauthorizedResponse, panelBase, problemMessage, type Schemas } from '../api/client'
import { useBackgroundTask } from '../composables/useBackgroundTask'
import { formatTime } from '../util/format'

type Dep = Schemas['DepStatus']
type Component = 'xray' | 'mihomo' | 'geo'
type Restart = 'sequential' | 'all' | 'none'

const message = useMessage()
const components = ref<Dep[]>([])
const loading = ref(true)
const checking = ref(false)
const trackTask = useBackgroundTask(load)
const restore = ref<Schemas['RestoreStatus'] | null>(null)
const titles: Record<string, string> = { xray: 'Xray', mihomo: 'Mihomo', geo: 'Geo 数据' }

async function load() {
  restore.value = (await api.GET('/api/backup/restore/status')).data ?? null
  const { data } = await api.GET('/api/deps')
  components.value = data?.components ?? []
  loading.value = false
}
onMounted(load)

async function checkUpdates() {
  checking.value = true
  const { error } = await api.POST('/api/deps/check')
  checking.value = false
  if (error) message.error(problemMessage(error, '检查失败') + (error.errors?.map((e) => `：${e.message}`).join('') ?? ''))
  load()
}

// 安装对话框：升级到最新、安装指定版本、上传安装包。
const dialog = ref<{ component: Component; mode: 'latest' | 'version' | 'upload'; version: string; restart: Restart } | null>(null)
const releases = ref<Schemas['Release'][]>([])
const includePre = ref(false)
const loadingReleases = ref(false)
const upload = ref<{ file: File | null; sha256: string }>({ file: null, sha256: '' })
const notes = ref<Schemas['Release'] | null>(null)

async function loadReleases() {
  if (!dialog.value) return
  loadingReleases.value = true
  const { data, error } = await api.GET('/api/deps/{component}/releases', {
    params: { path: { component: dialog.value.component }, query: { prerelease: includePre.value } },
  })
  loadingReleases.value = false
  if (error) {
    message.error(problemMessage(error, '获取版本列表失败') + (error.errors?.map((e) => `：${e.message}`).join('') ?? ''))
    return
  }
  releases.value = data?.releases ?? []
}

function open(component: Component, mode: 'latest' | 'version' | 'upload') {
  dialog.value = { component, mode, version: '', restart: 'sequential' }
  upload.value = { file: null, sha256: '' }
  releases.value = []
  if (mode === 'version') loadReleases()
}

async function install() {
  const d = dialog.value!
  if (d.mode === 'upload') {
    const form = new FormData()
    form.append('file', upload.value.file!)
    form.append('sha256', upload.value.sha256.trim())
    form.append('restart', d.restart)
    const response = await fetch(`${panelBase}api/deps/${d.component}/upload`, {
      method: 'POST',
      body: form,
      headers: { 'X-CSRF-Token': getCSRFToken() },
      credentials: 'same-origin',
    })
    if (handleUnauthorizedResponse(response)) return
    const body = await response.json()
    if (!response.ok) {
      message.error(problemMessage(body, '上传失败') + (body.errors?.map((e: { message: string }) => `：${e.message}`).join('') ?? ''))
      return
    }
    trackTask(body.task_id)
  } else {
    const { data, error } = await api.POST('/api/deps/{component}/install', {
      params: { path: { component: d.component } },
      body: { version: d.mode === 'latest' ? 'latest' : d.version, restart: d.restart },
    })
    if (error) {
      message.error(problemMessage(error, '安装失败'))
      return
    }
    trackTask(data!.task_id)
  }
  dialog.value = null
}

async function installSource(component: Component) {
  const { data, error } = await api.POST('/api/deps/{component}/install', {
    params: { path: { component } },
    body: { version: restore.value?.deps[component] ?? 'latest', restart: 'sequential' },
  })
  if (error) {
    message.error(problemMessage(error, '安装失败'))
    return
  }
  trackTask(data!.task_id)
}

async function rollback(component: Component) {
  const { data, error } = await api.POST('/api/deps/{component}/rollback', {
    params: { path: { component } },
    body: { restart: 'sequential' },
  })
  if (error) {
    message.error(problemMessage(error, '回滚失败'))
    return
  }
  trackTask(data!.task_id)
}

const historyColumns: DataTableColumns<Schemas['DepInstall']> = [
  { title: '时间', key: 'finished_at', render: (row) => formatTime(row.finished_at) },
  { title: '版本', key: 'version' },
  { title: '来源', key: 'source', ellipsis: { tooltip: true } },
  { title: '操作者', key: 'actor', width: 80 },
  {
    title: '结果',
    key: 'result',
    render: (row) =>
      h(NTag, { size: 'small', type: row.result === 'succeeded' ? 'success' : row.result === 'rolled_back' ? 'warning' : 'error' }, () =>
        ({ succeeded: '成功', failed: '失败', rolled_back: '已回滚' })[row.result],
      ),
  },
  { title: '原因', key: 'error', ellipsis: { tooltip: true } },
]

function onFile({ file }: { file: UploadFileInfo }) {
  upload.value.file = file.file ?? null
  return false
}
</script>

<template>
  <NSpin :show="loading">
    <NAlert v-if="restore?.pending" type="warning" :bordered="false" title="恢复尚未完成" style="margin-bottom: 12px">
      <template v-if="restore.missing.length">
        安装缺失的依赖后自动重新生成配置并启动 stack。源实例的版本：
        <NSpace style="margin-top: 8px">
          <NButton v-for="component in restore.missing" :key="component" size="small" @click="installSource(component as Component)">
            安装 {{ component }} {{ restore.deps[component] ?? 'latest' }}
          </NButton>
        </NSpace>
      </template>
      <template v-else>依赖已就绪，正在重新生成配置。</template>
    </NAlert>
    <NCard size="small" style="margin-bottom: 20px">
      <NSpace justify="space-between" align="center">
        <NText depth="3">
          下载经由 <RouterLink :to="{ name: 'settings', query: { section: 'download_proxy' } }">系统设置 → 下载代理</RouterLink>。
          新版本由 <RouterLink :to="{ name: 'jobs' }">定时任务</RouterLink> 中的「依赖版本检查」按计划自动检查。
        </NText>
        <NButton :loading="checking" @click="checkUpdates">立即检查更新</NButton>
      </NSpace>
    </NCard>
    <NGrid :cols="1" :y-gap="20">
      <NGridItem v-for="dep in components" :key="dep.component">
        <NCard :title="titles[dep.component]" size="small">
          <template #header-extra>
            <NSpace>
              <NTag v-if="!dep.installed" type="warning" size="small">未安装</NTag>
              <NTag v-else-if="dep.latest" type="success" size="small" :bordered="false">已是最新</NTag>
              <NButton v-if="!dep.installed || dep.update_available" size="small" type="primary" @click="open(dep.component, 'latest')">
                {{ dep.installed ? `升级到 ${dep.latest}` : '安装最新版' }}
              </NButton>
              <NButton v-if="dep.component !== 'geo'" size="small" @click="open(dep.component, 'version')">安装指定版本</NButton>
              <NButton size="small" @click="open(dep.component, 'upload')">上传安装包</NButton>
              <NPopconfirm v-if="dep.previous" @positive-click="rollback(dep.component)">
                <template #trigger><NButton size="small">{{ dep.previous_version ? `回滚到 ${dep.previous_version}` : '回滚' }}</NButton></template>
                切换回上一个版本{{ dep.previous_version ? `（${dep.previous_version}）` : '' }}，并逐个重启使用它的服务？
              </NPopconfirm>
            </NSpace>
          </template>
          <NDescriptions :column="2" label-placement="left" size="small" class="dep-info">
            <NDescriptionsItem label="当前版本">{{ dep.version || (dep.installed ? '未知' : '—') }}</NDescriptionsItem>
            <NDescriptionsItem label="最新版本">{{ dep.latest || '尚未检查' }}</NDescriptionsItem>
            <NDescriptionsItem label="安装时间">{{ dep.installed_at && !dep.installed_at.startsWith('0001') ? formatTime(dep.installed_at) : '—' }}</NDescriptionsItem>
            <NDescriptionsItem label="来源">{{ dep.source || '—' }}</NDescriptionsItem>
            <NDescriptionsItem label="平台">{{ dep.platform }}</NDescriptionsItem>
            <NDescriptionsItem label="使用中的 stack">{{ dep.used_by.join('、') || '无' }}</NDescriptionsItem>
            <NDescriptionsItem label="sha256" :span="2"><code style="word-break: break-all">{{ dep.sha256 || '—' }}</code></NDescriptionsItem>
          </NDescriptions>
          <NDataTable v-if="dep.history.length" :columns="historyColumns" :data="dep.history" :scroll-x="960" size="small" :max-height="200" style="margin-top: 8px" />
        </NCard>
      </NGridItem>
    </NGrid>
  </NSpin>

  <NModal :show="!!dialog" preset="card" style="max-width: 560px" :title="dialog ? `${titles[dialog.component]}：${{ latest: '安装最新版', version: '安装指定版本', upload: '上传安装包' }[dialog.mode]}` : ''" @update:show="(v) => !v && (dialog = null)">
    <NForm v-if="dialog" label-placement="left" label-width="100">
      <template v-if="dialog.mode === 'version'">
        <NFormItem label="包含预发布">
          <NSwitch v-model:value="includePre" @update:value="loadReleases" />
        </NFormItem>
        <NFormItem label="版本">
          <NSelect
            v-model:value="dialog.version"
            :loading="loadingReleases"
            :options="releases.map((r) => ({ label: `${r.tag}${r.prerelease ? '（预发布）' : ''} · ${new Date(r.published_at).toLocaleDateString()}`, value: r.tag }))"
            filterable
            tag
            placeholder="选择或输入版本号，可降级"
          />
        </NFormItem>
        <NButton v-if="dialog.version" text type="primary" style="margin: -8px 0 12px 100px" @click="notes = releases.find((r) => r.tag === dialog!.version) ?? null">
          查看更新说明
        </NButton>
      </template>
      <template v-if="dialog.mode === 'upload'">
        <NUpload :max="1" :show-file-list="false" :custom-request="() => {}" @before-upload="onFile">
          <NUploadDragger>{{ upload.file ? upload.file.name : '点击或拖入 release 归档或二进制' }}</NUploadDragger>
        </NUpload>
        <NFormItem label="sha256" required style="margin-top: 12px">
          <NInput v-model:value="upload.sha256" placeholder="64 位十六进制" />
        </NFormItem>
      </template>
      <NFormItem label="替换后">
        <NRadioGroup v-model:value="dialog.restart">
          <NRadioButton value="sequential">逐个重启</NRadioButton>
          <NRadioButton value="all">同时重启全部</NRadioButton>
          <NRadioButton value="none">暂不重启</NRadioButton>
        </NRadioGroup>
      </NFormItem>
      <NAlert v-if="dialog.restart === 'none'" type="info" :bordered="false">新版本在下次应用或手动重启时生效。</NAlert>
    </NForm>
    <template #footer>
      <NSpace justify="end">
        <NButton @click="dialog = null">取消</NButton>
        <NButton
          type="primary"
          :disabled="!dialog || (dialog.mode === 'version' && !dialog.version) || (dialog.mode === 'upload' && (!upload.file || !/^[0-9a-fA-F]{64}$/.test(upload.sha256.trim())))"
          @click="install"
        >
          开始安装
        </NButton>
      </NSpace>
    </template>
  </NModal>

  <NDrawer :show="!!notes" :width="560" @update:show="(v) => !v && (notes = null)">
    <NDrawerContent :title="notes ? `${notes.tag} 更新说明` : ''" closable>
      <pre style="white-space: pre-wrap; font-family: inherit">{{ notes?.notes || '没有更新说明' }}</pre>
    </NDrawerContent>
  </NDrawer>

</template>

<style scoped>
/* 固定列宽，让各依赖卡片的两列信息上下对齐。 */
.dep-info :deep(.n-descriptions-table) { table-layout: fixed; }
</style>
