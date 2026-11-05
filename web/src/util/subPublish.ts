/** 订阅端在线发布的接入状态判定，「SSH 发布与部署」和「发布订阅包」两个页面共用。 */

export interface PublishTargetState {
  id?: number
  name: string
  enabled: boolean
  deploy_state?: string
  last_deploy_at?: string
  last_deploy_error?: string
  last_test_at?: string
  last_test_error?: string
  last_publish_at?: string
  last_publish_error?: string
  last_sequence?: number
}

/** 后端用零值时间表示从未执行。 */
export const hasTime = (value?: string): value is string => !!value && !value.startsWith('0001') && !value.startsWith('1970')

/** 部署之后测试过且通过：最近一次部署会让之前的测试结果失效。 */
export function testedAfterDeploy(target: PublishTargetState): boolean {
  if (!hasTime(target.last_test_at) || target.last_test_error) return false
  return !hasTime(target.last_deploy_at) || Date.parse(target.last_test_at) >= Date.parse(target.last_deploy_at)
}

/** 部署完成且部署后测试通过，才可以选择并签发。 */
export function targetReady(target: PublishTargetState): boolean {
  return target.deploy_state === 'installed' && testedAfterDeploy(target)
}

/** 单个目标的下一步操作；全部完成时返回 null。 */
export function targetNextStep(target: PublishTargetState): 'inspect' | 'deploy' | 'test' | null {
  if (!target.deploy_state) return 'inspect'
  if (target.deploy_state !== 'installed') return 'deploy'
  return testedAfterDeploy(target) ? null : 'test'
}

export type FlowStepKey = 'identity' | 'target' | 'deploy' | 'test' | 'publish'
export type FlowStepStatus = 'done' | 'current' | 'error' | 'waiting'

export interface FlowStep {
  key: FlowStepKey
  title: string
  status: FlowStepStatus
  /** 当前状态的一句话说明。 */
  detail: string
}

export interface FlowInput {
  /** 管理端 SSH 密钥是否已生成；null 表示未知。 */
  identity: boolean | null
  targets: PublishTargetState[]
  /** 订阅内容在上次签发后有变化。 */
  bundlePending: boolean
}

export interface FlowState {
  steps: FlowStep[]
  /** 第一个未完成的步骤；全部完成时为 null。 */
  current: FlowStep | null
  /** 当前步骤针对的订阅端（部署、测试步骤）。 */
  target: PublishTargetState | null
}

/**
 * 在线发布的整体进度：生成密钥 → 添加订阅端 → 部署 → 测试 → 签发并发布。
 * 前四步只要有一个启用的订阅端完成即视为完成；第五步要求最近一次签发已成功送达且之后内容没有变化。
 */
export function publishFlow(input: FlowInput): FlowState {
  const enabled = input.targets.filter((target) => target.enabled)
  const installed = enabled.filter((target) => target.deploy_state === 'installed')
  const ready = enabled.filter(targetReady)
  const published = ready.filter((target) => target.last_sequence && hasTime(target.last_publish_at) && !target.last_publish_error)
  const publishFailed = ready.filter((target) => target.last_publish_error)
  // 部署、测试步骤优先处理离完成最近的目标。
  const deployTarget = enabled.find((target) => target.deploy_state && target.deploy_state !== 'installed') ?? enabled.find((target) => !target.deploy_state) ?? null
  const testTarget = installed.find((target) => !targetReady(target)) ?? null

  const raw: Array<Omit<FlowStep, 'status'> & { done: boolean; error?: boolean }> = [
    {
      key: 'identity', title: '生成 SSH 密钥', done: input.identity === true || enabled.length > 0,
      detail: input.identity === null && !enabled.length ? '在「SSH 发布与部署」中查看' : '管理端用它登录所有订阅端',
    },
    {
      key: 'target', title: '添加订阅端', done: enabled.length > 0,
      detail: enabled.length ? `已启用 ${enabled.length} 个` : '填写 SSH 地址并核对 Host Key',
    },
    {
      key: 'deploy', title: '部署', done: installed.length > 0,
      error: !installed.length && enabled.some((target) => target.last_deploy_error),
      detail: installed.length
        ? `${installed.length} 个已部署`
        : deployTarget?.last_deploy_error ? `${deployTarget.name} 部署失败` : deployTarget?.deploy_state ? `${deployTarget.name} 待部署` : '先检测部署环境，再部署',
    },
    {
      key: 'test', title: '测试', done: ready.length > 0,
      error: !ready.length && installed.some((target) => target.last_test_error),
      detail: ready.length ? `${ready.length} 个可签发` : testTarget?.last_test_error ? `${testTarget.name} 测试失败` : '部署后验证转发和发布 Secret',
    },
    {
      key: 'publish', title: '签发并发布', done: published.length > 0 && !input.bundlePending,
      error: !published.length && publishFailed.length > 0,
      detail: published.length
        ? input.bundlePending ? '内容有变化，需要重新发布' : `已发布到 ${published.length} 个订阅端`
        : publishFailed.length ? `${publishFailed[0].name} 发布失败` : '签发订阅包并发送到订阅端',
    },
  ]
  const firstOpen = raw.findIndex((step) => !step.done)
  const steps = raw.map((step, index): FlowStep => ({
    key: step.key,
    title: step.title,
    detail: step.detail,
    status: step.done && (firstOpen < 0 || index < firstOpen) ? 'done' : index === firstOpen ? (step.error ? 'error' : 'current') : 'waiting',
  }))
  const current = firstOpen < 0 ? null : steps[firstOpen]
  const target = current?.key === 'deploy' ? deployTarget : current?.key === 'test' ? testTarget : null
  return { steps, current, target }
}
