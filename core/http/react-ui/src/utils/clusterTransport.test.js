import assert from 'node:assert/strict'
import test from 'node:test'

import { canForce, followStatus, formatCountdown, otherCarrier, replicaReady, stateLabel, workerProfile } from './clusterTransport.js'

test('workerProfile classifies by the carriers a worker can follow', () => {
  assert.equal(workerProfile({ follow: ['nats', 'tunnel'] }), 'dual')
  assert.equal(workerProfile({ follow: ['tunnel'] }), 'tunnel-only')
  assert.equal(workerProfile({ follow: ['nats'] }), 'nats-only')
  assert.equal(workerProfile({}), 'legacy')
})

test('replicaReady needs the epoch in flight and no reason', () => {
  assert.equal(replicaReady({ ready_epoch: 3 }, { state: 'prepare', epoch: 4 }), false)
  assert.equal(replicaReady({ ready_epoch: 4 }, { state: 'prepare', epoch: 4 }), true)
  assert.equal(replicaReady({ ready_epoch: 4, ready_reason: 'cannot connect' }, { state: 'prepare', epoch: 4 }), false)
  assert.equal(replicaReady({ ready_epoch: 0 }, { state: 'stable', epoch: 4 }), true)
})

test('formatCountdown formats nanoseconds', () => {
  assert.equal(formatCountdown(65e9), '1:05')
  assert.equal(formatCountdown(3725e9), '1:02:05')
  assert.equal(formatCountdown(-5), '0:00')
})

test('stateLabel reports draining after a commit', () => {
  assert.equal(stateLabel({ state: 'stable', drain_remaining_ns: 5e9 }), 'draining')
  assert.equal(stateLabel({ state: 'stable' }), 'stable')
  assert.equal(stateLabel({ state: 'prepare' }), 'prepare')
})

test('canForce needs blockers and all of them forceable', () => {
  assert.equal(canForce({ blockers: [] }), false)
  assert.equal(canForce({ blockers: [{ forceable: true }, { forceable: false }] }), false)
  assert.equal(canForce({ blockers: [{ forceable: true }] }), true)
  assert.equal(otherCarrier('nats'), 'tunnel')
})

test('followStatus says whether a worker can follow a change to the other carrier', () => {
  const dual = { follow: ['nats', 'tunnel'], attached: ['nats'] }
  assert.deepEqual(followStatus(dual, 'nats'), { ok: true, text: 'ok' })

  const legacy = { attached: [] }
  assert.equal(followStatus(legacy, 'nats').ok, false)
  assert.match(followStatus(legacy, 'nats').text, /predates carrier switching/)

  const natsOnly = { follow: ['nats'], attached: ['nats'] }
  assert.equal(followStatus(natsOnly, 'nats').ok, false)
  assert.match(followStatus(natsOnly, 'nats').text, /Database tunnel/)

  const tunnelOnly = { follow: ['tunnel'], attached: ['tunnel'] }
  assert.equal(followStatus(tunnelOnly, 'tunnel').ok, false)
  assert.match(followStatus(tunnelOnly, 'tunnel').text, /NATS/)
  assert.deepEqual(followStatus(tunnelOnly, 'nats'), { ok: true, text: 'ok' })

  const reported = { follow: ['tunnel'], attached: ['tunnel'], follow_error: 'no routable address' }
  assert.deepEqual(followStatus(reported, 'tunnel'), { ok: false, text: 'no routable address' })
})
