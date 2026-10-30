import test from 'node:test'
import assert from 'node:assert/strict'
import { upgradeAvailability } from '../../src/components/addons/updateState.js'

const D = 'sha256:' + 'b'.repeat(64)
const base = { id: 'notes', title: 'Notes', digest: 'sha256:' + 'a'.repeat(64), generation: 1 }
const avail = { state: 'update-available', available: true, version: '2.0.0', digest: D }

test('availability: a confirmed candidate for an administrator can start', () => {
  assert.deepEqual(upgradeAvailability({ ...base, update: avail }, { isAdmin: true }), { startable: true, reason: '', reasons: [], canCheck: false })
})

test('availability: a user who is not an administrator is told so and cannot check', () => {
  const a = upgradeAvailability({ ...base, update: avail }, { isAdmin: false })
  assert.equal(a.startable, false)
  assert.equal(a.reason, 'role')
  assert.equal(a.canCheck, false)
})

test('availability: update-available without a digest, or available false, needs a check', () => {
  for (const update of [{ state: 'update-available', available: true, version: '2' }, { state: 'update-available', available: false, digest: D }, { available: true }]) {
    const a = upgradeAvailability({ ...base, update }, { isAdmin: true })
    assert.equal(a.startable, false, JSON.stringify(update))
    assert.equal(a.reason, 'needs-check')
    assert.equal(a.canCheck, true)
  }
})

test('availability: a notice with no readable record, or a record that says current, asks for a check', () => {
  assert.equal(upgradeAvailability(null, { isAdmin: true, pending: true }).reason, 'needs-check')
  assert.equal(upgradeAvailability({ ...base }, { isAdmin: true, pending: true }).reason, 'needs-check')
  const current = upgradeAvailability({ ...base, update: { state: 'up-to-date' } }, { isAdmin: true, pending: true })
  assert.equal(current.reason, 'current')
  assert.equal(current.canCheck, true)
})

test('availability: incompatible, failed and integrity states carry their reasons', () => {
  const inc = upgradeAvailability({ ...base, update: { state: 'incompatible', version: '2', reasons: ['host-version', 'made-up'] } }, { isAdmin: true })
  assert.equal(inc.reason, 'incompatible')
  assert.deepEqual(inc.reasons, ['host-version'])
  assert.equal(inc.canCheck, false)
  assert.equal(upgradeAvailability({ ...base, update: { state: 'check-failed', error: 'refresh-failed' } }, { isAdmin: true, pending: true }).reason, 'check-failed')
  assert.equal(upgradeAvailability({ ...base, update: { state: 'check-failed' } }, { isAdmin: true, pending: true }).canCheck, true)
  assert.equal(upgradeAvailability({ ...base, update: { state: 'integrity-error' } }, { isAdmin: true }).reason, 'integrity')
})

test('availability: an add-on whose data needs recovery cannot be upgraded', () => {
  const a = upgradeAvailability({ ...base, recovery: 'needs-recovery', update: avail }, { isAdmin: true })
  assert.equal(a.startable, false)
  assert.equal(a.reason, 'recovery')
})

test('availability: nothing to say when there is no update and no notice', () => {
  const a = upgradeAvailability({ ...base, update: { state: 'up-to-date' } }, { isAdmin: true })
  assert.equal(a.reason, '')
  assert.equal(a.startable, false)
})
