import { computed, onBeforeUnmount, onMounted, ref, type Ref } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'

/** 跟踪表单是否有未保存的改动：离开页面或关闭标签页时要求确认。 */
export function useDirty<T>(state: Ref<T | null>) {
  const baseline = ref('')
  const dirty = computed(() => state.value !== null && baseline.value !== '' && JSON.stringify(state.value) !== baseline.value)

  function markClean() {
    baseline.value = JSON.stringify(state.value)
  }

  const confirmLeave = () => !dirty.value || window.confirm('有未保存的修改，确定离开吗？')
  onBeforeRouteLeave(confirmLeave)
  // 同一路由换了参数（例如从一个用户跳到另一个用户）也会丢弃当前表单；只改查询参数（切换标签页）不算离开。
  onBeforeRouteUpdate((to, from) => to.path === from.path || confirmLeave())

  const beforeUnload = (event: BeforeUnloadEvent) => {
    if (dirty.value) {
      event.preventDefault()
    }
  }
  onMounted(() => window.addEventListener('beforeunload', beforeUnload))
  onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))

  /** 修改基准快照中的部分字段：当场生效的操作或取回明文凭据后，不应让页面显示为有未保存修改。 */
  function patchBaseline(patch: (saved: T) => void) {
    if (!baseline.value) return
    const saved = JSON.parse(baseline.value) as T
    patch(saved)
    baseline.value = JSON.stringify(saved)
  }

  /** 基准快照的解析结果，供页面按区域判断哪些字段改过。 */
  const saved = computed(() => (baseline.value ? (JSON.parse(baseline.value) as T) : null))

  return { dirty, markClean, patchBaseline, saved }
}
