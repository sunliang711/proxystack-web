<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NAlert, NButton, NInput, NModal, NSpace, NText } from 'naive-ui'

/** 高风险操作的确认弹窗：列出影响范围，输入确认词后才能执行。 */
const props = defineProps<{
  title: string
  /** 影响范围，每项一句话。 */
  impacts: string[]
  /** 需要输入的确认词。 */
  phrase: string
  positiveText: string
  loading?: boolean
}>()
const emit = defineEmits<{ confirm: [] }>()
const show = defineModel<boolean>('show', { required: true })

const typed = ref('')
watch(show, (open) => { if (open) typed.value = '' })
const matched = computed(() => typed.value.trim() === props.phrase)

function confirm() {
  if (matched.value) emit('confirm')
}
</script>

<template>
  <NModal v-model:show="show" preset="card" :title="title" style="width: 560px; max-width: calc(100vw - 32px)">
    <NSpace vertical :size="14">
      <NAlert type="error" :bordered="false" title="影响范围">
        <ul class="impacts">
          <li v-for="impact in impacts" :key="impact">{{ impact }}</li>
        </ul>
      </NAlert>
      <NText>输入 <NText strong code>{{ phrase }}</NText> 确认执行：</NText>
      <NInput v-model:value="typed" :placeholder="phrase" :input-props="{ 'aria-label': `输入 ${phrase} 确认` }" @keyup.enter="confirm" />
    </NSpace>
    <template #footer>
      <NSpace justify="end">
        <NButton @click="show = false">取消</NButton>
        <NButton type="error" :disabled="!matched" :loading="loading" @click="confirm">{{ positiveText }}</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.impacts { margin: 0; padding-left: 18px; }
.impacts li + li { margin-top: 4px; }
</style>
