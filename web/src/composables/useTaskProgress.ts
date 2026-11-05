import { onScopeDispose, ref, watch, type Ref } from 'vue'
import { api, type Schemas } from '../api/client'
import { onTaskEvent } from '../runtime'

type Progress = Schemas['TaskProgress']

/**
 * 跟踪一个后台作业的阶段进度：先读取一次详情作为快照（页面刷新后可以接着显示），之后由事件流更新。
 * 同时记录最近两次下载进度的时间，用来估算下载速度。
 */
export function useTaskProgress(taskID: Ref<number | null | undefined>) {
  const progress = ref<Progress | null>(null)
  const status = ref('')
  /** 下载速度，字节每秒；样本不足时为 0。 */
  const speed = ref(0)
  let last: { at: number; done: number } | null = null

  function accept(next: Progress | null | undefined) {
    if (!next) return
    if (next.stage === 'download' && next.done) {
      const now = Date.now()
      if (last && next.done > last.done && now > last.at) {
        const instant = ((next.done - last.done) * 1000) / (now - last.at)
        // 平滑处理，避免读数跳动。
        speed.value = speed.value ? speed.value * 0.6 + instant * 0.4 : instant
      }
      last = { at: now, done: next.done }
    }
    progress.value = next
  }

  watch(taskID, async (id) => {
    progress.value = null
    status.value = ''
    speed.value = 0
    last = null
    if (!id) return
    const { data } = await api.GET('/api/tasks/{id}', { params: { path: { id } } })
    if (!data || taskID.value !== id) return
    status.value = data.status
    if (!progress.value) accept(data.progress)
  }, { immediate: true })

  const stop = onTaskEvent((event) => {
    if (event.id !== taskID.value) return
    status.value = event.status
    accept(event.progress)
  })
  onScopeDispose(stop)

  return { progress, status, speed }
}
