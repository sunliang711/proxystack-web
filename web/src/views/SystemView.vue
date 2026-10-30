<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NCard, NDescriptions, NDescriptionsItem, NSpin, NTag, NText } from 'naive-ui'
import { api, type Schemas } from '../api/client'

const info = ref<Schemas['SystemInfo'] | null>(null)

onMounted(async () => {
  const { data } = await api.GET('/api/system/info')
  info.value = data ?? null
})

const tlsLabels: Record<string, string> = {
  'self-signed': '自签证书',
  acme: 'ACME',
  proxy: '反向代理',
}
</script>

<template>
  <NCard title="系统信息">
    <NSpin :show="!info">
      <NDescriptions v-if="info" label-placement="left" :column="1" bordered>
        <NDescriptionsItem label="版本">
          {{ info.version }}
          <NTag v-if="info.dev_mode" size="small" type="warning" style="margin-left: 8px">开发模式</NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="提交">{{ info.commit }}</NDescriptionsItem>
        <NDescriptionsItem label="构建时间">{{ info.build_date }}</NDescriptionsItem>
        <NDescriptionsItem label="运行环境">{{ info.go_version }} · {{ info.platform }}</NDescriptionsItem>
        <NDescriptionsItem label="面板地址">{{ info.panel_url }}</NDescriptionsItem>
        <NDescriptionsItem label="TLS">{{ tlsLabels[info.tls_mode] ?? info.tls_mode }}</NDescriptionsItem>
        <NDescriptionsItem v-if="info.fingerprint" label="证书指纹">
          <NText code style="word-break: break-all">{{ info.fingerprint }}</NText>
        </NDescriptionsItem>
      </NDescriptions>
    </NSpin>
  </NCard>
</template>
