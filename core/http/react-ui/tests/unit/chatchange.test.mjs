import test from 'node:test'
import assert from 'node:assert/strict'
import { addonChange } from '../../src/utils/addonChange.js'

const entry = (name, doc) => `${name}: {"table":"t"}\n${typeof doc === 'string' ? doc : JSON.stringify(doc)}`

test('chat change: a change made at once is shown with what it did and that it cannot be undone', () => {
  const text = entry('addon__tables__rows-insert', { kind: 'applied', tool: 'x', applied: { id: 'o1', addonId: 'tables', action: 'write', count: 2, undoable: false }, note: 'n' })
  assert.deepEqual(addonChange(text), { kind: 'applied', addonId: 'tables', action: 'write', count: 2, undoable: false, changeId: '', resourceId: '', undo: '', reason: '' })
})

test('chat change: a change the add-on kept a change set for can be undone, with what undo does', () => {
  const text = entry('addon__tables__rows-insert', { kind: 'applied', applied: { id: 'o1', addonId: 'tables', resourceId: 'r0123456789abcdef0123456789abcde', action: 'write', count: 2, undoable: true, changeId: 't0123abc', undo: 'Delete the 2 rows added to People' } })
  assert.deepEqual(addonChange(text), { kind: 'applied', addonId: 'tables', action: 'write', count: 2, undoable: true, changeId: 't0123abc', resourceId: 'r0123456789abcdef0123456789abcde', undo: 'Delete the 2 rows added to People', reason: '' })
})

test('chat change: a change that cannot be undone says why, without repeating the sentence the card already has', () => {
  const text = entry('addon__tables__rows-delete', { kind: 'applied', applied: { id: 'o1', action: 'write', count: 9, undoable: false, reason: 'cannot be undone: the data it changed is too large to keep' } })
  const c = addonChange(text)
  assert.equal(c.undoable, false)
  assert.equal(c.reason, 'the data it changed is too large to keep')
})

test('chat change: an undo button needs a change ID and a resource ID of the right shape, never model text', () => {
  const base = { kind: 'applied', applied: { count: 1, undoable: true, undo: 'x' } }
  const bad = (a) => addonChange(entry('addon__tables__rows-insert', { ...base, applied: { ...base.applied, ...a } }))
  assert.equal(bad({ changeId: '', resourceId: 'r0123456789abcdef0123456789abcde' }).undoable, false, 'no change ID')
  assert.equal(bad({ changeId: 't1', resourceId: '' }).undoable, false, 'no resource')
  assert.equal(bad({ changeId: '../x', resourceId: 'r0123456789abcdef0123456789abcde' }).undoable, false, 'a path in the change ID')
  assert.equal(bad({ changeId: 't1', resourceId: 'a/b' }).undoable, false, 'a path in the resource ID')
  assert.equal(bad({ changeId: 'T1', resourceId: 'r0123456789abcdef0123456789abcde' }).undoable, false)
  const ok = bad({ changeId: 't1', resourceId: 'r0123456789abcdef0123456789abcde' })
  assert.equal(ok.undoable, true)
})

test('chat change: a change the host could not apply is shown as waiting, never as made', () => {
  const text = entry('addon__tables__rows-insert', { kind: 'proposal', proposal: { id: 'p1' } })
  assert.deepEqual(addonChange(text), { kind: 'proposed', addonId: 'tables' })
})

test('chat change: other entries, other tools and capped or invalid output show nothing', () => {
  assert.equal(addonChange(entry('addon__tables__rows-get', { kind: 'result', tool: 'x' })), null)
  assert.equal(addonChange(entry('bash', { kind: 'applied', applied: { undoable: true } })), null, 'a tool outside the add-on namespace')
  assert.equal(addonChange(entry('addon__tables__rows-insert', '{"kind":"applied","applied":{"id":"o')), null, 'capped output')
  assert.equal(addonChange('addon__tables__rows-insert: {}'), null)
  assert.equal(addonChange(undefined), null)
})

test('chat change: only the host decides what is undoable', () => {
  const text = entry('addon__tables__rows-insert', { kind: 'applied', applied: { undoable: 'yes', count: 'many', action: 7 } })
  assert.deepEqual(addonChange(text), { kind: 'applied', addonId: 'tables', action: '', count: 0, undoable: false, changeId: '', resourceId: '', undo: '', reason: '' })
})
