import { onScopeDispose } from 'vue'
import { taskCenter } from '../tasks'

/** 页面只订阅自己提交的任务结果；离开页面后由全局后台任务继续追踪。 */
export function useBackgroundTask(finished?: (status: string) => unknown) {
  const ids = new Set<number>()
  const stop = taskCenter.onFinished((task) => {
    if (task.source === 'task' && ids.delete(task.id)) finished?.(task.status)
  })
  onScopeDispose(() => { stop(); ids.clear() })
  return (id: number) => {
    ids.add(id)
    void taskCenter.track(id).then((task) => {
      // 快速任务可能在提交响应到达前已经推送完成事件，补发给当前页面。
      if (task && task.status !== 'running' && ids.delete(id)) finished?.(task.status)
    })
  }
}
