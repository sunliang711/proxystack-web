<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useOsTheme } from 'naive-ui'
import { basicSetup, EditorView } from 'codemirror'
import { Compartment, EditorState } from '@codemirror/state'
import { yaml } from '@codemirror/lang-yaml'
import { json } from '@codemirror/lang-json'
import { oneDark } from '@codemirror/theme-one-dark'

const props = withDefaults(defineProps<{ language?: 'yaml' | 'json' | 'text'; readonly?: boolean; minHeight?: string }>(), {
  language: 'yaml',
  readonly: false,
  minHeight: '160px',
})
const value = defineModel<string>('value', { required: true })
const host = ref<HTMLDivElement>()
const osTheme = useOsTheme()
const themeSlot = new Compartment()
let view: EditorView | undefined

function languageExtension() {
  if (props.language === 'json') return json()
  if (props.language === 'yaml') return yaml()
  return []
}

onMounted(() => {
  view = new EditorView({
    parent: host.value!,
    state: EditorState.create({
      doc: value.value,
      extensions: [
        basicSetup,
        languageExtension(),
        EditorState.readOnly.of(props.readonly),
        EditorView.lineWrapping,
        themeSlot.of(osTheme.value === 'dark' ? oneDark : []),
        EditorView.theme({ '&': { minHeight: props.minHeight, fontSize: '13px' }, '.cm-scroller': { minHeight: props.minHeight } }),
        EditorView.updateListener.of((update) => {
          if (update.docChanged) {
            value.value = update.state.doc.toString()
          }
        }),
      ],
    }),
  })
})

watch(value, (next) => {
  if (view && next !== view.state.doc.toString()) {
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: next } })
  }
})

watch(osTheme, (theme) => {
  view?.dispatch({ effects: themeSlot.reconfigure(theme === 'dark' ? oneDark : []) })
})

onBeforeUnmount(() => view?.destroy())
</script>

<template>
  <div ref="host" class="code-editor" />
</template>

<style scoped>
.code-editor {
  border: 1px solid var(--n-border-color, rgba(128, 128, 128, 0.3));
  border-radius: 3px;
  overflow: hidden;
  width: 100%;
}
</style>
