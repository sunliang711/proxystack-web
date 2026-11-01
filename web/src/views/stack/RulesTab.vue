<script setup lang="ts">
import { computed, ref } from 'vue'
import { NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NSelect, NSpace, NSwitch, NText } from 'naive-ui'
import { fieldState, type FormErrors, type Schemas } from '../../api/client'
import { ipRuleTypes, ruleTypes } from '../../util/options'
import { proxyTargets } from './ports'

type Rule = Schemas['Rule']

const props = defineProps<{ stack: Schemas['Stack']; errors: FormErrors }>()
const textMode = ref(false)
const text = ref('')
const parseError = ref('')

const targets = computed(() => proxyTargets(props.stack).map((t) => ({ label: t, value: t })))
const typeOptions = ruleTypes.map((t) => ({ label: t, value: t }))
const f = (path: string, hint?: string) => fieldState(props.errors, `mihomo.rules.${path}`, hint)
const rules = computed(() => props.stack.mihomo.rules)

function format(rule: Rule): string {
  return [rule.type, rule.value, rule.target, rule.no_resolve ? 'no-resolve' : ''].filter(Boolean).join(',')
}

function parse(line: string): Rule {
  const parts = line.split(',').map((p) => p.trim())
  const noResolve = parts[parts.length - 1] === 'no-resolve'
  if (noResolve) parts.pop()
  if (parts.length < 3) throw new Error(`格式应为 TYPE,VALUE,TARGET：${line}`)
  return { type: parts[0], value: parts.slice(1, -1).join(','), target: parts[parts.length - 1], no_resolve: noResolve }
}

function toggleText(on: boolean) {
  if (on) {
    text.value = rules.value.extra.map(format).join('\n')
    parseError.value = ''
    textMode.value = true
    return
  }
  try {
    rules.value.extra = text.value.split('\n').map((l) => l.trim()).filter((l) => l && !l.startsWith('#')).map(parse)
    parseError.value = ''
    textMode.value = false
  } catch (error) {
    parseError.value = (error as Error).message
  }
}

function move(index: number, delta: number) {
  const [rule] = rules.value.extra.splice(index, 1)
  rules.value.extra.splice(index + delta, 0, rule)
}
</script>

<template>
  <NForm label-placement="left" label-width="90" style="max-width: 980px">
    <NFormItem label="profile" v-bind="f('profile')">
      <NSelect v-model:value="rules.profile" :options="[{ label: 'default', value: 'default' }]" />
    </NFormItem>
    <NFormItem label="final" v-bind="f('final', '其余流量的去向（MATCH）')">
      <NSelect v-model:value="rules.final" :options="targets" filterable />
    </NFormItem>
    <NFormItem label="附加规则">
      <NSpace vertical style="width: 100%">
        <NSpace align="center">
          <NSwitch :value="textMode" @update:value="toggleText" />
          <NText depth="3">文本模式：每行一条 mihomo 规则</NText>
        </NSpace>
        <template v-if="textMode">
          <NInput v-model:value="text" type="textarea" :autosize="{ minRows: 6 }" style="font-family: ui-monospace, monospace" />
          <NAlert v-if="parseError" type="error" :bordered="false">{{ parseError }}</NAlert>
        </template>
        <template v-else>
          <div v-for="(rule, index) in rules.extra" :key="index" class="rule-row">
            <NSelect v-model:value="rule.type" :options="typeOptions" filterable class="rule-type" :status="f(`extra[${index}].type`).validationStatus" />
            <NInput v-model:value="rule.value" placeholder="匹配值" class="rule-value" :status="f(`extra[${index}].value`).validationStatus" />
            <NSelect v-model:value="rule.target" :options="targets" filterable class="rule-target" :status="f(`extra[${index}].target`).validationStatus" />
            <NCheckbox v-if="ipRuleTypes.includes(rule.type)" v-model:checked="rule.no_resolve">no-resolve</NCheckbox>
            <NButton quaternary size="small" :disabled="index === 0" @click="move(index, -1)">上移</NButton>
            <NButton quaternary size="small" :disabled="index === rules.extra.length - 1" @click="move(index, 1)">下移</NButton>
            <NButton quaternary size="small" type="error" @click="rules.extra.splice(index, 1)">删除</NButton>
          </div>
          <NButton size="small" @click="rules.extra.push({ type: 'DOMAIN-SUFFIX', value: '', target: rules.final, no_resolve: false })">添加规则</NButton>
          <NText v-for="(message, path) in Object.fromEntries(Object.entries(errors.fields).filter(([p]) => p.startsWith('mihomo.rules.extra')))" :key="path" type="error" tag="div">
            {{ path.replace('mihomo.rules.extra', '第') }}：{{ message }}
          </NText>
        </template>
      </NSpace>
    </NFormItem>
  </NForm>
</template>

<style scoped>
.rule-row { display: flex; flex-wrap: wrap; gap: 8px; padding: 12px; border: 1px solid var(--ps-border); border-radius: 10px; background: var(--ps-surface-raised); }
.rule-type, .rule-target { flex: 1 1 160px; min-width: 0; }
.rule-value { flex: 2 1 220px; min-width: 0; }
</style>
