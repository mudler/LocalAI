import test from 'node:test'
import assert from 'node:assert/strict'
import {
  MAX_CHANNELS, canSave, channelsProblem, connectorForm, connectorPayload, emptyConnector, grantableFor, hasNoCaps, roleHas, setCap, setGrant, usualCaps,
} from '../../../internal/connector-ui/src/connector.js'

const CAPS = [
  { name: 'memory.write', title: 'Save to memory', description: 'x', sensitive: false, usual: true },
  { name: 'talkback.channels', title: 'Post', description: 'x', sensitive: false, usual: true },
  { name: 'tools.write', title: 'Tools', description: 'x', sensitive: false, usual: false },
  { name: 'profile.write', title: 'Profile', description: 'x', sensitive: true, usual: false },
]
const KIND = { roles: [{ id: 'admin', title: 'Admin' }], permissions: [{ name: 'memory.write', sensitive: false }, { name: 'profile.write', sensitive: true }, { name: 'talkback.channels', sensitive: false }] }

test('permissions: a new binding has no caps, grants or channels', () => {
  const c = emptyConnector()
  assert.deepEqual([c.caps, c.grants, c.channels], [[], [], []])
  assert.equal(hasNoCaps(c), true)
  assert.equal(hasNoCaps({ caps: ['memory.write'] }), false)
  assert.equal(hasNoCaps({}), true, 'a binding from an older host has no caps field')
})

test('permissions: the usual preset is the non-sensitive caps the host marks, and never a sensitive one', () => {
  assert.deepEqual(usualCaps(CAPS), ['memory.write', 'talkback.channels'])
  assert.deepEqual(usualCaps([{ name: 'profile.write', sensitive: true, usual: true }]), [], 'a sensitive cap is never usual, whatever the list says')
  assert.deepEqual(usualCaps(undefined), [])
})

test('permissions: turning a cap off removes it from every role grant and drops empty grants', () => {
  let f = { caps: ['memory.write', 'talkback.channels'], grants: [{ role: 'admin', caps: ['memory.write', 'talkback.channels'] }, { role: 'x', caps: ['memory.write'] }] }
  f = setCap(f, 'memory.write', false)
  assert.deepEqual(f.caps, ['talkback.channels'])
  assert.deepEqual(f.grants, [{ role: 'admin', caps: ['talkback.channels'] }])
  f = setCap(f, 'memory.write', true)
  assert.deepEqual(f.caps, ['talkback.channels', 'memory.write'])
  assert.equal(setCap(f, 'memory.write', true).caps.length, 2, 'no repeats')
})

test('permissions: a role is granted only a non-sensitive cap that is in the ceiling', () => {
  const f = { caps: ['memory.write', 'profile.write'], grants: [] }
  const g = setGrant(f, 'admin', 'memory.write', true, CAPS)
  assert.equal(roleHas(g, 'admin', 'memory.write'), true)
  assert.equal(setGrant(f, 'admin', 'profile.write', true, CAPS), f, 'sensitive: refused')
  assert.equal(setGrant(f, 'admin', 'talkback.channels', true, CAPS), f, 'not in the ceiling: refused')
  const off = setGrant(g, 'admin', 'memory.write', false, CAPS)
  assert.deepEqual(off.grants, [], 'the last cap of a role removes its grant')
})

test('permissions: the matrix lists what the kind can grant, inside the ceiling, never a sensitive cap', () => {
  assert.deepEqual(grantableFor(KIND, ['memory.write', 'profile.write']), ['memory.write'])
  assert.deepEqual(grantableFor(KIND, []), [])
  assert.deepEqual(grantableFor({}, ['memory.write']), [])
})

test('permissions: channels need a unique id with no space, at most ten', () => {
  assert.equal(channelsProblem([{ id: 'C1', label: 'ops', approved: true }]), '')
  assert.equal(channelsProblem([{ id: '', label: '' }]), 'id')
  assert.equal(channelsProblem([{ id: 'a b' }]), 'id')
  assert.equal(channelsProblem([{ id: 'C1' }, { id: 'C1' }]), 'repeat')
  assert.equal(channelsProblem([{ id: 'C'.repeat(65) }]), 'id')
  assert.equal(channelsProblem([{ id: 'C1', label: 'x'.repeat(61) }]), 'label')
  assert.equal(channelsProblem(Array.from({ length: MAX_CHANNELS + 1 }, (_, i) => ({ id: `C${i}` }))), 'count')
})

test('permissions: the request carries caps, grants and channels, and a legacy binding reads back unchanged', () => {
  const saved = { id: 'b1', name: 'B', agent_id: 'a', caps: ['memory.write'], grants: [{ role: 'admin', caps: ['memory.write'] }], channels: [{ id: 'C1', label: 'ops', approved: true }] }
  const body = connectorPayload(connectorForm(saved), 'slack')
  assert.deepEqual(body.caps, ['memory.write'])
  assert.deepEqual(body.grants, [{ role: 'admin', caps: ['memory.write'] }])
  assert.deepEqual(body.channels, [{ id: 'C1', label: 'ops', approved: true }])
  const legacy = connectorPayload(connectorForm({ id: 'b2', name: 'B', agent_id: 'a' }), 'slack')
  assert.deepEqual([legacy.caps, legacy.grants, legacy.channels], [[], [], []])
})

test('permissions: a bad channel list blocks saving', () => {
  const ok = { ...emptyConnector(), name: 'B', agent_id: 'a', token: '1:x' }
  assert.equal(canSave(ok, false), true)
  assert.equal(canSave({ ...ok, channels: [{ id: '' }] }, false), false)
})
