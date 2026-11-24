import test from 'node:test'
import assert from 'node:assert/strict'
import {
  QUEUE_CONFIRM, MAX_QUEUE_ITEMS, isAddonInstallOp, needsApproval, rowState, queueRows, busyAddonIds,
  queueBody, queueOutcome, batchStart, batchCurrent, batchDone, batchAccept, batchSkip, batchPosition,
} from '../../src/utils/addonQueue.js'
import { createHostGrantsApi } from '../../src/utils/hostGrantsApi.js'
import { createAddonsApi } from '../../src/utils/addonsApi.js'
import { normalizePreview, approvalBody } from '../../src/components/addons/hostGrantsState.js'
import { canRetryOp as isRetryable, detailOf, reasonOf } from '../../src/utils/opsActivity.js'

const row = (over = {}) => ({ id: 'addon:notes', name: 'notes', jobID: 'j1', isAddon: true, taskType: 'installation', isQueued: false, phase: 'downloading', progress: 30, cancellable: true, ...over })
const entry = (id, over = {}) => ({ key: `main/${id}/1.0.0`, id, sourceId: 'main', version: '1.0.0', ...over })

test('addonqueue: only the installs of the queue are told from model, backend and upgrade rows', () => {
  assert.equal(isAddonInstallOp(row()), true)
  assert.equal(isAddonInstallOp(row({ taskType: 'upgrade' })), false)
  assert.equal(isAddonInstallOp({ id: 'm', isBackend: false }), false)
  assert.equal(isAddonInstallOp({ id: 'b', isBackend: true }), false)
  assert.equal(isAddonInstallOp(null), false)
})

test('addonqueue: a failure that asks for a new approval is told from a plain error', () => {
  assert.equal(needsApproval(row({ error: 'the add-on needs your approval again: it asks for egress.check' })), true)
  assert.equal(needsApproval(row({ error: '  The add-on needs your approval again' })), true)
  assert.equal(needsApproval(row({ error: 'download failed' })), false)
  assert.equal(needsApproval(row()), false)
  assert.equal(needsApproval(row({ error: 'an error occurred', needsApproval: true })), true)
  assert.equal(rowState(row({ error: 'the add-on needs your approval again' })), 'review')
  assert.equal(rowState(row({ error: 'download failed' })), 'failed')
  assert.equal(rowState(row({ isQueued: true, phase: 'queued' })), 'queued')
  assert.equal(rowState(row()), 'running')
})

test('addonqueue: rows keep running first, then waiting ones in order, then failures', () => {
  const ops = [
    row({ jobID: 'f', name: 'f', error: 'boom' }),
    row({ jobID: 'q1', name: 'q1', isQueued: true }),
    { id: 'model', name: 'qwen', isAddon: false },
    row({ jobID: 'r', name: 'r' }),
    row({ jobID: 'q2', name: 'q2', isQueued: true }),
    row({ jobID: 'u', name: 'u', taskType: 'upgrade' }),
  ]
  assert.deepEqual(queueRows(ops).map(o => o.jobID), ['r', 'q1', 'q2', 'f'])
  assert.deepEqual(queueRows(undefined), [])
})

test('addonqueue: an add-on with an install waiting or running is busy, a failed one is not', () => {
  const ids = busyAddonIds([row({ name: 'a' }), row({ name: 'b', isQueued: true }), row({ name: 'c', error: 'x' }), { name: 'qwen', isAddon: false }])
  assert.deepEqual([...ids].sort(), ['a', 'b'])
})

test('addonqueue: the request carries each approval of its own preview and drops the action', () => {
  const preview = normalizePreview({ digest: 'sha256:aa', revision: 4, nonce: 'n1', ops: [{ name: 'egress.check', status: 'not_allowed' }], requested: { required: [], optional: ['egress.check'] } })
  const a = approvalBody(preview, ['egress.check'], { operationId: 'op1', action: 'install', sourceId: 'main', addonId: 'notes', version: '1.0.0' })
  const b = approvalBody(normalizePreview({ digest: 'unpinned', revision: 4, nonce: 'n2' }), [], { operationId: 'op2', action: 'install', sourceId: 'main', addonId: 'dev', version: '0.1.0' })
  const body = queueBody([a, b])
  assert.deepEqual(body, { items: [
    { operationId: 'op1', sourceId: 'main', addonId: 'notes', version: '1.0.0', approve: { digest: 'sha256:aa', ops: ['egress.check'], revision: 4, nonce: 'n1' } },
    { operationId: 'op2', sourceId: 'main', addonId: 'dev', version: '0.1.0', approve: { digest: 'unpinned', ops: [], revision: 4, nonce: 'n2' } },
  ] })
  assert.equal(queueBody(Array.from({ length: 40 }, (_, i) => ({ addonId: `a${i}` }))).items.length, MAX_QUEUE_ITEMS)
  assert.deepEqual(queueBody(undefined), { items: [] })
})

test('addonqueue: one refused item does not hide the others, and a missing answer counts as refused', () => {
  const sent = [{ addonId: 'a', operationId: 'o1' }, { addonId: 'b', operationId: 'o2' }, { addonId: 'c', operationId: 'o3' }]
  const out = queueOutcome(sent, { items: [
    { addonId: 'a', operationId: 'o1', jobId: 'job-a' },
    { addonId: 'b', error: { type: 'approval_stale', code: 409, message: 'changed' } },
  ] })
  assert.deepEqual(out.queued, [{ addonId: 'a', jobId: 'job-a' }])
  assert.deepEqual(out.refused.map(r => [r.addonId, r.error.type]), [['b', 'approval_stale'], ['c', 'no_answer']])
  assert.deepEqual(queueOutcome(sent, null).queued, [])
})

test('addonqueue: a batch is reviewed one add-on at a time, with accept and skip, and ends after the last', () => {
  let b = batchStart([entry('a'), entry('b'), entry('c'), entry('a')])
  assert.equal(b.entries.length, 3)
  assert.equal(batchCurrent(b).id, 'a')
  assert.deepEqual(batchPosition(b), { number: 1, total: 3, last: false })
  b = batchAccept(b, { addonId: 'a' })
  b = batchSkip(b)
  assert.equal(batchCurrent(b).id, 'c')
  assert.equal(batchPosition(b).last, true)
  assert.equal(batchDone(b), false)
  b = batchAccept(b, { addonId: 'c' })
  assert.equal(batchDone(b), true)
  assert.equal(batchCurrent(b), null)
  assert.deepEqual(b.accepted.map(a => a.addonId), ['a', 'c'])
  assert.equal(batchStart([]).entries.length, 0)
})

test('addonqueue: the queue call sends the queue confirm header and the body as given', async () => {
  const seen = []
  const fetch = async (url, init) => { seen.push({ url, init }); return { ok: true, status: 200, json: async () => ({ items: [{ addonId: 'a', jobId: 'j' }] }) } }
  const api = createHostGrantsApi(createAddonsApi({ fetch }))
  const body = { items: [{ operationId: 'o', sourceId: 'main', addonId: 'a', version: '1', approve: { digest: 'sha256:aa', ops: [], revision: 1, nonce: 'n' } }] }
  const out = await api.queue(body)
  assert.equal(seen[0].url.endsWith('/api/addons/host-grants/queue'), true)
  assert.equal(seen[0].init.method, 'POST')
  assert.equal(seen[0].init.headers['X-LocalAI-Confirm'], QUEUE_CONFIRM)
  assert.equal(QUEUE_CONFIRM, 'queue')
  assert.deepEqual(JSON.parse(seen[0].init.body), body)
  assert.equal(out.items[0].jobId, 'j')
})

test('addonqueue: a preview from the library index says so and keeps an unpinned digest as sent', () => {
  const p = normalizePreview({ digest: 'unpinned', fromIndex: true, revision: 2, nonce: 'n' })
  assert.equal(p.fromIndex, true)
  assert.equal(p.digest, 'unpinned')
  assert.equal(normalizePreview({ digest: 'sha256:aa' }).fromIndex, false)
  const body = approvalBody(p, [], { operationId: 'o', sourceId: 'main', addonId: 'dev', version: '0.1.0' })
  assert.equal(body.approve.digest, 'unpinned')
})

test('addonqueue: the global lists leave retry of an add-on install to the Add-ons page and word the review', () => {
  assert.equal(isRetryable(row({ error: 'download failed' })), false)
  assert.equal(isRetryable({ id: 'm', error: 'x', isAddon: false }), true)
  assert.equal(isRetryable({ id: 'u', error: 'x', isAddon: true, taskType: 'upgrade', jobID: 'addon:u:1' }), true)
  const t = (key) => key
  assert.equal(reasonOf(row({ error: 'the add-on needs your approval again: more' }), t), 'activity.addonNeedsReview')
  assert.equal(reasonOf(row({ error: 'download failed' }), t), 'download failed')
  assert.match(detailOf(row({ phase: 'downloading', currentBytes: 5 * 1048576, totalBytes: 40 * 1048576 }), t), /activity\.addonStage\.downloading/)
})
