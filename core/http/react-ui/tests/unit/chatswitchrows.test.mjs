import { test } from 'node:test'
import assert from 'node:assert/strict'
import { bulkIds, bulkable, offWith, rowOn, switchCounts, withOn } from '../../src/utils/chatSwitchRows.js'

const rows = [
  { id: 'web', on: true },
  { id: 'saved_agents', on: false, optIn: true },
  { id: 'crm', on: false },
  { id: 'manual', on: true, usable: false },
]

test('a row that cannot be used shows off', () => {
  assert.equal(rowOn(rows[0]), true)
  assert.equal(rowOn(rows[3]), false)
})

test('the counts leave out what cannot be used', () => {
  assert.deepEqual(switchCounts(rows), { on: 1, total: 3 })
  assert.equal(bulkable(rows), 3)
})

test('Select all skips opt-in rows and rows that cannot be used', () => {
  assert.deepEqual(bulkIds(rows, true), ['crm'])
})

test('Disable all switches opt-in rows off but leaves rows that cannot be used', () => {
  assert.deepEqual(bulkIds([...rows.slice(0, 1), { ...rows[1], on: true }, ...rows.slice(2)], false), ['web', 'saved_agents'])
})

test('withOn changes only the items named', () => {
  const items = [{ id: 'a', off: false }, { id: 'b', off: false }]
  assert.deepEqual(withOn(items, (x) => x.id, new Set(['b']), false), [{ id: 'a', off: false }, { id: 'b', off: true }])
})

test('offWith adds the keys in order and removes them on', () => {
  assert.deepEqual(offWith(['x'], ['a', 'b'], false), ['x', 'a', 'b'])
  assert.deepEqual(offWith(['x', 'a', 'b'], ['a', 'x'], true), ['b'])
  assert.deepEqual(offWith(['a'], ['a'], false), ['a'])
})
