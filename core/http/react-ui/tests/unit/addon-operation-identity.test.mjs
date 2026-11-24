import test from 'node:test'
import assert from 'node:assert/strict'
import { requestFingerprint } from '../../src/components/addons/actionBook.js'
import { createUpgradeStore } from '../../src/utils/addonUpgrades.js'

// The owner answers a known operation ID with a different request as an
// operation conflict. One ID belongs to one request: the same request retried
// keeps it, a different request mints a new one.

test('request fingerprint ignores the operation id and the key order, and sees any other difference', () => {
  const a = { operationId: 'x', addonId: 'notes', approve: { ops: ['a', 'b'], nonce: 'n1' } }
  const same = { approve: { nonce: 'n1', ops: ['a', 'b'] }, addonId: 'notes', operationId: 'y' }
  assert.equal(requestFingerprint(a), requestFingerprint(same))
  assert.notEqual(requestFingerprint(a), requestFingerprint({ ...a, approve: { ...a.approve, ops: ['a'] } }))
  assert.notEqual(requestFingerprint(a), requestFingerprint({ ...a, approve: { ...a.approve, nonce: 'n2' } }))
})

test('approve key differs for other ticks and stays for a retry of the same approval', async () => {
  const src = await import('node:fs').then(fs => fs.readFileSync(new URL('../../src/components/addons/approvalRequest.js', import.meta.url), 'utf8'))
  assert.match(src, /approve:\$\{addon\.id\}:\$\{addon\.pendingUpgrade\.version\}:\$\{requestFingerprint\(body\)\}/)
  assert.doesNotMatch(src, /actions\.run\(`approve:\$\{addon\.id\}:\$\{addon\.pendingUpgrade\.version\}`/)
})

test('upgrade store: a retry of the same request keeps its id, another candidate gets a new one', async () => {
  const store = createUpgradeStore({ waits: [] })
  const sent = []
  const api = { updates: { upgrade: async (_id, body) => { sent.push(body); throw Object.assign(new Error('down'), { kind: 'network' }) } }, installed: async () => [] }
  const addon = digest => ({ id: 'notes', digest: 'old', generation: 1, update: { state: 'update-available', digest } })
  await store.start(api, addon('new-1'))
  await store.start(api, addon('new-1'))
  await store.start(api, addon('new-2'))
  assert.equal(sent.length, 3)
  assert.equal(sent[0].operationId, sent[1].operationId)
  assert.notEqual(sent[1].operationId, sent[2].operationId)
  assert.equal(sent[2].candidateDigest, 'new-2')
})
