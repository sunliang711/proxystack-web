<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  NButton, NCheckbox, NCheckboxGroup, NCollapse, NCollapseItem, NDynamicTags, NForm, NFormItem, NGrid, NFormItemGi,
  NInput, NInputNumber, NRadioButton, NRadioGroup, NSelect, NSpace, NSpin, NSwitch, NTimePicker, NAlert, NText,
  useDialog, useMessage,
} from 'naive-ui'
import { api, emptyErrors, fieldState, MASKED, problemMessage, splitProblem, type FormErrors, type Schemas } from '../api/client'
import { randomString } from '../util/generate'
import SecretInput from '../components/SecretInput.vue'
import ErrorList from '../components/ErrorList.vue'
import SettingsSection from '../components/SettingsSection.vue'
import EntityLink from '../components/EntityLink.vue'
import { useSettingsSections } from '../composables/useSettingsSections'
import { useDirty } from '../composables/useDirty'
import { mihomoLogLevels, xrayLogLevels } from '../util/options'

type Settings = Schemas['Settings']
type PoolKey = keyof Settings['port_pools']

const settings = ref<Settings | null>(null)
const errors = ref<FormErrors>(emptyErrors())
const saving = ref(false)
const message = useMessage()
const dialog = useDialog()
const { dirty, markClean } = useDirty(settings)
const { expanded, revealErrors } = useSettingsSections()
const policyExpanded = computed({
  get: () => expanded.value.includes('policy') ? ['policy'] : [],
  set: (names: Array<string | number>) => {
    expanded.value = [...expanded.value.filter((name) => name !== 'policy'), ...names.map(String)]
  },
})

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
  const wasDirty = dirty.value
  if (settings.value.node_export.token === MASKED) settings.value.node_export.token = data.node_export.token
  if (settings.value.download_proxy.password === MASKED) settings.value.download_proxy.password = data.download_proxy.password
  if (!wasDirty) markClean()
}

const proxyStack = () => stacks.value.find((s) => s.name === settings.value?.download_proxy.stack)


onMounted(async () => {
  const { data } = await api.GET('/api/config/settings')
  settings.value = data ?? null
  markClean()
})

function toggleNoAuth(value: boolean) {
  if (!settings.value) return
  if (!value) {
    settings.value.security.allow_noauth_public = false
    return
  }
  dialog.warning({
    title: '允许公开的无认证入站？',
    content: '打开后，监听在非本机地址的 SOCKS5/HTTP 入站可以不设密码，任何能访问该端口的人都能使用代理。',
    positiveText: '仍要允许',
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
    revealErrors(errors.value.fields)
    return
  }
  errors.value = emptyErrors()
  settings.value = data!
  markClean()
  message.success('已保存；需要应用的生成物变更已进入“待应用变更”')
}
</script>

<template>
  <NSpin :show="!settings">
    <NForm v-if="settings" label-placement="left" label-width="180" class="settings-form">
      <ErrorList :errors="errors.global" />
      <NAlert type="info" :bordered="false" style="margin-bottom: 12px">
        下载代理、通知和采集设置保存后立即影响后续后台任务；涉及 xray / mihomo 生成物的设置会进入“待应用变更”，需要再执行应用。
      </NAlert>
      <SettingsSection title="基本" name="basic" v-model:expanded-names="expanded">
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

      <SettingsSection title="端口池" name="port_pools" v-model:expanded-names="expanded">
        <NFormItem v-for="pool in pools" :key="pool.key" :label="pool.label" v-bind="f(`port_pools.${pool.key}`)">
          <div class="range-inputs">
            <NInputNumber v-model:value="form.port_pools[pool.key].start" :min="1" :max="65535" :show-button="false" />
            <span>—</span>
            <NInputNumber v-model:value="form.port_pools[pool.key].end" :min="1" :max="65535" :show-button="false" />
          </div>
        </NFormItem>
      </SettingsSection>

      <SettingsSection title="默认值" name="defaults" v-model:expanded-names="expanded">
        <NFormItem label="Mihomo 模式">
          <NRadioGroup v-model:value="form.defaults.mihomo.mode">
            <NRadioButton value="Rule">Rule</NRadioButton>
            <NRadioButton value="Global">Global</NRadioButton>
            <NRadioButton value="Direct">Direct</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem label="Mihomo 日志级别">
          <NSelect v-model:value="form.defaults.mihomo.loglevel" :options="mihomoLogLevels.map((v) => ({ label: v, value: v }))" />
        </NFormItem>
        <NFormItem label="Xray 日志级别">
          <NSelect v-model:value="form.defaults.xray.loglevel" :options="xrayLogLevels.map((v) => ({ label: v, value: v }))" />
        </NFormItem>
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
        <NCollapse v-model:expanded-names="policyExpanded">
          <NCollapseItem title="policy 统计开关" name="policy">
            <NGrid :cols="1">
              <NFormItemGi v-for="flag in policyFlags" :key="flag.key" :label="flag.label" label-width="280">
                <NSwitch v-model:value="form.defaults.xray.policy[flag.key]" />
              </NFormItemGi>
            </NGrid>
          </NCollapseItem>
        </NCollapse>
      </SettingsSection>

      <SettingsSection title="安全" name="security" v-model:expanded-names="expanded">
        <NFormItem label="公开入站必须认证" label-width="200">
          <NSwitch v-model:value="form.security.require_auth_for_public_socks_http" />
        </NFormItem>
        <NFormItem label="允许公开的无认证入站" label-width="200">
          <NSwitch :value="form.security.allow_noauth_public" @update:value="toggleNoAuth" />
        </NFormItem>
      </SettingsSection>

      <SettingsSection title="订阅" name="subscription" v-model:expanded-names="expanded">
        <NAlert type="info" :bordered="false" style="margin-bottom: 12px">
          订阅由单独部署的订阅端提供。这里的设置随订阅包下发，在「发布订阅包」页签发并拷贝到订阅端后生效。
        </NAlert>
        <NFormItem label="来源名" v-bind="f('subscription.source_name', '订阅端按来源名找到验签用的公钥；多台管理端向同一个订阅端发布时各用不同的来源名')">
          <NInput v-model:value="form.subscription.source_name" placeholder="main" />
        </NFormItem>
        <NFormItem label="订阅端对外地址" v-bind="f('subscription.public_base_url', '订阅链接和 Surge managed 首行使用的地址，例如 https://sub.example.com；为空时订阅端用请求地址，面板上只显示链接路径')">
          <NInput v-model:value="form.subscription.public_base_url" placeholder="https://sub.example.com" />
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
        <NFormItem label="停用或到期用户" v-bind="f('subscription.disabled_response')">
          <NRadioGroup v-model:value="form.subscription.disabled_response">
            <NRadioButton value="not_found">返回 404</NRadioButton>
            <NRadioButton value="empty">返回空订阅</NRadioButton>
          </NRadioGroup>
        </NFormItem>
      </SettingsSection>

      <SettingsSection title="节点导出" name="node_export" v-model:expanded-names="expanded">
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

      <SettingsSection title="流量" name="traffic" v-model:expanded-names="expanded">
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
        <NFormItem label="暂停时断开现有连接" v-bind="f('traffic.disconnect_on_pause', '自动暂停后重启受影响的 xray 以切断已建立的连接，其他用户的连接会被短暂中断')">
          <NSwitch v-model:value="form.traffic.disconnect_on_pause" />
        </NFormItem>
      </SettingsSection>

      <SettingsSection title="通知" name="notify" v-model:expanded-names="expanded">
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

      <SettingsSection title="依赖源" name="deps" v-model:expanded-names="expanded">
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

      <SettingsSection title="下载代理" name="download_proxy" v-model:expanded-names="expanded">
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

      <NSpace class="actions">
        <NButton type="primary" :loading="saving" :disabled="!dirty" @click="save">保存</NButton>
      </NSpace>
    </NForm>
  </NSpin>
</template>

<style scoped>
.range-inputs {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  align-items: center;
  gap: 12px;
  width: 100%;
  max-width: 420px;
}
.actions {
  position: sticky;
  bottom: 16px;
  z-index: 10;
  padding: 14px 20px;
  border: 1px solid var(--ps-border);
  border-radius: 12px;
  background: var(--ps-surface);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.15);
}
</style>
