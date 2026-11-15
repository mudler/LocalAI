import test from 'node:test'
import assert from 'node:assert/strict'
import { confirmGone, followJob, runAddonRow, runGalleryRow, runLibraryRow } from '../../src/utils/updateRun.js'

// A clock that only moves when the code sleeps, so the tests are instant.
function clock() {
  let t = 0
  return { now: () => t, sleep: async (ms) => { t += ms } }
}
const snap = (listed, extra = {}) => ({
  throttled: false,
  sources: { gallery: { state: 'ok' }, library: { state: 'ok' } },
  items: listed ? [{ id: 'gallery:llama-cpp' }] : [],
  ...extra,
})
const row = { id: 'gallery:llama-cpp', kind: 'gallery', name: 'llama-cpp' }

test('run: starting an update is not finishing it, the row waits for the operation to leave the list', async () => {
  const c = clock()
  let polls = 0
  const events = []
  const result = await runGalleryRow({
    row,
    start: async () => { events.push('start'); return 'job1' },
    // Listed for three polls, then gone.
    getOperations: () => { polls += 1; return polls <= 3 ? [{ jobID: 'job1', progress: 10 }] : [] },
    recheck: async () => { events.push('recheck'); return snap(false) },
    ...c,
  })
  assert.deepEqual(result, { ok: true })
  assert.deepEqual(events, ['start', 'recheck'], 'the check runs after the end of the operation, not at dispatch')
  assert.ok(polls >= 4)
})

test('run: an operation that failed is a failed update and no check is asked', async () => {
  const c = clock()
  let rechecks = 0
  const result = await runGalleryRow({
    row,
    start: async () => 'job1',
    getOperations: () => [{ jobID: 'job1', error: 'download interrupted' }],
    recheck: async () => { rechecks += 1; return snap(false) },
    ...c,
  })
  assert.deepEqual(result, { ok: false, code: 'failed', error: 'download interrupted' })
  assert.equal(rechecks, 0)
})

test('run: an operation that ended with the item still listed did not change the version', async () => {
  const c = clock()
  const result = await runGalleryRow({ row, start: async () => 'j', getOperations: () => [], recheck: async () => snap(true), ...c })
  assert.deepEqual(result, { ok: false, code: 'unchanged' })
})

test('run: a throttled answer that still lists the item is asked again, and a later answer decides', async () => {
  const c = clock()
  const answers = [snap(true, { throttled: true }), snap(true, { throttled: true }), snap(false)]
  let calls = 0
  const result = await runGalleryRow({ row, start: async () => 'j', getOperations: () => [], recheck: async () => answers[calls++], ...c })
  assert.deepEqual(result, { ok: true })
  assert.equal(calls, 3)
})

test('run: a throttled answer all the way is unchanged, not done', async () => {
  const c = clock()
  let calls = 0
  const result = await runGalleryRow({ row, start: async () => 'j', getOperations: () => [], recheck: async () => { calls += 1; return snap(true, { throttled: true }) }, maxTries: 3, ...c })
  assert.deepEqual(result, { ok: false, code: 'unchanged' })
  assert.equal(calls, 4)
})

test('run: when the source cannot answer after the update, the update is unconfirmed, never done', async () => {
  const c = clock()
  const failedSource = snap(false, { sources: { gallery: { state: 'failed' }, library: { state: 'ok' } } })
  assert.deepEqual(await runGalleryRow({ row, start: async () => 'j', getOperations: () => [], recheck: async () => failedSource, ...c }), { ok: false, code: 'unconfirmed' })
  assert.deepEqual(await runGalleryRow({ row, start: async () => 'j', getOperations: () => [], recheck: async () => { throw new Error('down') }, ...c }), { ok: false, code: 'unconfirmed' })
})

test('run: a start that the server refused is a start failure with its words', async () => {
  const c = clock()
  const result = await runGalleryRow({ row, start: async () => { throw new Error('backend_install_disabled') }, getOperations: () => [], recheck: async () => snap(false), ...c })
  assert.deepEqual(result, { ok: false, code: 'start', error: 'backend_install_disabled' })
})

test('follow: an operation that is never listed ends after the grace time, one that never ends times out', async () => {
  const c = clock()
  const out = await followJob({ jobID: 'x', getOperations: () => [], graceMs: 2000, pollMs: 500, ...c })
  assert.equal(out.verdict, 'ended')
  assert.ok(c.now() >= 2000)
  const c2 = clock()
  const out2 = await followJob({ jobID: 'x', getOperations: () => [{ jobID: 'x' }], pollMs: 1000, timeoutMs: 5000, ...c2 })
  assert.deepEqual(out2, { verdict: 'failed', error: 'timeout' })
})

test('follow: an abort stops the wait', async () => {
  const ctl = new AbortController()
  const sleep = async () => { ctl.abort(); throw new DOMException('Aborted', 'AbortError') }
  await assert.rejects(followJob({ jobID: 'x', getOperations: () => [{ jobID: 'x' }], sleep, signal: ctl.signal }), { name: 'AbortError' })
})

test('confirm: the fresh check is the only thing that says done', async () => {
  const c = clock()
  assert.equal(await confirmGone({ row, recheck: async () => snap(false), ...c }), 'done')
  assert.equal(await confirmGone({ row, recheck: async () => snap(true), ...c }), 'unchanged')
})

test('library: the install job ending is not enough, the check must stop listing it', async () => {
  const c = clock()
  const libRow = { id: 'library:official/piper', kind: 'library', name: 'piper' }
  const listed = { throttled: false, sources: { gallery: { state: 'ok' }, library: { state: 'ok' } }, items: [{ id: libRow.id }] }
  const gone = { ...listed, items: [] }
  assert.deepEqual(await runLibraryRow({ row: libRow, install: async () => ({}), recheck: async () => gone, ...c }), { ok: true })
  assert.deepEqual(await runLibraryRow({ row: libRow, install: async () => ({}), recheck: async () => listed, ...c }), { ok: false, code: 'unchanged' })
  assert.deepEqual(await runLibraryRow({ row: libRow, install: async () => { throw new Error('digest mismatch') }, recheck: async () => gone, ...c }), { ok: false, code: 'failed', error: 'digest mismatch' })
})

test('add-on: done when the owner ended without error and the record no longer lists the update', async () => {
  const addonRow = { id: 'addon:notes', addonId: 'notes', to: 'v1.1.0', record: { id: 'notes' } }
  const stillThere = [{ id: 'notes', update: { state: 'update-available', version: '1.1.0', digest: 'sha256:bb', available: true } }]
  const moved = [{ id: 'notes', update: { state: 'up-to-date' } }]
  assert.deepEqual(await runAddonRow({ row: addonRow, upgrade: async () => ({ ok: true }), reread: async () => moved }), { ok: true })
  assert.deepEqual(await runAddonRow({ row: addonRow, upgrade: async () => ({ ok: true }), reread: async () => stillThere }), { ok: false, code: 'unchanged' })
  assert.deepEqual(await runAddonRow({ row: addonRow, upgrade: async () => ({ ok: true }), reread: async () => { throw new Error('x') } }), { ok: false, code: 'unconfirmed' })
  assert.deepEqual(await runAddonRow({ row: addonRow, upgrade: async () => ({ ok: false, message: 'upgrade_failed' }), reread: async () => moved }), { ok: false, code: 'failed', error: 'upgrade_failed', errorKey: 'upgrade_failed' })
  assert.equal((await runAddonRow({ row: addonRow, upgrade: async () => null, reread: async () => moved })).ok, false)
})
