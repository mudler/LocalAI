// SPDX-License-Identifier: MIT
// The rules behind the long lists of the Knowledge and Skills pages: search,
// order, runs of one group and how many rows to draw.
import test from 'node:test'
import assert from 'node:assert/strict'
import { matchesQuery, sortItems, runsOf, rowLimit } from '../../src/utils/libraryList.js'

const get = (x) => x

test('library list: every word of a search must match, in any order, in any field', () => {
  assert.equal(matchesQuery('', 'a'), true)
  assert.equal(matchesQuery('  ', 'a'), true)
  assert.equal(matchesQuery('pdf read', 'pdf', 'Read and create PDFs'), true)
  assert.equal(matchesQuery('create pdf', 'pdf', 'Read and create PDFs'), true)
  assert.equal(matchesQuery('xlsx', 'pdf', 'Read and create PDFs'), false)
  assert.equal(matchesQuery('pdf', undefined, null), false)
})

test('library list: names order the way a person counts, and ties end on the name', () => {
  const items = [
    { name: 'report-10', used: 0, group: 'b' }, { name: 'report-2', used: 2, group: 'a' },
    { name: 'alpha', used: 2, group: 'b' }, { name: 'zeta', used: 0, group: 'a' },
  ]
  const names = (mode, groups) => sortItems(items, mode, get, groups).map(i => i.name)
  assert.deepEqual(names('name'), ['alpha', 'report-2', 'report-10', 'zeta'])
  assert.deepEqual(names('name-desc'), ['zeta', 'report-10', 'report-2', 'alpha'])
  assert.deepEqual(names('used'), ['alpha', 'report-2', 'report-10', 'zeta'])
  assert.deepEqual(names('unused'), ['report-10', 'zeta', 'alpha', 'report-2'])
  assert.deepEqual(names('source', ['b', 'a']), ['alpha', 'report-10', 'report-2', 'zeta'])
  assert.deepEqual(names('something-old'), names('name'), 'an unknown stored order falls back to the name')
  assert.equal(items[0].name, 'report-10', 'the input is not changed')
})

test('library list: runs of one group, for the headings', () => {
  const runs = runsOf([{ g: 'a' }, { g: 'a' }, { g: 'b' }, { g: 'a' }], x => x.g)
  assert.deepEqual(runs.map(r => [r.group, r.items.length]), [['a', 2], ['b', 1], ['a', 1]])
  assert.deepEqual(runsOf([], x => x.g), [])
})

test('library list: a page of rows, widened so the open row is never cut off', () => {
  assert.equal(rowLimit(60, 200), 60)
  assert.equal(rowLimit(60, 20), 20)
  assert.equal(rowLimit(60, 200, 150), 151)
  assert.equal(rowLimit(60, 200, 10), 60)
  assert.equal(rowLimit(60, 200, -1), 60)
})
