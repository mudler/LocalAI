import test from 'node:test'
import assert from 'node:assert/strict'
import { checkFeedback, upgradeAvailability } from '../../src/components/addons/updateState.js'

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

// A check the page ran answered: the card says what it found and when, and a
// failed check says why. Clicking "Check again" is never silent.
test('availability: a check that still names no build to pin says so plainly instead of asking for another check', () => {
  const update = { state: 'update-available', available: true, version: '0.0.0-development' }
  const before = upgradeAvailability({ ...base, update }, { isAdmin: true, pending: true })
  assert.equal(before.reason, 'needs-check')
  const after = upgradeAvailability({ ...base, update }, { isAdmin: true, pending: true, checked: true })
  assert.equal(after.startable, false)
  assert.equal(after.reason, 'unpinned')
  // A confirmed candidate is still startable after a check.
  assert.equal(upgradeAvailability({ ...base, update: avail }, { isAdmin: true, checked: true }).startable, true)
})

test('check feedback: nothing before a check and while it runs', () => {
  assert.equal(checkFeedback(undefined), null)
  assert.equal(checkFeedback({ phase: 'working' }), null)
})

test('check feedback: the answer of the check, with checked just now', () => {
  const now = Date.parse('2026-10-30T16:00:30Z')
  const at = '2026-10-30T16:00:00Z'
  assert.deepEqual(checkFeedback({ phase: 'done', status: { state: 'up-to-date', checkedAt: at } }, now),
    { tone: 'ok', state: 'up-to-date', version: '', justNow: true, checkedAt: at, throttled: false, unpinned: false, errorKey: '' })
  const found = checkFeedback({ phase: 'done', status: { state: 'update-available', available: true, candidateVersion: '1.1.0', candidateDigest: D, checkedAt: at } }, now)
  assert.equal(found.tone, 'warn')
  assert.equal(found.state, 'update-available')
  assert.equal(found.version, '1.1.0')
  assert.equal(found.unpinned, false)
  const unpinned = checkFeedback({ phase: 'done', status: { state: 'update-available', available: true, candidateVersion: '0.0.0-development', checkedAt: at } }, now)
  assert.equal(unpinned.unpinned, true)
  const older = checkFeedback({ phase: 'done', throttled: true, status: { state: 'up-to-date', checkedAt: at } }, now + 10 * 60 * 1000)
  assert.equal(older.justNow, false)
  assert.equal(older.throttled, true)
  assert.equal(checkFeedback({ phase: 'done', status: { state: 'check-failed', error: 'refresh-failed', checkedAt: at } }, now).tone, 'error')
  // An answer without a readable status is unknown, never current.
  assert.equal(checkFeedback({ phase: 'done' }, now).state, 'unknown')
})

test('check feedback: a failed check names its cause', () => {
  const typed = checkFeedback({ phase: 'error', error: { kind: 'invalid', type: 'check_failed' } })
  assert.equal(typed.tone, 'error')
  assert.equal(typed.errorKey, 'check_failed')
  assert.equal(checkFeedback({ phase: 'error', error: { kind: 'network' } }).errorKey, 'network')
  assert.equal(checkFeedback({ phase: 'error', error: { kind: 'denied' } }).errorKey, 'denied')
})
