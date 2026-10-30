import type { Schemas } from '../../api/client'

/** stack 表单中当前使用的全部端口，供自动分配时避开。 */
export function stackPorts(stack: Schemas['Stack']): number[] {
  const ports = stack.xray.inbounds.map((inbound) => inbound.port)
  ports.push(stack.xray.api_port, stack.mihomo.controller_port, stack.mihomo.socks.port)
  if (stack.mihomo.http.enabled) ports.push(stack.mihomo.http.port)
  return ports.filter((port) => port > 0)
}

/** 代理组和规则可以指向的目标：上游、代理组和内置策略。 */
export function proxyTargets(stack: Schemas['Stack'], exclude?: string): string[] {
  return [
    ...stack.mihomo.upstreams.map((u) => u.name),
    ...stack.mihomo.groups.map((g) => g.name),
    'DIRECT',
    'REJECT',
  ].filter((name) => name && name !== exclude)
}
