import test from 'node:test'
import assert from 'node:assert/strict'
import { upgradeStatusFields, endedAsCancelled, overlayLive, unfollowedUpgrades, mergeOperations, liveWithServer } from '../../src/utils/addonUpgradeRows.js'
import { createUpgradeStore } from '../../src/utils/addonUpgrades.js'
import { canRetryOp } from '../../src/utils/opsActivity.js'
import { normalizeOperation } from '../../src/utils/addonsApi.js'

const server = (name, over = {}) => ({ id: `addon:${name}`, name, jobID: `job-${name}`, isAddon: true, taskType: 'upgrade', isQueued: false, progress: 0, cancellable: true, ...over })
const entry = (name, over = {}) => ({ addonId: name, name, jobID: `addon:${name}:op`, galleryJobId: `job-${name}`, isAddon: true, taskType: 'upgrade', state: 'running', phase: 'running', stage: 'preparing', cancellable: true, ...over })

test('addonupgraderows: an accepted status is queued, and cancel is offered while the job is known and not writing', () => {
  const running = { state: 'running' }
  const accepted = upgradeStatusFields({ phase: 'accepted', jobId: 'j1' }, running)
  assert.deepEqual([accepted.phase, accepted.stage, accepted.galleryJobId, accepted.cancellable], ['queued', '', 'j1', true])
  const preparing = upgradeStatusFields({ phase: 'running', stage: 'preparing' }, { ...running, galleryJobId: 'j1' })
  assert.deepEqual([preparing.phase, preparing.cancellable, preparing.galleryJobId], ['running', true, 'j1'])
  assert.equal(upgradeStatusFields({ phase: 'running', stage: 'committing' }, { ...running, galleryJobId: 'j1' }).cancellable, false)
  assert.equal(upgradeStatusFields({ phase: 'running', stage: 'starting' }, { ...running, galleryJobId: 'j1' }).cancellable, false)
  // No job known yet, a cancel in progress or a final status: nothing to cancel.
  assert.equal(upgradeStatusFields({ phase: 'running', stage: 'preparing' }, running).cancellable, false)
  assert.equal(upgradeStatusFields({ phase: 'running' }, { state: 'cancelling', galleryJobId: 'j1' }).cancellable, false)
  assert.equal(upgradeStatusFields({ phase: 'failed', failureCode: 'cancelled' }, { ...running, galleryJobId: 'j1' }).cancellable, false)
})

test('addonupgraderows: cancelled while queued, or interrupted after our cancel, is not a failure', () => {
  assert.equal(endedAsCancelled('cancelled'), true)
  assert.equal(endedAsCancelled('interrupted', { requested: true }), true)
  assert.equal(endedAsCancelled('interrupted'), false)
  assert.equal(endedAsCancelled('upgrade_failed', { requested: true }), false)
})

test('addonupgraderows: the owner status carries the job id', () => {
  assert.equal(normalizeOperation({ OperationID: 'o', AddonID: 'a', Phase: 'accepted', jobId: 'j9' }).jobId, 'j9')
  assert.equal(normalizeOperation({}).jobId, '')
})

test('addonupgraderows: one upgrade listed by the server and followed here is one row, with the words of the store', () => {
  const live = [entry('notes', { fromVersion: '1', toVersion: '2' })]
  const ops = [server('notes', { isQueued: true }), { id: 'm', name: 'qwen', jobID: 'jm' }]
  const merged = mergeOperations(ops, live, (e) => ({ ...e, message: '1 to 2' }))
  assert.equal(merged.filter(o => o.name === 'notes').length, 1)
  const row = merged.find(o => o.name === 'notes')
  assert.equal(row.jobID, 'addon:notes:op')
  assert.equal(row.message, '1 to 2')
  assert.equal(row.phase, 'queued')
  assert.ok(merged.some(o => o.name === 'qwen'))
})

test('addonupgraderows: the server says whether it waits, how far it is and whether it can be cancelled', () => {
  const [queued] = overlayLive([entry('a', { phase: 'queued', stage: '' })], [server('a', { isQueued: true })])
  assert.equal(queued.phase, 'queued')
  const [started] = overlayLive([entry('a', { phase: 'queued', stage: '' })], [server('a', { progress: 40, currentBytes: 4, totalBytes: 10 })])
  assert.deepEqual([started.phase, started.progress, started.currentBytes, started.cancellable], ['running', 40, 4, true])
  const [writing] = overlayLive([entry('a', { stage: 'committing' })], [server('a', { progress: 100 })])
  assert.equal(writing.cancellable, false)
  // A failed entry keeps its own state.
  assert.equal(overlayLive([entry('a', { state: 'failed' })], [server('a', { isQueued: true })])[0].phase, 'running')
})

test('addonupgraderows: an upgrade only the server knows still shows, and can be cancelled by its job', () => {
  const ops = [server('other', { isQueued: true }), server('mine')]
  const live = [entry('mine')]
  assert.deepEqual(unfollowedUpgrades(ops, live).map(o => o.name), ['other'])
  const merged = mergeOperations(ops, live)
  assert.deepEqual(merged.map(o => o.name).sort(), ['mine', 'other'])
  const stand = liveWithServer(live, ops)
  assert.equal(stand.length, 2)
  const other = stand.find(e => e.addonId === 'other')
  assert.deepEqual([other.jobID, other.phase, other.cancellable, other.state], ['job-other', 'queued', true, 'running'])
  // A failed one of another tab is not made a running row.
  assert.equal(liveWithServer([], [server('x', { error: 'boom' })]).length, 0)
})

test('addonupgraderows: only an upgrade of this browser store is retried from a list', () => {
  assert.equal(canRetryOp({ id: 'u', error: 'x', isAddon: true, taskType: 'upgrade', jobID: 'addon:u:1' }), true)
  assert.equal(canRetryOp({ id: 'u', error: 'x', isAddon: true, taskType: 'upgrade', jobID: 'b1b2-uuid' }), false)
  assert.equal(canRetryOp({ id: 'm', error: 'x', isAddon: false, jobID: 'j' }), true)
})

const record = { id: 'notes', digest: 'old', generation: 1, version: '1', update: { state: 'update-available', digest: 'new', version: '2' } }
// A short real wait between polls, so a test can look at the entry while it waits.
const instant = { sleep: () => new Promise(r => setTimeout(r, 4)), waits: Array.from({ length: 500 }, () => 0) }

test('upgrade store: three upgrades are all sent before any answer, and a queued one is cancelled by its job', async () => {
  const store = createUpgradeStore(instant)
  const sent = []
  const statuses = {}
  const api = {
    updates: { upgrade: async (id) => { sent.push(id); return { phase: 'accepted', jobId: `job-${id}` } } },
    operationStatus: async (_op, id) => statuses[id] || { phase: 'accepted', jobId: `job-${id}` },
    installed: async () => [],
  }
  const works = ['a', 'b', 'c'].map(id => store.start(api, { ...record, id }))
  assert.deepEqual(sent, ['a', 'b', 'c'], 'every POST was sent in the same tick')
  await new Promise(r => setTimeout(r, 12))
  const live = store.getSnapshot().live
  assert.deepEqual(live.map(e => [e.phase, e.galleryJobId, e.cancellable]), [['queued', 'job-a', true], ['queued', 'job-b', true], ['queued', 'job-c', true]])
  const cancelled = []
  assert.equal(store.cancel(live[1].jobID, async (id) => { cancelled.push(id) }), true)
  assert.deepEqual(cancelled, ['job-b'])
  assert.equal(store.getSnapshot().live[1].state, 'cancelling')
  // The owner ends the cancelled one as failed with the code; it settles as cancelled.
  statuses.b = { phase: 'failed', failureCode: 'cancelled' }
  statuses.a = { phase: 'failed', failureCode: 'upgrade_failed' }
  statuses.c = { phase: 'failed', failureCode: 'cancelled' }
  const outs = await Promise.all(works)
  assert.deepEqual(outs.map(o => o.outcome), ['failed', 'cancelled', 'cancelled'])
  const snap = store.getSnapshot()
  assert.deepEqual(snap.records.map(r => [r.addonId, r.outcome]).sort(), [['b', 'cancelled'], ['c', 'cancelled']])
  assert.equal(snap.live.length, 1)
  assert.equal(snap.live[0].state, 'failed')
})

test('upgrade store: a refused cancel puts the upgrade back as running and cancellable', async () => {
  const store = createUpgradeStore(instant)
  let status = { phase: 'accepted', jobId: 'job-a' }
  const api = { updates: { upgrade: async () => status }, operationStatus: async () => status, installed: async () => [] }
  void store.start(api, { ...record, id: 'a' })
  await new Promise(r => setTimeout(r, 12))
  const jobID = store.getSnapshot().live[0].jobID
  store.cancel(jobID, async () => { throw new Error('refused') })
  await new Promise(r => setTimeout(r, 12))
  const e = store.getSnapshot().live[0]
  assert.deepEqual([e.state, e.cancellable], ['running', true])
})
