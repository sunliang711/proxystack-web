<script setup lang="ts">
import { computed, h, ref } from 'vue'
import {
  NButton, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NPopconfirm, NRadioButton,
  NRadioGroup, NSelect, NSpace, NTag, type DataTableColumns,
} from 'naive-ui'
import { fieldState, hasErrorsUnder, type FormErrors, type Schemas } from '../../api/client'
import OrderedPicker from '../../components/OrderedPicker.vue'
import { proxyTargets } from './ports'

type Group = Schemas['Group']

const props = defineProps<{ stack: Schemas['Stack']; errors: FormErrors }>()
const editing = ref<{ index: number; draft: Group } | null>(null)
const rows = computed(() => props.stack.mihomo.groups.map((group, index) => ({ ...group, _index: index })))

const columns = computed<DataTableColumns<Group & { _index: number }>>(() => [
  {
    title: '名称',
    key: 'name',
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        row.name,
        hasErrorsUnder(props.errors, `mihomo.groups[${row._index}]`) ? h(NTag, { size: 'small', type: 'error' }, () => '有错误') : null,
      ]),
  },
  { title: '类型', key: 'type', width: 120 },
  { title: '成员', key: 'proxies', render: (row) => row.proxies.join(' → ') },
  {
    title: '操作',
    key: 'actions',
    width: 200,
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'small', quaternary: true, disabled: row._index === 0, onClick: () => move(row._index, -1) }, () => '上移'),
        h(NButton, { size: 'small', onClick: () => open(row._index) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => props.stack.mihomo.groups.splice(row._index, 1) }, {
          trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '删除'),
          default: () => '删除这个代理组？',
        }),
      ]),
  },
])

function move(index: number, delta: number) {
  const groups = props.stack.mihomo.groups
  const [group] = groups.splice(index, 1)
  groups.splice(index + delta, 0, group)
}

function open(index: number) {
  const source: Group =
    index < props.stack.mihomo.groups.length
      ? props.stack.mihomo.groups[index]
      : { name: '', type: 'select', proxies: [], url: '', interval: 0, strategy: '' }
  editing.value = { index, draft: JSON.parse(JSON.stringify(source)) }
}

function onType(type: Group['type']) {
  const draft = editing.value!.draft
  draft.type = type
  if (type !== 'select') {
    draft.url ||= 'http://www.gstatic.com/generate_204'
    draft.interval ||= 120
  }
  draft.strategy = type === 'load-balance' ? draft.strategy || 'consistent-hashing' : ''
}

function confirm() {
  if (!editing.value) return
  const { index, draft } = editing.value
  if (index < props.stack.mihomo.groups.length) {
    const oldName = props.stack.mihomo.groups[index].name
    props.stack.mihomo.groups[index] = draft
    if (oldName && oldName !== draft.name) renameTarget(oldName, draft.name)
  } else {
    props.stack.mihomo.groups.push(draft)
  }
  editing.value = null
}

/** 代理组改名时同步本 stack 内的组成员和规则目标。 */
function renameTarget(oldName: string, newName: string) {
  for (const group of props.stack.mihomo.groups) {
    group.proxies = group.proxies.map((p) => (p === oldName ? newName : p))
  }
  const rules = props.stack.mihomo.rules
  if (rules.final === oldName) rules.final = newName
  for (const rule of rules.extra) if (rule.target === oldName) rule.target = newName
}

const candidates = computed(() => proxyTargets(props.stack, editing.value?.draft.name))
const prefix = computed(() => `mihomo.groups[${editing.value?.index ?? 0}]`)
const f = (path: string, hint?: string) => fieldState(props.errors, `${prefix.value}.${path}`, hint)
</script>

<template>
  <NSpace justify="end" style="margin-bottom: 16px">
    <NButton type="primary" @click="open(stack.mihomo.groups.length)">添加代理组</NButton>
  </NSpace>
  <NDataTable :columns="columns" :data="rows" :scroll-x="760" size="small" />

  <NDrawer :show="!!editing" :width="560" @update:show="(v) => !v && (editing = null)">
    <NDrawerContent v-if="editing" :title="editing.draft.name ? `代理组 ${editing.draft.name}` : '新代理组'" closable>
      <NForm label-placement="left" label-width="90">
        <NFormItem label="name" required v-bind="f('name')">
          <NInput v-model:value="editing.draft.name" />
        </NFormItem>
        <NFormItem label="type" v-bind="f('type')">
          <NRadioGroup :value="editing.draft.type" @update:value="onType">
            <NRadioButton value="select">select</NRadioButton>
            <NRadioButton value="url-test">url-test</NRadioButton>
            <NRadioButton value="load-balance">load-balance</NRadioButton>
            <NRadioButton value="fallback">fallback</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem label="proxies" v-bind="f('proxies', '按顺序排列；不能选择自身，不能形成循环')">
          <OrderedPicker v-model:value="editing.draft.proxies" :options="candidates" />
        </NFormItem>
        <template v-if="editing.draft.type !== 'select'">
          <NFormItem label="url" v-bind="f('url')">
            <NInput v-model:value="editing.draft.url" />
          </NFormItem>
          <NFormItem label="interval" v-bind="f('interval', '秒')">
            <NInputNumber v-model:value="editing.draft.interval" :min="1" />
          </NFormItem>
        </template>
        <NFormItem v-if="editing.draft.type === 'load-balance'" label="strategy" v-bind="f('strategy')">
          <NSelect
            v-model:value="editing.draft.strategy"
            :options="['consistent-hashing', 'round-robin', 'sticky-sessions'].map((v) => ({ label: v, value: v }))"
          />
        </NFormItem>
      </NForm>
      <template #footer>
        <NSpace>
          <NButton @click="editing = null">取消</NButton>
          <NButton type="primary" :disabled="!editing.draft.name" @click="confirm">确定</NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>
