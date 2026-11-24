// "View as": who the Users page and the command palette offer it for.
import test from 'node:test'
import assert from 'node:assert/strict'

const access = await import('../../src/utils/access.js')
const palette = await import('../../src/components/admin/paletteModel.js')

const people = [
  { id: 'u-morgan', name: 'morgan', email: 'morgan@lab.example', role: 'admin', status: 'active' },
  { id: 'u-riley', name: 'riley', email: 'riley@lab.example', role: 'admin', status: 'active' },
  { id: 'u-alice', name: 'alice', email: 'alice@lab.example', role: 'user', status: 'active' },
  { id: 'u-dave', name: 'dave', email: 'dave@lab.example', role: 'user', status: 'pending' },
  { id: 'u-erin', name: '', email: 'erin@lab.example', role: 'user', status: 'disabled' },
  { id: 'u-bob', name: '', email: 'bob@lab.example', role: 'user', status: 'active' },
]
const self = { id: 'u-morgan', email: 'morgan@lab.example' }

test('canViewAs: active, not the viewer, not an administrator', () => {
  assert.deepEqual(people.filter(p => access.canViewAs(p, self)).map(p => p.id), ['u-alice', 'u-bob'])
  assert.equal(access.canViewAs(null, self), false)
})

test('the palette offers View as for the same people, and the group words match a query', () => {
  const words = { kind: 'View as', title: n => `View as ${n}`, where: e => `${e} · read-only first` }
  const entries = palette.viewAsEntries(people, self, words)
  assert.deepEqual(entries.map(e => e.title), ['View as alice', 'View as bob@lab.example'])
  assert.deepEqual(entries[0].viewAs, { id: 'u-alice', name: 'alice' })
  assert.equal(entries[0].to, undefined)
  assert.deepEqual(palette.rank(entries, 'alice').map(e => e.id), ['viewas:u-alice'])
  assert.equal(palette.rank(entries, 'view as').length, 2)
  assert.ok(palette.KINDS.indexOf('viewas') < palette.KINDS.indexOf('person'), 'View as comes before People')
})
