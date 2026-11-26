import test from 'node:test'
import assert from 'node:assert/strict'
import { withLoadedAliases } from '../../src/utils/loadedAliases.js'

const models = [
  { id: 'real-model' },
  { id: 'default', alias_of: 'real-model' },
  { id: 'other', alias_of: 'unloaded-model' },
  { id: 'broken', alias_error: 'target not found' },
  { id: 'broken-with-target', alias_of: 'real-model', alias_error: 'x' },
]

test('loaded aliases: an alias of a loaded model is warm', () => {
  const ids = withLoadedAliases(new Set(['real-model']), models)
  assert.ok(ids.has('default'))
  assert.ok(ids.has('real-model'))
})

test('loaded aliases: an alias of a model that is not loaded stays cold', () => {
  const ids = withLoadedAliases(new Set(['real-model']), models)
  assert.ok(!ids.has('other'))
  assert.ok(!ids.has('broken'))
  assert.ok(!ids.has('broken-with-target'))
})

test('loaded aliases: nothing loaded or no model list keeps the set as it is', () => {
  const empty = new Set()
  assert.equal(withLoadedAliases(empty, models), empty)
  const one = new Set(['real-model'])
  assert.equal(withLoadedAliases(one, undefined), one)
  assert.equal(withLoadedAliases(undefined, models).size, 0)
})

test('loaded aliases: the input set is not changed', () => {
  const src = new Set(['real-model'])
  withLoadedAliases(src, models)
  assert.equal(src.size, 1)
})
