import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createServer } from 'vite'

let server
let VersionCheckTimeoutError
let waitForVersion

before(async () => {
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ VersionCheckTimeoutError, waitForVersion } = await server.ssrLoadModule('/src/util/self-update.ts'))
})

after(async () => server?.close())

test('旧版本和重启断线后检测到目标版本', async () => {
  let now = 0
  const results = ['1.0.0', new Error('service unavailable'), 'v1.1.0']
  const states = []
  const version = await waitForVersion({
    targetVersion: '1.1.0',
    signal: new AbortController().signal,
    probe: async () => {
      const result = results.shift()
      if (result instanceof Error) throw result
      return result
    },
    onReachable: (value) => states.push(`reachable:${value}`),
    onUnavailable: () => states.push('unavailable'),
    interval: 1000,
    timeout: 5000,
    now: () => now,
    delay: async (milliseconds) => { now += milliseconds },
  })
  assert.equal(version, 'v1.1.0')
  assert.deepEqual(states, ['reachable:1.0.0', 'unavailable'])
})

test('超过截止时间后停止检测', async () => {
  let now = 0
  let probes = 0
  await assert.rejects(waitForVersion({
    targetVersion: '2.0.0',
    signal: new AbortController().signal,
    probe: async () => { probes++; return '1.0.0' },
    interval: 1000,
    timeout: 2500,
    now: () => now,
    delay: async (milliseconds) => { now += milliseconds },
  }), (error) => error instanceof VersionCheckTimeoutError)
  assert.equal(probes, 3)
})

test('取消后不再请求版本', async () => {
  const controller = new AbortController()
  controller.abort()
  let probes = 0
  await assert.rejects(waitForVersion({
    targetVersion: '2.0.0',
    signal: controller.signal,
    probe: async () => { probes++; return '2.0.0' },
  }), (error) => error?.name === 'AbortError')
  assert.equal(probes, 0)
})
