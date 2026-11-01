<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NCollapse, NCollapseItem, NDataTable, NEmpty, NModal, NPopconfirm, NSpace, NTable, NTag,
  NText, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import ExportDialog from '../components/ExportDialog.vue'
import EntityLink from '../components/EntityLink.vue'
import { formatTime } from '../util/format'

type Revision = Schemas['RevisionItem']
type DiffEntry = Schemas['DiffEntry']

const message = useMessage()
const items = ref<Revision[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const loading = ref(false)
const checked = ref<number[]>([])
const diff = ref<{ title: string; entries: DiffEntry[] } | null>(null)
const exportRevision = ref<number | null>(null)

async function load() {
  loading.value = true
  const { data } = await api.GET('/api/revisions', { params: { query: { limit: pageSize, offset: (page.value - 1) * pageSize } } })
  loading.value = false
  items.value = data?.items ?? []
  total.value = data?.total ?? 0
}
onMounted(load)

async function showDiff(from: number, to: number) {
  const { data, error } = await api.GET('/api/revisions/diff', { params: { query: { from, to } } })
  if (error) {
    message.error(problemMessage(error))
    return
  }
  diff.value = { title: to ? `版本 #${from} → #${to}` : `版本 #${from} → 当前配置`, entries: data?.entries ?? [] }
}

function compareChecked() {
  const [a, b] = [...checked.value].sort((x, y) => x - y)
  showDiff(a, b)
}

async function rollback(id: number) {
  const { error } = await api.POST('/api/revisions/{id}/rollback', { params: { path: { id } } })
  if (error) {
    message.error(problemMessage(error, '回滚失败') + (error.errors?.map((e) => `；${e.message}`).join('') ?? ''))
    return
  }
  message.success(`已回滚到版本 #${id}，并生成了新版本`)
  load()
}

const columns: DataTableColumns<Revision> = [
  { type: 'selection', disabled: (row) => checked.value.length >= 2 && !checked.value.includes(row.id) },
  { title: '版本', key: 'id', width: 80, render: (row) => `#${row.id}` },
  { title: '时间', key: 'created_at', width: 190, render: (row) => formatTime(row.created_at) },
  { title: '操作者', key: 'actor', width: 100 },
  { title: '摘要', key: 'summary' },
  {
    title: '操作',
    key: 'actions',
    width: 260,
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'small', onClick: () => showDiff(row.id, 0) }, () => '与当前比较'),
        h(NButton, { size: 'small', onClick: () => (exportRevision.value = row.id) }, () => '导出'),
        h(NPopconfirm, { onPositiveClick: () => rollback(row.id) }, {
          trigger: () => h(NButton, { size: 'small', type: 'warning', ghost: true }, () => '回滚'),
          default: () => '把配置恢复为这个版本的内容，并生成一个新版本？',
        }),
      ]),
  },
]

const changeLabels: Record<string, [string, 'success' | 'error' | 'warning']> = {
  added: ['新增', 'success'],
  removed: ['删除', 'error'],
  modified: ['修改', 'warning'],
}
const kindLabels: Record<string, string> = { settings: '系统设置', user: '用户', stack: 'Stack', manual_node: '手工节点', remote_source: '远端节点源', templates: '订阅模板' }
const showExport = computed({
  get: () => exportRevision.value !== null,
  set: (value: boolean) => {
    if (!value) exportRevision.value = null
  },
})
</script>

<template>
  <NCard title="版本记录" class="table-panel">
    <template #header-extra>
      <NSpace justify="end">
        <NButton :disabled="checked.length !== 2" @click="compareChecked">比较所选两个版本</NButton>
      </NSpace>
    </template>
    <NDataTable
      v-model:checked-row-keys="checked"
      remote
      :columns="columns"
      :data="items"
      :scroll-x="1000"
      :loading="loading"
      :row-key="(row: Revision) => row.id"
      :pagination="{ page, pageSize, itemCount: total, onChange: (p: number) => ((page = p), load()) }"
    />
  </NCard>

  <NModal :show="!!diff" preset="card" :title="diff?.title" style="max-width: 900px" @update:show="(v) => !v && (diff = null)">
    <NEmpty v-if="diff && !diff.entries.length" description="两个版本没有差异" />
    <NCollapse v-else-if="diff">
      <NCollapseItem v-for="(entry, i) in diff.entries" :key="i" :name="i">
        <template #header>
          <NSpace align="center" :size="6">
            <NTag size="small" :type="changeLabels[entry.change][1]">{{ changeLabels[entry.change][0] }}</NTag>
            <NText>{{ kindLabels[entry.kind] }} <EntityLink v-if="entry.kind === 'stack' || entry.kind === 'user'" :kind="entry.kind" :name="entry.name" @navigate="diff = null" /><template v-else>{{ entry.name }}</template></NText>
            <NText v-if="entry.fields.length" depth="3">{{ entry.fields.length }} 处字段</NText>
          </NSpace>
        </template>
        <NTable v-if="entry.fields.length" size="small" :single-line="false">
          <thead><tr><th>字段</th><th>原值</th><th>新值</th></tr></thead>
          <tbody>
            <tr v-for="field in entry.fields" :key="field.path">
              <td><code>{{ field.path }}</code></td>
              <td><code>{{ field.old }}</code></td>
              <td><code>{{ field.new }}</code></td>
            </tr>
          </tbody>
        </NTable>
        <NAlert v-else type="info" :bordered="false">整体{{ changeLabels[entry.change][0] }}</NAlert>
      </NCollapseItem>
    </NCollapse>
  </NModal>

  <ExportDialog v-model:show="showExport" scope="all" :revision-id="exportRevision ?? undefined" />
</template>
