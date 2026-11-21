import assert from 'node:assert/strict'
import test from 'node:test'

import { busLabel, formatCountdown, replicaReady, routeLabel, stateLabel } from './clusterTransport.js'

test('busLabel and routeLabel word the API values', () => {
  assert.equal(busLabel('nats'), 'NATS')
  assert.equal(busLabel('tunnel'), 'PostgreSQL')
  assert.equal(busLabel(undefined), 'unknown')
  assert.equal(routeLabel('nats'), 'NATS')
  assert.equal(routeLabel('tunnel'), 'WebSocket')
  assert.equal(routeLabel('p2p'), 'p2p')
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
