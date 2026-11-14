<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import YAML from 'yaml'
import {
  NAlert, NButton, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NPopconfirm, NSelect, NSpace, NTag, NText,
  type DataTableColumns,
} from 'naive-ui'
import { fieldState, hasErrorsUnder, type FormErrors, type Schemas } from '../../api/client'
import CodeEditor from '../../components/CodeEditor.vue'

type Override = Schemas['DirectOverride']
type RawConfig = Record<string, unknown>

const props = defineProps<{ stack: Schemas['Stack']; errors: FormErrors }>()

/** 与后端一致：协议和名称由上游与订阅规则决定，服务器专用字段和订阅内部字段在生成节点时被丢弃。 */
const FORBIDDEN = ['type', 'name', 'dialer-proxy', 'interface-name', 'routing-mark', 'id', 'user', 'direct', 'tag', 'remark', 'region']
const SERVER_ONLY = ['dialer-proxy', 'interface-name', 'routing-mark']
/** 通常填写字符串的凭据字段；YAML 会把纯数字解析成数字，导致前导零丢失或精度变化。 */
const STRING_FIELDS = ['uuid', 'password', 'username', 'private-key', 'psk', 'auth-str', 'obfs-password']
/** 订阅渲染同时接受连字符和下划线写法（例如 ws-opts 与 ws_opts），比较字段时统一为连字符。 */
const canonical = (key: string) => key.replaceAll('_', '-')

const overrides = computed(() => props.stack.direct_overrides ?? [])
const directUsers = computed(() => props.stack.direct_users ?? [])
const rawUpstreams = computed(() => props.stack.mihomo.upstreams.filter((upstream) => upstream.type === 'raw'))
const userOptions = computed(() => directUsers.value.map((name) => ({ label: name, value: name })))
const upstreamOptions = computed(() => rawUpstreams.value.map((upstream) => ({ label: upstream.name, value: upstream.name })))

const rows = computed(() => overrides.value.map((override, index) => ({ ...override, _index: index })))
const columns = computed<DataTableColumns<Override & { _index: number }>>(() => [
  {
    title: '用户',
    key: 'user',
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        row.user,
        hasErrorsUnder(props.errors, `direct_overrides[${row._index}]`) ? h(NTag, { size: 'small', type: 'error' }, () => '有错误') : null,
      ]),
  },
  { title: '上游', key: 'upstream' },
  { title: '覆盖字段', key: 'fields', render: (row) => Object.keys((row.config ?? {}) as RawConfig).join('、') },
  {
    title: '操作',
    key: 'actions',
    width: 140,
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'small', onClick: () => open(row._index) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => remove(row._index) }, {
          trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, () => '删除'),
          default: () => '删除该覆盖，恢复使用上游原配置？',
        }),
      ]),
  },
])

const editing = ref<{ index: number; user: string | null; upstream: string | null } | null>(null)
const yamlText = ref('')

function open(index: number) {
  const source = overrides.value[index]
  editing.value = {
    index,
    user: source?.user ?? null,
    upstream: source?.upstream ?? (rawUpstreams.value.length === 1 ? rawUpstreams.value[0].name : null),
  }
  yamlText.value = source?.config ? YAML.stringify(source.config) : ''
}

function remove(index: number) {
  props.stack.direct_overrides = overrides.value.filter((_, position) => position !== index)
}

/** 解析 YAML 并在本地做与后端相同的结构检查，返回配置或错误信息。 */
const parsed = computed<{ config?: RawConfig; error?: string }>(() => {
  if (!yamlText.value.trim()) return { error: '至少填写一个要覆盖的字段' }
  let value: unknown
  try {
    value = YAML.parse(yamlText.value)
  } catch (error) {
    return { error: (error as Error).message }
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return { error: '必须是映射' }
  const config = value as RawConfig
  if (!Object.keys(config).length) return { error: '至少填写一个要覆盖的字段' }
  const forbidden = Object.keys(config).filter((key) => FORBIDDEN.includes(canonical(key)))
  if (forbidden.length) return { error: `不能覆盖字段：${forbidden.join('、')}` }
  return { config }
})

const duplicate = computed(() => {
  const current = editing.value
  if (!current?.user || !current.upstream) return false
  return overrides.value.some((override, index) => index !== current.index && override.user === current.user && override.upstream === current.upstream)
})

/** 覆盖中被 YAML 解析成非字符串的凭据字段。 */
const nonStringFields = computed(() =>
  Object.entries(parsed.value.config ?? {})
    .filter(([key, value]) => STRING_FIELDS.includes(canonical(key)) && typeof value !== 'string')
    .map(([key]) => key),
)

/**
 * 合并后实际下发的节点字段，与后端规则一致：顶层字段整体替换，连字符和下划线写法视为同一字段，
 * 替换保留原位置并采用覆盖的写法，新字段追加到末尾，最后去掉服务器专用字段。
 */
const preview = computed(() => {
  const current = editing.value
  const upstream = rawUpstreams.value.find((candidate) => candidate.name === current?.upstream)
  if (!upstream) return ''
  const overlay = new Map(Object.entries(parsed.value.config ?? {}).map(([key, value]) => [canonical(key), [key, value] as const]))
  const used = new Set<string>()
  const entries: (readonly [string, unknown])[] = []
  for (const entry of Object.entries((upstream.config ?? {}) as RawConfig)) {
    const name = canonical(entry[0])
    const replacement = overlay.get(name)
    if (!replacement) entries.push(entry)
    else if (!used.has(name)) {
      entries.push(replacement)
      used.add(name)
    }
  }
  for (const [name, entry] of overlay) if (!used.has(name)) entries.push(entry)
  const merged = Object.fromEntries(entries.filter(([key]) => !SERVER_ONLY.includes(key)))
  return YAML.stringify(merged)
})
// 只读预览需要可写的绑定值，内容始终由 preview 计算。
const previewText = ref('')
watch(preview, (value) => { previewText.value = value }, { immediate: true })

const canConfirm = computed(() => !!editing.value?.user && !!editing.value.upstream && !!parsed.value.config && !duplicate.value)

function confirm() {
  const current = editing.value
  if (!current || !canConfirm.value) return
  const next: Override = { user: current.user!, upstream: current.upstream!, config: parsed.value.config! }
  const list = [...overrides.value]
  if (current.index < list.length) list[current.index] = next
  else list.push(next)
  props.stack.direct_overrides = list
  editing.value = null
}

const prefix = computed(() => `direct_overrides[${editing.value?.index ?? 0}]`)
const f = (path: string, hint?: string) => fieldState(props.errors, `${prefix.value}.${path}`, hint)
</script>

<template>
  <NSpace justify="space-between" align="center" style="margin: 16px 0 8px">
    <NText strong>单独覆盖</NText>
    <NButton size="small" :disabled="!directUsers.length || !rawUpstreams.length" @click="open(overrides.length)">添加覆盖</NButton>
  </NSpace>
  <NText depth="3" style="display: block; margin-bottom: 8px">
    为某个直连用户在某个外部上游上单独指定字段，例如独立的 uuid 或密码。顶层字段整体替换上游中的同名字段，嵌套字段（如 ws-opts）整块替换；未覆盖的用户和字段沿用上游配置。
  </NText>
  <NAlert v-if="fieldState(errors, 'direct_overrides').feedback" type="error" :bordered="false" style="margin-bottom: 8px">
    {{ fieldState(errors, 'direct_overrides').feedback }}
  </NAlert>
  <NDataTable v-if="overrides.length" :columns="columns" :data="rows" size="small" />

  <NDrawer :show="!!editing" :width="640" @update:show="(v) => !v && (editing = null)">
    <NDrawerContent v-if="editing" :title="editing.index < overrides.length ? '编辑直连覆盖' : '添加直连覆盖'" closable>
      <NForm label-placement="left" label-width="90">
        <NFormItem label="用户" required v-bind="f('user', '只能选择已允许直连的用户')">
          <NSelect v-model:value="editing.user" :options="userOptions" filterable placeholder="选择直连用户" />
        </NFormItem>
        <NFormItem label="上游" required v-bind="f('upstream', '只能选择外部节点类型的上游')">
          <NSelect v-model:value="editing.upstream" :options="upstreamOptions" filterable placeholder="选择外部上游" />
        </NFormItem>
        <NFormItem label="覆盖字段" required v-bind="f('config')">
          <NSpace vertical style="width: 100%">
            <CodeEditor v-model:value="yamlText" language="yaml" min-height="140px" />
            <NText depth="3" style="font-size: 12px">按 mihomo 代理字段填写，例如 <NText code>uuid: …</NText>；不能写 type、name 和 dialer-proxy 等服务器专用字段。</NText>
          </NSpace>
        </NFormItem>
        <NAlert v-if="duplicate" type="warning" :bordered="false" style="margin-bottom: 12px">该用户在此上游上已有覆盖，请直接编辑那一条。</NAlert>
        <NAlert v-else-if="yamlText.trim() && parsed.error" type="error" :bordered="false" style="margin-bottom: 12px">{{ parsed.error }}</NAlert>
        <NAlert v-else-if="nonStringFields.length" type="warning" :bordered="false" style="margin-bottom: 12px">
          {{ nonStringFields.join('、') }} 被解析为非字符串，纯数字会丢失前导零或精度；如需原样保留，请给值加上引号。
        </NAlert>
        <NFormItem v-if="preview" label="合并结果">
          <NSpace vertical style="width: 100%">
            <CodeEditor v-model:value="previewText" language="yaml" readonly min-height="120px" />
            <NText depth="3" style="font-size: 12px">该用户直连此上游时下发的节点字段（不含节点名）。</NText>
          </NSpace>
        </NFormItem>
      </NForm>
      <template #footer>
        <NSpace align="center">
          <NText depth="3" class="drawer-hint">暂存后回到页面点「保存」才会写入</NText>
          <NButton @click="editing = null">取消</NButton>
          <NButton type="primary" :disabled="!canConfirm" @click="confirm">暂存修改</NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.drawer-hint { font-size: 12px; }
</style>
