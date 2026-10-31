import type { Schemas } from '../api/client'

/** 节点的配色类别。 */
export type FlowTone = 'client' | 'inbound' | 'stack' | 'auto' | 'xray' | 'mihomo' | 'rules' | 'group' | 'upstream' | 'internet' | 'reject'

/** 流量图示中的一个节点，column 从 0 开始，同一列按加入顺序自上而下排列。 */
export interface FlowNode {
  id: string
  column: number
  title: string
  subtitle?: string
  lines?: string[]
  badge?: string
  tone?: FlowTone
  /** 悬停提示中追加的说明。 */
  hint?: string
  dim?: boolean
  clickable?: boolean
  /** 点击后打开的 stack。 */
  stackId?: number
}

/** 有向边：流量从 from 流向 to；dashed 表示备选路径（例如 select 组里没有默认选中的成员）。 */
export interface FlowEdge {
  from: string
  to: string
  label?: string
  dashed?: boolean
}

export interface FlowGraph {
  nodes: FlowNode[]
  edges: FlowEdge[]
  columns: string[]
}

type Summary = Schemas['StackSummary']
type Stack = Schemas['Stack']

const MAX_LINES = 3

/** 最多列出 MAX_LINES 行，其余合并成「另 N 个」。 */
function limit(lines: string[], unit = '个'): string[] {
  if (lines.length <= MAX_LINES) return lines
  return [...lines.slice(0, MAX_LINES - 1), `… 另 ${lines.length - MAX_LINES + 1} ${unit}`]
}

/** 按引用关系给节点分层：没有来源的在第 0 层，其余排在所有来源之后。 */
function layers(names: string[], sources: Map<string, string[]>): Map<string, number> {
  const out = new Map<string, number>()
  const visit = (name: string, trail: Set<string>): number => {
    const known = out.get(name)
    if (known !== undefined) return known
    if (trail.has(name)) return 0
    trail.add(name)
    const parents = sources.get(name) ?? []
    const layer = parents.length ? Math.max(...parents.map((parent) => visit(parent, trail) + 1)) : 0
    trail.delete(name)
    out.set(name, layer)
    return layer
  }
  names.forEach((name) => visit(name, new Set()))
  return out
}

const protocolNames: Record<string, string> = { vmess: 'VMess', shadowsocks: 'Shadowsocks', socks5: 'SOCKS5', http: 'HTTP' }
const groupTypes: Record<string, string> = { select: '手动选择', 'url-test': '自动测速', 'load-balance': '负载均衡', fallback: '故障转移' }

/** Stack 列表页的整体拓扑：客户端 → stack（按引用关系分层）→ 出口 → 互联网。 */
export function stackTopology(stacks: Summary[]): FlowGraph {
  const nodes: FlowNode[] = []
  const edges: FlowEdge[] = []
  if (!stacks.length) return { nodes, edges, columns: [] }

  const names = new Set(stacks.map((stack) => stack.name))
  // A 的上游引用 B 的入站时，流量从 A 流向 B。
  const referrers = new Map<string, string[]>()
  const references = new Map<string, { stack: string; inbound: string }[]>()
  for (const stack of stacks) {
    if (!stack.mihomo_enabled || stack.outbound.type !== 'mihomo') continue
    for (const upstream of stack.upstreams) {
      if (upstream.type !== 'xray-socks5' || !names.has(upstream.stack)) continue
      referrers.set(upstream.stack, [...(referrers.get(upstream.stack) ?? []), stack.name])
      references.set(stack.name, [...(references.get(stack.name) ?? []), { stack: upstream.stack, inbound: upstream.inbound }])
    }
  }
  const layer = layers(
    stacks.map((stack) => stack.name),
    referrers,
  )
  const deepest = Math.max(...layer.values())
  const exitColumn = deepest + 2
  const columns = ['客户端', ...Array.from({ length: deepest + 1 }, (_, index) => (deepest ? `Stack · 第 ${index + 1} 跳` : 'Stack')), '出口', '互联网']

  const subInbounds = stacks.flatMap((stack) => stack.inbounds.filter((inbound) => inbound.sub))
  nodes.push({ id: 'client', column: 0, title: '客户端', subtitle: '订阅用户', tone: 'client', lines: [`${subInbounds.length} 个订阅入站`] })

  let usesDirect = false
  for (const stack of stacks) {
    const id = `stack:${stack.name}`
    const auto = stack.role === 'auto'
    nodes.push({
      id,
      column: 1 + (layer.get(stack.name) ?? 0),
      title: stack.name,
      badge: !stack.enabled ? '停用' : auto ? 'auto' : undefined,
      subtitle: auto ? `${stack.members.length} 个成员` : stack.xray_enabled ? 'Xray → Mihomo' : 'Xray 未启用',
      lines: limit(stack.inbounds.map((inbound) => `${inbound.name} ${inbound.protocol} :${inbound.port}`)),
      tone: auto ? 'auto' : 'stack',
      dim: !stack.enabled,
      clickable: true,
      stackId: stack.id,
      hint: '点击打开编辑页',
    })
    if (stack.inbounds.some((inbound) => inbound.sub)) edges.push({ from: 'client', to: id })
    for (const reference of references.get(stack.name) ?? []) {
      edges.push({ from: id, to: `stack:${reference.stack}`, label: reference.inbound })
    }

    if (!stack.xray_enabled) continue
    if (stack.outbound.type === 'direct') {
      usesDirect = true
      edges.push({ from: id, to: 'direct', label: 'Xray 直连' })
    } else if (stack.outbound.type !== 'mihomo') {
      const exit = `external:${stack.outbound.target}`
      if (!nodes.some((node) => node.id === exit)) {
        nodes.push({ id: exit, column: exitColumn, title: '外部代理', subtitle: stack.outbound.type.toUpperCase(), lines: [stack.outbound.target], tone: 'upstream' })
        edges.push({ from: exit, to: 'internet' })
      }
      edges.push({ from: id, to: exit })
    } else if (stack.mihomo_enabled) {
      const raw = stack.upstreams.filter((upstream) => upstream.type === 'raw')
      if (raw.length) {
        const exit = `upstreams:${stack.name}`
        nodes.push({
          id: exit,
          column: exitColumn,
          title: `${stack.name} 的上游`,
          subtitle: `${raw.length} 个外部节点`,
          lines: limit(raw.map((upstream) => `${upstream.name} · ${upstream.protocol || '?'}`)),
          tone: 'upstream',
        })
        edges.push({ from: id, to: exit }, { from: exit, to: 'internet' })
      } else if (!references.has(stack.name)) {
        // 没有任何上游时代理组里只剩 DIRECT。
        usesDirect = true
        edges.push({ from: id, to: 'direct', label: '无上游' })
      }
    }
  }
  if (usesDirect) {
    nodes.push({ id: 'direct', column: exitColumn, title: '直连', subtitle: 'DIRECT', tone: 'internet', lines: ['由本机直接访问目标'] })
    edges.push({ from: 'direct', to: 'internet' })
  }
  nodes.push({ id: 'internet', column: exitColumn + 1, title: '互联网', subtitle: '目标网站', tone: 'internet' })
  return { nodes, edges, columns }
}

/**
 * 单个 stack 的详细流向：来源 → 入站 → 出站 / 监听 → 规则 → 代理组 → 上游 → 出口。
 * Xray 出站转发到本 stack 的 Mihomo 时，出站和 Mihomo SOCKS 监听合成一个节点。
 */
export function stackFlow(stack: Stack, summaries: Summary[]): FlowGraph {
  const nodes: FlowNode[] = []
  const edges: FlowEdge[] = []
  const columns = ['来源', '入站', '出站 / 监听']
  const inbounds = stack.xray.enabled ? stack.xray.inbounds : []

  // 来源：订阅客户端、引用本 stack 入站的其他 stack、本机程序。
  const referrers = summaries.filter((other) => other.name !== stack.name && other.upstreams.some((upstream) => upstream.type === 'xray-socks5' && upstream.stack === stack.name))
  const referenced = new Set(referrers.flatMap((other) => other.upstreams.filter((upstream) => upstream.stack === stack.name).map((upstream) => upstream.inbound)))
  const subUsers = new Set(inbounds.filter((inbound) => inbound.sub).flatMap((inbound) => inbound.users.map((ref) => ref.user)))
  if (inbounds.some((inbound) => inbound.sub)) {
    nodes.push({ id: 'client', column: 0, title: '订阅客户端', subtitle: '经订阅导入节点', lines: [`${subUsers.size} 个用户`], tone: 'client' })
  }
  for (const other of referrers) {
    nodes.push({ id: `ref:${other.name}`, column: 0, title: other.name, subtitle: '其他 stack 的上游', tone: other.role === 'auto' ? 'auto' : 'stack', clickable: true, stackId: other.id, hint: '点击打开编辑页' })
  }
  const needsLocal =
    inbounds.some((inbound) => !inbound.sub && !referenced.has(inbound.name)) ||
    (stack.mihomo.enabled && (stack.xray.outbound.type !== 'mihomo' || !stack.xray.enabled || stack.mihomo.http.enabled))
  if (needsLocal) {
    nodes.push({ id: 'local', column: 0, title: '本机或其他程序', subtitle: '直接连接监听端口', tone: 'client' })
  }

  for (const inbound of inbounds) {
    const id = `in:${inbound.name}`
    const credentials =
      inbound.protocol === 'socks5' || inbound.protocol === 'http'
        ? inbound.auth_type === 'password' ? '用户名密码认证' : '无认证'
        : `${inbound.users.length} 个用户`
    nodes.push({
      id,
      column: 1,
      title: inbound.name,
      badge: inbound.sub ? '订阅' : undefined,
      subtitle: [protocolNames[inbound.protocol] ?? inbound.protocol, inbound.protocol === 'vmess' ? inbound.network : ''].filter(Boolean).join(' · '),
      lines: [`${inbound.listen}:${inbound.port}`, credentials],
      tone: 'inbound',
    })
    if (inbound.sub) edges.push({ from: 'client', to: id })
    for (const other of referrers) {
      if (other.upstreams.some((upstream) => upstream.stack === stack.name && upstream.inbound === inbound.name)) {
        edges.push({ from: `ref:${other.name}`, to: id, label: 'relay' })
      }
    }
    if (!inbound.sub && !referenced.has(inbound.name)) edges.push({ from: 'local', to: id })
  }

  const outbound = stack.xray.outbound
  const mihomo = stack.mihomo
  const authLabel = (listener: Schemas['Listener']) => ({ inherit: '认证跟随全局', none: '不认证', accounts: `${listener.accounts.length} 个账号` })[listener.auth] ?? listener.auth
  const merged = stack.xray.enabled && mihomo.enabled && outbound.type === 'mihomo'
  const outboundTitles: Record<string, string> = { mihomo: '转发到 Mihomo', socks5: '外部 SOCKS5', http: '外部 HTTP', direct: '直连' }
  nodes.push(
    merged
      ? {
          id: 'xray',
          column: 2,
          title: 'Xray → Mihomo',
          subtitle: `SOCKS 监听 ${mihomo.socks.name || 'socks'}`,
          lines: [`${mihomo.socks.listen}:${mihomo.socks.port}`, authLabel(mihomo.socks)],
          tone: 'mihomo',
          hint: 'Xray 出站转发到本 stack 的 Mihomo SOCKS 监听',
        }
      : {
          id: 'xray',
          column: 2,
          title: 'Xray 出站',
          subtitle: stack.xray.enabled ? outboundTitles[outbound.type] : 'Xray 未启用',
          lines: [
            ...(outbound.type === 'socks5' || outbound.type === 'http' ? [`${outbound.server}:${outbound.port}`] : []),
            ...(outbound.private_direct ? ['私网目标直连'] : []),
          ],
          tone: 'xray',
          dim: !stack.xray.enabled,
        },
  )
  for (const inbound of inbounds) edges.push({ from: `in:${inbound.name}`, to: 'xray' })

  const internet: FlowNode = { id: 'internet', column: 0, title: '互联网', subtitle: '目标网站', tone: 'internet' }
  if (!stack.mihomo.enabled || outbound.type !== 'mihomo') {
    if (outbound.type === 'direct') {
      edges.push({ from: 'xray', to: 'internet' })
    } else if (outbound.type !== 'mihomo') {
      edges.push({ from: 'xray', to: 'internet', label: `经 ${outbound.server}` })
    }
  }
  if (!stack.mihomo.enabled) {
    columns.push('出口')
    nodes.push({ ...internet, column: 3 })
    return { nodes, edges, columns }
  }

  // Mihomo：监听 → 规则 → 代理组（按嵌套分层）→ 上游。监听和 Xray 出站在同一列。
  const socks = merged ? 'xray' : 'socks'
  if (!merged) {
    nodes.push({ id: 'socks', column: 2, title: `Mihomo ${mihomo.socks.name || 'socks'}`, subtitle: 'SOCKS 监听', lines: [`${mihomo.socks.listen}:${mihomo.socks.port}`, authLabel(mihomo.socks)], tone: 'mihomo' })
    edges.push({ from: 'local', to: 'socks' })
  }
  if (mihomo.http.enabled) {
    nodes.push({ id: 'http', column: 2, title: `Mihomo ${mihomo.http.name || 'http'}`, subtitle: 'HTTP 监听', lines: [`${mihomo.http.listen}:${mihomo.http.port}`, authLabel(mihomo.http)], tone: 'mihomo' })
    edges.push({ from: 'local', to: 'http' })
  }

  const rules = mihomo.rules
  nodes.push({
    id: 'rules',
    column: 3,
    title: '规则',
    subtitle: `模式：${mihomo.mode === 'inherit' ? '跟随全局' : mihomo.mode}`,
    lines: [`附加规则 ${rules.extra.length} 条`, ...rules.extra.slice(0, 2).map((rule) => `${rule.type} ${rule.value}`), `兜底 → ${rules.final}`],
    tone: 'rules',
  })
  edges.push({ from: socks, to: 'rules' })
  if (mihomo.http.enabled) edges.push({ from: 'http', to: 'rules' })

  const groups = new Map(mihomo.groups.map((group) => [group.name, group]))
  const parents = new Map<string, string[]>()
  for (const group of mihomo.groups) {
    for (const proxy of group.proxies) {
      if (groups.has(proxy)) parents.set(proxy, [...(parents.get(proxy) ?? []), group.name])
    }
  }
  const groupLayer = layers(
    mihomo.groups.map((group) => group.name),
    parents,
  )
  const groupColumns = mihomo.groups.length ? Math.max(...groupLayer.values()) + 1 : 0
  columns.push('规则')
  for (let index = 0; index < groupColumns; index++) columns.push(index ? '代理组（嵌套）' : '代理组')
  const upstreamColumn = 4 + groupColumns
  columns.push('上游', '出口')

  const upstreams = new Map(mihomo.upstreams.map((upstream) => [upstream.name, upstream]))
  const targetId = (name: string) => (groups.has(name) ? `group:${name}` : upstreams.has(name) ? `up:${name}` : `builtin:${name}`)
  const used = new Set<string>()

  for (const group of mihomo.groups) {
    nodes.push({
      id: `group:${group.name}`,
      column: 4 + (groupLayer.get(group.name) ?? 0),
      title: group.name,
      subtitle: groupTypes[group.type] ?? group.type,
      lines: limit(group.proxies.map((proxy, index) => (group.type === 'select' && index === 0 ? `★ ${proxy}` : `· ${proxy}`))),
      tone: 'group',
      hint: group.type === 'select' ? '★ 为默认选中的成员' : undefined,
    })
    group.proxies.forEach((proxy, index) => {
      used.add(proxy)
      edges.push({ from: `group:${group.name}`, to: targetId(proxy), dashed: group.type === 'select' && index > 0 })
    })
  }

  // 规则指向的目标：兜底和附加规则合并到同一条边上。
  const ruleTargets = new Map<string, number>()
  for (const rule of rules.extra) ruleTargets.set(rule.target, (ruleTargets.get(rule.target) ?? 0) + 1)
  const targets = new Set([rules.final, ...ruleTargets.keys()])
  for (const target of targets) {
    used.add(target)
    const count = ruleTargets.get(target) ?? 0
    const label = [target === rules.final ? '兜底' : '', count ? `${count} 条规则` : ''].filter(Boolean).join(' + ')
    edges.push({ from: 'rules', to: targetId(target), label })
  }

  const byName = new Map(summaries.map((summary) => [summary.name, summary]))
  for (const upstream of mihomo.upstreams) {
    const id = `up:${upstream.name}`
    const unused = !used.has(upstream.name)
    if (upstream.type === 'xray-socks5') {
      nodes.push({
        id,
        column: upstreamColumn,
        title: upstream.name,
        subtitle: `经 stack ${upstream.stack}`,
        lines: [`${upstream.stack}.${upstream.inbound}`],
        tone: 'stack',
        dim: unused,
        hint: unused ? '没有被代理组或规则引用，流量不会经过它' : '点击打开该 stack',
        clickable: byName.has(upstream.stack),
        stackId: byName.get(upstream.stack)?.id,
      })
      edges.push({ from: id, to: 'internet', label: `经 ${upstream.stack}` })
    } else {
      const config = (upstream.config ?? {}) as Record<string, unknown>
      nodes.push({
        id,
        column: upstreamColumn,
        title: upstream.name,
        subtitle: `外部节点 · ${config.type ?? '?'}`,
        lines: [`${config.server ?? ''}:${config.port ?? ''}`],
        tone: 'upstream',
        dim: unused,
        hint: unused ? '没有被代理组或规则引用，流量不会经过它' : undefined,
      })
      edges.push({ from: id, to: 'internet' })
    }
  }
  for (const builtin of ['DIRECT', 'REJECT']) {
    if (!used.has(builtin)) continue
    const direct = builtin === 'DIRECT'
    nodes.push({ id: `builtin:${builtin}`, column: upstreamColumn, title: direct ? '直连' : '拒绝', subtitle: builtin, lines: [direct ? '由本机直接访问目标' : '丢弃连接'], tone: direct ? 'internet' : 'reject' })
    if (direct) edges.push({ from: 'builtin:DIRECT', to: 'internet' })
  }
  nodes.push({ ...internet, column: upstreamColumn + 1 })
  return { nodes, edges, columns }
}
