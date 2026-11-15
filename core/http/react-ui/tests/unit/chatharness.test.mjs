import test from 'node:test'
import assert from 'node:assert/strict'
import { harnessProblem, contextLadder, pickWindow, modelsWithWindow } from '../../src/utils/chatHarness.js'

const GIB = 1024 ** 3
const budget = (gib) => ({ totalMemory: gib * GIB, hasGpu: true, scope: 'local' })
const ADMIN = { minimum: 24576, recommended: 32768 }
const USER = { minimum: 16384, recommended: 32768 }

test('harness: nothing to show when the chat is fine or the server said nothing', () => {
  assert.equal(harnessProblem(null), null)
  assert.equal(harnessProblem(undefined), null)
  assert.equal(harnessProblem({ ok: true, window: 32768, minimum: 16384, recommended: 32768 }), null)
  assert.equal(harnessProblem({ minimum: 16384 }), null)
  const bad = { ok: false, window: 8192, minimum: 16384, recommended: 32768, can_fix: false }
  assert.equal(harnessProblem(bad), bad)
})

test('harness: the ladder runs from the recommended window down to the minimum, taking both from the server', () => {
  assert.deepEqual(contextLadder(ADMIN), [32768, 24576])
  assert.deepEqual(contextLadder(USER), [32768, 24576, 16384])
})

test('harness: the ladder stops at what the model was trained for', () => {
  assert.deepEqual(contextLadder({ ...USER, maxContext: 20480 }), [16384])
  assert.deepEqual(contextLadder({ ...USER, maxContext: 131072 }), [32768, 24576, 16384])
  // A model trained below the minimum cannot be fixed by a setting.
  assert.deepEqual(contextLadder({ ...USER, maxContext: 8192 }), [])
})

test('harness: the recommended window is offered when it fits this machine', () => {
  const ladder = contextLadder(USER)
  const pick = pickWindow({ ladder, readings: { 32768: { bytes: 10 * GIB } }, budget: budget(24) })
  assert.deepEqual(pick, { kind: 'fits', value: 32768, bytes: 10 * GIB, wanted: true })
})

test('harness: the largest window that fits is offered when the recommended one does not', () => {
  const ladder = contextLadder(USER)
  const readings = { 32768: { bytes: 30 * GIB }, 24576: { bytes: 20 * GIB }, 16384: { bytes: 12 * GIB } }
  const pick = pickWindow({ ladder, readings, budget: budget(16) })
  assert.equal(pick.kind, 'fits')
  assert.equal(pick.value, 16384)
  assert.equal(pick.wanted, false)
})

test('harness: nothing is offered when even the minimum does not fit, so a smaller model is suggested', () => {
  const ladder = contextLadder(USER)
  const readings = { 32768: { bytes: 30 * GIB }, 24576: { bytes: 20 * GIB }, 16384: { bytes: 18 * GIB } }
  assert.deepEqual(pickWindow({ ladder, readings, budget: budget(8) }), { kind: 'none' })
  assert.deepEqual(pickWindow({ ladder: [], readings: {}, budget: budget(8) }), { kind: 'none' })
})

test('harness: with no estimate at all the administrator may still choose the recommended window', () => {
  const ladder = contextLadder(USER)
  assert.deepEqual(pickWindow({ ladder, readings: { 32768: null, 24576: null, 16384: null }, budget: budget(8) }), { kind: 'unknown', value: 32768 })
  // No reading of this machine's memory: the estimate cannot say either.
  const pick = pickWindow({ ladder, readings: { 32768: { bytes: GIB } }, budget: { totalMemory: 0 } })
  assert.equal(pick.kind, 'none')
})

test('harness: a regular user is offered installed models with a large enough window, largest first', () => {
  const models = [
    { id: 'small', context_size: 8192 },
    { id: 'current', context_size: 65536 },
    { id: 'big', context_size: 65536 },
    { id: 'ok', context_size: 16384 },
    { id: 'off', context_size: 32768, disabled: true },
    { id: 'broken', context_size: 32768, alias_error: 'unknown target' },
    { id: 'unknown' },
  ]
  assert.deepEqual(modelsWithWindow(models, { minimum: 16384, current: 'current' }), [
    { id: 'big', window: 65536 },
    { id: 'ok', window: 16384 },
  ])
  assert.deepEqual(modelsWithWindow([{ id: 'small', context_size: 8192 }], { minimum: 16384, current: 'x' }), [])
  assert.deepEqual(modelsWithWindow(undefined, { minimum: 16384, current: 'x' }), [])
})
