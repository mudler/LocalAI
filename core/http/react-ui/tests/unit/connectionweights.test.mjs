import test from 'node:test'
import assert from 'node:assert/strict'
import {
  CONNECTIONS, DEFAULT_WEIGHTS, defaultWeights, parseWeight, resetPayload, tieWinner, preferenceOrder, weightErrors, weightsInForce, weightsPayload,
} from '../../src/utils/connectionWeights.js'

test('connection weights: the three connections and their defaults', () => {
  assert.deepEqual(CONNECTIONS.map(c => c.key), ['nats', 'websocket', 'p2p'])
  assert.deepEqual(DEFAULT_WEIGHTS, { nats: 100, websocket: 50, p2p: 25 })
})

test('connection weights: the weights in force come from the server, the default fills a gap', () => {
  assert.deepEqual(weightsInForce({ carrier_weights: { nats: 7, websocket: 8, p2p: 9 } }), { nats: 7, websocket: 8, p2p: 9 })
  assert.deepEqual(weightsInForce({ carrier_weights: { nats: 7 } }), { nats: 7, websocket: 50, p2p: 25 })
  assert.deepEqual(weightsInForce(undefined), DEFAULT_WEIGHTS)
  assert.deepEqual(defaultWeights({ carrier_weights_default: { nats: 1, websocket: 2, p2p: 3 } }), { nats: 1, websocket: 2, p2p: 3 })
})

test('connection weights: a weight is a whole number from 0 to 1000', () => {
  for (const good of ['0', '1000', ' 250 ', '7']) assert.equal(parseWeight(good).ok, true, good)
  assert.equal(parseWeight('0').value, 0)
  for (const bad of ['', ' ', '1001', '-1', '1.5', 'high', '1e3', '0x10', '+5']) assert.equal(parseWeight(bad).ok, false, bad)
  assert.equal(parseWeight('1001').error, 'range')
  assert.equal(parseWeight('').error, 'empty')
  assert.equal(parseWeight('1.5').error, 'whole')
})

test('connection weights: each field that is not a weight has its own error', () => {
    const errors = weightErrors({ nats: '10', websocket: '5000', p2p: 'x' })
  assert.deepEqual(Object.keys(errors), ['websocket', 'p2p'])
  assert.deepEqual(weightErrors({ nats: '0', websocket: '1000', p2p: '25' }), {})
})

test('connection weights: a save sends only the weights that changed', () => {
  const current = { nats: 100, websocket: 50, p2p: 25 }
  assert.deepEqual(weightsPayload({ nats: '100', websocket: '60', p2p: '25' }, current), { carrier_weights: { websocket: 60 } })
  assert.deepEqual(weightsPayload({ nats: '0', websocket: '50', p2p: '900' }, current), { carrier_weights: { nats: 0, p2p: 900 } })
})

test('connection weights: nothing to save when nothing changed or a field is wrong', () => {
  const current = { nats: 100, websocket: 50, p2p: 25 }
  assert.equal(weightsPayload({ nats: '100', websocket: '50', p2p: '25' }, current), null)
  assert.equal(weightsPayload({ nats: '100', websocket: '5000', p2p: '25' }, current), null)
})

test('connection weights: the defaults go back with null for each weight that is not at its default', () => {
  const defaults = { nats: 100, websocket: 50, p2p: 25 }
  assert.deepEqual(resetPayload({ nats: 100, websocket: 5, p2p: 1 }, defaults), { carrier_weights: { websocket: null, p2p: null } })
  assert.equal(resetPayload(defaults, defaults), null)
})

test('connection weights: the live sentence names the winner of a tie', () => {
  assert.deepEqual(tieWinner({ nats: 100, websocket: 50, p2p: 25 }, 'nats', 'websocket'), ['nats', 'websocket'])
  assert.deepEqual(tieWinner({ nats: 10, websocket: 50, p2p: 25 }, 'nats', 'websocket'), ['websocket', 'nats'])
  assert.equal(tieWinner({ nats: 50, websocket: 50, p2p: 25 }, 'nats', 'websocket'), null)
  assert.deepEqual(preferenceOrder({ nats: 100, websocket: 50, p2p: 25 }), ['nats', 'websocket', 'p2p'])
  assert.deepEqual(preferenceOrder({ nats: 0, websocket: 50, p2p: 25 }), ['websocket', 'p2p', 'nats'])
})
