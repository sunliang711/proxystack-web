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
  /** 点击后在列表中筛选的 stack；用于按类别汇总的节点。 */
  filter?: { label: string; stackIds: number[] }
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
  /** 一句话概括从哪里进、经过哪里、从哪里出。 */
  summary?: string
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

/**
 * Stack 列表页的概要流向：客户端 → 聚合测速 Stack → 独立出口 Stack → 出口类别 → 互联网。
 * 节点按类别汇总、显示数量，不逐个列出 stack；每个类别节点带上所含 stack，点击后在列表中筛选。
 * 单个 stack 的详细流向在 stack 详情页查看。
 */
export function stackTopology(stacks: Summary[]): FlowGraph {
  const nodes: FlowNode[] = []
  const edges: FlowEdge[] = []
  if (!stacks.length) return { nodes, edges, columns: [] }

  const byName = new Map(stacks.map((stack) => [stack.name, stack]))
  // 只有 Xray 出站经过 Mihomo 时，Mihomo 的上游才承载流量。
  const viaMihomo = (stack: Summary) => stack.xray_enabled && stack.mihomo_enabled && stack.outbound.type === 'mihomo'
  // 上游里引用的其他 stack（经其入站转发）。
  const referenced = (stack: Summary) =>
    viaMihomo(stack) ? [...new Set(stack.upstreams.filter((upstream) => upstream.type === 'xray-socks5' && byName.has(upstream.stack)).map((upstream) => upstream.stack))] : []

  type Exit = 'upstream' | 'external' | 'direct'
  /** stack 自己的出口；只经其他 stack 转发、或 Xray / Mihomo 未启用时没有自己的出口。 */
  const exitOf = (stack: Summary): Exit | null => {
    if (!stack.xray_enabled) return null
    if (stack.outbound.type === 'direct') return 'direct'
    if (stack.outbound.type !== 'mihomo') return 'external'
    if (!viaMihomo(stack)) return null
    if (stack.upstreams.some((upstream) => upstream.type === 'raw')) return 'upstream'
    // 没有任何上游时代理组里只剩 DIRECT。
    return referenced(stack).length ? null : 'direct'
  }

  const autos = stacks.filter((stack) => stack.role === 'auto')
  const plain = stacks.filter((stack) => stack.role !== 'auto')
  const hasAuto = autos.length > 0
  const exitColumn = hasAuto ? 3 : 2
  const columns = hasAuto ? ['客户端', '聚合 Stack', '独立出口 Stack', '出口', '互联网'] : ['客户端', 'Stack', '出口', '互联网']

  const MAX_NAMES = 8
  const names = (members: Summary[]) =>
    members.length > MAX_NAMES ? `${members.slice(0, MAX_NAMES).map((stack) => stack.name).join('、')} 等 ${members.length} 个` : members.map((stack) => stack.name).join('、')
  const filterOf = (label: string, members: Summary[]) => ({ label, stackIds: members.map((stack) => stack.id) })
  const group = (id: string, column: number, title: string, members: Summary[], tone: FlowTone, label: string, lines: string[] = []): FlowNode => {
    const disabled = members.filter((stack) => !stack.enabled).length
    return {
      id, column, title, tone, lines,
      subtitle: `${members.length} 个 Stack${disabled ? `（${disabled} 个停用）` : ''}`,
      hint: `包含：${names(members)}\n点击在下方列表中只显示这些 Stack`,
      clickable: true,
      filter: filterOf(label, members),
    }
  }
  const count = (members: Summary[]) => `${members.length} 个 Stack`

  const subscribed = stacks.filter((stack) => stack.inbounds.some((inbound) => inbound.sub))
  const subInbounds = stacks.reduce((sum, stack) => sum + stack.inbounds.filter((inbound) => inbound.sub).length, 0)
  nodes.push({
    id: 'client', column: 0, title: '客户端', subtitle: '订阅用户', tone: 'client', lines: [`${subInbounds} 个订阅入站`],
    hint: subscribed.length ? `订阅入站所在：${names(subscribed)}\n点击在下方列表中只显示这些 Stack` : '还没有加入订阅的入站',
    clickable: subscribed.length > 0,
    filter: subscribed.length ? filterOf('有订阅入站', subscribed) : undefined,
  })

  // 引用关系：A 的上游引用 B 时流量从 A 流向 B，按类别汇总成一条边。
  const autoTargets = new Set(autos.flatMap(referenced))
  const chained = plain.filter((stack) => referenced(stack).length)
  if (hasAuto) {
    nodes.push(group('auto', 1, '聚合测速 Stack', autos, 'auto', '聚合测速 Stack', [`共 ${autos.reduce((sum, stack) => sum + stack.members.length, 0)} 个成员引用`]))
  }
  if (plain.length) {
    const lines = chained.length ? [`其中 ${chained.length} 个经其他 Stack 转发`] : []
    nodes.push(group('edge', hasAuto ? 2 : 1, '独立出口 Stack', plain, 'stack', '独立出口 Stack', lines))
  }
  const subscribedAutos = autos.filter((stack) => subscribed.includes(stack))
  const subscribedPlain = plain.filter((stack) => subscribed.includes(stack))
  if (subscribedAutos.length) edges.push({ from: 'client', to: 'auto', label: count(subscribedAutos) })
  if (subscribedPlain.length) edges.push({ from: 'client', to: 'edge', label: count(subscribedPlain) })
  const autoToPlain = [...autoTargets].filter((name) => byName.get(name)?.role !== 'auto')
  if (hasAuto && plain.length && autoToPlain.length) edges.push({ from: 'auto', to: 'edge', label: `引用 ${autoToPlain.length} 个` })

  const exits: { id: Exit; title: string; subtitle?: string; tone: FlowTone; label: string; lines: (members: Summary[]) => string[] }[] = [
    {
      id: 'upstream', title: '上游节点', tone: 'upstream', label: '经上游节点出口',
      lines: (members) => [`${members.reduce((sum, stack) => sum + stack.upstreams.filter((upstream) => upstream.type === 'raw').length, 0)} 个外部节点`],
    },
    {
      id: 'external', title: '外部代理', tone: 'upstream', label: '经外部代理出口',
      lines: (members) => limit([...new Set(members.map((stack) => `${stack.outbound.type.toUpperCase()} ${stack.outbound.target}`))]),
    },
    { id: 'direct', title: '本机直连', tone: 'internet', label: '本机直连出口', lines: () => ['由本机直接访问目标'] },
  ]
  const exitLabels: string[] = []
  for (const exit of exits) {
    const members = stacks.filter((stack) => exitOf(stack) === exit.id)
    if (!members.length) continue
    nodes.push(group(exit.id, exitColumn, exit.title, members, exit.tone, exit.label, exit.lines(members)))
    edges.push({ from: exit.id, to: 'internet' })
    const fromAuto = members.filter((stack) => stack.role === 'auto')
    const fromPlain = members.filter((stack) => stack.role !== 'auto')
    if (fromAuto.length) edges.push({ from: 'auto', to: exit.id, label: count(fromAuto) })
    if (fromPlain.length) edges.push({ from: 'edge', to: exit.id, label: count(fromPlain) })
    exitLabels.push(`${exit.title}（${members.length}）`)
  }
  nodes.push({ id: 'internet', column: exitColumn + 1, title: '互联网', subtitle: '目标网站', tone: 'internet' })

  const via = [`${plain.length} 个独立出口 Stack`, hasAuto ? `${autos.length} 个聚合测速 Stack` : ''].filter(Boolean).join('、')
  const summary = `从 ${subInbounds} 个订阅入站进入 → 经过 ${via} → ${exitLabels.length ? `从${exitLabels.join('、')}出去` : '还没有可用的出口'}`
  return { nodes, edges, columns, summary }
}

/**
 * 单个 stack 的详细流向：来源 → 入站 → 出站 → Mihomo 监听 → 规则 → 代理组 → 上游 → 出口。
 * Xray 出站转发到本 stack 的 Mihomo 时，出站和 Mihomo SOCKS 监听仍分别展示。
 */
export function stackFlow(stack: Stack, summaries: Summary[]): FlowGraph {
  const nodes: FlowNode[] = []
  const edges: FlowEdge[] = []
  const columns = ['来源', '入站', '出站']
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
  const viaMihomo = stack.xray.enabled && mihomo.enabled && outbound.type === 'mihomo'
  const outboundTitles: Record<string, string> = { mihomo: '转发到 Mihomo', socks5: '外部 SOCKS5', http: '外部 HTTP', direct: '直连' }
  nodes.push({
    id: 'xray',
    column: 2,
    title: 'Xray 出站',
    subtitle: stack.xray.enabled ? outboundTitles[outbound.type] : 'Xray 未启用',
    lines: [
      ...(viaMihomo ? [`→ ${mihomo.socks.listen}:${mihomo.socks.port}`] : []),
      ...(outbound.type === 'socks5' || outbound.type === 'http' ? [`${outbound.server}:${outbound.port}`] : []),
      ...(outbound.private_direct ? ['私网目标直连'] : []),
    ],
    tone: 'xray',
    dim: !stack.xray.enabled,
  })
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

  // Mihomo：监听 → 规则 → 代理组（按嵌套分层）→ 上游。Xray 不经过 Mihomo 时，监听只服务本机程序。
  columns.push('Mihomo 监听')
  nodes.push({ id: 'socks', column: 3, title: `Mihomo ${mihomo.socks.name || 'socks'}`, subtitle: 'SOCKS 监听', lines: [`${mihomo.socks.listen}:${mihomo.socks.port}`, authLabel(mihomo.socks)], tone: 'mihomo' })
  edges.push({ from: viaMihomo ? 'xray' : 'local', to: 'socks' })
  if (mihomo.http.enabled) {
    nodes.push({ id: 'http', column: 3, title: `Mihomo ${mihomo.http.name || 'http'}`, subtitle: 'HTTP 监听', lines: [`${mihomo.http.listen}:${mihomo.http.port}`, authLabel(mihomo.http)], tone: 'mihomo' })
    edges.push({ from: 'local', to: 'http' })
  }

  const rules = mihomo.rules
  nodes.push({
    id: 'rules',
    column: 4,
    title: '规则',
    subtitle: `模式：${mihomo.mode === 'inherit' ? '跟随全局' : mihomo.mode}`,
    lines: [`附加规则 ${rules.extra.length} 条`, ...rules.extra.slice(0, 2).map((rule) => `${rule.type} ${rule.value}`), `兜底 → ${rules.final}`],
    tone: 'rules',
  })
  edges.push({ from: 'socks', to: 'rules' })
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
  const upstreamColumn = 5 + groupColumns
  columns.push('上游', '出口')

  const upstreams = new Map(mihomo.upstreams.map((upstream) => [upstream.name, upstream]))
  const targetId = (name: string) => (groups.has(name) ? `group:${name}` : upstreams.has(name) ? `up:${name}` : `builtin:${name}`)
  const used = new Set<string>()

  for (const group of mihomo.groups) {
    nodes.push({
      id: `group:${group.name}`,
      column: 5 + (groupLayer.get(group.name) ?? 0),
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
