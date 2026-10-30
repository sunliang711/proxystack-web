<script setup lang="ts">
import { NButton, NDivider, NFormItem, NInput, NRadioButton, NRadioGroup, NSelect, NSpace, NSwitch } from 'naive-ui'
import { fieldState, type FormErrors, type Schemas } from '../../api/client'
import PortInput from '../../components/PortInput.vue'
import SecretInput from '../../components/SecretInput.vue'
import { randomString } from '../../util/generate'

const props = defineProps<{
  title: string
  kind: 'socks' | 'http'
  listener: Schemas['Listener']
  errors: FormErrors
  prefix: string
  stackId?: number
  ports: number[]
  reveal: () => Promise<void>
}>()

const f = (path: string, hint?: string) => fieldState(props.errors, `${props.prefix}.${path}`, hint)
const listenOptions = ['127.0.0.1', '0.0.0.0', '::'].map((v) => ({ label: v, value: v }))

function addAccount() {
  props.listener.accounts.push({ username: '', password: randomString(24) })
}
</script>

<template>
  <NDivider title-placement="left">{{ title }}</NDivider>
  <NFormItem v-if="kind === 'http'" label="启用 HTTP 监听">
    <NSwitch v-model:value="listener.enabled" />
  </NFormItem>
  <template v-if="kind === 'socks' || listener.enabled">
    <NFormItem label="name" v-bind="f('name')">
      <NInput v-model:value="listener.name" />
    </NFormItem>
    <NFormItem label="listen" v-bind="f('listen', '监听非本机地址时 mihomo 会开启 allow-lan')">
      <NSelect v-model:value="listener.listen" :options="listenOptions" filterable tag />
    </NFormItem>
    <NFormItem label="port" v-bind="f('port')">
      <PortInput v-model:value="listener.port" :pool="kind === 'socks' ? 'mihomo_socks' : 'mihomo_http'" :stack-id="stackId" :exclude="ports" />
    </NFormItem>
    <NFormItem label="认证方式" v-bind="f('auth')">
      <NRadioGroup v-model:value="listener.auth">
        <NRadioButton value="inherit">跟随全局</NRadioButton>
        <NRadioButton value="none">不认证</NRadioButton>
        <NRadioButton value="accounts">指定账号</NRadioButton>
      </NRadioGroup>
    </NFormItem>
    <NFormItem v-if="listener.auth === 'accounts'" label="账号" v-bind="f('accounts')">
      <NSpace vertical style="width: 100%">
        <NSpace v-for="(account, index) in listener.accounts" :key="index" :wrap="false" style="width: 100%">
          <NInput v-model:value="account.username" placeholder="用户名" :status="errors.fields[`${prefix}.accounts[${index}].username`] ? 'error' : undefined" />
          <SecretInput v-model:value="account.password" :generate="() => randomString(24)" :reveal="reveal" />
          <NButton quaternary type="error" @click="listener.accounts.splice(index, 1)">删除</NButton>
        </NSpace>
        <NButton size="small" @click="addAccount">添加账号</NButton>
      </NSpace>
    </NFormItem>
  </template>
</template>
