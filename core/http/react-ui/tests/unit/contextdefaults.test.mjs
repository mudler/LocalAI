import test from 'node:test'
import assert from 'node:assert/strict'
import { isUnsetContextSize, DEFAULT_CONTEXT_SIZE } from '../../src/utils/contextDefaults.js'
import MODEL_TEMPLATES from '../../src/utils/modelTemplates.js'
import { FIELD_BY_KEY } from '../../src/utils/settingsSchema.js'

test('context size: 0, empty and negative values mean unset', () => {
  for (const v of [0, '0', '', null, undefined, -1, 'abc', NaN]) assert.equal(isUnsetContextSize(v), true, String(v))
})

test('context size: a positive value is kept', () => {
  for (const v of [1, 2048, '4096']) assert.equal(isUnsetContextSize(v), false, String(v))
})

test('context size: the LLM template starts from an unset value', () => {
  const llm = MODEL_TEMPLATES.find(t => t.id === 'llm')
  assert.equal(isUnsetContextSize(llm.fields.context_size), true)
})

test('context size: the Settings placeholder shows the effective default', () => {
  assert.equal(FIELD_BY_KEY.context_size.placeholder, String(DEFAULT_CONTEXT_SIZE))
})
