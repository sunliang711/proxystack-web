<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NPopconfirm, NSpace, NSwitch,
  NTag, NText, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, emptyErrors, fieldState, splitProblem, type FormErrors, type Schemas } from '../api/client'
import ErrorList from '../components/ErrorList.vue'
import SecretInput from '../components/SecretInput.vue'
import { formatTime } from '../util/format'

type Source = Schemas['RemoteSourceView']
type Plain = Schemas['RemoteSource']

const message = useMessage()
const sources = ref<Source[]>([])
const errors = ref<FormErrors>(emptyErrors())
const syncing = ref<number | null>(null)

async function load() {
  sources.value = (await api.GET('/api/remote-sources')).data ?? []
}
onMounted(load)

const strip = (s: Source): Plain => ({ id: s.id, name: s.name, url: s.url, token: s.token, enabled: s.enabled })

async function save(next: Plain[], success: string): Promise<boolean> {
  const { data, error } = await api.PUT('/api/remote-sources', { body: next })
  if (error) {
    errors.value = splitProblem(error)
    return false
  }
  errors.value = emptyErrors()
  sources.value = data ?? []
  message.success(success)
  return true
}

async function sync(id: number) {
  syncing.value = id
  const { data } = await api.POST('/api/remote-sources/sync', { body: { id } })
  syncing.value = null
  if (data?.error) message.error(data.error, { duration: 8000 })
  else message.success('同步完成')
  load()
}

const columns: DataTableColumns<Source> = [
  { title: '名称', key: 'name' },
  { title: '地址', key: 'url', ellipsis: { tooltip: true } },
  { title: '启用', key: 'enabled', width: 70, render: (row) => (row.enabled ? '是' : '否') },
  {
    title: '最近同步',
    key: 'status',
    render: (row) =>
      row.status.last_sync_at.startsWith('0001') || row.status.last_sync_at.startsWith('1970')
        ? '从未同步'
        : h(NSpace, { size: 4, vertical: true }, () => [
            `${formatTime(row.status.last_sync_at)} · ${row.status.node_count} 个节点`,
            row.status.last_error ? h(NText, { type: 'error' }, () => row.status.last_error) : h(NTag, { size: 'small', type: 'success' }, () => '成功'),
          ]),
  },
  {
    title: '操作',
    key: 'actions',
    width: 200,
    render: (row, index) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'small', loading: syncing.value === row.id, onClick: () => sync(row.id!) }, () => '同步'),
        h(NButton, { size: 'small', onClick: () => open(index) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => save(sources.value.filter((_, i) => i !== index).map(strip), '已删除') }, {
          trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '删除'),
          default: () => '删除这个远端节点源？',
        }),
      ]),
  },
]

const editing = ref<{ index: number; draft: Plain } | null>(null)
/** 抽屉打开时 editing 非空，模板中用这个别名避免多处非空断言。 */
const draft = computed(() => editing.value?.draft as Plain)
function open(index: number) {
  const source = index < sources.value.length ? strip(sources.value[index]) : { name: '', url: '', token: '', enabled: true }
  editing.value = { index, draft: { ...source } }
}
async function confirm() {
  const { index, draft } = editing.value!
  const next = sources.value.map(strip)
  if (index < next.length) next[index] = draft
  else next.push(draft)
  if (await save(next, '已保存')) editing.value = null
}
const f = (path: string, hint?: string) => fieldState(errors.value, `[${editing.value?.index ?? 0}].${path}`, hint)
</script>

<template>
  <NCard title="节点来源" class="table-panel">
    <template #header-extra>
      <NSpace justify="end">
        <NButton :loading="syncing === 0" @click="sync(0)">全部同步</NButton>
        <NButton type="primary" @click="open(sources.length)">添加</NButton>
      </NSpace>
    </template>
    <NAlert type="info" :bordered="false" style="margin-bottom: 12px">
      在另一台 proxystack-web 的「系统设置 → 节点导出」中开启导出，把那里显示的导出地址和 token 填到这里。
      拉取到的节点按用户名分给本机同名用户，每 30 分钟自动同步一次。
    </NAlert>
    <ErrorList :errors="errors.global" />
    <NDataTable :columns="columns" :data="sources" :scroll-x="860" size="small" />
  </NCard>

  <NDrawer :show="!!editing" :width="520" @update:show="(v) => !v && (editing = null)">
    <NDrawerContent v-if="editing" :title="editing.draft.name || '新的远端节点源'" closable>
      <NForm label-placement="left" label-width="90">
        <NFormItem label="名称" v-bind="f('name')"><NInput v-model:value="draft.name" /></NFormItem>
        <NFormItem label="导出地址" v-bind="f('url', '形如 https://other.example.com:9444/export/nodes')"><NInput v-model:value="draft.url" /></NFormItem>
        <NFormItem label="token" v-bind="f('token')"><SecretInput v-model:value="draft.token" /></NFormItem>
        <NFormItem label="启用"><NSwitch v-model:value="draft.enabled" /></NFormItem>
      </NForm>
      <template #footer>
        <NSpace>
          <NButton @click="editing = null">取消</NButton>
          <NButton type="primary" @click="confirm">保存</NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>
