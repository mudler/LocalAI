import test from 'node:test'
import assert from 'node:assert/strict'
import { percentOf, ACTIVE } from '../src/lib/progress.js'

test('a final status reads as a known percent: completed is 100', () => {
  assert.equal(percentOf({ status: 'completed', progress_percent: 0 }), 100)
})

test('a running job with a percent of 0 is unknown, not 0', () => {
  for (const status of ACTIVE) assert.equal(percentOf({ status, progress_percent: 0 }), null, status)
  assert.equal(percentOf({ status: 'training' }), null)
  assert.equal(percentOf({ status: 'training', progress_percent: 0, current_step: 0, total_steps: 100 }), null)
})

test('a running job with a percent above 0 reports it, capped at 100', () => {
  assert.equal(percentOf({ status: 'training', progress_percent: 42.4 }), 42.4)
  assert.equal(percentOf({ status: 'training', progress_percent: 140 }), 100)
})

test('a running job with steps but no percent is computed from the steps', () => {
  assert.equal(percentOf({ status: 'training', progress_percent: 0, current_step: 25, total_steps: 100 }), 25)
})

test('no event is unknown', () => {
  assert.equal(percentOf(null), null)
  assert.equal(percentOf(undefined), null)
})

test('a failed or stopped job keeps the percent it reported, and 0 there is a real 0', () => {
  assert.equal(percentOf({ status: 'failed', progress_percent: 0 }), 0)
  assert.equal(percentOf({ status: 'stopped', progress_percent: 30 }), 30)
})
