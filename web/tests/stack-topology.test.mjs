import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let stackTopology
before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ stackTopology } = await server.ssrLoadModule('/src/util/flow.ts'))
})
after(async () => { await server?.close() })

let nextId = 1
function stack(name, options = {}) {
  return {
    id: nextId++, name, role: 'edge', enabled: true, xray_enabled: true, mihomo_enabled: true, members: [],
    outbound: { type: 'mihomo', target: '' }, upstreams: [],
    inbounds: [{ name: 'main', protocol: 'vmess', port: 10000, sub: true }],
    ...options,
  }
}
const raw = (name) => ({ type: 'raw', name, protocol: 'vmess', stack: '', inbound: '' })
const relay = (target) => ({ type: 'xray-socks5', name: target, protocol: 'socks5', stack: target, inbound: 'relay' })

test('没有 stack 时不画图', () => {
  assert.deepEqual(stackTopology([]), { nodes: [], edges: [], columns: [] })
})

test('按类别汇总节点，箭头标出走这条路的 stack 数量', () => {
  const hk = stack('hk', { upstreams: [raw('a'), raw('b')] })
  const jp = stack('jp', { upstreams: [raw('c')] })
  const direct = stack('plain', { outbound: { type: 'direct', target: '' }, inbounds: [] })
  const ext = stack('ext', { outbound: { type: 'socks', target: '1.2.3.4:1080' }, enabled: false })
  const auto = stack('auto1', { role: 'auto', members: ['hk', 'jp'], upstreams: [relay('hk'), relay('jp')] })
  const graph = stackTopology([hk, jp, direct, ext, auto])

  assert.deepEqual(graph.columns, ['客户端', '聚合 Stack', '独立出口 Stack', '出口', '互联网'])
  assert.deepEqual(graph.nodes.map((node) => node.id), ['client', 'auto', 'edge', 'upstream', 'external', 'direct', 'internet'])
  const node = (id) => graph.nodes.find((item) => item.id === id)
  assert.equal(node('edge').subtitle, '4 个 Stack（1 个停用）')
  assert.deepEqual(node('edge').filter.stackIds, [hk.id, jp.id, direct.id, ext.id])
  assert.deepEqual(node('upstream').lines, ['3 个外部节点'])
  assert.deepEqual(node('external').lines, ['SOCKS 1.2.3.4:1080'])
  assert.deepEqual(node('client').filter.stackIds, [hk.id, jp.id, ext.id, auto.id])
  assert.match(node('upstream').hint, /包含：hk、jp/)

  const edge = (from, to) => graph.edges.find((item) => item.from === from && item.to === to)
  assert.equal(edge('client', 'edge').label, '3 个 Stack')
  assert.equal(edge('client', 'auto').label, '1 个 Stack')
  assert.equal(edge('auto', 'edge').label, '引用 2 个')
  assert.equal(edge('edge', 'upstream').label, '2 个 Stack')
  assert.equal(edge('edge', 'direct').label, '1 个 Stack')
  // 聚合 stack 只经成员转发，自己没有出口。
  assert.equal(graph.edges.some((item) => item.from === 'auto' && item.to !== 'edge'), false)
  assert.equal(graph.summary, '从 4 个订阅入站进入 → 经过 4 个独立出口 Stack、1 个聚合测速 Stack → 从上游节点（2）、外部代理（1）、本机直连（1）出去')
})

test('没有聚合 stack 时省去聚合列；只经其他 stack 转发的 stack 不单独出口', () => {
  const exit = stack('exit', { upstreams: [raw('a')] })
  const chained = stack('chain', { upstreams: [relay('exit')] })
  const empty = stack('empty')
  const graph = stackTopology([exit, chained, empty])
  assert.deepEqual(graph.columns, ['客户端', 'Stack', '出口', '互联网'])
  const node = (id) => graph.nodes.find((item) => item.id === id)
  assert.deepEqual(node('edge').lines, ['其中 1 个经其他 Stack 转发'])
  assert.deepEqual(node('upstream').filter.stackIds, [exit.id])
  // 没有任何上游的 stack 只剩 DIRECT。
  assert.deepEqual(node('direct').filter.stackIds, [empty.id])
})
