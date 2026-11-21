import test from 'node:test'
import assert from 'node:assert/strict'
import {
  CARRIERS, DEFAULT_WEIGHTS, defaultWeights, parseWeight, resetPayload, weightErrors, weightsInForce, weightsPayload,
} from '../../src/utils/carrierWeights.js'

test('carrier weights: the three carriers and their defaults', () => {
  assert.deepEqual(CARRIERS.map(c => c.key), ['nats', 'websocket', 'p2p'])
  assert.deepEqual(DEFAULT_WEIGHTS, { nats: 100, websocket: 50, p2p: 25 })
})

test('carrier weights: the weights in force come from the server, the default fills a gap', () => {
  assert.deepEqual(weightsInForce({ carrier_weights: { nats: 7, websocket: 8, p2p: 9 } }), { nats: 7, websocket: 8, p2p: 9 })
  assert.deepEqual(weightsInForce({ carrier_weights: { nats: 7 } }), { nats: 7, websocket: 50, p2p: 25 })
  assert.deepEqual(weightsInForce(undefined), DEFAULT_WEIGHTS)
  assert.deepEqual(defaultWeights({ carrier_weights_default: { nats: 1, websocket: 2, p2p: 3 } }), { nats: 1, websocket: 2, p2p: 3 })
})

test('carrier weights: a weight is a whole number from 0 to 1000', () => {
  for (const good of ['0', '1000', ' 250 ', '7']) assert.equal(parseWeight(good).ok, true, good)
  assert.equal(parseWeight('0').value, 0)
  for (const bad of ['', ' ', '1001', '-1', '1.5', 'high', '1e3', '0x10', '+5']) assert.equal(parseWeight(bad).ok, false, bad)
  assert.match(parseWeight('1001').error, /0 to 1000/)
})

test('carrier weights: each field that is not a weight has its own error', () => {
  assert.ok(weightErrors({ nats: '10', websocket: '5000', p2p: 'x' }).websocket.length > 0)
  const errors = weightErrors({ nats: '10', websocket: '5000', p2p: 'x' })
  assert.deepEqual(Object.keys(errors), ['websocket', 'p2p'])
  assert.deepEqual(weightErrors({ nats: '0', websocket: '1000', p2p: '25' }), {})
})

test('carrier weights: a save sends only the weights that changed', () => {
  const current = { nats: 100, websocket: 50, p2p: 25 }
  assert.deepEqual(weightsPayload({ nats: '100', websocket: '60', p2p: '25' }, current), { carrier_weights: { websocket: 60 } })
  assert.deepEqual(weightsPayload({ nats: '0', websocket: '50', p2p: '900' }, current), { carrier_weights: { nats: 0, p2p: 900 } })
})

test('carrier weights: nothing to save when nothing changed or a field is wrong', () => {
  const current = { nats: 100, websocket: 50, p2p: 25 }
  assert.equal(weightsPayload({ nats: '100', websocket: '50', p2p: '25' }, current), null)
  assert.equal(weightsPayload({ nats: '100', websocket: '5000', p2p: '25' }, current), null)
})

test('carrier weights: the defaults go back with null for each weight that is not at its default', () => {
  const defaults = { nats: 100, websocket: 50, p2p: 25 }
  assert.deepEqual(resetPayload({ nats: 100, websocket: 5, p2p: 1 }, defaults), { carrier_weights: { websocket: null, p2p: null } })
  assert.equal(resetPayload(defaults, defaults), null)
})
