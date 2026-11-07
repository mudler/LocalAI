import test from 'node:test'
import assert from 'node:assert/strict'
import { createTurnTracker } from '../../src/lib/liveTurn.js'

test('turn: a typed turn running before the voice message is not the voice turn', () => {
  const t = createTurnTracker()
  assert.equal(t.observe({ running: true, failure: null }), null)
  // The voice message was queued behind it.
  assert.equal(t.arm({ started: false, running: true, failure: null }), null)
  assert.equal(t.observe({ running: true, failure: null }), null)
  assert.equal(t.observe({ running: false, failure: null }), null)
  assert.equal(t.observe({ running: true, failure: null }), 'started')
  assert.deepEqual(t.observe({ running: false, failure: null }), { ended: true, failed: false })
})

test('turn: a started send is watched from its running flag until it drops', () => {
  const t = createTurnTracker()
  t.observe({ running: false, failure: null })
  assert.equal(t.arm({ started: true, running: true, failure: null }), 'started')
  assert.equal(t.observe({ running: true, failure: null }), null)
  assert.deepEqual(t.observe({ running: false, failure: null }), { ended: true, failed: false })
  assert.equal(t.observe({ running: false, failure: null }), null)
})

test('turn: running that shows up after the send resolved starts the turn', () => {
  const t = createTurnTracker()
  t.observe({ running: false, failure: null })
  assert.equal(t.arm({ started: true, running: false, failure: null }), null)
  assert.equal(t.observe({ running: true, failure: null }), 'started')
  assert.deepEqual(t.observe({ running: false, failure: null }), { ended: true, failed: false })
})

test('turn: a new failure during the turn marks it failed', () => {
  const t = createTurnTracker()
  const old = { code: 'old' }
  t.observe({ running: false, failure: old })
  t.arm({ started: true, running: true, failure: old })
  assert.deepEqual(t.observe({ running: false, failure: { code: 'new' } }), { ended: true, failed: true })
})

test('turn: an old failure that stays set does not fail the turn', () => {
  const t = createTurnTracker()
  const old = { code: 'old' }
  t.observe({ running: false, failure: old })
  t.arm({ started: true, running: true, failure: old })
  assert.deepEqual(t.observe({ running: false, failure: old }), { ended: true, failed: false })
})

test('turn: reset forgets the armed turn', () => {
  const t = createTurnTracker()
  t.observe({ running: false, failure: null })
  t.arm({ started: true, running: true, failure: null })
  t.reset()
  assert.equal(t.observe({ running: false, failure: null }), null)
})
