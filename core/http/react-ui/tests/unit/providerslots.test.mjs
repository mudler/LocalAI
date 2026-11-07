import test from 'node:test'
import assert from 'node:assert/strict'
import { forgettable, pickable, providerFacts, providerFailure, slotOf } from '../../src/utils/providerSlots.js'

const builtin = { id: 'builtin', kind: 'builtin', available: true, selected: true }
const fake = { id: 'fake-notes', kind: 'addon', title: 'Fake notes', available: true, selected: false, properties: { remote: true, undo: 'none' } }
const off = { id: 'off', kind: 'off', available: true, selected: false }
const slot = (resolution, providers = [builtin, fake, off]) => ({ id: 'memory', providers, resolution })

test('provider slots: the slot of an id is found in the list, or null', () => {
  const list = { slots: [{ id: 'memory' }] }
  assert.equal(slotOf(list, 'memory').id, 'memory')
  assert.equal(slotOf(list, 'calendar'), null)
  assert.equal(slotOf(null, 'memory'), null)
})

test('provider slots: a slot that works has no failure', () => {
  assert.equal(providerFailure(slot({ selected: 'builtin', active: 'builtin', off: false })), null)
  assert.equal(providerFailure(slot({ selected: 'off', active: 'off', off: true, code: 'provider_off' })), null, 'a person who chose Off is not a failure')
  assert.equal(providerFailure(null), null)
})

test('provider slots: an add-on that was picked and cannot serve names itself', () => {
  const r = { selected: 'fake-notes', active: 'off', off: true, code: 'provider_unavailable', addon_id: 'fake-notes', addon_title: 'Fake notes' }
  assert.deepEqual(providerFailure(slot(r)), { id: 'fake-notes', title: 'Fake notes' })
  assert.deepEqual(providerFailure(slot({ ...r, addon_title: '' })), { id: 'fake-notes', title: 'fake-notes' }, 'the id stands in for a title')
})

test('provider slots: an add-on shows where its data goes and how a change is undone, the built-in one shows nothing', () => {
  assert.deepEqual(providerFacts(fake), ['remote', 'undoNone'])
  assert.deepEqual(providerFacts({ kind: 'addon', properties: { remote: false, undo: 'provider' } }), ['local', 'undoProvider'])
  assert.deepEqual(providerFacts({ kind: 'addon', properties: { undo: 'host' } }), ['local', 'undoHost'])
  assert.deepEqual(providerFacts({ kind: 'addon' }), ['local'])
  assert.deepEqual(providerFacts(builtin), [])
  assert.deepEqual(providerFacts(off), [])
  assert.deepEqual(providerFacts(null), [])
})

test('provider slots: only a provider that is listed, available and not already picked can be picked', () => {
  const s = slot({ selected: 'builtin', active: 'builtin', off: false })
  assert.equal(pickable(s, 'fake-notes'), 'fake-notes')
  assert.equal(pickable(s, 'off'), 'off')
  assert.equal(pickable(s, 'builtin'), null, 'it is picked already')
  assert.equal(pickable(s, 'missing'), null)
  const down = slot({ selected: 'builtin' }, [builtin, { ...fake, available: false }, off])
  assert.equal(pickable(down, 'fake-notes'), null, 'an add-on that cannot serve now')
  assert.equal(pickable(null, 'off'), null)
})

test('provider slots: forget is offered only for an add-on that serves and declares the forget role', () => {
  const can = { ...fake, selected: true, roles: ['read', 'forget'] }
  const cannot = { ...fake, selected: true, roles: ['read'] }
  const on = { selected: 'fake-notes', active: 'fake-notes', off: false }
  assert.deepEqual(forgettable(slot(on, [builtin, can, off])), { id: 'fake-notes', title: 'Fake notes', undo: 'none' })
  assert.equal(forgettable(slot(on, [builtin, cannot, off])), null, 'no forget role')
  assert.equal(forgettable(slot({ selected: 'builtin', active: 'builtin', off: false })), null, 'the built-in provider has none')
  assert.equal(forgettable(slot({ selected: 'off', active: 'off', off: true, code: 'provider_off' }, [builtin, can, off])), null, 'Off serves nothing')
  assert.equal(forgettable(slot({ selected: 'fake-notes', active: 'off', off: true, code: 'provider_unavailable' }, [builtin, { ...can, available: false }, off])), null, 'a provider that is down')
  assert.equal(forgettable(null), null)
})
