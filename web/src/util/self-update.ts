/** 升级阶段：前五个来自后台作业上报，confirm 是页面等待重启后的服务报告新版本，done 表示全部完成。 */
export type UpgradeStage = 'resolve' | 'download' | 'verify' | 'install' | 'restart' | 'confirm' | 'done'

/** 后台作业上报的阶段名转换为升级阶段；未知阶段返回 null。 */
export function upgradeStage(stage: string | undefined): UpgradeStage | null {
  return stage && ['resolve', 'download', 'verify', 'install', 'restart'].includes(stage) ? (stage as UpgradeStage) : null
}

export class VersionCheckTimeoutError extends Error {
  constructor() {
    super('version check timed out')
    this.name = 'VersionCheckTimeoutError'
  }
}

type WaitForVersionOptions = {
  targetVersion: string
  signal: AbortSignal
  probe: (signal: AbortSignal) => Promise<string>
  onReachable?: (version: string) => void
  onUnavailable?: (cause: unknown) => void
  interval?: number
  timeout?: number
  now?: () => number
  delay?: (milliseconds: number, signal: AbortSignal) => Promise<void>
}

function normalizedVersion(value: string): string {
  return value.trim().replace(/^v/, '')
}

function aborted(): DOMException {
  return new DOMException('The operation was aborted', 'AbortError')
}

function delay(milliseconds: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(aborted())
      return
    }
    const timer = window.setTimeout(done, milliseconds)
    signal.addEventListener('abort', cancel, { once: true })
    function done() {
      signal.removeEventListener('abort', cancel)
      resolve()
    }
    function cancel() {
      window.clearTimeout(timer)
      reject(aborted())
    }
  })
}

/** 等待重启后的服务报告目标版本；连接失败是正常的重启阶段，不立即判定失败。 */
export async function waitForVersion(options: WaitForVersionOptions): Promise<string> {
  const interval = options.interval ?? 1500
  const now = options.now ?? Date.now
  const wait = options.delay ?? delay
  const deadline = now() + (options.timeout ?? 300_000)
  while (true) {
    if (options.signal.aborted) throw aborted()
    if (now() >= deadline) throw new VersionCheckTimeoutError()
    try {
      const version = await options.probe(options.signal)
      if (normalizedVersion(version) === normalizedVersion(options.targetVersion)) return version
      options.onReachable?.(version)
    } catch (cause) {
      if (options.signal.aborted) throw aborted()
      options.onUnavailable?.(cause)
    }
    const remaining = deadline - now()
    if (remaining <= 0) throw new VersionCheckTimeoutError()
    await wait(Math.min(interval, remaining), options.signal)
  }
}
