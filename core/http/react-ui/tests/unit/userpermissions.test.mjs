// The admin Users access sheet saves the user's stored permission map (PUT
// replaces it). It must send the keys that were stored plus the ones the
// administrator changed, never the effective map: a feature the user does not
// name follows its default, and a later change of that default must reach them.
import test from 'node:test'
import assert from 'node:assert/strict'
import {
  accessInfo, featureState, followDefault, permissionPayload, setFeature, storedPermissions,
} from '../../src/utils/access.js'

const bob = {
  id: 'u-bob', role: 'user',
  permissions: { chat: true, mcp: true, p2p_pair: false, fine_tuning: false },
  explicit_permissions: { mcp: true },
}

test('the stored map is explicit_permissions, or the effective map from an older server', () => {
  assert.deepEqual(storedPermissions(bob), { mcp: true })
  assert.deepEqual(storedPermissions({ permissions: { chat: true } }), { chat: true })
  assert.deepEqual(storedPermissions({}), {})
})

test('a feature the user does not name follows the default in force', () => {
  const stored = storedPermissions(bob)
  assert.deepEqual(featureState(stored, {}, 'p2p_pair', false), { on: false, explicit: false })
  assert.deepEqual(featureState(stored, {}, 'p2p_pair', true), { on: true, explicit: false })
  assert.deepEqual(featureState(stored, {}, 'mcp', false), { on: true, explicit: true })
})

test('saving with no change sends the stored keys only', () => {
  assert.deepEqual(permissionPayload(storedPermissions(bob), {}), { mcp: true })
})

test('a changed feature is added to the stored keys; the others keep following the default', () => {
  const stored = storedPermissions(bob)
  const edits = setFeature(stored, {}, 'fine_tuning', true, false)
  assert.deepEqual(permissionPayload(stored, edits), { mcp: true, fine_tuning: true })
  assert.equal(featureState(stored, edits, 'fine_tuning', false).explicit, true)
})

test('switching a following feature away and back leaves it following the default', () => {
  const stored = storedPermissions(bob)
  let edits = setFeature(stored, {}, 'p2p_pair', true, false)
  edits = setFeature(stored, edits, 'p2p_pair', false, false)
  assert.deepEqual(edits, {})
  assert.deepEqual(permissionPayload(stored, edits), { mcp: true })
})

test('setting a stored feature back to its stored value drops the edit', () => {
  const stored = storedPermissions(bob)
  let edits = setFeature(stored, {}, 'mcp', false, false)
  assert.deepEqual(permissionPayload(stored, edits), { mcp: false })
  edits = setFeature(stored, edits, 'mcp', true, false)
  assert.deepEqual(edits, {})
})

test('"Use default" removes the explicit key', () => {
  const stored = { mcp: true, p2p_pair: false }
  const edits = followDefault(stored, {}, 'p2p_pair')
  assert.deepEqual(permissionPayload(stored, edits), { mcp: true })
  assert.deepEqual(featureState(stored, edits, 'p2p_pair', true), { on: true, explicit: false })
})

test('"Use default" on a feature changed in this sheet only drops the change', () => {
  const stored = { mcp: true }
  let edits = setFeature(stored, {}, 'p2p_pair', true, false)
  edits = followDefault(stored, edits, 'p2p_pair')
  assert.deepEqual(edits, {})
})

test('the switch follows a default flipped while the sheet is open', () => {
  const stored = storedPermissions(bob)
  assert.equal(featureState(stored, {}, 'p2p_pair', false).on, false)
  assert.equal(featureState(stored, {}, 'p2p_pair', true).on, true)
  // An explicit value does not move with the default.
  const edits = setFeature(stored, {}, 'p2p_pair', false, true)
  assert.equal(featureState(stored, edits, 'p2p_pair', true).on, false)
  assert.deepEqual(permissionPayload(stored, edits), { mcp: true, p2p_pair: false })
})

test('the access summary reads the stored keys against the current defaults', () => {
  const meta = { general_features: [{ key: 'p2p_pair', default: true }], api_features: [{ key: 'mcp', default: false }] }
  // The effective map says p2p_pair is off, but bob follows the default, now on.
  const info = accessInfo(bob, meta)
  assert.equal(info.fewer, 0)
  assert.equal(info.more, 1)
})
