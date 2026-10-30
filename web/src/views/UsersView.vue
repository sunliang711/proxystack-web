<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import {
  NButton, NCard, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NSpace, NTag, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { api, emptyErrors, fieldState, splitProblem, type FormErrors, type Schemas } from '../api/client'
import ErrorList from '../components/ErrorList.vue'
import ExportDialog from '../components/ExportDialog.vue'
import ImportDialog from '../components/ImportDialog.vue'
import { formatBytes } from '../util/format'

type User = Schemas['User']

const router = useRouter()
const message = useMessage()
const users = ref<User[]>([])
const loading = ref(true)
const checked = ref<string[]>([])
const creating = ref(false)
const draft = ref({ name: '', remark: '' })
const errors = ref<FormErrors>(emptyErrors())
const showImport = ref(false)
const showExport = ref(false)

async function load() {
  loading.value = true
  const { data } = await api.GET('/api/users')
  users.value = data?.users ?? []
  loading.value = false
}
onMounted(load)

const columns: DataTableColumns<User> = [
  { type: 'selection' },
  {
    title: '名称',
    key: 'name',
    render: (row) => h(RouterLink, { to: { name: 'user', params: { id: row.id } } }, { default: () => row.name }),
  },
  {
    title: '状态',
    key: 'enabled',
    width: 80,
    render: (row) => h(NTag, { size: 'small', type: row.enabled ? 'success' : 'default' }, { default: () => (row.enabled ? '启用' : '停用') }),
  },
  { title: '备注', key: 'remark' },
  { title: '额度', key: 'quota_bytes', render: (row) => (row.quota_bytes ? formatBytes(row.quota_bytes) : '不限') },
  { title: '重置', key: 'reset_mode', render: (row) => (row.reset_mode === 'monthly' ? `每月 ${row.reset_day} 日` : '不重置') },
  { title: '到期', key: 'expire_at', render: (row) => (row.expire_at ? new Date(row.expire_at).toLocaleString() : '—') },
  { title: '档案', key: 'profiles', width: 70, render: (row) => String(row.profiles.length + 1) },
]

const rowKey = (row: User) => row.name
const selectedNames = computed(() => checked.value)

async function create() {
  const body: User = {
    name: draft.value.name, remark: draft.value.remark, enabled: true, uuid: '', ss_password: '', display_template: '',
    tag: '', sub_token: '', quota_bytes: 0, reset_mode: 'none', reset_day: 0, expire_at: '', note: '', profiles: [],
  }
  const { data, error } = await api.POST('/api/users', { body })
  if (error) {
    errors.value = splitProblem(error)
    return
  }
  creating.value = false
  message.success('已创建，UUID、密码和订阅 token 已随机生成')
  router.push({ name: 'user', params: { id: data!.id } })
}
</script>

<template>
  <NCard title="用户">
    <template #header-extra>
      <NSpace>
        <NButton @click="showImport = true">导入</NButton>
        <NButton :disabled="!selectedNames.length" @click="showExport = true">导出所选</NButton>
        <NButton type="primary" @click="(creating = true), (draft = { name: '', remark: '' }), (errors = emptyErrors())">新建用户</NButton>
      </NSpace>
    </template>
    <NDataTable
      v-model:checked-row-keys="checked"
      :columns="columns"
      :data="users"
      :loading="loading"
      :row-key="rowKey"
      :pagination="{ pageSize: 50 }"
    />
  </NCard>

  <NDrawer v-model:show="creating" :width="420">
    <NDrawerContent title="新建用户" closable>
      <ErrorList :errors="errors.global" />
      <NForm label-placement="top">
        <NFormItem label="名称" required v-bind="fieldState(errors, 'name', '字母或数字开头，可含 _ . -')">
          <NInput v-model:value="draft.name" />
        </NFormItem>
        <NFormItem label="备注">
          <NInput v-model:value="draft.remark" />
        </NFormItem>
      </NForm>
      <template #footer>
        <NButton type="primary" :disabled="!draft.name" @click="create">创建</NButton>
      </template>
    </NDrawerContent>
  </NDrawer>

  <ImportDialog v-model:show="showImport" scope="users" allow-csv @done="load" />
  <ExportDialog v-model:show="showExport" scope="users" :users="selectedNames" allow-csv />
</template>
