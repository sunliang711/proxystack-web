<script setup lang="ts">
import { NButton, NCollapse, NCollapseItem, NInput, NSpace, NTag, NText } from 'naive-ui'
import { templateFunctionHelp, templateFunctions, templateVariableHelp, templateVariables } from '../util/options'

defineProps<{ placeholder?: string }>()
const value = defineModel<string>('value', { required: true })
const delimiters = '{{ ... }}'
const examples = [
  { template: '{{ .stack | toUpper }} {{ .inbound }}', result: 'USA1 vmess（推荐，可区分 Stack 和入站）' },
  { template: '{{ .stack | toUpper }}', result: 'USA1（同一 Stack 只有一个订阅节点时适用）' },
  { template: '{{ .remark | trim | replace " " "-" | toUpper }}', result: 'USA1-VMESS（备注为 usa1 vmess 时）' },
]

function insert(text: string) {
  value.value = (value.value ?? '') + text
}
</script>

<template>
  <div style="width: 100%">
    <NInput v-model:value="value" :placeholder="placeholder ?? '例如 {{ .stack }} {{ .remark }}'" />
    <NText depth="3" style="display: block; margin-top: 4px">
      表达式必须写在 <code>{{ delimiters }}</code> 中，点击标签可插入完整表达式。
    </NText>
    <NSpace :size="4" style="margin-top: 4px">
      <NTag v-for="v in templateVariables" :key="v" :title="templateVariableHelp[v]" size="small" checkable @click="insert(`{{ ${v} }}`)">{{ v }}</NTag>
      <NTag v-for="f in templateFunctions" :key="f" :title="templateFunctionHelp[f]!.description" size="small" type="info" checkable @click="insert(templateFunctionHelp[f]!.example)">{{ f }}</NTag>
    </NSpace>
    <NCollapse style="margin-top: 8px">
      <NCollapseItem title="完整语法帮助与示例" name="help">
        <NSpace vertical :size="12">
          <div>
            使用 Go template 语法：<code>{{ delimiters }}</code> 内计算表达式，括号外的文字原样保留。
            管道符 <code>|</code> 将前一步的结果交给后面的函数；可以连续调用多个函数。
            <code>toUpper .stack</code> 等函数调用也必须放在括号内。
          </div>
          <div>
            <strong>变量</strong>
            <div v-for="v in templateVariables" :key="v"><code>{{ v }}</code>：{{ templateVariableHelp[v] }}</div>
          </div>
          <div>
            <strong>函数（点击示例插入）</strong>
            <div v-for="f in templateFunctions" :key="f">
              <code>{{ f }}</code>：{{ templateFunctionHelp[f]!.description }}；
              <NButton text type="primary" class="template-example" @click="insert(templateFunctionHelp[f]!.example)">{{ templateFunctionHelp[f]!.example }}</NButton>
            </div>
          </div>
          <div>
            <strong>示例（点击插入，假设 Stack 为 usa1、入站为 vmess）</strong>
            <div v-for="example in examples" :key="example.template">
              <NButton text type="primary" class="template-example" @click="insert(example.template)">{{ example.template }}</NButton>
              <div>→ {{ example.result }}</div>
            </div>
          </div>
          <div>
            <strong>留空与覆盖规则</strong>
            <div>依次采用非空模板：入站中的用户覆盖 → 所选用户档案（default 即用户本身）→ 入站模板。</div>
            <div>均未填写模板时，使用生效的备注；备注也为空时，使用 Stack 名称加协议。</div>
            <div>可以填写固定文字。同一用户的所有订阅节点名称必须唯一；只有 Stack 名的模板在同一 Stack 有多个订阅入站时也可能重名。</div>
          </div>
        </NSpace>
      </NCollapseItem>
    </NCollapse>
  </div>
</template>

<style scoped>
.template-example {
  height: auto;
  max-width: 100%;
  white-space: normal;
  text-align: left;
  font-family: monospace;
}
</style>
