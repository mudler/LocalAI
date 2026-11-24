import test from 'node:test'
import assert from 'node:assert/strict'
import { createUpgradeStore } from '../../src/utils/addonUpgrades.js'
import { approvalRequest } from '../../src/components/addons/approvalRequest.js'
import { queueBody } from '../../src/utils/addonQueue.js'
import { upgradeFailureWords } from '../../src/components/addons/updateReview.js'
import { updateErrorKey } from '../../src/components/addons/updateState.js'

// An approved upgrade is the same operation as a plain upgrade: the approval
// is queued on the host, the job shows in the operations list, and the upgrade
// store follows it, so a row can show queued, running and the result, and
// Cancel stops it.

const addon = { id: 'notes', title: 'Notes', version: '1.2.0', generation: 1, pendingUpgrade: { version: '1.3.0', delta: ['workspace.export'] } }
const body = { action: 'upgrade', sourceId: 'main', addonId: 'notes', version: '1.3.0', approve: { digest: 'sha256:x', ops: ['workspace.export'], revision: 7, nonce: 'n1' } }
const instant = { sleep: () => new Promise(r => setTimeout(r, 4)), waits: Array.from({ length: 500 }, () => 0) }
const tick = (ms = 14) => new Promise(r => setTimeout(r, ms))

function rig() {
  const sent = []
  const statuses = { current: { phase: 'accepted', jobId: 'job-notes' } }
  let installed = [{ id: 'notes', version: '1.2.0', generation: 1 }]
  const queue = async (request) => {
    sent.push(request)
    return { items: request.items.map(i => ({ addonId: i.addonId, operationId: i.operationId, jobId: 'job-notes' })) }
  }
  const api = { operationStatus: async () => statuses.current, installed: async () => installed }
  return { sent, statuses, api, queue, setInstalled: (list) => { installed = list } }
}

test('the queue request of an approval carries the upgrade action and nothing a plain install sends', () => {
  const out = queueBody([{ ...body, operationId: 'op1' }])
  assert.equal(out.items[0].action, 'upgrade')
  assert.deepEqual(Object.keys(out.items[0]).sort(), ['action', 'addonId', 'approve', 'operationId', 'sourceId', 'version'])
  // An install keeps its old shape: the host takes an install when no action is given.
  const install = queueBody([{ ...body, action: 'install', operationId: 'op2' }])
  assert.equal('action' in install.items[0], false)
})

test('an approval is queued once and shows as queued with a job to cancel, then completes', async () => {
  const r = rig()
  const store = createUpgradeStore(instant)
  const work = store.startApproved(r.api, addon, approvalRequest(addon, body, r.queue))
  await tick()
  assert.equal(r.sent.length, 1)
  assert.equal(r.sent[0].items[0].action, 'upgrade')
  const [entry] = store.getSnapshot().live
  assert.deepEqual([entry.kind, entry.taskType, entry.phase, entry.galleryJobId, entry.cancellable, entry.toVersion], ['approval', 'upgrade', 'queued', 'job-notes', true, '1.3.0'])
  // The owner runs it, then it completes and the installed version is the approved one.
  r.statuses.current = { phase: 'running', stage: 'downloading' }
  await tick()
  assert.equal(store.getSnapshot().live[0].stage, 'downloading')
  r.statuses.current = { phase: 'completed' }
  r.setInstalled([{ id: 'notes', version: '1.3.0', generation: 2 }])
  const out = await work
  assert.equal(out.ok, true)
  const snap = store.getSnapshot()
  assert.equal(snap.live.length, 0)
  assert.deepEqual(snap.records.map(x => [x.addonId, x.outcome]), [['notes', 'completed']])
  assert.equal(r.sent.length, 1, 'nothing was sent a second time')
})

test('a second approval of an add-on that has an operation is not sent', async () => {
  const r = rig()
  const store = createUpgradeStore(instant)
  void store.startApproved(r.api, addon, approvalRequest(addon, body, r.queue))
  await tick()
  const again = await store.startApproved(r.api, addon, approvalRequest(addon, body, r.queue))
  assert.deepEqual([again.ok, again.outcome], [false, 'busy'])
  assert.equal(r.sent.length, 1)
})

test('cancel stops the queued approval on the server, and no approval or operation id is held afterwards', async () => {
  const r = rig()
  const store = createUpgradeStore(instant)
  const work = store.startApproved(r.api, addon, approvalRequest(addon, body, r.queue))
  await tick()
  const cancelled = []
  assert.equal(store.cancel(store.getSnapshot().live[0].jobID, async (id) => { cancelled.push(id) }), true)
  assert.deepEqual(cancelled, ['job-notes'])
  assert.equal(store.getSnapshot().live[0].state, 'cancelling')
  r.statuses.current = { phase: 'failed', failureCode: 'cancelled' }
  const out = await work
  assert.deepEqual([out.ok, out.outcome], [false, 'cancelled'])
  const snap = store.getSnapshot()
  assert.equal(snap.live.length, 0, 'the row is back to its previous state')
  assert.deepEqual(snap.records.map(x => [x.addonId, x.outcome]), [['notes', 'cancelled']])
  // A new approval of the same add-on is a new operation, not the held one.
  const first = r.sent[0].items[0].operationId
  void store.startApproved(r.api, addon, approvalRequest(addon, { ...body, approve: { ...body.approve, nonce: 'n2' } }, r.queue))
  await tick()
  assert.equal(r.sent.length, 2)
  assert.notEqual(r.sent[1].items[0].operationId, first)
})

test('a running approval that is stopped after our cancel ends as cancelled, not as a failure', async () => {
  const r = rig()
  const store = createUpgradeStore(instant)
  const work = store.startApproved(r.api, addon, approvalRequest(addon, body, r.queue))
  await tick()
  r.statuses.current = { phase: 'running', stage: 'downloading' }
  await tick()
  assert.equal(store.getSnapshot().live[0].cancellable, true)
  store.cancel(store.getSnapshot().live[0].jobID, async () => {})
  r.statuses.current = { phase: 'failed', failureCode: 'interrupted' }
  assert.equal((await work).outcome, 'cancelled')
})

test('a refusal of the host in the answer of the queue is the failure of the row, in its own words', async () => {
  const r = rig()
  const store = createUpgradeStore(instant)
  const refuse = async (request) => ({ items: request.items.map(i => ({ addonId: i.addonId, operationId: i.operationId, error: { type: 'approval_stale', code: 409, message: 'stale' } })) })
  const out = await store.startApproved(r.api, addon, approvalRequest(addon, body, refuse))
  assert.deepEqual([out.ok, out.message], [false, 'approval_stale'])
  const [failed] = store.getSnapshot().live
  assert.deepEqual([failed.state, failed.errorKey], ['failed', 'approval_stale'])
  const words = upgradeFailureWords('approval_stale', (key) => key)
  assert.equal(words, 'hostAccess.errors.approval_stale')
  assert.equal(updateErrorKey({ type: 'blocked_by_policy' }), 'blocked_by_policy')
  // The approval is spent: a retry is a new review, never the old approval again.
  const retried = await store.retry(r.api, failed.jobID)
  assert.deepEqual([retried.ok, retried.outcome], [false, 'review'])
  assert.equal(r.sent.length, 0)
})

test('an approved upgrade that fails on the queue keeps the reason the host gave', async () => {
  const r = rig()
  const store = createUpgradeStore(instant)
  const work = store.startApproved(r.api, addon, approvalRequest(addon, body, r.queue))
  await tick()
  r.statuses.current = { phase: 'failed', failureCode: 'approval_required' }
  const out = await work
  assert.deepEqual([out.ok, out.message], [false, 'approval_required'])
  assert.equal(store.getSnapshot().live[0].errorKey, 'approval_required')
})

test('a lost answer keeps the operation id of the same approval for the retry', async () => {
  const r = rig()
  const store = createUpgradeStore({ ...instant, waits: [] })
  let n = 0
  const flaky = async (request) => { n += 1; if (n === 1) throw Object.assign(new Error('down'), { kind: 'network' }); return r.queue(request) }
  const seen = []
  const queue = async (request, signal) => { seen.push(request.items[0].operationId); return flaky(request, signal) }
  await store.startApproved(r.api, addon, approvalRequest(addon, body, queue))
  void store.startApproved(r.api, addon, approvalRequest(addon, body, queue))
  await tick()
  assert.equal(seen.length, 2)
  assert.equal(seen[0], seen[1])
})
