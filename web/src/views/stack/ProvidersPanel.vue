<script setup lang="ts">
import { computed, h, ref } from 'vue'
import {
  NAlert, NButton, NCard, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber,
  NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NSwitch, NTag, NText, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, fieldState, hasErrorsUnder, MASKED, splitProblem, type FormErrors, type Schemas } from '../../api/client'
import SecretInput from '../../components/SecretInput.vue'
import { usePolling } from '../../composables/usePolling'
import { formatTime } from '../../util/format'

type Provider = Schemas['Provider']
type Status = Schemas['ProviderView']
const props = defineProps<{
  stack: Schemas['Stack']; errors: FormErrors; dirty: boolean; savedVersion: number; reveal: () => Promise<void>
}>()
const message = useMessage()
const editing = ref<{ index: number; draft: Provider } | null>(null)
const draft = computed(() => editing.value?.draft as Provider)
const selectedGroups = ref<string[]>([])
const updating = ref<number | null>(null)
const nodesFor = ref<number | null>(null)
const sources = computed(() => props.stack.mihomo.providers ?? [])
const status = usePolling(() => [props.stack.id, props.savedVersion], async (signal) => {
  if (!props.stack.id) return []
  const { data, error } = await api.GET('/api/stacks/{id}/providers', { params: { path: { id: props.stack.id } }, signal })
  if (error) throw new Error(splitProblem(error).global.join('；'))
  return data ?? []
}, 15_000)
const states = computed(() => new Map((status.data.value ?? []).map((view) => [view.id, view])))
const shown = computed(() => status.data.value?.find((view) => view.id === nodesFor.value))

// Open a local draft; changes join the stack's existing save/apply workflow.
function open(index: number) {
  const source = sources.value[index] ?? {
    name: '', url: '', fetch_mode: 'direct', auto_update: true, interval: 3600, filter: '', exclude_filter: '',
  }
  editing.value = { index, draft: JSON.parse(JSON.stringify(source)) }
  selectedGroups.value = props.stack.mihomo.groups.filter((group) => group.use?.includes(source.name)).map((group) => group.name)
  if (index === sources.value.length) {
    const automatic = props.stack.mihomo.groups.filter((group) => group.type !== 'select')
    selectedGroups.value = automatic.length ? automatic.map((group) => group.name) : props.stack.mihomo.groups.filter((group) => group.name === props.stack.mihomo.rules.final).map((group) => group.name)
  }
}

// Reveal through the audited stack endpoint and update only an unchanged masked draft.
async function revealURL() {
  await props.reveal()
  const draft = editing.value?.draft
  if (draft?.url === MASKED) {
    const plain = sources.value.find((source) => source.id === draft.id)
    if (plain) draft.url = plain.url
  }
}

// Replace name-based group references together with the provider draft.
function confirm() {
  if (!editing.value) return
  const { index, draft } = editing.value
  const oldName = sources.value[index]?.name
  props.stack.mihomo.providers ??= []
  if (index < sources.value.length) props.stack.mihomo.providers[index] = draft
  else props.stack.mihomo.providers.push(draft)
  for (const group of props.stack.mihomo.groups) {
    const kept = (group.use ?? []).filter((name) => name !== oldName && name !== draft.name)
    group.use = selectedGroups.value.includes(group.name) ? [...kept, draft.name] : kept
  }
  editing.value = null
}

// Remove the collection and its group references in the same local edit.
function remove(index: number) {
  const name = sources.value[index].name
  props.stack.mihomo.providers!.splice(index, 1)
  for (const group of props.stack.mihomo.groups) group.use = (group.use ?? []).filter((value) => value !== name)
}

// Refresh the applied provider through the backend, retaining useful failure state.
async function update(source: Provider) {
  if (!source.id || !props.stack.id) return
  updating.value = source.id
  try {
    const { error } = await api.POST('/api/stacks/{id}/providers/{provider_id}/update', {
      params: { path: { id: props.stack.id, provider_id: source.id } },
    })
    if (error) message.error(splitProblem(error).global.join('；') || '更新失败，请查看状态')
    else message.success('订阅已更新')
  } finally {
    updating.value = null
    void status.refresh()
  }
}

// Format only valid success/attempt timestamps rather than displaying an epoch date.
const timeLabel = (value?: number) => value ? formatTime(new Date(value * 1000).toISOString()) : '—'
const columns = computed<DataTableColumns<Provider & { _index: number }>>(() => [
  {
    title: '名称', key: 'name', render: (row) => h(NSpace, { size: 4 }, () => [
      row.name, hasErrorsUnder(props.errors, `mihomo.providers[${row._index}]`) ? h(NTag, { type: 'error', size: 'small' }, () => '有错误') : null,
    ]),
  },
  { title: '获取方式', key: 'fetch_mode', render: (row) => row.fetch_mode === 'system' ? '系统下载代理' : '直连' },
  { title: '自动更新', key: 'auto_update', render: (row) => row.auto_update ? `每 ${row.interval} 秒` : '关闭' },
  {
    title: '运行状态', key: 'status', render: (row) => {
      const view = states.value.get(row.id ?? 0)
      if (!view?.applied) return '待保存并应用'
      return h(NSpace, { size: 4, vertical: true }, () => [
        `${view.running ? `${view.node_count} 个节点` : 'Stack 未运行'}${view.pending ? ' · 有待应用修改' : ''}`,
        `最近成功：${timeLabel(view.last_success_at)}`,
        `最近尝试：${timeLabel(view.last_attempt_at)}`,
        view.last_error ? h(NText, { type: 'error' }, () => view.last_error) : null,
      ])
    },
  },
  {
    title: '操作', key: 'actions', width: 280, render: (row) => {
      const view = states.value.get(row.id ?? 0)
      return h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'small', disabled: props.dirty || !view?.running || !view.applied || updating.value !== null, loading: updating.value === row.id || view?.updating, onClick: () => update(row) }, () => '立即更新'),
        h(NButton, { size: 'small', disabled: !view?.applied, onClick: () => { nodesFor.value = row.id! } }, () => '节点'),
        h(NButton, { size: 'small', onClick: () => open(row._index) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => remove(row._index) }, {
          trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '删除'),
          default: () => '删除订阅来源，并从所有代理组中移除引用？',
        }),
      ])
    },
  },
])
const rows = computed(() => sources.value.map((source, index) => ({ ...source, _index: index })))
const conflict = computed(() => editing.value && sources.value.some((source, index) => index !== editing.value!.index && source.name === editing.value!.draft.name))
const f = (field: string, hint?: string) => fieldState(props.errors, `mihomo.providers[${editing.value?.index ?? 0}].${field}`, hint)
const nodeColumns: DataTableColumns<Status['nodes'][number]> = [{ title: '节点名称', key: 'name' }, { title: '协议', key: 'type' }]
</script>

<template>
  <NCard title="上游订阅" size="small" style="margin-top: 20px">
    <template #header-extra><NButton size="small" type="primary" @click="open(sources.length)">添加订阅来源</NButton></template>
    <NAlert type="info" :bordered="false" style="margin-bottom: 12px">
      从 Clash/Mihomo 订阅获取动态节点，供代理组使用。新增或修改来源后保存并应用；更新节点无需重启 Stack。
      自动更新关闭后仍可手动更新；首次启动没有缓存时会初始化获取一次。
    </NAlert>
    <NAlert v-if="dirty" type="warning" :bordered="false" style="margin-bottom: 12px">请先保存修改。更新使用当前已应用的订阅地址和下载方式。</NAlert>
    <NAlert v-if="status.error.value" type="error" :bordered="false">{{ status.error.value }}</NAlert>
    <NDataTable :columns="columns" :data="rows" :scroll-x="1050" size="small" />
  </NCard>

  <NDrawer :show="!!editing" :width="620" @update:show="(value) => !value && (editing = null)">
    <NDrawerContent v-if="editing" :title="draft.name || '新订阅来源'" closable>
      <NForm label-placement="top">
        <NFormItem label="名称" required v-bind="f('name', '使用字母、数字、下划线、点或横线；同一个 Stack 内唯一')"><NInput v-model:value="draft.name" /></NFormItem>
        <NFormItem label="订阅地址" required v-bind="f('url')"><SecretInput v-model:value="draft.url" :reveal="revealURL" placeholder="https://example.com/subscription" /></NFormItem>
        <NFormItem label="获取方式" v-bind="f('fetch_mode', '系统下载代理为直连模式时，这里也通过直连获取；代理失败不会自动切换直连')">
          <NRadioGroup v-model:value="draft.fetch_mode"><NRadioButton value="direct">直连</NRadioButton><NRadioButton value="system">使用系统下载代理</NRadioButton></NRadioGroup>
        </NFormItem>
        <NFormItem label="自动更新" v-bind="f('auto_update')"><NSwitch v-model:value="draft.auto_update" /></NFormItem>
        <NFormItem v-if="draft.auto_update" label="更新周期（秒）" v-bind="f('interval')"><NInputNumber v-model:value="draft.interval" :min="60" :max="2592000" :show-button="false" /></NFormItem>
        <NFormItem label="包含正则表达式" v-bind="f('filter', '留空不过滤；填写前需安装 Mihomo；例如 (?i)香港|hk|hong kong')"><NInput v-model:value="draft.filter" placeholder="(?i)香港|hk|hong kong" /></NFormItem>
        <NFormItem label="排除正则表达式" v-bind="f('exclude_filter', '匹配节点名称；同时设置时，需要匹配包含表达式且不匹配排除表达式')"><NInput v-model:value="draft.exclude_filter" placeholder="到期|剩余流量|套餐|官网" /></NFormItem>
        <NFormItem label="加入代理组"><NSelect v-model:value="selectedGroups" multiple :options="stack.mihomo.groups.map((group) => ({ label: group.name, value: group.name }))" /></NFormItem>
      </NForm>
      <template #footer><NSpace align="center"><NText v-if="conflict" type="error">来源名称重复</NText><NButton @click="editing = null">取消</NButton><NButton type="primary" :disabled="!draft.name || !draft.url || !!conflict" @click="confirm">暂存修改</NButton></NSpace></template>
    </NDrawerContent>
  </NDrawer>
  <NDrawer :show="nodesFor !== null" :width="640" @update:show="(value) => !value && (nodesFor = null)">
    <NDrawerContent :title="`${shown?.name ?? ''} · 当前节点`" closable>
      <NText depth="3">显示已应用筛选条件后的运行节点，共 {{ shown?.node_count ?? 0 }} 个。</NText>
      <NDataTable :columns="nodeColumns" :data="shown?.nodes ?? []" :pagination="{ pageSize: 20 }" size="small" style="margin-top: 12px" />
    </NDrawerContent>
  </NDrawer>
</template>
