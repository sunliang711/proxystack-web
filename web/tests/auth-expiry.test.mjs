import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createServer } from 'vite'

let server
let handleUnauthorizedResponse
let setUnauthorizedHandler

before(async () => {
  globalThis.document = { baseURI: 'http://localhost/' }
  server = await createServer({ configFile: false, server: { middlewareMode: true }, logLevel: 'silent' })
  ;({ handleUnauthorizedResponse, setUnauthorizedHandler } = await server.ssrLoadModule('/src/api/client.ts'))
})

after(async () => {
  await server?.close()
  delete globalThis.document
})

test('仅在 401 时通知统一的会话失效处理', () => {
  const paths = []
  setUnauthorizedHandler((path) => paths.push(path))

  assert.equal(handleUnauthorizedResponse(new Response(null, { status: 401 }), '/api/users'), true)
  assert.equal(handleUnauthorizedResponse(new Response(null, { status: 403 }), '/api/users'), false)
  assert.deepEqual(paths, ['/api/users'])
})
