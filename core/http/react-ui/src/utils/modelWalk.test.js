import assert from 'node:assert/strict'
import test from 'node:test'

import { modelPath, publishWalk, resetWalk, walkFor } from './modelWalk.js'

test('a model in the middle of the published list has both neighbours and a position', () => {
  publishWalk('explore', ['a', 'b', 'c', 'd'])
  assert.deepEqual(walkFor('b'), { view: 'explore', index: 1, total: 4, previous: 'a', next: 'c' })
})

test('the ends of the list have one neighbour', () => {
  publishWalk('installed', ['a', 'b', 'c'])
  assert.equal(walkFor('a').previous, null)
  assert.equal(walkFor('a').next, 'b')
  assert.equal(walkFor('c').next, null)
  assert.equal(walkFor('c').view, 'installed')
})

test('a model the list does not hold has no walker, and neither does a list of one', () => {
  publishWalk('explore', ['a', 'b'])
  assert.equal(walkFor('z'), null)
  publishWalk('explore', ['a'])
  assert.equal(walkFor('a'), null)
})

test('with nothing published there is no walker', () => {
  resetWalk()
  assert.equal(walkFor('a'), null)
})

test('a model page is addressed by its encoded id', () => {
  assert.equal(modelPath('qwen3-8b'), '/app/models/qwen3-8b')
  assert.equal(modelPath('org/name@v1'), '/app/models/org%2Fname%40v1')
})
