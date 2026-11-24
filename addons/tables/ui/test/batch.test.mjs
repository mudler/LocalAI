import test from 'node:test'
import assert from 'node:assert/strict'
import { planInsert, insertSteps, idSteps, chunk, toggle, setAll, allSelected, MAX_INSERT_ROWS, MAX_ROW_IDS } from '../src/lib/batch.js'
import { describeError } from '../src/lib/errors.js'

const columns = [
  { name: 'Item', type: 'text', required: true },
  { name: 'Qty', type: 'integer', required: false },
  { name: 'Note', type: 'text', required: false },
  { name: 'Paid', type: 'boolean', required: false },
]
const cell = (text, isNull = false) => ({ text, isNull })

test('batch: typed rows become wire rows and an untouched optional column is left to its default', () => {
  const plan = planInsert([
    { Item: cell('pen'), Qty: cell('3'), Note: cell(''), Paid: cell('') },
    { Item: cell('ink'), Qty: cell('', true), Note: cell('', true), Paid: cell('true') },
  ], columns)
  assert.deepEqual(plan.errors, {})
  assert.deepEqual(plan.rows, [{ Item: 'pen', Qty: 3 }, { Item: 'ink', Qty: null, Note: null, Paid: true }])
})

test('batch: every cell error is named by row and column and nothing is sent', () => {
  const plan = planInsert([{ Item: cell(''), Qty: cell('x') }, { Item: cell('ok'), Qty: cell('1.5') }], columns)
  assert.deepEqual(plan.errors, { '0:Item': 'required', '0:Qty': 'integer', '1:Qty': 'integer' })
})

test('batch: steps stay inside the add-on bounds', () => {
  const rows = Array.from({ length: 250 }, (_, i) => i)
  assert.deepEqual(insertSteps(rows).map(s => s.length), [100, 100, 50])
  const ids = Array.from({ length: 2500 }, (_, i) => i + 1)
  assert.deepEqual(idSteps(ids).map(s => s.length), [1000, 1000, 500])
  assert.equal(MAX_INSERT_ROWS, 100)
  assert.equal(MAX_ROW_IDS, 1000)
  assert.deepEqual(chunk([], 3), [])
})

test('batch: the selection toggles by row identifier and survives paging', () => {
  let s = new Set()
  s = toggle(s, 4)
  s = toggle(s, 9)
  s = toggle(s, 4)
  assert.deepEqual([...s], [9])
  s = setAll(s, [1, 2, 3], true)
  assert.equal(allSelected(s, [1, 2, 3]), true)
  assert.equal(allSelected(s, [1, 2, 3, 4]), false)
  assert.equal(allSelected(s, []), false)
  s = setAll(s, [1, 2, 3], false)
  assert.deepEqual([...s], [9])
})

test('errors: each answer of the host gets its own words and recovery', () => {
  assert.equal(describeError({ kind: 'stale' }).kind, 'stale')
  assert.equal(describeError({ kind: 'denied', type: 'addon_error' }).kind, 'denied')
  assert.equal(describeError({ kind: 'denied', type: 'quota_reached' }).kind, 'quota')
  assert.equal(describeError({ kind: 'denied', type: 'outcome_blocked' }).kind, 'blocked')
  assert.equal(describeError({ kind: 'unavailable' }).kind, 'unavailable')
  const unknown = describeError({ kind: 'unknown_outcome', operationId: 'op-3' })
  assert.deepEqual([unknown.kind, unknown.retry, unknown.operationId], ['unknown', true, 'op-3'])
  assert.equal(describeError({ kind: 'sharing_disabled' }).kind, 'sharing')
  assert.equal(describeError({ code: 'withdrawn' }).kind, 'withdrawn')
  assert.equal(describeError({ name: 'AbortError' }).kind, 'withdrawn')
  assert.equal(describeError(null).kind, 'error')
  assert.equal(describeError({ kind: 'invalid' }).retry, false)
})
