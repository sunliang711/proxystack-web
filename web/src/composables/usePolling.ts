import { onScopeDispose, ref, shallowRef, watch, type WatchSource } from 'vue'

/** 定时读取统计；切换对象和离开页面时取消请求，刷新失败保留上次数据。 */
export function usePolling<T>(source: WatchSource, request: (signal: AbortSignal) => Promise<T>, interval = 10_000) {
  const data = shallowRef<T | null>(null)
  const loading = ref(false)
  const error = ref('')
  const updatedAt = ref(0)
  let controller: AbortController | null = null
  let version = 0
  let stopped = false

  async function refresh() {
    // 慢请求仍在运行时跳过本次轮询，避免请求堆积。
    if (stopped || controller) return
    const current = ++version
    const active = new AbortController()
    controller = active
    loading.value = true
    try {
      const result = await request(active.signal)
      if (stopped || current !== version) return
      data.value = result
      error.value = ''
      updatedAt.value = Date.now()
    } catch (cause) {
      if (stopped || current !== version) return
      error.value = cause instanceof Error ? cause.message : '统计刷新失败'
    } finally {
      if (current === version) {
        controller = null
        loading.value = false
      }
    }
  }

  watch(source, () => {
    version++
    controller?.abort()
    controller = null
    data.value = null
    error.value = ''
    updatedAt.value = 0
    void refresh()
  }, { immediate: true })
  const timer = setInterval(() => { void refresh() }, interval)
  onScopeDispose(() => {
    stopped = true
    version++
    clearInterval(timer)
    controller?.abort()
  })
  return { data, loading, error, updatedAt, refresh }
}
