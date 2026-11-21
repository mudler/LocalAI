import assert from 'node:assert/strict'
import test from 'node:test'

import { busLabel, formatCountdown, replicaReady, routeChips, routeLabel, stateLabel } from './clusterTransport.js'

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

test('routeChips shows each route with the tone of its state and hides a live heartbeat', () => {
  const chips = routeChips([
    { route: 'heartbeat', state: 'live' },
    { route: 'nats', state: 'down', reason: 'connecting' },
    { route: 'tunnel', transport: 'p2p', state: 'reconnecting' },
  ])
  assert.deepEqual(chips.map(c => [c.key, c.name, c.state, c.tone]), [
    ['nats', 'nats', 'down', 'error'],
    ['tunnel', 'p2p', 'reconnecting', 'warn'],
  ])
  assert.equal(chips[0].reason, 'connecting')
})

test('routeChips shows a lost heartbeat, words an unknown tunnel as the tunnel, and reads no state as down', () => {
  const chips = routeChips([
    { route: 'heartbeat', state: 'down' },
    { route: 'tunnel', state: 'live' },
    { route: 'nats', state: 'weird' },
  ])
  assert.deepEqual(chips.map(c => [c.name, c.state, c.tone]), [
    ['heartbeat', 'down', 'error'],
    ['tunnel', 'live', 'ok'],
    ['nats', 'down', 'error'],
  ])
  assert.deepEqual(routeChips(undefined), [])
})
