import test from 'node:test'
import assert from 'node:assert/strict'
import { draftOf, draftProblem, same, valuesOf, DEFAULT_CHARS } from '../src/settings.js'

test('pikvm settings: a device with no saved switches is off and short', () => {
  assert.deepEqual(draftOf(undefined), { allowTyping: false, allowHardPower: false, maxTypeChars: String(DEFAULT_CHARS) })
  assert.deepEqual(draftOf({ allowTyping: true, allowHardPower: 'yes', maxTypeChars: 300 }), { allowTyping: true, allowHardPower: false, maxTypeChars: '300' })
})

test('pikvm settings: the length is a whole number from 1 to 1000', () => {
  for (const ok of ['1', '200', '1000']) assert.equal(draftProblem({ maxTypeChars: ok }), '')
  for (const bad of ['0', '1001', '-3', '1.5', '', 'abc']) assert.equal(draftProblem({ maxTypeChars: bad }), 'settings.maxCharsShape', bad)
})

test('pikvm settings: a draft is dirty only when something differs, and sends numbers', () => {
  const a = draftOf({ allowTyping: true, maxTypeChars: 200 })
  assert.equal(same(a, draftOf({ allowTyping: true, maxTypeChars: 200 })), true)
  assert.equal(same(a, { ...a, allowHardPower: true }), false)
  assert.deepEqual(valuesOf({ ...a, maxTypeChars: '350' }), { allowTyping: true, allowHardPower: false, maxTypeChars: 350 })
})
