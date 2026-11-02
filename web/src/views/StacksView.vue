<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import {
  NAlert, NButton, NCard, NDataTable, NDynamicTags, NForm, NFormItem, NGrid, NGridItem, NIcon, NInput, NModal, NRadio,
  NRadioGroup, NSelect, NSpace, NStep, NSteps, NTag, NText, NTooltip, useMessage, type DataTableColumns,
} from 'naive-ui'
import { InformationCircleOutline, RefreshOutline } from '@vicons/ionicons5'
import { api, emptyErrors, fieldState, splitProblem, type FormErrors, type Schemas } from '../api/client'
import EgressCell from '../components/EgressCell.vue'
import EgressHealthStatus from '../components/EgressHealthStatus.vue'
import { usePolling } from '../composables/usePolling'
import InboundSummary from '../components/InboundSummary.vue'
import MihomoSummary from '../components/MihomoSummary.vue'
import FlowDiagram from '../components/FlowDiagram.vue'
import ErrorList from '../components/ErrorList.vue'
import ExportDialog from '../components/ExportDialog.vue'
import ImportDialog from '../components/ImportDialog.vue'
import UnitStatus from '../components/UnitStatus.vue'
import { useEgress } from '../composables/useEgress'
import { runtime } from '../runtime'
import { stackTopology, type FlowNode } from '../util/flow'

type Summary = Schemas['StackSummary']

const router = useRouter()
const message = useMessage()
const stacks = ref<Summary[]>([])
const loading = ref(true)
const checked = ref<string[]>([])
const showImport = ref(false)
const showExport = ref(false)

async function load() {
  loading.value = true
  const { data } = await api.GET('/api/stacks')
  stacks.value = data?.stacks ?? []
  loading.value = false
}

onMounted(load)

// 流量拓扑：展开状态记在浏览器里。
const topology = computed(() => stackTopology(stacks.value))
const showTopology = ref(readTopologyPreference())
function readTopologyPreference(): boolean {
  try {
    return localStorage.getItem('psw-stack-topology') !== 'hidden'
  } catch {
    return true
  }
}
watch(showTopology, (show) => {
  try {
    localStorage.setItem('psw-stack-topology', show ? 'shown' : 'hidden')
  } catch {
    // 存不下时只在本次会话生效。
  }
})
function openStack(node: FlowNode) {
  if (node.stackId) router.push({ name: 'stack', params: { id: node.stackId } })
}

// 出口 IP：优先读取浏览器本地缓存，没有缓存时查询，手动刷新后更新缓存。
const egress = useEgress()
const health = usePolling(() => true, async (signal) => {
  const { data, error } = await api.GET('/api/runtime/egress-health', { signal })
  if (error || !data) throw new Error('后台出口状态刷新失败')
  return data
})
const healthByID = computed(() => new Map(health.data.value?.stacks.map((item) => [item.stack_id, item]) ?? []))
const egressTargets = computed(() => stacks.value.filter((stack) => stack.mihomo_enabled))
watch(egressTargets, (targets) => egress.ensure(targets))
// mihomo 从停止变为运行后自动重新检测。
watch(
  () => egressTargets.value.filter((stack) => egress.states[stack.id]?.status === 'stopped' && mihomoActive(stack.name)),
  (started) => started.forEach((stack) => egress.refreshOne(stack)),
)

function mihomoActive(name: string): boolean {
  return runtime.stacks[name]?.units.some((unit) => unit.unit.includes('mihomo') && unit.active === 'active') ?? false
}

const columns: DataTableColumns<Summary> = [
  { type: 'selection' },
  {
    title: '名称',
    key: 'name',
    minWidth: 120,
    render: (row) => h(RouterLink, { to: { name: 'stack', params: { id: row.id } } }, { default: () => row.name }),
  },
  {
    title: '状态',
    key: 'enabled',
    width: 150,
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        h(NTag, { size: 'small', type: row.enabled ? 'success' : 'default' }, () => (row.enabled ? '启用' : '停用')),
        row.enabled ? h(NTag, { size: 'small', type: row.desired_running ? 'info' : 'warning' }, () => (row.desired_running ? '期望运行' : '期望停止')) : null,
      ]),
  },
  { title: '运行', key: 'runtime', render: (row) => h(UnitStatus, { stack: row.name, actions: true }) },
  {
    title: () =>
      h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
        '出口 IP',
        h(NTooltip, null, {
          trigger: () => h(NIcon, { size: 14, style: 'opacity: 0.5; cursor: help' }, () => h(InformationCircleOutline)),
          default: () => '经该 stack 的 mihomo 并发查询 IPv4 和 IPv6 出口（IPv4 优先用 ipinfo.io），哪个先完成就先显示；另一项继续检测，失败原因可悬停查看。首次查询后保存在当前浏览器，进入页面或刷新页面时优先使用缓存；点顶部「出口IP」或行内的刷新按钮重新检测并更新缓存。',
        }),
      ]),
    key: 'egress',
    minWidth: 300,
    render: (row) =>
      h('div', [
        h(EgressCell, {
          state: egress.states[row.id],
          mihomoEnabled: row.mihomo_enabled,
          onRefresh: () => egress.refreshOne(row),
        }),
        h(EgressHealthStatus, { health: healthByID.value.get(row.id), error: health.error.value }),
      ]),
  },
  { title: '角色', key: 'role', width: 70 },
  {
    title: 'Xray 入站',
    key: 'inbounds',
    minWidth: 190,
    render: (row) => h(InboundSummary, { stack: row }),
  },
  {
    title: 'Mihomo',
    key: 'mihomo',
    minWidth: 140,
    render: (row) => h(MihomoSummary, { stack: row }),
  },
  {
    title: '标记',
    key: 'labels',
    minWidth: 120,
    render: (row) =>
      h(NSpace, { size: 4 }, () => [
        ...row.labels.map((label) => h(NTag, { size: 'small', bordered: false }, () => label)),
        row.loopback ? h(NTag, { size: 'small', type: 'info' }, () => '仅本机') : null,
        row.role === 'auto' ? h(NText, { depth: 3 }, () => `成员：${row.members.join('、') || '无'}`) : null,
      ]),
  },
]

const rowKey = (row: Summary) => row.name

// 新建向导
const wizard = ref(false)
const step = ref(1)
type Template = 'pair' | 'auto-url-test' | 'load-balance'
const draft = ref({ template: 'pair' as Template, name: '', labels: [] as string[], members: [] as string[] })
const errors = ref<FormErrors>(emptyErrors())
const creating = ref(false)
/** 用户数；入站要引用用户，没有用户时不能创建 stack。null 表示还没查询到。 */
const userCount = ref<number | null>(null)
const noUsers = computed(() => userCount.value === 0)

const templates: { value: Template; title: string; diagram: string }[] = [
  { value: 'pair', title: 'pair · 单节点链路', diagram: '客户端 → Xray 入站 → Mihomo → 上游节点' },
  { value: 'auto-url-test', title: 'auto-url-test · 自动测速', diagram: '客户端 → Xray → Mihomo url-test → 多个 edge 的 relay' },
  { value: 'load-balance', title: 'load-balance · 负载均衡', diagram: '客户端 → Xray → Mihomo load-balance → 多个 edge 的 relay' },
]
const isAuto = computed(() => draft.value.template !== 'pair')
const memberOptions = computed(() =>
  stacks.value
    .filter((s) => s.role === 'edge' && s.inbounds.some((i) => i.name === 'relay' && i.protocol === 'socks5'))
    .map((s) => ({ label: s.name, value: s.name })),
)

async function openWizard() {
  draft.value = { template: 'pair', name: '', labels: [], members: [] }
  errors.value = emptyErrors()
  step.value = 1
  wizard.value = true
  userCount.value = null
  const { data } = await api.GET('/api/users')
  userCount.value = data?.users.length ?? null
}

/** 向导只有名称和成员两个可以定位错误的控件，其他字段的错误要显示在顶部，否则点了创建没有任何反应。 */
function wizardErrors(problem: Parameters<typeof splitProblem>[0]): FormErrors {
  const out = splitProblem(problem, '创建失败')
  for (const [field, text] of Object.entries(out.fields)) {
    if (field !== 'name' && field !== 'members') {
      out.global.push(text)
      delete out.fields[field]
    }
  }
  return out
}

async function create() {
  creating.value = true
  const { data, error } = await api.POST('/api/stacks', { body: { ...draft.value, members: isAuto.value ? draft.value.members : [] } })
  creating.value = false
  if (error) {
    errors.value = wizardErrors(error)
    step.value = errors.value.fields.name ? 2 : step.value
    return
  }
  wizard.value = false
  message.success('已创建，端口已自动分配，凭据已随机生成')
  router.push({ name: 'stack', params: { id: data!.id } })
}
</script>

<template>
  <NCard title="流量拓扑" size="small" style="margin-bottom: 20px">
    <template #header-extra>
      <NButton text size="small" @click="showTopology = !showTopology">{{ showTopology ? '收起' : '展开' }}</NButton>
    </template>
    <template v-if="showTopology">
      <NText depth="3" style="display: block; margin-bottom: 8px; font-size: 13px">
        客户端经订阅连到各 stack 的入站，节点里列出入站和 Xray 出站；stack 之间的箭头表示上游引用（例如自动测速 stack 经成员的 relay 入站转发），最后经上游节点、外部代理或直连到达互联网。点击 stack 节点打开编辑页，悬停突出相连的路径。
      </NText>
      <FlowDiagram v-if="topology.nodes.length" :nodes="topology.nodes" :edges="topology.edges" :columns="topology.columns" @select="openStack" />
      <NText v-else-if="!loading" depth="3">还没有 stack。</NText>
    </template>
  </NCard>

  <NCard title="Stack 列表" class="table-panel">
    <template #header-extra>
      <NSpace justify="end">
        <NButton :loading="egress.refreshing.value" :disabled="!egressTargets.length" @click="egress.refresh(egressTargets)">
          <template #icon><NIcon><RefreshOutline /></NIcon></template>
          出口IP
        </NButton>
        <NButton @click="showImport = true">导入</NButton>
        <NButton :disabled="!checked.length" @click="showExport = true">导出所选</NButton>
        <NButton type="primary" @click="openWizard">新建 Stack</NButton>
      </NSpace>
    </template>
    <NDataTable
      v-model:checked-row-keys="checked"
      :columns="columns"
      :data="stacks"
      :loading="loading"
      :row-key="rowKey"
      :scroll-x="1440"
      class="pre-line"
    />
  </NCard>

  <NModal v-model:show="wizard" preset="card" title="新建 stack" style="max-width: 760px">
    <NSteps :current="step" size="small" style="margin-bottom: 16px">
      <NStep title="模板" />
      <NStep title="基本信息" />
      <NStep v-if="isAuto" title="成员" />
    </NSteps>
    <NAlert v-if="noUsers" type="warning" :bordered="false" style="margin-bottom: 12px">
      当前没有用户。stack 的入站要引用用户，请先<RouterLink :to="{ name: 'users' }">创建用户</RouterLink>。
    </NAlert>
    <ErrorList :errors="errors.global" title="创建失败" />

    <NRadioGroup v-if="step === 1" v-model:value="draft.template" style="width: 100%">
      <NGrid :cols="3" :x-gap="12" responsive="screen" item-responsive>
        <NGridItem v-for="t in templates" :key="t.value" span="3 m:1">
          <NCard size="small" hoverable :class="{ selected: draft.template === t.value }" @click="draft.template = t.value">
            <NRadio :value="t.value">{{ t.title }}</NRadio>
            <NText depth="3" tag="div" style="margin-top: 8px; font-size: 12px">{{ t.diagram }}</NText>
          </NCard>
        </NGridItem>
      </NGrid>
    </NRadioGroup>

    <NForm v-if="step === 2" label-placement="left" label-width="80">
      <NFormItem label="名称" required v-bind="fieldState(errors, 'name', '字母或数字开头，可含 _ . -')">
        <NInput v-model:value="draft.name" placeholder="例如 usa1" />
      </NFormItem>
      <NFormItem label="标签">
        <NDynamicTags v-model:value="draft.labels" />
      </NFormItem>
      <NAlert type="info" :bordered="false">端口从端口池自动分配，UUID、secret 和密码随机生成；创建后可在编辑页调整。</NAlert>
    </NForm>

    <NForm v-if="step === 3 && isAuto" label-placement="top">
      <NFormItem label="成员" v-bind="fieldState(errors, 'members', '只列出带 relay SOCKS5 入站的 edge stack')">
        <NSelect v-model:value="draft.members" :options="memberOptions" multiple filterable />
      </NFormItem>
    </NForm>

    <template #footer>
      <NSpace justify="end">
        <NButton v-if="step > 1" @click="step--">上一步</NButton>
        <NButton v-if="step === 1 || (step === 2 && isAuto)" type="primary" :disabled="noUsers || (step === 2 && !draft.name)" @click="step++">下一步</NButton>
        <NButton
          v-else
          type="primary"
          :loading="creating"
          :disabled="noUsers || !draft.name || (isAuto && !draft.members.length)"
          @click="create"
        >
          创建
        </NButton>
      </NSpace>
    </template>
  </NModal>

  <ImportDialog v-model:show="showImport" scope="stacks" @done="load" />
  <ExportDialog v-model:show="showExport" scope="stacks" :stacks="checked" />
</template>

<style scoped>
.selected {
  border-color: var(--n-color-target, #18a058);
}
.pre-line :deep(td) {
  white-space: pre-line;
}
</style>
