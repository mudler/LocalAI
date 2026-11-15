import test from 'node:test'
import assert from 'node:assert/strict'
import { initialSession, sessionStep, retryDelay, isSessionExempt, RETRY_CAP_MS } from '../../src/utils/authSession.js'

const run = (events, start = initialSession) => events.reduce((s, e) => sessionStep(s, e), start)
const SIGNED_IN = { type: 'status-ok', data: { authEnabled: true, user: { id: 'u1', role: 'user', permissions: { chat: true } } } }
const ANON = { type: 'status-ok', data: { authEnabled: true, user: null } }
const AUTH_OFF = { type: 'status-ok', data: { authEnabled: false } }
const API = '/api/backends/installed'

test('session: happy path signed in', () => {
  const s = run([SIGNED_IN])
  assert.equal(s.status, 'signed-in')
  assert.equal(s.user.id, 'u1')
  assert.deepEqual(s.permissions, { chat: true })
  assert.equal(s.authEnabled, true)
})

test('session: no user is anonymous, and auth off keeps the auth-off meaning', () => {
  assert.equal(run([ANON]).status, 'anonymous')
  const off = run([AUTH_OFF])
  assert.equal(off.status, 'anonymous')
  assert.equal(off.authEnabled, false)
  assert.equal(off.user, null)
})

test('session: a failed status read is unreachable, never auth disabled', () => {
  const s = run([{ type: 'status-failed' }])
  assert.equal(s.status, 'unreachable')
  assert.equal(s.failures, 1)
  assert.equal(s.user, null)
})

test('session: status failure, then retry, then ok', () => {
  let s = run([{ type: 'status-failed' }, { type: 'retry' }])
  assert.equal(s.status, 'unreachable')
  assert.equal(s.retrying, true)
  s = sessionStep(s, SIGNED_IN)
  assert.equal(s.status, 'signed-in')
  assert.equal(s.failures, 0)
  assert.equal(s.retrying, false)
})

test('session: repeated failure counts up and backs off to a cap', () => {
  let s = initialSession
  for (let i = 0; i < 8; i++) s = run([{ type: 'status-failed' }, { type: 'retry' }], s)
  s = sessionStep(s, { type: 'status-failed' })
  assert.equal(s.status, 'unreachable')
  assert.equal(s.failures, 9)
  assert.equal(s.retrying, false)
  assert.ok(retryDelay(1) < retryDelay(2))
  assert.equal(retryDelay(50), RETRY_CAP_MS)
})

test('session: retry only means something while unreachable', () => {
  const s = run([SIGNED_IN])
  assert.equal(sessionStep(s, { type: 'retry' }), s)
})

test('session: a failed refresh does not sign out a signed-in person', () => {
  const s = run([SIGNED_IN])
  assert.equal(sessionStep(s, { type: 'status-failed' }), s)
})

test('session: parallel 401s end the session once', () => {
  const signed = run([SIGNED_IN])
  const once = sessionStep(signed, { type: 'unauthorized', url: API })
  assert.equal(once.status, 'expired')
  assert.equal(once.user, null)
  assert.deepEqual(once.permissions, {})
  assert.equal(once.authEnabled, true)
  assert.equal(once.sessionEpoch, signed.sessionEpoch + 1)
  const again = run([{ type: 'unauthorized', url: API }, { type: 'unauthorized', url: '/api/models' }], once)
  assert.equal(again, once)
})

test('session: a 401 from the login, register, status or anonymous calls is ignored', () => {
  const signed = run([SIGNED_IN])
  for (const url of ['/api/auth/login', '/api/auth/register', '/api/auth/status', '/api/auth/token-login', '/api/auth/logout', '/api/branding', 'http://host.example/app/api/auth/login?x=1']) {
    assert.equal(sessionStep(signed, { type: 'unauthorized', url }), signed, url)
  }
  assert.equal(isSessionExempt('/api/auth/login'), true)
  assert.equal(isSessionExempt(API), false)
})

test('session: a 401 when nobody is signed in or auth is off changes nothing', () => {
  for (const start of [initialSession, run([ANON]), run([AUTH_OFF]), run([{ type: 'status-failed' }])]) {
    assert.equal(sessionStep(start, { type: 'unauthorized', url: API }), start)
  }
})

test('session: after expiry a new status read signs in again', () => {
  const expired = run([SIGNED_IN, { type: 'unauthorized', url: API }])
  assert.equal(sessionStep(expired, SIGNED_IN).status, 'signed-in')
})

test('session: events after logout do not bring the session back', () => {
  const out = run([SIGNED_IN, { type: 'logout' }])
  assert.equal(out.status, 'anonymous')
  assert.equal(out.user, null)
  assert.equal(out.authEnabled, true)
  assert.equal(sessionStep(out, { type: 'unauthorized', url: API }), out)
  assert.equal(sessionStep(out, { type: 'status-failed' }), out)
  assert.equal(sessionStep(out, { type: 'retry' }), out)
})
