import test from 'node:test'
import assert from 'node:assert/strict'
import { addRowsPlace, canShare } from '../src/lib/layout.js'

test('layout: Share is offered only where the installation has named accounts', () => {
  assert.equal(canShare({ sharing: true }), true)
  assert.equal(canShare({ sharing: false }), false)
  assert.equal(canShare({}), false)
  assert.equal(canShare(null), false)
})

test('layout: a table with no rows offers Add rows once, in its empty state', () => {
  const base = { canWrite: true, columnCount: 2, loaded: true, rowCount: 0, filtered: false }
  assert.equal(addRowsPlace(base), 'empty')
  assert.equal(addRowsPlace({ ...base, rowCount: 3 }), 'toolbar')
  assert.equal(addRowsPlace({ ...base, filtered: true }), 'toolbar')
  assert.equal(addRowsPlace({ ...base, loaded: false }), 'toolbar')
})

test('layout: a reader, or a table with no columns, is offered no Add rows', () => {
  assert.equal(addRowsPlace({ canWrite: false, columnCount: 2, loaded: true, rowCount: 0, filtered: false }), 'none')
  assert.equal(addRowsPlace({ canWrite: true, columnCount: 0, loaded: true, rowCount: 0, filtered: false }), 'none')
})
