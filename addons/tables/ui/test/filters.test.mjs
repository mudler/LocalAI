import test from 'node:test'
import assert from 'node:assert/strict'
import { buildFilter, operators, columnType, blankCondition, MAX_NODES } from '../src/lib/filters.js'
import { buildQuery, newPager, pagerNext, pagerPrev, pagerCursor, flipSort } from '../src/lib/query.js'

const columns = [
  { name: 'Item', type: 'text' }, { name: 'Qty', type: 'integer' }, { name: 'Price', type: 'real' },
  { name: 'Paid', type: 'boolean' }, { name: 'Due', type: 'datetime' },
]

test('filters: operators follow the column type', () => {
  assert.ok(operators('text').includes('contains'))
  assert.ok(!operators('integer').includes('contains'))
  assert.deepEqual(operators('boolean'), ['eq', 'ne', 'is-null', 'is-not-null'])
  assert.deepEqual(operators('blob'), [])
  assert.equal(columnType(columns, '_id'), 'integer')
  assert.equal(columnType(columns, 'Nope'), '')
  assert.deepEqual(blankCondition(columns), { column: 'Item', op: 'eq', value: '' })
})

test('filters: a leaf, a list and the null tests are built from typed values', () => {
  assert.deepEqual(buildFilter([{ column: 'Qty', op: 'gte', value: '2' }], columns), { filter: { column: 'Qty', op: 'gte', value: 2 }, errors: {} })
  const both = buildFilter([{ column: 'Item', op: 'starts-with', value: 'pen' }, { column: 'Paid', op: 'eq', value: 'true' }], columns, 'and')
  assert.deepEqual(both.filter, { and: [{ column: 'Item', op: 'starts-with', value: 'pen' }, { column: 'Paid', op: 'eq', value: true }] })
  const either = buildFilter([{ column: 'Item', op: 'is-null' }, { column: 'Qty', op: 'in', value: '1, 2,3' }], columns, 'or')
  assert.deepEqual(either.filter, { or: [{ column: 'Item', op: 'is-null' }, { column: 'Qty', op: 'in', values: [1, 2, 3] }] })
  assert.deepEqual(buildFilter([], columns), { filter: null, errors: {} })
  assert.deepEqual(buildFilter([{ column: '_id', op: 'eq', value: '7' }], columns).filter, { column: '_id', op: 'eq', value: 7 })
})

test('filters: a condition that cannot be sent is an error, never dropped', () => {
  const errs = (c) => buildFilter([c], columns).errors[0]
  assert.equal(errs({ column: 'Qty', op: 'eq', value: '' }), 'value')
  assert.equal(errs({ column: 'Qty', op: 'eq', value: 'x' }), 'integer')
  assert.equal(errs({ column: 'Qty', op: 'contains', value: '1' }), 'operator')
  assert.equal(errs({ column: 'Ghost', op: 'eq', value: '1' }), 'column')
  assert.equal(errs({ column: 'Due', op: 'gt', value: 'later' }), 'datetime')
  assert.equal(errs({ column: 'Qty', op: 'in', value: ' , ' }), 'value')
  assert.equal(errs({ column: 'Item', op: 'eq', value: 'x'.repeat(1025) }), 'size')
  assert.equal(errs({ column: 'Qty', op: 'in', value: Array.from({ length: 101 }, (_, i) => i).join(',') }), 'too-many')
  const many = Array.from({ length: MAX_NODES + 1 }, () => ({ column: 'Qty', op: 'is-null' }))
  assert.equal(buildFilter(many, columns).errors[0], 'too-many')
  // Empty text is a real value for a text column.
  assert.deepEqual(buildFilter([{ column: 'Item', op: 'eq', value: '' }], columns).filter, { column: 'Item', op: 'eq', value: '' })
})

test('query: the arguments are the tool schema and nothing else', () => {
  const q = buildQuery({ table: 'Orders', conditions: [{ column: 'Qty', op: 'gt', value: '1' }], sort: [{ column: 'Qty', desc: true }, { column: 'Qty', desc: false }, { column: 'Item' }], limit: 100, cursor: 'abc' }, columns)
  assert.deepEqual(q.arguments, { table: 'Orders', filter: { column: 'Qty', op: 'gt', value: 1 }, sort: [{ column: 'Qty', desc: true }, { column: 'Item', desc: false }], limit: 100, cursor: 'abc' })
  assert.deepEqual(buildQuery({ table: 'T', limit: 7 }, columns).arguments, { table: 'T', limit: 50 })
  const five = buildQuery({ table: 'T', sort: columns.concat([{ name: '_id' }]).map(c => ({ column: c.name })) }, columns)
  assert.equal(five.arguments.sort.length, 4)
  assert.equal(buildQuery({ table: 'T', conditions: [{ column: 'Qty', op: 'eq', value: 'z' }] }, columns).errors[0], 'integer')
})

test('query: pages go forward by the add-on cursor and back by the ones kept', () => {
  let p = newPager()
  assert.equal(pagerCursor(p), '')
  p = pagerNext(p, 'c1')
  p = pagerNext(p, 'c2')
  assert.equal(pagerCursor(p), 'c2')
  p = pagerPrev(p)
  assert.equal(pagerCursor(p), 'c1')
  // Going forward from an earlier page forgets the later ones.
  p = pagerNext(p, 'c9')
  assert.deepEqual(p.cursors, ['', 'c1', 'c9'])
  assert.equal(pagerPrev(newPager()).at, 0)
  assert.equal(pagerNext(newPager(), '').at, 0)
})

test('query: a column header cycles ascending, descending and none', () => {
  let s = flipSort([], 'Qty')
  assert.deepEqual(s, [{ column: 'Qty', desc: false }])
  s = flipSort(s, 'Qty')
  assert.deepEqual(s, [{ column: 'Qty', desc: true }])
  assert.deepEqual(flipSort(s, 'Qty'), [])
})
