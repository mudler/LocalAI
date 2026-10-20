import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { load } from './render.test.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const require = createRequire(join(resolve(here, '../../../../core/http/react-ui'), 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

test('library: the first paint of the library page has the core test id', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'library', path: '' }))
  assert.match(html, /data-testid="voice-library"/)
})

test('create: the first paint of the create page has the core test id', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'create', path: '' }))
  assert.match(html, /data-testid="voice-profile-create"/)
})

test('api: the voice profile calls use the core routes through sdk.core.fetch', async () => {
  const { mod, sdk, calls } = await load()
  const { createApi } = await import('../src/api.js')
  const { createContext } = await import('../src/ctx.jsx').catch(() => ({}))
  const ctx = mod.__ctxFor ? mod.__ctxFor(sdk) : { core: sdk.core, json: async (p, i) => (await sdk.core.fetch(p, i)).json() }
  const api = createApi(ctx)
  await api.voiceProfiles.list()
  await api.voiceProfiles.remove('a/b')
  assert.equal(calls[0][0], '/api/voice-profiles')
  assert.equal(calls[1][0], '/api/voice-profiles/a%2Fb')
  assert.equal(calls[1][1].method, 'DELETE')
  assert.equal(api.voiceProfiles.audioUrl('a/b'), '/api/voice-profiles/a%2Fb/audio')
  void createContext
})

// renderToString does not run effects. This host React queues them so a test
// can run them after the first paint and see which calls the page makes.
function queuedEffects() {
  const queue = []
  return { React: { ...React, useEffect: (fn) => { queue.push(fn) } }, run: () => queue.splice(0).forEach(fn => fn()) }
}
const profileCalls = (calls) => calls.filter(([url]) => /^\/api\/(voice-profiles|models|operations)/.test(url))

for (const page of ['library', 'create']) {
  test(`${page}: a viewer who is not an administrator sees the off notice and the page makes no call`, async () => {
    const q = queuedEffects()
    const { mod, sdk, calls } = await load({ React: q.React, viewer: { isAdmin: () => false, hasFeature: () => true } })
    const addon = await mod.createAddon(sdk)
    const html = renderToString(React.createElement(addon.Component, { page, path: '' }))
    q.run()
    await new Promise(r => setTimeout(r, 20))
    assert.match(html, /data-testid="voice-admin-only"/)
    assert.doesNotMatch(html, /data-testid="voice-library-new"|data-testid="voice-library"|data-testid="voice-profile-create"/)
    assert.deepEqual(profileCalls(calls), [])
  })
}

test('library: an administrator gets the library and the profile list is requested', async () => {
  const q = queuedEffects()
  const { mod, sdk, calls } = await load({ React: q.React })
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'library', path: '' }))
  q.run()
  await new Promise(r => setTimeout(r, 20))
  assert.doesNotMatch(html, /voice-admin-only/)
  assert.ok(calls.some(([url]) => url === '/api/voice-profiles'), 'the list call was not made')
})

test('ctx: audioUrl adds the path prefix of the page, read from the base element', async () => {
  const { mod, sdk } = await load()
  const { createApi } = await import('../src/api.js')
  const saved = globalThis.document
  globalThis.document = { querySelector: (sel) => (sel === 'base[href]' ? { getAttribute: () => 'http://host.test/prefix/' } : null) }
  try {
    const ctx = mod.__ctxFor(sdk)
    assert.equal(ctx.basePath, '/prefix')
    assert.equal(createApi(ctx).voiceProfiles.audioUrl('a/b'), '/prefix/api/voice-profiles/a%2Fb/audio')
  } finally {
    if (saved === undefined) delete globalThis.document
    else globalThis.document = saved
  }
})

test('ctx: json throws the message of the API error and falls back to the status', async () => {
  const bad = (body) => ({ fetch: async () => ({ ok: false, status: 500, json: async () => { if (body instanceof Error) throw body; return body } }) })
  let loaded = await load(bad({ error: { message: 'boom' } }))
  await assert.rejects(() => loaded.mod.__ctxFor(loaded.sdk).json('/api/voice-profiles'), /boom/)
  loaded = await load(bad(new Error('not json')))
  await assert.rejects(() => loaded.mod.__ctxFor(loaded.sdk).json('/api/voice-profiles'), /HTTP 500/)
})
