import test from 'node:test'
import assert from 'node:assert/strict'
import { compactCount } from '../../src/utils/modelSearch.js'

test('model search: counts are compact', () => {
  assert.equal(compactCount(0), '')
  assert.equal(compactCount(undefined), '')
  assert.equal(compactCount(950), '950')
  assert.equal(compactCount(1234), '1.2k')
  assert.equal(compactCount(3400000), '3.4M')
  assert.equal(compactCount(2e9), '2B')
})
