import test from 'node:test'
import assert from 'node:assert/strict'
import { FIELD_BY_KEY, getValue, isChanged } from '../../src/utils/settingsSchema.js'
import { payloadFor } from '../../src/utils/settingsPending.js'

const FIELD = FIELD_BY_KEY.chats_tool_search

test('tool search setting: a select of auto, on and off in Settings > Tools, auto by default', () => {
  assert.ok(FIELD, 'the field is in the schema')
  assert.equal(FIELD.kind, 'select')
  assert.deepEqual(FIELD.options, ['auto', 'on', 'off'])
  assert.equal(FIELD.group, 'tools')
  assert.equal(FIELD.default, 'auto')
  assert.equal(getValue(FIELD, {}), 'auto')
  assert.equal(getValue(FIELD, { chats_tool_search: '' }), 'auto')
})

test('tool search setting: auto is the default, any other mode counts as changed and is sent as the setting', () => {
  assert.equal(isChanged(FIELD, {}), false)
  assert.equal(isChanged(FIELD, { chats_tool_search: 'auto' }), false)
  assert.equal(isChanged(FIELD, { chats_tool_search: 'on' }), true)
  assert.deepEqual(payloadFor([{ field: FIELD }], { chats_tool_search: 'off' }), { chats_tool_search: 'off' })
})
