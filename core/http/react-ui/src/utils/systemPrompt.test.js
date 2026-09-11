import assert from 'node:assert/strict'
import test from 'node:test'

import { effectiveSystemPrompt, shouldSendSystemPrompt } from './systemPrompt.js'

test('empty string is omitted', () => {
  assert.equal(effectiveSystemPrompt(''), '')
  assert.equal(shouldSendSystemPrompt(''), false)
})

test('whitespace-only is omitted', () => {
  assert.equal(effectiveSystemPrompt('   \n\t  '), '')
  assert.equal(shouldSendSystemPrompt(' \t'), false)
})

test('non-empty prompt is kept trimmed', () => {
  assert.equal(effectiveSystemPrompt('  You are helpful.  '), 'You are helpful.')
  assert.equal(shouldSendSystemPrompt('You are helpful.'), true)
})

test('non-strings are treated as empty', () => {
  assert.equal(effectiveSystemPrompt(null), '')
  assert.equal(effectiveSystemPrompt(undefined), '')
  assert.equal(shouldSendSystemPrompt(0), false)
})
