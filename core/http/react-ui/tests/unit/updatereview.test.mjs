import test from 'node:test'
import assert from 'node:assert/strict'
import { upgradeFailureWords } from '../../src/components/addons/updateReview.js'
import { createUpgradeStore } from '../../src/utils/addonUpgrades.js'
import { AddonsError } from '../../src/utils/addonsApi.js'

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

test('upgrade store: cancel aborts the request and reports what the owner says, not the click', async () => {
  const abortable = (signal) => new Promise((_, reject) => signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))))
  const owner = fakeOwner({ fate: { notes: abortable, cal: abortable }, status: async () => ({ phase: 'failed' }) })
  const s = store()
  const run = s.start(owner.api, pendingAddon('notes'))
  await new Promise(r => setImmediate(r))
  const job = s.getSnapshot().live[0]
  assert.equal(job.cancellable, true)
  assert.equal(s.cancel(job.jobID), true)
  assert.equal(s.getSnapshot().live[0].state, 'cancelling')
  const out = await run
  assert.equal(out.outcome, 'cancelled')
  assert.equal(s.getSnapshot().live.length, 0)
  assert.equal(s.getSnapshot().records[0].outcome, 'cancelled')
  // The owner finished it before the cancel landed: that is an upgrade.
  const owner2 = fakeOwner({ fate: { cal: abortable }, status: async () => ({ phase: 'completed' }) })
  const run2 = s.start(owner2.api, pendingAddon('cal'))
  await new Promise(r => setImmediate(r))
  s.cancel(s.getSnapshot().live[0].jobID)
  assert.equal((await run2).ok, true)
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
