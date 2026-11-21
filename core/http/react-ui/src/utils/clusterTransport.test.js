import assert from 'node:assert/strict'
import test from 'node:test'

import { attachedRoutes, busLabel, busNote, routeChips, routeLabel } from './clusterTransport.js'

test('busLabel words the bus of the frontend, and routeLabel the route of a worker', () => {
  assert.equal(busLabel({ bus: 'nats', nats_configured: true, nats_connected: true }), 'NATS connected')
  assert.equal(busLabel({ bus: 'postgres' }), 'PostgreSQL')
  assert.equal(busLabel(undefined), 'unknown')
  assert.equal(routeLabel('nats'), 'NATS')
  assert.equal(routeLabel('tunnel'), 'WebSocket')
  assert.equal(routeLabel('p2p'), 'p2p')
})

test('busNote speaks only when NATS is configured and out of reach', () => {
  assert.equal(busNote({ bus: 'postgres' }), '')
  assert.equal(busNote({ bus: 'nats', nats_configured: true, nats_connected: true }), '')
  assert.match(busNote({ bus: 'postgres', nats_configured: true, nats_connected: false }), /does not reach it/)
  assert.equal(busNote(undefined), '')
})

test('attachedRoutes reads the attached column of a node', () => {
  assert.deepEqual(attachedRoutes({ attached: 'nats, tunnel' }), ['nats', 'tunnel'])
  assert.deepEqual(attachedRoutes({ attached: '' }), [])
  assert.deepEqual(attachedRoutes(undefined), [])
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
