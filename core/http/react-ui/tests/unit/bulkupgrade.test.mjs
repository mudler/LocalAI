import test from 'node:test'
import assert from 'node:assert/strict'
import {
  initialSelection, toggleItem, selectAllItems, beginRun, summarize, failedIds, runSequential, canStart,
} from '../../src/utils/bulkUpgrade.js'

const items = [
  { id: 'a', name: 'A', from: '1', to: '2', startable: true },
  { id: 'b', name: 'B', from: '1', to: '2', startable: false, reason: 'needs-check' },
  { id: 'c', name: 'C', from: '1', to: '2', startable: true },
  { id: 'd', name: 'D', from: '1', to: '2', startable: true },
]

test('bulk upgrade: only startable items are selected or selectable', () => {
  assert.deepEqual([...initialSelection(items)], ['a', 'c', 'd'])
  assert.deepEqual([...toggleItem(new Set(['a']), items, 'b')], ['a'], 'a blocked item never joins')
  assert.deepEqual([...toggleItem(new Set(['a']), items, 'c')].sort(), ['a', 'c'])
  assert.deepEqual([...toggleItem(new Set(['a', 'c']), items, 'a')], ['c'])
  assert.deepEqual([...selectAllItems(items)], ['a', 'c', 'd'])
})

test('bulk upgrade: a run needs a selection and never starts twice', () => {
  assert.equal(beginRun(null, items, new Set()), null)
  assert.equal(canStart(null, items, new Set(['b'])), false, 'a selection of blocked items is empty')
  const run = beginRun(null, items, new Set(['a', 'c', 'b']))
  assert.deepEqual(run.order, ['a', 'c'], 'items run in list order, blocked ones dropped')
  assert.equal(run.phase, 'running')
  assert.equal(run.status.a.phase, 'queued')
  assert.equal(beginRun(run, items, new Set(['d'])), null, 'a second start while running is refused')
})

test('bulk upgrade: items run one after another and a failure does not stop the rest', async () => {
  const run = beginRun(null, items, new Set(['a', 'c', 'd']))
  const calls = []
  let active = 0
  let overlap = false
  const seen = []
  const final = await runSequential(run, async (item) => {
    active += 1
    if (active > 1) overlap = true
    calls.push(item.id)
    await new Promise(r => setTimeout(r, 2))
    active -= 1
    if (item.id === 'c') return { ok: false, message: 'candidate_changed' }
    return { ok: true }
  }, { onUpdate: (r) => seen.push(r.status.c && r.status.c.phase) })
  assert.deepEqual(calls, ['a', 'c', 'd'])
  assert.equal(overlap, false)
  assert.equal(final.phase, 'finished')
  assert.deepEqual(summarize(final), { total: 3, done: 2, failed: 1, skipped: 0, queued: 0, upgrading: 0 })
  assert.deepEqual(failedIds(final), ['c'])
  assert.equal(final.status.c.message, 'candidate_changed')
  assert.ok(seen.includes('upgrading'))
})

test('bulk upgrade: a thrown error is a failed item, an abort skips the rest', async () => {
  const run = beginRun(null, items, new Set(['a', 'c', 'd']))
  const ctl = new AbortController()
  const final = await runSequential(run, async (item) => {
    if (item.id === 'a') throw new Error('boom')
    ctl.abort()
    return { ok: true }
  }, { signal: ctl.signal })
  assert.equal(final.status.a.phase, 'failed')
  assert.equal(final.status.a.message, 'boom')
  assert.equal(final.status.c.phase, 'done')
  assert.equal(final.status.d.phase, 'skipped')
  assert.equal(final.phase, 'finished')
})

test('bulk upgrade: a retry runs only the failed items and may start after the run finished', async () => {
  let run = beginRun(null, items, new Set(['a', 'c']))
  run = await runSequential(run, async (item) => (item.id === 'a' ? { ok: false, message: 'x' } : { ok: true }))
  const again = beginRun(run, items, new Set(failedIds(run)))
  assert.deepEqual(again.order, ['a'])
  assert.equal(again.status.c, undefined, 'the retry starts a fresh run')
})
