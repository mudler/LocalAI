import test from 'node:test'
import assert from 'node:assert/strict'
import { addonChange } from '../../src/utils/addonChange.js'

const entry = (name, doc) => `${name}: {"table":"t"}\n${typeof doc === 'string' ? doc : JSON.stringify(doc)}`

test('chat change: a change made at once is shown with what it did and that it cannot be undone', () => {
  const text = entry('addon__tables__rows-insert', { kind: 'applied', tool: 'x', applied: { id: 'o1', addonId: 'tables', action: 'write', count: 2, undoable: false }, note: 'n' })
  assert.deepEqual(addonChange(text), { kind: 'applied', addonId: 'tables', action: 'write', count: 2, undoable: false })
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
  assert.deepEqual(addonChange(text), { kind: 'applied', addonId: 'tables', action: '', count: 0, undoable: false })
})
