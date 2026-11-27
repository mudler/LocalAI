import test from 'node:test'
import assert from 'node:assert/strict'
import {
  NO_OWNER, TTL_CHOICES, deviceOwners, devicesOfOwner, myMintFailure, newDevice, pairBlocked, ttlWords, userTtlChoices,
} from '../../src/utils/p2p.js'
import {
  USER_PAIRING_DEFAULTS, userPairingForm, userPairingFrom, userPairingIssues, userPairingLimitsPatch,
} from '../../src/components/p2psettings/p2pSettingsModel.js'

test('p2p pairing: the lives a user may pick stop at the cap', () => {
  assert.deepEqual(userTtlChoices(3600).map(c => c.seconds), [600, 3600])
  assert.deepEqual(userTtlChoices(86400).map(c => c.seconds), [600, 3600, 86400])
  // A cap that is not a usual life is offered as the last choice.
  assert.deepEqual(userTtlChoices(7200).map(c => c.seconds), [600, 3600, 7200])
  assert.deepEqual(userTtlChoices(300).map(c => c.seconds), [300])
  assert.equal(userTtlChoices(7200)[2].id, '7200')
  // No cap known: the usual choices.
  assert.deepEqual(userTtlChoices(undefined), TTL_CHOICES)
  assert.deepEqual(userTtlChoices(0), TTL_CHOICES)
})

test('p2p pairing: the words of a life', () => {
  assert.deepEqual(ttlWords(600), { key: 'ttl.600' })
  assert.deepEqual(ttlWords(86400), { key: 'ttl.86400' })
  assert.deepEqual(ttlWords(7200), { key: 'ttl.hours', values: { count: 2 } })
  assert.deepEqual(ttlWords(300), { key: 'ttl.minutes', values: { count: 5 } })
  assert.deepEqual(ttlWords(20), { key: 'ttl.minutes', values: { count: 1 } })
})

test('p2p pairing: why a user cannot pair now', () => {
  const limits = { max_devices: 5, max_pending: 2 }
  assert.equal(pairBlocked(null), 'unavailable')
  assert.equal(pairBlocked({ available: false, limits }), 'unavailable')
  assert.equal(pairBlocked({ available: true, limits, devices: 1, pending_invites: 0 }), '')
  assert.equal(pairBlocked({ available: true, limits, devices: 5, pending_invites: 0 }), 'devices')
  assert.equal(pairBlocked({ available: true, limits, devices: 1, pending_invites: 2 }), 'pending')
  // The device limit is named first: a pending invite would not help.
  assert.equal(pairBlocked({ available: true, limits, devices: 5, pending_invites: 2 }), 'devices')
})

test('p2p pairing: a refused mint by status and type', () => {
  assert.equal(myMintFailure({ status: 409, body: { error: { type: 'needs_account' } } }), 'needsAccount')
  assert.equal(myMintFailure({ status: 409, body: { error: { type: 'p2p_error' } } }), 'notShare')
  assert.equal(myMintFailure({ status: 429, body: { error: { type: 'limit_reached' } } }), 'limit')
  assert.equal(myMintFailure({ status: 400 }), 'ttl')
  assert.equal(myMintFailure({ status: 403 }), 'forbidden')
  assert.equal(myMintFailure({ status: 500 }), 'other')
  assert.equal(myMintFailure(new Error('network')), 'other')
})

test('p2p pairing: the device a redeem added', () => {
  const devices = [
    { id: 'a', status: 'active' },
    { id: 'b', status: 'revoked' },
    { id: 'c', status: 'active' },
  ]
  assert.equal(newDevice(devices, new Set(['a'])).id, 'c')
  assert.equal(newDevice(devices, ['a', 'c']), null)
  // A revoked device that was not known is not the new phone.
  assert.equal(newDevice([{ id: 'b', status: 'revoked' }], []), null)
  assert.equal(newDevice(null, []), null)
})

test('p2p pairing: the owners of the admin device table and the filter', () => {
  const devices = [
    { id: 'd1', owner_id: 'u2', owner_name: 'bob' },
    { id: 'd2', owner_id: 'u1', owner_name: 'alice' },
    { id: 'd3', owner_id: '' },
    { id: 'd4', owner_id: 'u1', owner_name: 'alice' },
    { id: 'd5', owner_id: 'u3' },
  ]
  assert.deepEqual(deviceOwners(devices), [
    { id: 'u1', name: 'alice' },
    { id: 'u2', name: 'bob' },
    { id: 'u3', name: '' },
    { id: NO_OWNER, name: '' },
  ])
  assert.deepEqual(devicesOfOwner(devices, 'u1').map(d => d.id), ['d2', 'd4'])
  assert.deepEqual(devicesOfOwner(devices, NO_OWNER).map(d => d.id), ['d3'])
  assert.equal(devicesOfOwner(devices, '').length, 5)
  assert.deepEqual(deviceOwners(undefined), [])
})

test('p2p pairing: the user pairing setting, its form and its patch', () => {
  assert.deepEqual(userPairingFrom(undefined), USER_PAIRING_DEFAULTS)
  const s = userPairingFrom({ relays: 'none', allow_open_relay: true, omit_lan_addrs: true, max_devices: 3, max_pending: 1, max_ttl_seconds: 1800 })
  assert.deepEqual(s, { relays: 'none', allow_open_relay: true, omit_lan_addrs: true, max_devices: 3, max_pending: 1, max_ttl_seconds: 1800 })
  assert.equal(userPairingFrom({ relays: 'bogus', max_devices: -1 }).relays, 'auto')
  assert.equal(userPairingFrom({ max_devices: -1 }).max_devices, 5)

  const form = userPairingForm(s)
  assert.deepEqual(form, { max_devices: '3', max_pending: '1', ttl_minutes: '30' })
  assert.deepEqual(userPairingIssues(form), {})
  assert.deepEqual(userPairingLimitsPatch(s, form), {})

  assert.deepEqual(userPairingIssues({ max_devices: '0', max_pending: '21', ttl_minutes: 'x' }), { max_devices: true, max_pending: true, ttl_minutes: true })
  assert.deepEqual(userPairingIssues({ max_devices: '100', max_pending: '20', ttl_minutes: '1440' }), {})
  assert.deepEqual(userPairingIssues({ max_devices: '101', max_pending: '1', ttl_minutes: '1441' }), { max_devices: true, ttl_minutes: true })

  assert.deepEqual(userPairingLimitsPatch(s, { max_devices: '4', max_pending: '1', ttl_minutes: '60' }), { max_devices: 4, max_ttl_seconds: 3600 })
  // A life the form rounded to minutes is not a change.
  const odd = userPairingFrom({ max_ttl_seconds: 90 })
  assert.deepEqual(userPairingLimitsPatch(odd, userPairingForm(odd)), {})
})
