import test from 'node:test'
import assert from 'node:assert/strict'
import { upgradeFailureWords } from '../../src/components/addons/updateReview.js'
import { createUpgradeStore } from '../../src/utils/addonUpgrades.js'
import { AddonsError } from '../../src/utils/addonsApi.js'
import { classifyResponse } from '../../src/components/catalog/catalogState.js'
import { updateErrorKey } from '../../src/components/addons/updateState.js'
import { describeError } from '../../src/components/addons/errors.js'

// The add-on upgrade store: upgrades started from the add-ons page run here, so
// they go on when the page is left and show in Activity. The selection of what
// to upgrade is the shared "Upgrade all" dialog (bulkupgrade.test.mjs).

const tr = (key, vars) => (vars && vars.version ? `${key}:${vars.version}` : key)
const digestA = `sha256:${'a'.repeat(64)}`
const digestB = `sha256:${'b'.repeat(64)}`
const installed = (id, update, extra = {}) => ({ id, title: id.toUpperCase(), version: '1.0.0', digest: digestA, generation: 1, sourceId: 'lib', channel: 'stable', update, ...extra })

test('add-ons: the words of a failed upgrade follow the key the store reports', () => {
  assert.equal(upgradeFailureWords('canceled', tr), 'updates.class.canceled')
  assert.equal(upgradeFailureWords('upgrade_failed', tr), 'updates.errors.upgrade_failed')
  assert.equal(upgradeFailureWords('', tr), 'updates.errors.unavailable')
})

test('an operation the owner does not know is its own kind and never the administrator-only text', () => {
  const unknown = classifyResponse(404, { error: { type: 'operation_not_found', message: 'x' } })
  assert.equal(unknown.kind, 'operation_not_found')
  assert.equal(updateErrorKey(unknown), 'operationNotFound')
  assert.equal(upgradeFailureWords('operationNotFound', tr), 'updates.errors.operationNotFound')
  assert.equal(describeError(unknown, tr), 'errors.operationNotFound')
  // A real refusal of the role keeps the denied words.
  const refused = classifyResponse(403, { error: { type: 'addon_error', message: '' } })
  assert.equal(updateErrorKey(refused), 'denied')
  assert.equal(describeError(refused, tr), 'errors.denied')
  assert.equal(classifyResponse(404, { error: { type: 'other' } }).kind, 'unavailable')
})

// A fake owner: upgrade() decides each add-on's fate; installed() reports the
// generation after it.
function fakeOwner({ fate, status = async () => ({ phase: 'completed' }) }) {
  const gens = { notes: 1, cal: 1, wiki: 1 }
  const calls = []
  const api = {
    updates: {
      async upgrade(id, body, signal) {
        calls.push({ id, body })
        const out = await fate[id](signal)
        if (out === 'applied') gens[id] += 1
        return { operationId: body.operationId, addonId: id, phase: out === 'applied' ? 'completed' : out }
      },
    },
    async installed() { return Object.entries(gens).map(([id, generation]) => installed(id, undefined, { generation })) },
    operationStatus: (...args) => status(...args),
  }
  return { api, calls, gens }
}
const pendingAddon = (id) => installed(id, { state: 'update-available', available: true, version: '1.1.0', digest: digestB })
const store = (extra = {}) => createUpgradeStore({ waits: [], ...extra })

test('upgrade store: a batch over three add-ons continues past a failure and reports each one', async () => {
  const owner = fakeOwner({
    fate: {
      notes: async () => 'applied',
      cal: async () => { throw new AddonsError({ kind: 'invalid', type: 'upgrade_failed', status: 409 }) },
      wiki: async () => 'applied',
    },
  })
  const s = store()
  const batch = ['notes', 'cal', 'wiki'].map(id => ({ key: id, name: id }))
  const results = []
  for (const item of batch) {
    const out = await s.start(owner.api, pendingAddon(item.key))
    results.push({ key: item.key, ok: out.ok, message: out.message })
  }
  assert.deepEqual(results.map(r => [r.key, r.ok]), [['notes', true], ['cal', false], ['wiki', true]])
  assert.equal(results[1].message, 'upgrade_failed')
  assert.deepEqual(owner.calls.map(c => c.id), ['notes', 'cal', 'wiki'])
  const snap = s.getSnapshot()
  assert.deepEqual(snap.records.map(r => r.addonId).sort(), ['notes', 'wiki'])
  assert.equal(snap.live.length, 1)
  assert.equal(snap.live[0].addonId, 'cal')
  assert.equal(snap.live[0].state, 'failed')
  assert.equal(snap.live[0].errorKey, 'upgrade_failed')
  // The request pinned both digests and the generation, as the owner requires.
  assert.deepEqual(Object.keys(owner.calls[0].body).sort(), ['candidateDigest', 'expectedDigest', 'generation', 'operationId'])
})

test('upgrade store: a completed status that changed nothing is not reported as an upgrade', async () => {
  const owner = fakeOwner({ fate: { notes: async () => 'completed' } })
  const s = store()
  const out = await s.start(owner.api, pendingAddon('notes'))
  assert.equal(out.ok, false)
  assert.equal(out.outcome, 'not_applied')
  assert.equal(s.getSnapshot().records.length, 0)
})

test('upgrade store: an add-on without a confirmed candidate, or one already running, is refused before any request', async () => {
  const owner = fakeOwner({ fate: { notes: () => new Promise(() => {}) } })
  const s = store()
  assert.equal((await s.start(owner.api, installed('notes', { state: 'incompatible', version: '2', digest: digestB }))).ok, false)
  assert.equal(owner.calls.length, 0)
  const first = s.start(owner.api, pendingAddon('notes'))
  await new Promise(r => setImmediate(r))
  assert.equal(s.getSnapshot().live[0].state, 'running')
  assert.equal((await s.start(owner.api, pendingAddon('notes'))).outcome, 'busy')
  assert.equal(owner.calls.length, 1)
  s.cancel(s.getSnapshot().live[0].jobID)
  void first
})

test('upgrade store: owner-managed upgrades report stages, not synthetic percentages or cancellation', async () => {
  let finish
  const owner = fakeOwner({ fate: { notes: () => new Promise(resolve => { finish = resolve }) } })
  const s = store()
  const run = s.start(owner.api, pendingAddon('notes'))
  await new Promise(r => setImmediate(r))
  const job = s.getSnapshot().live[0]
  assert.equal(job.cancellable, false)
  assert.equal(job.progress, 0)
  assert.equal(job.phase, 'submitting')
  assert.equal(s.cancel(job.jobID), false)
  finish('applied')
  assert.equal((await run).ok, true)
})

test('upgrade store: an unknown outcome is read on retry and never submitted a second time', async () => {
  let reads = 0
  const owner = fakeOwner({
    fate: { notes: async () => { throw new AddonsError({ kind: 'unknown_outcome', operationId: 'will-be-ignored' }) } },
    status: async () => { reads += 1; return { phase: reads < 2 ? 'prepared' : 'completed' } },
  })
  const s = store()
  const first = await s.start(owner.api, pendingAddon('notes'))
  assert.equal(first.ok, false)
  assert.equal(first.message, 'unknownOutcome')
  const job = s.getSnapshot().live[0]
  assert.equal(job.unknown, true)
  // A different upgrade of the same add-on (another generation) while the
  // outcome is unknown is blocked and sends nothing.
  const blocked = await s.start(owner.api, { ...pendingAddon('notes'), generation: 2 })
  assert.equal(blocked.outcome, 'blocked')
  assert.equal(owner.calls.length, 1)
  // Retry only reads; the owner is not asked to upgrade again.
  await s.retry(owner.api, s.getSnapshot().live[0].jobID)
  assert.equal(owner.calls.length, 1)
  assert.ok(reads >= 1)
})

test('upgrade store: retry of a refused upgrade starts a new operation from the installed record as it is now', async () => {
  let attempt = 0
  const owner = fakeOwner({ fate: { notes: async () => { attempt += 1; if (attempt === 1) throw new AddonsError({ kind: 'invalid', type: 'upgrade_failed' }); return 'applied' } } })
  // The owner's installed record must offer the candidate for the retry.
  owner.api.installed = async () => [attempt >= 2 ? installed('notes', undefined, { generation: 2 }) : pendingAddon('notes')]
  const s = store()
  assert.equal((await s.start(owner.api, pendingAddon('notes'))).ok, false)
  const jobID = s.getSnapshot().live[0].jobID
  const again = await s.retry(owner.api, jobID)
  assert.equal(again.ok, true)
  assert.equal(owner.calls.length, 2)
  assert.notEqual(owner.calls[0].body.operationId, owner.calls[1].body.operationId)
  // Dismissing a failure moves it to the record.
  const s2 = store()
  await s2.start(fakeOwner({ fate: { notes: async () => { throw new AddonsError({ kind: 'invalid', type: 'upgrade_failed' }) } } }).api, pendingAddon('notes'))
  s2.dismiss(s2.getSnapshot().live[0].jobID)
  assert.equal(s2.getSnapshot().live.length, 0)
  assert.equal(s2.getSnapshot().records[0].outcome, 'failed')
})

test('upgrade store: while the add-on service does not answer, the reads back off and the entry says it waits', async () => {
  let reads = 0
  const slept = []
  const owner = fakeOwner({
    fate: { notes: async () => 'prepared' },
    status: async () => {
      reads += 1
      if (reads <= 3) throw new AddonsError({ kind: 'unavailable', status: 503 })
      return { phase: 'completed' }
    },
  })
  owner.gens.notes = 2 // the upgrade applied while the service restarted
  let seen = null
  const s = createUpgradeStore({
    waits: [2000, 2000, 2000, 2000, 2000],
    sleep: async (ms) => { slept.push(ms); seen = s.getSnapshot().live[0]?.waiting || null },
  })
  const out = await s.start(owner.api, pendingAddon('notes'))
  assert.equal(out.ok, true)
  // 2 s, then 4 s, then 8 s while it is down; the fourth read answers.
  assert.deepEqual(slept, [2000, 4000, 8000])
  assert.deepEqual(seen && seen.nextMs, 8000, 'the entry said it waited for the service')
  assert.equal(s.getSnapshot().live.length, 0)
})

test('upgrade store: check now ends the wait of a followed upgrade', async () => {
  let reads = 0
  const owner = fakeOwner({
    fate: { notes: async () => 'prepared' },
    status: async () => { reads += 1; if (reads === 1) throw new AddonsError({ kind: 'unavailable', status: 503 }); return { phase: 'completed' } },
  })
  owner.gens.notes = 2
  const s = createUpgradeStore({ waits: [60000] })
  const done = s.start(owner.api, pendingAddon('notes'))
  // Wait until the store sleeps, then wake it.
  for (let i = 0; i < 50 && !s.getSnapshot().live[0]?.waiting; i++) await new Promise(r => setTimeout(r, 5))
  const op = s.getSnapshot().live[0]
  assert.ok(op.waiting, 'the entry says it waits for the service')
  assert.equal(op.waiting.nextMs, 60000)
  assert.equal(s.checkNow(op.jobID), true)
  const out = await done
  assert.equal(out.ok, true)
  assert.equal(s.checkNow(op.jobID), false)
})


test('upgrade observers resume the same ID after reload and isolate account changes/logout', async () => {
 const values = new Map()
 const storage = { getItem: k => values.get(k), setItem: (k,v) => values.set(k,v), removeItem: k => values.delete(k) }
 const api = { updates: { upgrade: async () => ({ phase: 'accepted' }) }, operationStatus: async () => ({ phase: 'running', stage: 'backing-up' }), installed: async () => [] }
 const first = store()
 first.bind(api, 'alice', storage)
 await first.start(api, pendingAddon('notes'))
 const id = first.getSnapshot().live[0].operationId
 let reads = []
 const resumedApi = { ...api, operationStatus: async op => { reads.push(op); return { phase: 'failed', failureCode: 'interrupted' } } }
 const reloaded = store()
 reloaded.bind(resumedApi, 'bob', storage)
 assert.equal(reloaded.getSnapshot().live.length, 0)
 reloaded.bind(resumedApi, 'alice', storage)
 await new Promise(r => setImmediate(r))
 assert.deepEqual(reads, [id])
 assert.equal(reloaded.getSnapshot().live[0].failureCode, 'interrupted')
 reloaded.bind(resumedApi, '', storage)
 assert.equal(reloaded.getSnapshot().live.length, 0)
 assert.equal(values.has('addon-upgrades:alice'), false)
})


test('a saved upgrade whose operation the owner no longer knows is cleared, with its own words', async () => {
  const values = new Map()
  const storage = { getItem: k => values.get(k), setItem: (k, v) => values.set(k, v), removeItem: k => values.delete(k) }
  const gone = async () => { throw new AddonsError({ kind: 'operation_not_found', status: 404, type: 'operation_not_found' }) }
  const api = { updates: { upgrade: async () => ({ phase: 'accepted' }) }, operationStatus: async () => ({ phase: 'running', stage: 'backing-up' }), installed: async () => [] }
  const first = store()
  first.bind(api, 'alice', storage)
  await first.start(api, pendingAddon('notes'))
  assert.equal(JSON.parse(values.get('addon-upgrades:alice')).length, 1)
  // After a new login the saved operation is read and is not found.
  let reads = 0
  const later = { ...api, operationStatus: async (...args) => { reads += 1; return gone(...args) } }
  const reloaded = store()
  reloaded.bind(later, 'alice', storage)
  await new Promise(r => setImmediate(r))
  assert.equal(reads, 1, 'the unknown operation is read once, not polled')
  assert.equal(reloaded.getSnapshot().live.length, 0)
  assert.deepEqual(JSON.parse(values.get('addon-upgrades:alice')), [])
  // A started upgrade that turns unknown reports the neutral words, and the add-on can be upgraded again.
  const fresh = store()
  fresh.bind({ ...api, operationStatus: gone }, 'alice', storage)
  const out = await fresh.start({ ...api, operationStatus: gone }, pendingAddon('notes'))
  assert.equal(out.ok, false)
  assert.equal(out.outcome, 'operation_not_found')
  assert.equal(out.message, 'operationNotFound')
  assert.equal(fresh.getSnapshot().live.length, 0)
})

test('account change gives Bob a fresh action identity and restores Alice unknown identity on return', async () => {
  const values = new Map()
  const storage = { getItem: k => values.get(k), setItem: (k,v) => values.set(k,v), removeItem: k => values.delete(k) }
  const owner = fakeOwner({ fate: { notes: async () => { throw new AddonsError({ kind: 'unknown_outcome' }) } }, status: async () => ({ phase: 'running' }) })
  const s = store()
  s.bind(owner.api, 'alice', storage)
  await s.start(owner.api, pendingAddon('notes'))
  const aliceID = owner.calls[0].body.operationId
  s.bind(owner.api, 'bob', storage)
  await s.start(owner.api, pendingAddon('notes'))
  const bobID = owner.calls[1].body.operationId
  assert.notEqual(bobID, aliceID)
  s.bind(owner.api, 'alice', storage)
  await new Promise(r => setImmediate(r))
  const job = s.getSnapshot().live[0]
  assert.equal(job.operationId, aliceID)
  await s.retry(owner.api, job.jobID)
  assert.equal(owner.calls.length, 2, 'restored unknown operation must only poll, never resubmit')
})

test('logout cleanup clears persisted observers even in a fresh unbound store before navigation', () => {
  const values = new Map([['addon-upgrades:alice', '[{"operationId":"private"}]'], ['unrelated', 'keep']])
  const storage = { get length() { return values.size }, key: i => [...values.keys()][i], getItem: k => values.get(k), setItem: (k,v) => values.set(k,v), removeItem: k => values.delete(k) }
  const fresh = store()
  fresh.clearSession(storage)
  assert.equal(values.has('addon-upgrades:alice'), false)
  assert.equal(values.get('unrelated'), 'keep')
  assert.deepEqual(fresh.getSnapshot(), { live: [], records: [] })
})
