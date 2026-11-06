import test from 'node:test'
import assert from 'node:assert/strict'
import { SHARE_KIND, classifyShareError, connectionShareClient, shareAvailable, shareRequest, sharedOfKind } from '../../../internal/connector-ui/src/share.js'

const ID = 'r0123456789abcdef0123456789abcde'

test('offers sharing only when the host draws the dialog and the server shares', () => {
  const sdk = { ui: { ShareDialog: () => null } }
  assert.equal(shareAvailable(sdk, { sharing: true }), true)
  assert.equal(shareAvailable(sdk, { sharing: false }), false, 'local-owner mode shares nothing')
  assert.equal(shareAvailable({ ui: {} }, { sharing: true }), false, 'an older host has no dialog')
  assert.equal(shareAvailable(null, { sharing: true }), false)
  assert.equal(shareAvailable(sdk, null), false)
})

test('keeps the shared bindings of the page kind, and nothing else', () => {
  const listing = { shared: [{ id: 'a', kind: 'telegram', name: 'Team' }, { id: 'b', kind: 'slack', name: 'Other' }, null, { kind: 'telegram' }] }
  assert.deepEqual(sharedOfKind(listing, 'telegram').map((c) => c.id), ['a'])
  assert.deepEqual(sharedOfKind({}, 'telegram'), [])
})

test('builds the calls of the sharing API with the confirmation header and the use role only', () => {
  const grants = shareRequest('grants', ID)
  assert.equal(grants.path, `/api/shares/${SHARE_KIND}/${ID}/grants`)
  assert.equal(grants.init.headers['X-LocalAI-Confirm'], ID)
  const set = shareRequest('set', ID, { principal: 'user-1', role: 'use' })
  assert.equal(set.init.method, 'PUT')
  assert.equal(set.path, `/api/shares/connection/${ID}/grants/user-1`)
  assert.deepEqual(JSON.parse(set.init.body), { role: 'use' })
  assert.throws(() => shareRequest('set', ID, { principal: 'user-1', role: 'manage' }), TypeError)
  assert.throws(() => shareRequest('grants', '../x'), TypeError)
  assert.equal(shareRequest('revoke', ID, { principal: 'user-1' }).init.method, 'DELETE')
  assert.match(shareRequest('users', ID, { query: 'al' }).path, /\/users\?q=al&limit=8$/)
  assert.throws(() => shareRequest('users', ID, { query: 'a' }), TypeError)
})

test('gives the dialog the error kinds it reads', () => {
  const err = (status, type) => Object.assign(new Error('x'), { status, body: type ? { error: { type } } : null })
  assert.equal(classifyShareError(err(401)).kind, 'auth')
  assert.equal(classifyShareError(err(403)).kind, 'denied')
  assert.equal(classifyShareError(err(409, 'sharing_disabled')).kind, 'sharing_disabled')
  assert.equal(classifyShareError(err(503, 'outcome_unknown')).kind, 'unknown_outcome')
  assert.equal(classifyShareError(err(503)).kind, 'unavailable')
  assert.equal(classifyShareError(err(400)).kind, 'invalid')
  assert.equal(classifyShareError(new TypeError('Failed to fetch')).kind, 'network')
})

test('runs the client through the page call and classifies a refusal', async () => {
  const seen = []
  const call = async (path, init) => {
    seen.push([init.method, path])
    if (init.method === 'DELETE') throw Object.assign(new Error('no'), { status: 403 })
    return { grants: [] }
  }
  const client = connectionShareClient(call, ID)
  assert.deepEqual(await client.grants(), { grants: [] })
  await client.setGrant('user-1', 'use')
  await assert.rejects(client.revoke('user-1'), (e) => e.kind === 'denied')
  assert.deepEqual(seen.map((s) => s[0]), ['GET', 'PUT', 'DELETE'])
})
