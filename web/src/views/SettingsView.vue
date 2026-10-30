<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NButton, NCard, NCheckbox, NCheckboxGroup, NCollapse, NCollapseItem, NForm, NFormItem, NGrid, NFormItemGi,
  NInput, NInputNumber, NRadioButton, NRadioGroup, NSelect, NSpace, NSpin, NSwitch, useDialog, useMessage,
} from 'naive-ui'
import { api, emptyErrors, fieldState, splitProblem, type FormErrors, type Schemas } from '../api/client'
import ErrorList from '../components/ErrorList.vue'
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
    return
  }
  errors.value = emptyErrors()
  settings.value = data!
  markClean()
  message.success('已保存，配置变更将在下次应用时生效')
}
</script>

<template>
  <NSpin :show="!settings">
    <NForm v-if="settings" label-placement="left" label-width="160" style="max-width: 860px">
      <ErrorList :errors="errors.global" />
      <NCard title="基本" size="small" class="card">
        <NFormItem label="对外地址" v-bind="f('basic.external_host', '订阅节点默认使用的域名或 IP')">
          <NInput v-model:value="settings.basic.external_host" placeholder="proxy.example.com" />
        </NFormItem>
        <NFormItem label="时区" v-bind="f('basic.timezone', '流量统计的日、月边界按此时区计算')">
          <NSelect v-model:value="settings.basic.timezone" :options="timezones" filterable />
        </NFormItem>
      </NCard>

      <NCard title="端口池" size="small" class="card">
        <NFormItem v-for="pool in pools" :key="pool.key" :label="pool.label" v-bind="f(`port_pools.${pool.key}`)">
          <NSpace align="center">
            <NInputNumber v-model:value="settings.port_pools[pool.key].start" :min="1" :max="65535" :show-button="false" />
            <span>—</span>
            <NInputNumber v-model:value="settings.port_pools[pool.key].end" :min="1" :max="65535" :show-button="false" />
          </NSpace>
        </NFormItem>
      </NCard>

      <NCard title="默认值" size="small" class="card">
        <NFormItem label="Mihomo 模式">
          <NRadioGroup v-model:value="settings.defaults.mihomo.mode">
            <NRadioButton value="Rule">Rule</NRadioButton>
            <NRadioButton value="Global">Global</NRadioButton>
            <NRadioButton value="Direct">Direct</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem label="Mihomo 日志级别">
          <NSelect v-model:value="settings.defaults.mihomo.loglevel" :options="mihomoLogLevels.map((v) => ({ label: v, value: v }))" />
        </NFormItem>
        <NFormItem label="Xray 日志级别">
          <NSelect v-model:value="settings.defaults.xray.loglevel" :options="xrayLogLevels.map((v) => ({ label: v, value: v }))" />
        </NFormItem>
        <NFormItem label="Xray API">
          <NSwitch v-model:value="settings.defaults.xray.api_enabled" />
        </NFormItem>
        <NFormItem label="API 服务" v-bind="f('defaults.xray.api_services', 'HandlerService 是用户启停即时生效的前提，StatsService 用于流量统计')">
          <NCheckboxGroup v-model:value="settings.defaults.xray.api_services">
            <NSpace>
              <NCheckbox value="HandlerService">HandlerService</NCheckbox>
              <NCheckbox value="StatsService">StatsService</NCheckbox>
            </NSpace>
          </NCheckboxGroup>
        </NFormItem>
        <NFormItem label="Xray stats">
          <NSwitch v-model:value="settings.defaults.xray.stats" />
        </NFormItem>
        <NFormItem label="Xray policy">
          <NSwitch v-model:value="settings.defaults.xray.policy.enabled" />
        </NFormItem>
        <NCollapse>
          <NCollapseItem title="policy 统计开关" name="policy">
            <NGrid :cols="1">
              <NFormItemGi v-for="flag in policyFlags" :key="flag.key" :label="flag.label" label-width="280">
                <NSwitch v-model:value="settings.defaults.xray.policy[flag.key]" />
              </NFormItemGi>
            </NGrid>
          </NCollapseItem>
        </NCollapse>
      </NCard>

      <NCard title="安全" size="small" class="card">
        <NFormItem label="公开入站必须认证" label-width="200">
          <NSwitch v-model:value="settings.security.require_auth_for_public_socks_http" />
        </NFormItem>
        <NFormItem label="允许公开的无认证入站" label-width="200">
          <NSwitch :value="settings.security.allow_noauth_public" @update:value="toggleNoAuth" />
        </NFormItem>
      </NCard>

      <NSpace class="actions">
        <NButton type="primary" :loading="saving" :disabled="!dirty" @click="save">保存</NButton>
      </NSpace>
    </NForm>
  </NSpin>
</template>

<style scoped>
.card {
  margin-bottom: 12px;
}
.actions {
  position: sticky;
  bottom: 0;
  padding: 8px 0;
}
</style>
