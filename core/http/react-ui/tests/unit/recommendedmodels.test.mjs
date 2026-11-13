import test from 'node:test'
import assert from 'node:assert/strict'
import { splitRecommended, windowLabel, installRequest, fitTone } from '../../src/utils/recommendedModels.js'

const answer = {
  tier: 'gpu-mid',
  hardware: { detected: true, kind: 'gpu', scope: 'local' },
  nothing_fits: false,
  models: [
    { id: 'big', name: 'Big', role: 'default', context_size: 32768, fit: 'fits' },
    { id: 'mid', name: 'Mid', role: 'alternative', context_size: 24576, fit: 'tight' },
    { id: 'small', name: 'Small', role: 'alternative', context_size: 32768, fit: 'fits' },
  ],
}

test('recommended: the first model is the primary one and the rest are alternatives, in the server order', () => {
  const r = splitRecommended(answer)
  assert.equal(r.tier, 'gpu-mid')
  assert.equal(r.primary.id, 'big')
  assert.deepEqual(r.alternatives.map(m => m.id), ['mid', 'small'])
  assert.equal(r.detected, true)
  assert.equal(r.nothingFits, false)
})

test('recommended: no answer yet gives an empty split that a screen can draw nothing for', () => {
  for (const none of [null, undefined, {}, { models: null }]) {
    const r = splitRecommended(none)
    assert.equal(r.primary, null)
    assert.deepEqual(r.alternatives, [])
    assert.equal(r.tier, 'cpu')
  }
})

test('recommended: undetected hardware and a cluster node are passed through', () => {
  assert.equal(splitRecommended({ tier: 'cpu', hardware: { detected: false }, models: [] }).detected, false)
  const c = splitRecommended({ tier: 'gpu-large', hardware: { detected: true, scope: 'cluster', node_name: 'n1' }, models: [] })
  assert.equal(c.scope, 'cluster')
  assert.equal(c.nodeName, 'n1')
})

test('recommended: nothing fits is reported', () => {
  assert.equal(splitRecommended({ nothing_fits: true, models: [{ id: 'x', fit: 'does_not_fit' }] }).nothingFits, true)
})

test('recommended: the window is written the short way, and none when the server gave none', () => {
  assert.equal(windowLabel(32768), '32K')
  assert.equal(windowLabel(16384), '16K')
  assert.equal(windowLabel(24576), '24K')
  assert.equal(windowLabel(1536), '1.5K')
  assert.equal(windowLabel(512), '512')
  assert.equal(windowLabel(0), '')
  assert.equal(windowLabel(undefined), '')
  assert.equal(windowLabel(NaN), '')
})

test('recommended: an install pins the entry itself and carries the fitted window', () => {
  assert.deepEqual(installRequest({ id: 'qwen3.8-4b-q4', context_size: 24576 }), ['qwen3.8-4b-q4', 'qwen3.8-4b-q4', 24576])
})

test('recommended: an install without a window sends none, so the loader chooses', () => {
  assert.deepEqual(installRequest({ id: 'm', context_size: 0 }), ['m', 'm', undefined])
  assert.deepEqual(installRequest({ id: 'm' }), ['m', 'm', undefined])
})

test('recommended: a fit verdict has a tone', () => {
  assert.equal(fitTone('fits'), 'ok')
  assert.equal(fitTone('tight'), 'warn')
  assert.equal(fitTone('does_not_fit'), 'error')
  assert.equal(fitTone('unknown'), 'idle')
})
