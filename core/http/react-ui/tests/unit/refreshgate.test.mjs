import test from 'node:test'
import assert from 'node:assert/strict'
import { newGate, startFetch, requestRefresh, finishFetch } from '../../src/utils/refreshGate.js'

test('refresh gate: a request while idle runs at once', () => {
  const r = requestRefresh(newGate())
  assert.equal(r.runNow, true)
})

test('refresh gate: a request during a fetch waits, then one more fetch runs when it ends', () => {
  let g = newGate()
  const a = startFetch(g); g = a.gate
  const r = requestRefresh(g); g = r.gate
  assert.equal(r.runNow, false)
  const end = finishFetch(g, a.token); g = end.gate
  assert.equal(end.rerun, true)
  // The follow-up fetch is a new one; after it ends nothing more is owed.
  const b = startFetch(g); g = b.gate
  const end2 = finishFetch(g, b.token)
  assert.equal(end2.rerun, false)
})

test('refresh gate: several requests during one fetch still give one more fetch', () => {
  let g = newGate()
  const a = startFetch(g); g = a.gate
  g = requestRefresh(g).gate
  g = requestRefresh(g).gate
  const end = finishFetch(g, a.token)
  assert.equal(end.rerun, true)
  assert.equal(end.gate.pending, false)
})

test('refresh gate: only the newest response is applied', () => {
  let g = newGate()
  const older = startFetch(g); g = older.gate
  const newer = startFetch(g); g = newer.gate
  // The newer response arrives first, then the older one.
  const first = finishFetch(g, newer.token); g = first.gate
  assert.equal(first.apply, true)
  const second = finishFetch(g, older.token)
  assert.equal(second.apply, false)
})

test('refresh gate: an older response ending first is not applied and does not end loading', () => {
  let g = newGate()
  const older = startFetch(g); g = older.gate
  const newer = startFetch(g); g = newer.gate
  const first = finishFetch(g, older.token); g = first.gate
  assert.equal(first.apply, false)
  assert.equal(first.idle, false)
  const second = finishFetch(g, newer.token)
  assert.equal(second.apply, true)
  assert.equal(second.idle, true)
})

test('refresh gate: a request waits while any fetch of several still runs', () => {
  let g = newGate()
  const a = startFetch(g); g = a.gate
  const b = startFetch(g); g = b.gate
  g = requestRefresh(g).gate
  const e1 = finishFetch(g, a.token); g = e1.gate
  assert.equal(e1.rerun, false)
  const e2 = finishFetch(g, b.token)
  assert.equal(e2.rerun, true)
})
