<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ diff: string }>()
const lines = computed(() =>
  props.diff.split('\n').map((text) => ({
    text,
    kind: text.startsWith('+++') || text.startsWith('---') ? 'meta' : text.startsWith('@@') ? 'hunk' : text.startsWith('+') ? 'add' : text.startsWith('-') ? 'del' : '',
  })),
)
</script>

<template>
  <pre class="diff"><span v-for="(line, i) in lines" :key="i" :class="line.kind">{{ line.text }}
</span></pre>
</template>

<style scoped>
.diff {
  margin: 0;
  font-family: ui-monospace, monospace;
  font-size: 12px;
  max-height: 360px;
  overflow: auto;
  border: 1px solid rgba(128, 128, 128, 0.25);
  border-radius: 3px;
  padding: 6px 0;
}
.diff span {
  display: block;
  padding: 0 8px;
  white-space: pre;
}
.add {
  background: rgba(46, 160, 67, 0.18);
}
.del {
  background: rgba(248, 81, 73, 0.18);
}
.hunk {
  color: #6f8fbf;
}
.meta {
  color: #999;
}
</style>
