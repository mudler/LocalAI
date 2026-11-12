import test from 'node:test'
import assert from 'node:assert/strict'
import { hookHarness } from './chatcommands-lifecycle.mjs'
import { createUpgradeStore } from '../../src/utils/addonUpgrades.js'
import * as session from '../../src/utils/authSession.js'
import * as events from '../../src/utils/sessionEvents.js'

// Runs the real AuthProvider with a controlled fetch. Only its JSX boundary is
// replaced, as in addon-session.test.mjs.
async function provider(reply) {
  const calls = []
  const h = hookHarness('../../src/context/AuthContext.jsx', 'AuthProvider', {
    apiUrl: x => x, addonUpgrades: createUpgradeStore(), ...session, ...events,
    fetch: url => { calls.push(url); return reply(url, calls.length) },
    document: { cookie: '' }, window: { location: {} },
  }, source => source.replace(/return \(\s*<AuthContext.Provider[\s\S]*?\n  \)/, 'return value'))
  h.render({ children: null }); h.flush()
  const settle = async () => { await new Promise(r => setImmediate(r)); return h.render({ children: null }) }
  return { h, calls, settle }
}
const ok = (data) => Promise.resolve({ ok: true, json: async () => data })

test('provider: a status read that fails is unreachable, not auth off, and shows no admin rights', async () => {
  for (const reply of [
    () => Promise.reject(new TypeError('network down')),
    () => Promise.resolve({ ok: true, json: async () => { throw new SyntaxError('not json') } }),
    () => Promise.resolve({ ok: false, status: 503, json: async () => ({ error: 'busy' }) }),
  ]) {
    const p = await provider(reply)
    const auth = await p.settle()
    assert.equal(auth.status, 'unreachable')
    assert.equal(auth.unreachable, true)
    assert.equal(auth.loading, false)
    assert.equal(auth.user, null)
    assert.equal(auth.isAdmin, false)
    assert.equal(auth.hasFeature('chat'), false)
    p.h.unmount()
  }
})

test('provider: retry asks again and recovers', async () => {
  let up = false
  const p = await provider(() => up ? ok({ authEnabled: false }) : Promise.reject(new TypeError('down')))
  let auth = await p.settle()
  assert.equal(auth.status, 'unreachable')
  up = true
  await auth.retry()
  auth = await p.settle()
  assert.equal(auth.status, 'anonymous')
  assert.equal(auth.unreachable, false)
  assert.equal(auth.isAdmin, true) // the server said auth is off
  p.h.unmount()
})

test('provider: parallel 401s end the session once, after the status route confirms it', async () => {
  let signedIn = true
  const p = await provider(() => ok(signedIn ? { authEnabled: true, user: { id: 'u', role: 'user' } } : { authEnabled: true, user: null }))
  let auth = await p.settle()
  assert.equal(auth.status, 'signed-in')
  signedIn = false
  events.reportUnauthorized('/api/backends')
  events.reportUnauthorized('/api/models')
  events.reportUnauthorized('/api/nodes')
  auth = await p.settle()
  await p.settle()
  auth = await p.settle()
  assert.equal(auth.status, 'expired')
  assert.equal(auth.user, null)
  assert.equal(p.calls.length, 2, 'one status read at start, one to confirm the expiry')
  p.h.unmount()
})

test('provider: a 401 that the status route does not confirm leaves the session alone', async () => {
  const p = await provider(() => ok({ authEnabled: true, user: { id: 'u', role: 'user' } }))
  await p.settle()
  events.reportUnauthorized('/api/chats/connectors/x/test')
  await p.settle()
  const auth = await p.settle()
  assert.equal(auth.status, 'signed-in')
  p.h.unmount()
})

test('provider: a 401 from the sign-in call does not even ask', async () => {
  const p = await provider(() => ok({ authEnabled: true, user: { id: 'u', role: 'user' } }))
  await p.settle()
  events.reportUnauthorized('/api/auth/login')
  await p.settle()
  assert.equal(p.calls.length, 1)
  p.h.unmount()
})
