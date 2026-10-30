import { computed, onBeforeUnmount, onMounted, ref, type Ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'

/** 跟踪表单是否有未保存的改动：离开页面或关闭标签页时要求确认。 */
export function useDirty<T>(state: Ref<T | null>) {
  const baseline = ref('')
  const dirty = computed(() => state.value !== null && baseline.value !== '' && JSON.stringify(state.value) !== baseline.value)

  function markClean() {
    baseline.value = JSON.stringify(state.value)
  }

  onBeforeRouteLeave(() => {
    if (dirty.value && !window.confirm('有未保存的修改，确定离开吗？')) {
      return false
    }
    return true
  })

  const beforeUnload = (event: BeforeUnloadEvent) => {
    if (dirty.value) {
      event.preventDefault()
    }
  }
  onMounted(() => window.addEventListener('beforeunload', beforeUnload))
  onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))

  return { dirty, markClean }
}
