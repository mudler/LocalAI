import test from 'node:test'
import assert from 'node:assert/strict'
import { loadSeries, sparkPoints } from '../../src/utils/nodeLoad.js'

test('nodeload: fewer than two readings draw nothing', () => {
  assert.deepEqual(loadSeries({}), [])
  assert.deepEqual(loadSeries({ load_history: [] }), [])
  assert.deepEqual(loadSeries({ load_history: [40] }), [])
  assert.deepEqual(loadSeries({ load_history: 'x' }), [])
})

test('nodeload: readings are clamped and non-numbers dropped', () => {
  assert.deepEqual(loadSeries({ load_history: [10, null, 'a', 150, -5, NaN] }), [10, 100, 0])
})

test('nodeload: points run left to right, the highest reading at the top', () => {
  assert.equal(sparkPoints([0, 100], 100, 24, 2), '2.0,22.0 98.0,2.0')
  // A small move keeps a minimum span of 20 points: 10 points is half the height.
  assert.equal(sparkPoints([50, 60], 100, 24, 2), '2.0,22.0 98.0,12.0')
  assert.equal(sparkPoints([50], 100, 24), '')
})
