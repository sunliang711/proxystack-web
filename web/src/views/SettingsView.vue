<script setup lang="ts">
import { computed, h, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NBadge, NButton, NCheckbox, NCheckboxGroup, NDynamicTags, NForm, NFormItem, NGrid, NFormItemGi, NTabPane, NTabs,
  NInput, NInputNumber, NRadioButton, NRadioGroup, NSelect, NSpace, NSpin, NSwitch, NTimePicker, NAlert, NText, NIcon,
  useDialog, useMessage,
} from 'naive-ui'
import { api, emptyErrors, fieldState, MASKED, problemMessage, splitProblem, type FormErrors, type Schemas } from '../api/client'
import { randomString } from '../util/generate'
import SecretInput from '../components/SecretInput.vue'
import TemplateInput from '../components/TemplateInput.vue'
import ErrorList from '../components/ErrorList.vue'
import PauseWindowsInput from '../components/PauseWindowsInput.vue'
import BlockedDomainsInput from '../components/BlockedDomainsInput.vue'
import { refreshPauseWindow } from '../composables/usePauseWindow'
import SettingsSection from '../components/SettingsSection.vue'
import EntityLink from '../components/EntityLink.vue'
import { SearchOutline } from '@vicons/ionicons5'
import { sectionValue, settingsSections, settingsTabs, tabForField, tabOfSection, type SettingsTab } from '../composables/useSettingsSections'
import { useDirty } from '../composables/useDirty'
import { notifySaved } from '../runtime'
import { mihomoLogLevels, xrayLogLevels } from '../util/options'

type Settings = Schemas['Settings']
type PoolKey = keyof Settings['port_pools']

const settings = ref<Settings | null>(null)
const errors = ref<FormErrors>(emptyErrors())
const saving = ref(false)
const message = useMessage()
const dialog = useDialog()
const { dirty, markClean, patchBaseline, saved } = useDirty(settings)
const route = useRoute()
const router = useRouter()

// 当前标签页记在地址栏里；其他页面也可以用 ?section=notify 直接打开某个分组所在的标签页。
const initialSection = String(route.query.section ?? '')
const knownSection = (name: string) => (settingsSections as readonly string[]).includes(name)
const tab = ref<SettingsTab>(
  settingsTabs.some((t) => t.name === route.query.tab)
    ? (route.query.tab as SettingsTab)
    : knownSection(initialSection) ? tabOfSection(initialSection) : 'basic',
)
watch(tab, (name) => {
  if (route.query.tab !== name) void router.replace({ query: { ...route.query, section: undefined, tab: name === 'basic' ? undefined : name } })
})

const query = ref('')
/** 搜索命中的分组；未搜索时为 null。 */
const matches = ref<string[] | null>(null)
const matchCount = computed(() => matches.value?.length ?? 0)

/** 按分组内的标签、说明和选项文字匹配，多个关键词需要同时命中；命中后切到第一个有结果的标签页。 */
function runSearch() {
  const terms = query.value.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (!terms.length) {
    matches.value = null
    return
  }
  matches.value = settingsSections.filter((name) => {
    const text = (document.getElementById(`settings-${name}`)?.textContent ?? '').toLowerCase()
    return terms.every((term) => text.includes(term))
  })
  const hits = new Set(matches.value.map(tabOfSection))
  if (hits.size && !hits.has(tab.value)) tab.value = settingsTabs.find((t) => hits.has(t.name))!.name
}
watch(query, runSearch)
const sectionShown = (name: string) => !matches.value || matches.value.includes(name)
/** 模板中用到的常量。 */
const mihomoLevelOptions = mihomoLogLevels.map((v) => ({ label: v, value: v }))
const xrayLevelOptions = xrayLogLevels.map((v) => ({ label: v, value: v }))

const tabErrors = computed(() => new Set(Object.keys(errors.value.fields).map(tabForField)))
/** 各标签页内是否有未保存的修改。 */
const tabDirty = computed(() => {
  const out = new Set<SettingsTab>()
  const current = settings.value
  const base = saved.value
  if (!current || !base) return out
  for (const t of settingsTabs) {
    if (t.sections.some((key) => JSON.stringify(sectionValue(current, key)) !== JSON.stringify(sectionValue(base, key)))) out.add(t.name)
  }
  return out
})
const tabLabel = (name: SettingsTab, label: string) => () => {
  const hits = matches.value?.filter((section) => tabOfSection(section) === name).length ?? 0
  const text = matches.value && hits ? `${label} ${hits}` : label
  if (tabErrors.value.has(name)) return h(NBadge, { dot: true, type: 'error', offset: [6, 0] }, () => text)
  if (tabDirty.value.has(name)) return h(NBadge, { dot: true, type: 'warning', offset: [6, 0] }, () => text)
  return text
}

const timezones = (Intl as unknown as { supportedValuesOf(key: string): string[] })
  .supportedValuesOf('timeZone')
  .map((tz) => ({ label: tz, value: tz }))

const pools: { key: PoolKey; label: string }[] = [
  { key: 'xray_inbound', label: 'Xray 入站' },
  { key: 'mihomo_socks', label: 'Mihomo SOCKS 监听' },
  { key: 'mihomo_http', label: 'Mihomo HTTP 监听' },
  { key: 'xray_api', label: 'Xray API' },
  { key: 'mihomo_controller', label: 'Mihomo controller' },
]

const policyFlags: { key: keyof Settings['defaults']['xray']['policy']; label: string }[] = [
  { key: 'stats_user_uplink', label: '用户上行统计（statsUserUplink）' },
  { key: 'stats_user_downlink', label: '用户下行统计（statsUserDownlink）' },
  { key: 'stats_inbound_uplink', label: '入站上行统计（statsInboundUplink）' },
  { key: 'stats_inbound_downlink', label: '入站下行统计（statsInboundDownlink）' },
  { key: 'stats_outbound_uplink', label: '出站上行统计（statsOutboundUplink）' },
  { key: 'stats_outbound_downlink', label: '出站下行统计（statsOutboundDownlink）' },
]

const f = (path: string, hint?: string) => fieldState(errors.value, path, hint)

const depKeys = [
  { key: 'xray' as const, label: 'Xray' },
  { key: 'mihomo' as const, label: 'Mihomo' },
  { key: 'geo' as const, label: 'Geo 数据' },
]
const stacks = ref<Schemas['StackSummary'][]>([])
onMounted(async () => {
  stacks.value = (await api.GET('/api/stacks')).data?.stacks ?? []
})

/** HH:MM 与 NTimePicker 的毫秒值互转。 */
function timeValue(text: string): number | null {
  const [h, m] = text.split(':').map(Number)
  return Number.isFinite(h) && Number.isFinite(m) ? new Date(2000, 0, 1, h, m).getTime() : null
}
function timeText(value: number | null): string {
  if (value === null) return ''
  const date = new Date(value)
  return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
}

/** 粘贴 http://user:pass@host:port 或 socks5://… 时拆分填入各字段。 */
function pasteProxyURL(text: string) {
  const proxy = settings.value?.download_proxy
  if (!proxy) return false
  try {
    const url = new URL(text.trim())
    if (!['http:', 'socks5:', 'socks5h:'].includes(url.protocol)) return false
    proxy.protocol = url.protocol === 'http:' ? 'http' : 'socks5'
    proxy.host = url.hostname.replace(/^\[|\]$/g, '')
    proxy.port = Number(url.port) || (proxy.protocol === 'http' ? 8080 : 1080)
    proxy.username = decodeURIComponent(url.username)
    proxy.password = decodeURIComponent(url.password)
    if (url.protocol === 'socks5h:') proxy.remote_dns = true
    message.success('已按 URL 填入代理字段')
    return true
  } catch {
    return false
  }
}

/** 模板中 settings 非空时使用的别名，避免多处非空断言。 */
const form = computed(() => settings.value as Settings)

const proxyTest = ref<Schemas['ProxyTestResult'] | null>(null)
const testing = ref(false)
const failureLabels: Record<string, string> = {
  proxy_connect: '连接代理失败',
  proxy_auth: '代理认证失败',
  timeout: '超时',
  tls: 'TLS 错误',
  upstream: '上游返回错误',
  network: '网络错误',
  config: '代理设置不可用',
}
function failureLabel(kind: string): string {
  return failureLabels[kind] ?? '失败'
}

async function testProxy() {
  if (!settings.value) return
  testing.value = true
  proxyTest.value = null
  const { data } = await api.POST('/api/config/download-proxy/test', { body: settings.value.download_proxy })
  testing.value = false
  proxyTest.value = data ?? null
}
/** 订阅更新间隔：秒 ⇄ 小时/天。 */
const intervalUnit = ref<3600 | 86400>(86400)
const intervalValue = computed({
  get: () => (form.value ? Math.round((form.value.subscription.interval / intervalUnit.value) * 100) / 100 : 0),
  set: (value: number) => {
    if (form.value) form.value.subscription.interval = Math.round(value * intervalUnit.value)
  },
})

const notifyEvents = [
  { value: 'daily_limit', label: '每日阈值触发' },
  { value: 'daily_warning', label: '每日用量预警' },
  { value: 'quota_limit', label: '月度额度超限' },
  { value: 'quota_warning', label: '额度预警' },
  { value: 'expired', label: '到期暂停' },
  { value: 'expiring', label: '即将到期' },
  { value: 'egress_down', label: 'Stack 出口异常' },
  { value: 'egress_recovered', label: 'Stack 出口恢复' },
]
const testingMail = ref(false)
const mailResult = ref('')
async function testMail() {
  if (!form.value) return
  testingMail.value = true
  mailResult.value = ''
  const { data, error } = await api.POST('/api/notifications/test', { body: form.value.notify })
  testingMail.value = false
  mailResult.value = error ? problemMessage(error) : data?.error || 'ok'
}

/** 节点导出接口挂在面板地址下，与面板共用端口。 */
const exportURL = new URL('export/nodes', document.baseURI).href

async function revealSettings() {
  const { data } = await api.GET('/api/config/settings/secrets')
  if (!data || !settings.value) return
  // 当前值和基准快照里仍是占位的字段一并换成明文，取回明文本身不算修改。
  const fill = (target: Settings) => {
    if (target.node_export.token === MASKED) target.node_export.token = data.node_export.token
    if (target.download_proxy.password === MASKED) target.download_proxy.password = data.download_proxy.password
  }
  fill(settings.value)
  patchBaseline(fill)
}

const proxyStack = () => stacks.value.find((s) => s.name === settings.value?.download_proxy.stack)


onMounted(async () => {
  const { data } = await api.GET('/api/config/settings')
  settings.value = data ?? null
  markClean()
  if (knownSection(initialSection)) {
    await nextTick()
    document.getElementById(`settings-${initialSection}`)?.scrollIntoView({ block: 'start' })
  }
})

// 已在本页时从全局搜索选中另一个分组：切到它所在的标签页并滚动过去，随后从地址栏去掉 section，再次选中同一分组仍然生效。
watch(() => route.query.section, async (section) => {
  const name = String(section ?? '')
  if (route.name !== 'settings' || !knownSection(name) || !settings.value) return
  tab.value = tabOfSection(name)
  await nextTick()
  document.getElementById(`settings-${name}`)?.scrollIntoView({ block: 'start' })
  if (route.query.section) void router.replace({ query: { ...route.query, section: undefined } })
})

/** 两个安全字段合起来只决定一件事：校验是否要求公开的 SOCKS5/HTTP 入站设置密码。 */
const publicAuthRequired = computed(() => !!settings.value && settings.value.security.require_auth_for_public_socks_http && !settings.value.security.allow_noauth_public)
function togglePublicAuth(value: boolean) {
  if (!settings.value) return
  if (value) {
    settings.value.security.require_auth_for_public_socks_http = true
    settings.value.security.allow_noauth_public = false
    return
  }
  dialog.warning({
    title: '不再要求公开入站设置密码？',
    content: '关闭后，监听在非本机地址的 SOCKS5/HTTP 入站可以不设密码，任何能访问该端口的人都能使用代理。',
    positiveText: '仍要关闭',
    negativeText: '取消',
    onPositiveClick: () => {
      if (settings.value) settings.value.security.allow_noauth_public = true
    },
  })
}

async function save() {
  if (!settings.value) return
  saving.value = true
  const { data, error } = await api.PUT('/api/config/settings', { body: settings.value })
  saving.value = false
  if (error) {
    errors.value = splitProblem(error)
    // 出错的字段不在当前标签页时切过去。
    const first = Object.keys(errors.value.fields)[0]
    if (first && !tabErrors.value.has(tab.value)) tab.value = tabForField(first)
    return
  }
  errors.value = emptyErrors()
  settings.value = data!
  markClean()
  void refreshPauseWindow()
  void notifySaved(message, '已保存，后续后台任务立即使用新设置；生成的配置没有变化，无需应用')
}
</script>

<template>
  <NSpin :show="!settings">
    <NForm v-if="settings" label-placement="left" label-width="180" class="settings-form">
      <ErrorList :errors="errors.global" />
      <div class="settings-toolbar">
        <NInput v-model:value="query" clearable placeholder="搜索设置项，例如 SMTP、端口、时区" class="settings-search" :input-props="{ 'aria-label': '搜索设置项' }">
          <template #prefix><NIcon><SearchOutline /></NIcon></template>
        </NInput>
        <NText v-if="matches" depth="3" class="settings-hint">
          <template v-if="matchCount">找到 {{ matchCount }} 个分组，标签上的数字是各页命中的分组数；清空搜索后显示全部。</template>
          <template v-else>没有匹配的设置项。</template>
        </NText>
      </div>
      <NTabs v-model:value="tab" type="line" class="detail-tabs settings-tabs">
        <NTabPane name="basic" :tab="tabLabel('basic', '基本')" display-directive="show">
          <SettingsSection v-show="sectionShown('basic')" title="基本" name="basic">
            <NFormItem label="对外地址" v-bind="f('basic.external_host', '订阅节点默认使用的域名或 IP')">
              <NInput v-model:value="form.basic.external_host" placeholder="proxy.example.com" />
            </NFormItem>
            <NFormItem label="面板地址" v-bind="f('basic.panel_url', '经 nginx 访问面板的地址，用于通知邮件中的链接')">
              <NInput v-model:value="form.basic.panel_url" placeholder="https://panel.example.com" />
            </NFormItem>
            <NFormItem label="时区" v-bind="f('basic.timezone', '流量统计的日、月边界按此时区计算')">
              <NSelect v-model:value="form.basic.timezone" :options="timezones" filterable />
            </NFormItem>
          </SettingsSection>
          <SettingsSection v-show="sectionShown('pause')" title="停服时段" name="pause">
            <NFormItem label="每日停服时段" v-bind="f('traffic.pause_windows', '时段内自动暂停用户，结束后自动恢复；按上方的时区计算，可跨零点。用户可在详情页改用自己的时段或不受限制。留空表示不停服')">
              <PauseWindowsInput :value="form.traffic.pause_windows ?? []" @update:value="(v: string[]) => (form.traffic.pause_windows = v)" />
            </NFormItem>
            <NFormItem label="暂停时断开现有连接" v-bind="f('traffic.disconnect_on_pause', '停服时段、每日阈值、额度和到期等自动暂停生效后，重启引用了被暂停用户的 stack 的 xray，切断已建立的连接；同一 stack 上其他用户的连接会被短暂中断')">
              <NSwitch v-model:value="form.traffic.disconnect_on_pause" />
            </NFormItem>
          </SettingsSection>
          <SettingsSection v-show="sectionShown('blocklist')" title="域名黑名单" name="blocklist">
            <NFormItem label="全局域名黑名单" v-bind="f('traffic.blocked_domains', '对全部连接生效，由 xray 在服务端拦截，不修改订阅。每行一条：example.com 拦截该域名及全部子域名，full:www.example.com 只拦截该域名；国际化域名写成 xn-- 形式。用户详情页可以为单个用户追加名单。名单不为空的 stack 会开启流量嗅探，用 HTTP Host、TLS SNI 和 QUIC 识别域名；使用加密 SNI（ECH）或不带域名的连接无法识别；SSH 等由服务器先发数据的协议建立连接时会稍慢。保存后需要应用')">
              <BlockedDomainsInput :value="form.traffic.blocked_domains ?? []" @update:value="(v: string[]) => (form.traffic.blocked_domains = v)" />
            </NFormItem>
          </SettingsSection>
          <SettingsSection v-show="sectionShown('security')" title="安全" name="security">
            <NFormItem label="公开入站必须设密码" feedback="开启时，监听在非本机地址的 SOCKS5/HTTP 入站没有设置密码时，保存和应用都会校验失败；本机地址的入站不受限制">
              <NSwitch :value="publicAuthRequired" @update:value="togglePublicAuth" />
            </NFormItem>
          </SettingsSection>
        </NTabPane>
        <NTabPane name="subscription" :tab="tabLabel('subscription', '订阅')" display-directive="show">
          <SettingsSection v-show="sectionShown('subscription')" title="订阅" name="subscription">
            <NAlert type="info" :bordered="false" style="margin-bottom: 12px">
              订阅由单独部署的订阅端提供。这里的设置随订阅包下发，在「发布订阅包」页签发并拷贝到订阅端后生效。
            </NAlert>
            <NFormItem label="来源名" v-bind="f('subscription.source_name', '订阅端按来源名找到验签用的公钥；多台管理端向同一个订阅端发布时各用不同的来源名')">
              <NInput v-model:value="form.subscription.source_name" placeholder="main" />
            </NFormItem>
            <NFormItem label="订阅端对外地址" v-bind="f('subscription.public_base_url', '订阅链接和 Surge managed 首行使用的地址，例如 https://sub.example.com；为空时订阅端用请求地址，面板上只显示链接路径')">
              <NInput v-model:value="form.subscription.public_base_url" placeholder="https://sub.example.com" />
            </NFormItem>
            <NFormItem label="全局展示名模板" v-bind="f('subscription.display_template', '最低模板优先级；仅当入站用户覆盖、所选用户档案和入站模板均为空时生效')">
              <TemplateInput v-model:value="form.subscription.display_template" />
            </NFormItem>
            <NFormItem label="Surge managed config" v-bind="f('subscription.surge_managed')">
              <NSwitch v-model:value="form.subscription.surge_managed" />
            </NFormItem>
            <NFormItem label="更新间隔" v-bind="f('subscription.interval')">
              <NSpace>
                <NInputNumber v-model:value="intervalValue" :min="0" :precision="2" />
                <NSelect v-model:value="intervalUnit" :options="[{ label: '小时', value: 3600 }, { label: '天', value: 86400 }]" style="width: 90px" />
              </NSpace>
            </NFormItem>
            <NFormItem label="strict" v-bind="f('subscription.strict')">
              <NSwitch v-model:value="form.subscription.strict" />
            </NFormItem>
            <NFormItem label="自动发布订阅包" v-bind="f('subscription.manual_publish', '订阅内容变化后（包括应用 stack、暂停或恢复用户、修改用户和模板），自动签发并发布到已部署且测试通过的在线订阅端；关闭后只能在「发布订阅包」页面手动签发')">
              <NSwitch :value="!form.subscription.manual_publish" @update:value="(on: boolean) => (form.subscription.manual_publish = !on)" />
            </NFormItem>
            <NFormItem label="停用或到期用户" v-bind="f('subscription.disabled_response')">
              <NRadioGroup v-model:value="form.subscription.disabled_response">
                <NRadioButton value="not_found">返回 404</NRadioButton>
                <NRadioButton value="empty">返回空订阅</NRadioButton>
              </NRadioGroup>
            </NFormItem>
          </SettingsSection>
        </NTabPane>
        <NTabPane name="monitor" :tab="tabLabel('monitor', '流量与通知')" display-directive="show">
          <SettingsSection v-show="sectionShown('traffic')" title="流量" name="traffic">
            <NFormItem label="启用流量监控" v-bind="f('traffic.enabled', '开启后所有 stack 强制启用 stats 和用户级计数，生成物变化需要应用')">
              <NSwitch v-model:value="form.traffic.enabled" />
            </NFormItem>
            <NFormItem label="采集间隔" v-bind="f('traffic.interval')">
              <NRadioGroup v-model:value="form.traffic.interval">
                <NRadioButton :value="5">5 秒</NRadioButton>
                <NRadioButton :value="10">10 秒</NRadioButton>
                <NRadioButton :value="30">30 秒</NRadioButton>
              </NRadioGroup>
            </NFormItem>
            <NFormItem label="分钟明细保留" v-bind="f('traffic.retention_minute_days', '天，0 表示永久；至少 2 天')">
              <NInputNumber v-model:value="form.traffic.retention_minute_days" :min="0" />
            </NFormItem>
            <NFormItem label="小时明细保留" v-bind="f('traffic.retention_hour_days', '天，0 表示永久；至少 3 天')">
              <NInputNumber v-model:value="form.traffic.retention_hour_days" :min="0" />
            </NFormItem>
            <NFormItem label="日明细保留" v-bind="f('traffic.retention_day_days', '天，0 表示永久；至少 62 天')">
              <NInputNumber v-model:value="form.traffic.retention_day_days" :min="0" />
            </NFormItem>
            <NFormItem label="月明细保留" v-bind="f('traffic.retention_month_days', '天，0 表示永久')">
              <NInputNumber v-model:value="form.traffic.retention_month_days" :min="0" />
            </NFormItem>
            <NFormItem label="统计访问域名" v-bind="f('traffic.access_stats', '读取 xray 访问日志，按用户统计访问的主域名（连接次数），在「流量明细 → 访问域名」和用户详情的「用量」中查看；需要 Stack 开启「Xray 记录访问日志」。保存后立即生效，只统计开启以后的访问')">
              <NSwitch v-model:value="form.traffic.access_stats" />
            </NFormItem>
            <NFormItem label="访问域名保留" v-bind="f('traffic.access_retention_days', '天，1～3650')">
              <NInputNumber v-model:value="form.traffic.access_retention_days" :min="1" :max="3650" />
            </NFormItem>
          </SettingsSection>
          <SettingsSection v-show="sectionShown('notify')" title="通知" name="notify">
            <NFormItem label="启用邮件通知" v-bind="f('notify.enabled', '未启用时事件只记录在「通知记录」页')">
              <NSwitch v-model:value="form.notify.enabled" />
            </NFormItem>
            <NFormItem label="SMTP 服务器" v-bind="f('notify.host')">
              <NInput v-model:value="form.notify.host" placeholder="smtp.example.com" />
            </NFormItem>
            <NFormItem label="端口" v-bind="f('notify.port')">
              <NInputNumber v-model:value="form.notify.port" :min="1" :max="65535" :show-button="false" />
            </NFormItem>
            <NFormItem label="加密方式" v-bind="f('notify.security')">
              <NRadioGroup v-model:value="form.notify.security">
                <NRadioButton value="none">无</NRadioButton>
                <NRadioButton value="starttls">STARTTLS</NRadioButton>
                <NRadioButton value="tls">TLS</NRadioButton>
              </NRadioGroup>
            </NFormItem>
            <NFormItem label="用户名" v-bind="f('notify.username', '可留空（不认证）；未加密的连接不能使用密码认证')">
              <NInput v-model:value="form.notify.username" />
            </NFormItem>
            <NFormItem label="密码" v-bind="f('notify.password')">
              <SecretInput v-model:value="form.notify.password" :reveal="revealSettings" />
            </NFormItem>
            <NFormItem label="发件人" v-bind="f('notify.from', '邮箱地址，可带显示名')">
              <NInput v-model:value="form.notify.from" placeholder="proxystack &lt;panel@example.com&gt;" />
            </NFormItem>
            <NFormItem label="收件人" v-bind="f('notify.to')">
              <NDynamicTags v-model:value="form.notify.to" />
            </NFormItem>
            <NFormItem label="通知事件" v-bind="f('notify.events')">
              <NCheckboxGroup v-model:value="form.notify.events">
                <NSpace>
                  <NCheckbox v-for="event in notifyEvents" :key="event.value" :value="event.value" :label="event.label" />
                </NSpace>
              </NCheckboxGroup>
            </NFormItem>
            <NFormItem label="用量预警比例" v-bind="f('notify.warn_percent', '%')">
              <NInputNumber v-model:value="form.notify.warn_percent" :min="1" :max="100" />
            </NFormItem>
            <NFormItem label="到期提前提醒" v-bind="f('notify.expire_days', '天')">
              <NInputNumber v-model:value="form.notify.expire_days" :min="1" />
            </NFormItem>
            <NFormItem label="测试">
              <NSpace vertical>
                <NButton :loading="testingMail" @click="testMail">发送测试邮件</NButton>
                <NAlert v-if="mailResult" :type="mailResult === 'ok' ? 'success' : 'error'" :bordered="false">
                  {{ mailResult === 'ok' ? '已发送，请检查收件箱' : mailResult }}
                </NAlert>
              </NSpace>
            </NFormItem>
          </SettingsSection>
        </NTabPane>
        <NTabPane name="download" :tab="tabLabel('download', '下载与依赖')" display-directive="show">
          <SettingsSection v-show="sectionShown('download_proxy')" title="下载代理" name="download_proxy">
            <NFormItem label="模式" v-bind="f('download_proxy.mode', '作用于依赖管理和面板自更新的全部对外请求，保存后立即生效')">
              <NRadioGroup v-model:value="form.download_proxy.mode">
                <NRadioButton value="direct">直连</NRadioButton>
                <NRadioButton value="stack">经指定 stack 的 mihomo 监听</NRadioButton>
                <NRadioButton value="custom">自定义代理</NRadioButton>
              </NRadioGroup>
            </NFormItem>
            <template v-if="form.download_proxy.mode === 'stack'">
              <NFormItem label="stack" v-bind="f('download_proxy.stack', '该 stack 未运行时，下载作业会直接报错')">
                <NSpace vertical style="width: 100%">
                  <NSelect v-model:value="form.download_proxy.stack" :options="stacks.map((s) => ({ label: s.name, value: s.name }))" filterable />
                  <EntityLink v-if="form.download_proxy.stack" kind="stack" :name="form.download_proxy.stack" label="查看 Stack ↗" target="_blank" rel="noopener" />
                </NSpace>
              </NFormItem>
              <NFormItem label="监听" v-bind="f('download_proxy.listener', '监听要求认证时自动使用其账号')">
                <NRadioGroup v-model:value="form.download_proxy.listener">
                  <NRadioButton value="socks">SOCKS（{{ proxyStack()?.mihomo_socks || '—' }}）</NRadioButton>
                  <NRadioButton value="http" :disabled="!proxyStack()?.mihomo_http">HTTP（{{ proxyStack()?.mihomo_http || '未启用' }}）</NRadioButton>
                </NRadioGroup>
              </NFormItem>
            </template>
            <template v-if="form.download_proxy.mode === 'custom'">
              <NFormItem label="协议" v-bind="f('download_proxy.protocol')">
                <NRadioGroup v-model:value="form.download_proxy.protocol">
                  <NRadioButton value="http">HTTP</NRadioButton>
                  <NRadioButton value="socks5">SOCKS5</NRadioButton>
                </NRadioGroup>
              </NFormItem>
              <NFormItem label="主机" v-bind="f('download_proxy.host', '也可以直接粘贴 http://user:pass@host:port 或 socks5://… 形式的地址')">
                <NInput
                  v-model:value="form.download_proxy.host"
                  placeholder="proxy.example.com"
                  @paste="(e: ClipboardEvent) => pasteProxyURL(e.clipboardData?.getData('text') ?? '') && e.preventDefault()"
                />
              </NFormItem>
              <NFormItem label="端口" v-bind="f('download_proxy.port')">
                <NInputNumber v-model:value="form.download_proxy.port" :min="1" :max="65535" :show-button="false" />
              </NFormItem>
              <NFormItem label="用户名" v-bind="f('download_proxy.username', '用户名和密码可同时留空')">
                <NInput v-model:value="form.download_proxy.username" :input-props="{ autocomplete: 'off' }" />
              </NFormItem>
              <NFormItem label="密码" v-bind="f('download_proxy.password')">
                <SecretInput v-model:value="form.download_proxy.password" :reveal="revealSettings" />
              </NFormItem>
              <NFormItem v-if="form.download_proxy.protocol === 'socks5'" label="由代理解析域名" v-bind="f('download_proxy.remote_dns', '开启后本机不做 DNS 查询')">
                <NSwitch v-model:value="form.download_proxy.remote_dns" />
              </NFormItem>
            </template>
            <NFormItem label="测试连接">
              <NSpace vertical style="width: 100%">
                <NButton :loading="testing" @click="testProxy">用当前表单测试</NButton>
                <NAlert v-if="proxyTest" :type="proxyTest.ok ? 'success' : 'error'" :bordered="false">
                  <template v-if="proxyTest.ok">经代理访问 GitHub API 成功，耗时 {{ proxyTest.latency_ms }} ms</template>
                  <template v-else>{{ failureLabel(proxyTest.kind) }}：{{ proxyTest.message }}</template>
                </NAlert>
              </NSpace>
            </NFormItem>
          </SettingsSection>
          <SettingsSection v-show="sectionShown('deps')" title="依赖源" name="deps">
            <template v-for="dep in depKeys" :key="dep.key">
              <NFormItem :label="`${dep.label} 版本`" v-bind="f(`deps.${dep.key}.version`, 'latest 或具体版本号；「依赖」页的升级按钮按此安装')">
                <NInput v-model:value="form.deps[dep.key].version" />
              </NFormItem>
              <NFormItem :label="`${dep.label} 来源`" v-bind="f(`deps.${dep.key}.source`)">
                <NRadioGroup v-model:value="form.deps[dep.key].source">
                  <NRadioButton value="auto">自动</NRadioButton>
                  <NRadioButton value="github">GitHub</NRadioButton>
                  <NRadioButton value="custom">自定义 URL</NRadioButton>
                </NRadioGroup>
              </NFormItem>
              <template v-if="form.deps[dep.key].source === 'custom'">
                <NFormItem label="下载地址" v-bind="f(`deps.${dep.key}.url`)">
                  <NInput v-model:value="form.deps[dep.key].url" placeholder="https://…" />
                </NFormItem>
                <NFormItem label="sha256" v-bind="f(`deps.${dep.key}.sha256`, '自定义地址必须提供 sha256')">
                  <NInput v-model:value="form.deps[dep.key].sha256" />
                </NFormItem>
                <NFormItem label="归档成员" v-bind="f(`deps.${dep.key}.archive_member`, '归档中有多个文件时指定要取的成员')">
                  <NInput v-model:value="form.deps[dep.key].archive_member" />
                </NFormItem>
              </template>
            </template>
            <NFormItem label="mihomo 构建变体" v-bind="f('deps.mihomo_variant', '自动：CPU 不支持较新指令集时使用 compatible 构建')">
              <NRadioGroup v-model:value="form.deps.mihomo_variant">
                <NRadioButton value="auto">自动</NRadioButton>
                <NRadioButton value="standard">标准</NRadioButton>
                <NRadioButton value="compatible">compatible</NRadioButton>
              </NRadioGroup>
            </NFormItem>
            <NFormItem label="自动升级 xray">
              <NSwitch v-model:value="form.deps.auto_upgrade_xray" />
            </NFormItem>
            <NFormItem label="自动升级 mihomo">
              <NSwitch v-model:value="form.deps.auto_upgrade_mihomo" />
            </NFormItem>
            <NFormItem label="维护时间窗" v-bind="f('deps.window_start')">
              <div class="range-inputs">
                <NTimePicker :value="timeValue(form.deps.window_start)" format="HH:mm" @update:value="(v) => (form.deps.window_start = timeText(v))" />
                <span>—</span>
                <NTimePicker :value="timeValue(form.deps.window_end)" format="HH:mm" @update:value="(v) => (form.deps.window_end = timeText(v))" />
              </div>
            </NFormItem>
          </SettingsSection>
        </NTabPane>
        <NTabPane name="advanced" :tab="tabLabel('advanced', '高级')" display-directive="show">
          <SettingsSection v-show="sectionShown('defaults')" title="默认值" name="defaults">
            <NFormItem label="Mihomo 模式">
              <NRadioGroup v-model:value="form.defaults.mihomo.mode">
                <NRadioButton value="Rule">Rule</NRadioButton>
                <NRadioButton value="Global">Global</NRadioButton>
                <NRadioButton value="Direct">Direct</NRadioButton>
              </NRadioGroup>
            </NFormItem>
            <NFormItem label="Mihomo 日志级别">
              <NSelect v-model:value="form.defaults.mihomo.loglevel" :options="mihomoLevelOptions" />
            </NFormItem>
            <NFormItem label="Xray 日志级别">
              <NSelect v-model:value="form.defaults.xray.loglevel" :options="xrayLevelOptions" />
            </NFormItem>
            <NFormItem label="Xray 记录访问日志" v-bind="f('defaults.xray.access_log', '记录客户端请求日志，独立于错误日志级别；日志级别为 none 时关闭全部日志。')">
              <NSwitch :value="form.defaults.xray.access_log ?? true" @update:value="(value) => (form.defaults.xray.access_log = value)" aria-label="Xray 记录访问日志" />
            </NFormItem>
            <div class="advanced-fields">
            <NText depth="3" class="advanced-fields-title">Xray API、统计与 policy：流量监控开启时由系统强制打开所需项</NText>
            <NFormItem label="Xray API">
              <NSwitch v-model:value="form.defaults.xray.api_enabled" />
            </NFormItem>
            <NFormItem label="API 服务" v-bind="f('defaults.xray.api_services', 'HandlerService 是用户启停即时生效的前提，StatsService 用于流量统计')">
              <NCheckboxGroup v-model:value="form.defaults.xray.api_services">
                <NSpace>
                  <NCheckbox value="HandlerService">HandlerService</NCheckbox>
                  <NCheckbox value="StatsService">StatsService</NCheckbox>
                </NSpace>
              </NCheckboxGroup>
            </NFormItem>
            <NFormItem label="Xray stats">
              <NSwitch v-model:value="form.defaults.xray.stats" />
            </NFormItem>
            <NFormItem label="Xray policy">
              <NSwitch v-model:value="form.defaults.xray.policy.enabled" />
            </NFormItem>
            <div id="settings-policy" class="policy-flags">
              <NText depth="3" class="advanced-fields-title">policy 统计开关</NText>
                <NGrid :cols="1">
                  <NFormItemGi v-for="flag in policyFlags" :key="flag.key" :label="flag.label" label-width="280">
                    <NSwitch v-model:value="form.defaults.xray.policy[flag.key]" />
                  </NFormItemGi>
                </NGrid>
            </div>
            </div>
          </SettingsSection>
          <SettingsSection v-show="sectionShown('port_pools')" title="端口池" name="port_pools">
            <NFormItem v-for="pool in pools" :key="pool.key" :label="pool.label" v-bind="f(`port_pools.${pool.key}`)">
              <div class="range-inputs">
                <NInputNumber v-model:value="form.port_pools[pool.key].start" :min="1" :max="65535" :show-button="false" />
                <span>—</span>
                <NInputNumber v-model:value="form.port_pools[pool.key].end" :min="1" :max="65535" :show-button="false" />
              </div>
            </NFormItem>
          </SettingsSection>
          <SettingsSection v-show="sectionShown('node_export')" title="节点导出" name="node_export">
            <NFormItem label="开启只读导出" v-bind="f('node_export.enabled', '其他 proxystack-web 实例可以凭 token 拉取本机节点（远端节点源）')">
              <NSwitch v-model:value="form.node_export.enabled" />
            </NFormItem>
            <template v-if="form.node_export.enabled">
              <NFormItem label="token" v-bind="f('node_export.token', '至少 16 个字符')">
                <SecretInput v-model:value="form.node_export.token" :generate="() => randomString(32)" :reveal="revealSettings" />
              </NFormItem>
              <NFormItem label="导出地址">
                <NText code>{{ exportURL }}</NText>
              </NFormItem>
            </template>
          </SettingsSection>
        </NTabPane>
      </NTabs>
      <NSpace class="actions" :class="{ dirty }" align="center">
        <NButton type="primary" :loading="saving" :disabled="!dirty" @click="save">保存</NButton>
        <NText v-if="dirty" type="warning">有未保存的修改</NText>
        <NText v-else depth="3">没有未保存的修改</NText>
      </NSpace>
    </NForm>
  </NSpin>
</template>

<style scoped>
.settings-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 12px 20px; margin-bottom: 10px; }
.settings-search { flex: 1 1 280px; max-width: 420px; }
.settings-hint { display: block; margin-bottom: 12px; font-size: 13px; }
.advanced-fields { margin-top: 4px; padding-top: 12px; border-top: 1px dashed var(--ps-border); }
.policy-flags { margin-top: 8px; }
.advanced-fields-title { display: block; margin-bottom: 8px; font-size: 13px; }
.range-inputs {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  align-items: center;
  gap: 12px;
  width: 100%;
  max-width: 420px;
}
.actions {
  margin-top: 6px;
  padding: 14px 20px;
  border: 1px solid var(--ps-border);
  border-radius: 12px;
  background: var(--ps-surface);
}
/* 有未保存的修改时保存栏固定在底部，避免没有修改时一直遮挡内容。 */
.actions.dirty {
  position: sticky;
  bottom: 16px;
  z-index: 10;
  border-color: var(--ps-accent);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.15);
}
</style>
