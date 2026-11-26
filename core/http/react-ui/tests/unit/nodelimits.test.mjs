import { test } from 'node:test'
import assert from 'node:assert/strict'
import { draftOf, changesOf } from '../../src/components/nodes/nodeLimits.js'

const limits = { max_active_backends: 2, vram_budget: '', memory_reclaimer_threshold: 0.9, model_swap: true }

test('node limits: an untouched draft sends nothing', () => {
  assert.deepEqual(changesOf(limits, draftOf(limits)), {})
})

test('node limits: only the changed keys are sent, in the API shape', () => {
  const draft = { ...draftOf(limits), model_swap: false, memory_reclaimer_threshold: '80', vram_budget: ' 12GB ' }
  assert.deepEqual(changesOf(limits, draft), { model_swap: false, memory_reclaimer_threshold: 0.8, vram_budget: '12GB' })
})

test('node limits: a value out of range is not sent', () => {
  const draft = { ...draftOf(limits), max_active_backends: '-1', memory_reclaimer_threshold: '150' }
  assert.deepEqual(changesOf(limits, draft), {})
})
