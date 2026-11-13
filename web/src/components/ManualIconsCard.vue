<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { NButton, NDataTable, NInput, NPopconfirm, NRadioButton, NRadioGroup, NSpace, NText, useMessage, type DataTableColumns, type DataTableRowKey } from 'naive-ui'
import DomainIcon from './DomainIcon.vue'
import IconActions from './IconActions.vue'
import { useManualIcons, type IconKind, type ManualIcon } from '../composables/useManualIcons'
import { formatTime } from '../util/format'

/** 系统设置里的手动图标管理：列出、添加、替换和删除。修改立即生效，不随设置保存。 */
const message = useMessage()
const icons = useManualIcons()
const checked = ref<DataTableRowKey[]>([])
const addKind = ref<IconKind>('domain')
const addName = ref('')
const editing = ref<string | null>(null)
const busy = ref(false)

const rowKey = (row: ManualIcon) => `${row.kind}:${row.name}`
const formatSize = (bytes: number) => (bytes >= 1024 ? `${(bytes / 1024).toFixed(1)} KB` : `${bytes} B`)

async function removeKeys(keys: DataTableRowKey[]) {
  const items = icons.icons.value.filter((row) => keys.includes(rowKey(row))).map((row) => ({ kind: row.kind as IconKind, name: row.name }))
  if (!items.length) return
  busy.value = true
  const error = await icons.remove(items)
  busy.value = false
  if (error) {
    message.error(error)
    return
  }
  checked.value = checked.value.filter((key) => !keys.includes(key))
  if (editing.value && keys.includes(editing.value)) editing.value = null
  message.success(`已删除 ${items.length} 个手动图标`)
}

const columns = computed<DataTableColumns<ManualIcon>>(() => [
  { type: 'selection' },
  {
    title: '图标', key: 'icon', width: 60,
    render: (row) => h(DomainIcon, row.kind === 'service' ? { domain: '', service: row.name, size: 20 } : { domain: row.name, size: 20 }),
  },
  { title: '对象', key: 'name', minWidth: 160, render: (row) => (row.kind === 'service' ? `服务 ${row.name}` : row.name) },
  { title: '格式', key: 'content_type', minWidth: 110, render: (row) => row.content_type.replace('image/', '') },
  { title: '大小', key: 'size', minWidth: 80, render: (row) => formatSize(row.size) },
  { title: '设置', key: 'updated_at', minWidth: 170, render: (row) => `${formatTime(row.updated_at)}${row.updated_by ? ` · ${row.updated_by}` : ''}` },
  {
    title: '操作', key: 'actions', width: 150, fixed: 'right',
    render: (row) => h(NSpace, { size: 6, wrap: false }, () => [
      h(NButton, { size: 'tiny', secondary: true, onClick: () => { editing.value = editing.value === rowKey(row) ? null : rowKey(row) } }, () => '替换'),
      h(NPopconfirm, { onPositiveClick: () => removeKeys([rowKey(row)]) }, {
        trigger: () => h(NButton, { size: 'tiny', secondary: true, type: 'error' }, () => '删除'),
        default: () => '删除后恢复自动获取（服务改用代表域名的图标）。确定删除吗？',
      }),
    ]),
  },
])
const editingIcon = computed(() => icons.icons.value.find((row) => rowKey(row) === editing.value))
</script>

<template>
  <NSpace vertical :size="12" style="width: 100%">
    <NText depth="3" class="hint">
      手动设置的图标优先于自动获取，不过期，修改立即生效（不需要点保存）。也可以在「流量明细 → 访问域名」和用户详情的「常访问站点」中点击图标设置。
    </NText>
    <div class="add-row">
      <NRadioGroup v-model:value="addKind" size="small" aria-label="图标对象类型">
        <NRadioButton value="domain">主域名</NRadioButton>
        <NRadioButton value="service">服务</NRadioButton>
      </NRadioGroup>
      <NInput v-model:value="addName" size="small" :placeholder="addKind === 'domain' ? 'example.com' : 'instagram'" class="add-name" clearable />
    </div>
    <IconActions v-if="addName.trim()" :key="`${addKind}:${addName.trim()}`" :kind="addKind" :name="addName.trim()" @done="addName = ''" />
    <IconActions v-if="editingIcon" :key="`edit:${editing}`" :kind="editingIcon.kind as IconKind" :name="editingIcon.name" @done="editing = null" />
    <NSpace v-if="checked.length" align="center" :size="8">
      <NPopconfirm @positive-click="removeKeys(checked)">
        <template #trigger><NButton size="small" type="error" secondary :loading="busy">删除选中的 {{ checked.length }} 个</NButton></template>
        删除选中的 {{ checked.length }} 个手动图标？
      </NPopconfirm>
    </NSpace>
    <NDataTable v-model:checked-row-keys="checked" :columns="columns" :data="icons.icons.value" :row-key="rowKey" :pagination="{ pageSize: 20 }" :scroll-x="760" size="small" />
  </NSpace>
</template>

<style scoped>
.hint { font-size: 12px; }
.add-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; }
.add-name { width: 240px; }
@media (max-width: 600px) { .add-name { width: 100%; } }
</style>
