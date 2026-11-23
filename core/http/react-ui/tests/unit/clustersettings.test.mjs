import test from 'node:test'
import assert from 'node:assert/strict'
import { busState, matchSections, natsOffered, natsOutOfReach, natsState } from '../../src/utils/clusterSettings.js'

test('cluster settings: the bus of a frontend is NATS, PostgreSQL, or unknown', () => {
  assert.deepEqual(busState({ bus: 'nats' }), { key: 'nats', tone: 'ok' })
  assert.deepEqual(busState({ bus: 'postgres', nats_configured: false }), { key: 'postgres', tone: 'idle' })
  assert.deepEqual(busState({ bus: 'postgres', nats_configured: true }), { key: 'postgres', tone: 'warn' })
  assert.deepEqual(busState(undefined), { key: 'unknown', tone: 'idle' })
})

test('cluster settings: NATS out of reach is configured and not connected', () => {
  assert.equal(natsOutOfReach({ nats_configured: true, nats_connected: false }), true)
  assert.equal(natsOutOfReach({ nats_configured: true, nats_connected: true }), false)
  assert.equal(natsOutOfReach({ nats_configured: false }), false)
  assert.equal(natsState({ bus: { nats_configured: true, nats_connected: true } }), 'connected')
  assert.equal(natsState({ bus: { nats_configured: true, nats_connected: false } }), 'unreachable')
  assert.equal(natsState({ bus: { nats_configured: false } }), 'off')
})

test('cluster settings: NATS is offered to a new worker only when it is configured', () => {
  assert.equal(natsOffered({ bus: { nats_configured: true } }), true)
  assert.equal(natsOffered({ nats_url: 'nats://nats:4222' }), true)
  assert.equal(natsOffered({ bus: { nats_configured: false } }), false)
  assert.equal(natsOffered(null), false)
})

test('cluster settings: search finds a section by every word, in any language of its heading', () => {
  assert.deepEqual(matchSections('nats'), ['bus', 'nats'])
  assert.deepEqual(matchSections('weight'), ['weights'])
  assert.deepEqual(matchSections('auto approve'), ['joining'])
  assert.deepEqual(matchSections('nothing like this'), [])
  assert.deepEqual(matchSections(''), [])
  assert.deepEqual(matchSections('Verbindung', id => (id === 'weights' ? 'Verbindungspriorität' : '')), ['weights'])
})
