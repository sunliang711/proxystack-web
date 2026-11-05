import assert from 'node:assert/strict'
import { before, after, test } from 'node:test'
import { createServer } from 'vite'

let server
let publishFlow
let targetReady
before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ publishFlow, targetReady } = await server.ssrLoadModule('/src/util/subPublish.ts'))
})
after(() => server.close())

const zero = '0001-01-01T00:00:00Z'
const target = (patch = {}) => ({
  id: 1, name: 'sub1', enabled: true, deploy_state: '', last_deploy_at: zero, last_deploy_error: '',
  last_test_at: zero, last_test_error: '', last_publish_at: zero, last_publish_error: '', last_sequence: 0, ...patch,
})
const statuses = (flow) => flow.steps.map((step) => `${step.key}:${step.status}`).join(' ')

test('没有密钥和订阅端时停在第一步', () => {
  const flow = publishFlow({ identity: false, targets: [], bundlePending: false })
  assert.equal(flow.current.key, 'identity')
  assert.equal(statuses(flow), 'identity:current target:waiting deploy:waiting test:waiting publish:waiting')
})

test('零值测试时间不算测试通过', () => {
  assert.equal(targetReady(target({ deploy_state: 'installed' })), false)
  const flow = publishFlow({ identity: true, targets: [target({ deploy_state: 'installed' })], bundlePending: false })
  assert.equal(flow.current.key, 'test')
  assert.equal(flow.target.name, 'sub1')
})

test('部署失败标记为错误并指向该订阅端', () => {
  const flow = publishFlow({ identity: true, targets: [target({ deploy_state: 'missing', last_deploy_error: 'ssh: timeout' })], bundlePending: false })
  assert.equal(flow.current.key, 'deploy')
  assert.equal(flow.current.status, 'error')
  assert.match(flow.current.detail, /sub1 部署失败/)
})

test('部署后重新测试才算就绪', () => {
  const stale = target({ deploy_state: 'installed', last_test_at: '2026-11-01T00:00:00Z', last_deploy_at: '2026-11-02T00:00:00Z' })
  assert.equal(targetReady(stale), false)
  const fresh = { ...stale, last_test_at: '2026-11-03T00:00:00Z' }
  assert.equal(targetReady(fresh), true)
})

test('已发布但内容有变化时停在签发', () => {
  const published = target({ deploy_state: 'installed', last_test_at: '2026-11-03T00:00:00Z', last_publish_at: '2026-11-04T00:00:00Z', last_sequence: 3 })
  const pending = publishFlow({ identity: true, targets: [published], bundlePending: true })
  assert.equal(pending.current.key, 'publish')
  assert.match(pending.current.detail, /需要重新发布/)
  const done = publishFlow({ identity: true, targets: [published], bundlePending: false })
  assert.equal(done.current, null)
})

test('停用的订阅端不参与判定', () => {
  const flow = publishFlow({ identity: true, targets: [target({ enabled: false, deploy_state: 'installed' })], bundlePending: false })
  assert.equal(flow.current.key, 'target')
})
