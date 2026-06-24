import assert from 'node:assert/strict'
import test from 'node:test'

import { createTransferRateSampler } from './transferRate.js'

test('calculates speed and ETA from the oldest and newest samples in five seconds', () => {
  const sampler = createTransferRateSampler()

  assert.deepEqual(sampler.sample('job-1', 0, 10_000, 0), {})
  assert.deepEqual(sampler.sample('job-1', 2_000, 10_000, 2_000), {
    bytesPerSecond: 1_000,
    etaSeconds: 8,
  })
  assert.deepEqual(sampler.sample('job-1', 8_000, 10_000, 7_000), {
    bytesPerSecond: 1_200,
    etaSeconds: 2,
  })
})

test('resets samples after byte regression', () => {
  const sampler = createTransferRateSampler()

  sampler.sample('job-1', 4_000, 10_000, 0)
  sampler.sample('job-1', 6_000, 10_000, 1_000)
  assert.deepEqual(sampler.sample('job-1', 1_000, 10_000, 2_000), {})
  assert.deepEqual(sampler.sample('job-1', 2_000, 10_000, 3_000), {
    bytesPerSecond: 1_000,
    etaSeconds: 8,
  })
})

test('resets completed and invalid jobs', () => {
  const sampler = createTransferRateSampler()

  sampler.sample('job-1', 1_000, 10_000, 0)
  assert.deepEqual(sampler.sample('job-1', 10_000, 10_000, 1_000), {})
  assert.deepEqual(sampler.sample('job-1', 10_000, Number.NaN, 2_000), {})
  assert.deepEqual(sampler.sample('job-1', 11_000, 20_000, 3_000), {})
})

test('keeps job histories independent and removes replaced jobs', () => {
  const sampler = createTransferRateSampler()

  sampler.sample('old-job', 1_000, 10_000, 0)
  sampler.retain(['new-job'])
  assert.deepEqual(sampler.sample('old-job', 2_000, 10_000, 1_000), {})
  assert.deepEqual(sampler.sample('new-job', 1_000, 10_000, 1_000), {})
})

test('does not produce non-positive or non-finite rates', () => {
  const sampler = createTransferRateSampler()

  sampler.sample('job-1', 1_000, 10_000, 1_000)
  assert.deepEqual(sampler.sample('job-1', 1_000, 10_000, 2_000), {})

  sampler.reset('job-1')
  sampler.sample('job-1', 1_000, 10_000, 2_000)
  assert.deepEqual(sampler.sample('job-1', 2_000, 10_000, 2_000), {})
})

test('an invalid unnamed sample does not reset other jobs', () => {
  const sampler = createTransferRateSampler()

  sampler.sample('job-1', 1_000, 10_000, 0)
  sampler.sample(undefined, 1_000, 10_000, 500)
  assert.deepEqual(sampler.sample('job-1', 2_000, 10_000, 1_000), {
    bytesPerSecond: 1_000,
    etaSeconds: 8,
  })
})
