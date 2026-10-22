import test from 'node:test'
import assert from 'node:assert/strict'
import { modelFrom } from '../src/lib/model.js'

test('the model comes from ?model first and keeps slashes, colons and percent signs', () => {
  assert.equal(modelFrom('model=org%2Fname%3Av1', 'ignored'), 'org/name:v1')
})

test('without ?model the model is the first path segment', () => {
  assert.equal(modelFrom('', 'speechbrain/extra'), 'speechbrain')
  assert.equal(modelFrom('x=1', 'speechbrain'), 'speechbrain')
})

test('no model gives an empty string', () => {
  assert.equal(modelFrom('', ''), '')
  assert.equal(modelFrom(undefined, undefined), '')
})
