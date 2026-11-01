import type { CenterTask } from '../tasks'

export const taskStatus: Record<CenterTask['status'], { label: string; type: 'info' | 'success' | 'error' | 'default' | 'warning' }> = {
  running: { label: '运行中', type: 'info' },
  succeeded: { label: '成功', type: 'success' },
  failed: { label: '失败', type: 'error' },
  skipped: { label: '跳过', type: 'default' },
  timeout: { label: '超时', type: 'warning' },
}

export function taskDuration(task: CenterTask, now: number) {
  const end = task.status === 'running' ? now : Date.parse(task.finished_at)
  const seconds = Math.max(0, Math.floor((end - Date.parse(task.started_at)) / 1000))
  if (!Number.isFinite(seconds)) return '—'
  if (seconds < 60) return `${seconds} 秒`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
  return `${Math.floor(seconds / 3600)} 小时 ${Math.floor(seconds % 3600 / 60)} 分`
}

export const taskActor = (task: CenterTask) => task.source === 'job' ? (task.actor === 'manual' ? '手动执行' : '计划触发') : task.actor
