import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let accessRows
let accessRanks
let isServiceRow
let setListed
before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ accessRows, accessRanks, isServiceRow, setListed } = await server.ssrLoadModule('/src/util/access-rows.ts'))
})
after(async () => { await server?.close() })

const row = (domain, extra = {}) => ({ domain, count: 1, blocked: 0, listed: '', ...extra })

test('服务行展开为子行，代表域名与成员域名的 key 不冲突', () => {
  const rows = accessRows([
    row('instagram.com', { service: 'instagram', members: [row('cdninstagram.com', { service: 'instagram' }), row('instagram.com', { service: 'instagram' })] }),
    row('github.com', { service: 'github' }),
    row('example.org'),
  ])
  assert.deepEqual(rows.map((item) => item.key), ['service:instagram', 'domain:github.com', 'domain:example.org'])
  assert.deepEqual(rows[0].children.map((item) => item.key), ['domain:cdninstagram.com', 'domain:instagram.com'])
  assert.equal(rows[1].children, undefined)
  assert.equal(isServiceRow(rows[0]), true)
  assert.equal(isServiceRow(rows[1]), false)
  assert.deepEqual([...accessRanks(rows)], [['service:instagram', 1], ['domain:github.com', 2], ['domain:example.org', 3]])
})

test('黑名单状态更新到原数据的一级行和服务内的主域名', () => {
  const data = [
    row('instagram.com', { service: 'instagram', members: [row('cdninstagram.com'), row('instagram.com')] }),
    row('github.com'),
  ]
  setListed(data, 'instagram.com', 'global')
  setListed(data, 'github.com', 'user')
  assert.equal(data[0].listed, '', 'the service row itself is not a domain')
  assert.equal(data[0].members[1].listed, 'global')
  assert.equal(data[0].members[0].listed, '')
  assert.equal(data[1].listed, 'user')
})
