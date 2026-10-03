import assert from 'node:assert/strict'
import test from 'node:test'

import { chatHistoryFor } from './agentChatHistory.js'

test('maps the visible user and agent turns to roles', () => {
  const history = chatHistoryFor([
    { id: 1, sender: 'user', content: 'draft an offer' },
    { id: 2, sender: 'agent', content: 'offer AG-1' },
  ])
  assert.deepEqual(history, [
    { role: 'user', content: 'draft an offer' },
    { role: 'assistant', content: 'offer AG-1' },
  ])
})

test('leaves out system notices, errors and empty messages', () => {
  const history = chatHistoryFor([
    { sender: 'system', content: 'Agent is processing' },
    { sender: 'agent', content: '   ' },
    { sender: 'user', content: 'kept' },
  ])
  assert.deepEqual(history, [{ role: 'user', content: 'kept' }])
})

test('a new or cleared conversation sends no history', () => {
  assert.deepEqual(chatHistoryFor([]), [])
  assert.deepEqual(chatHistoryFor(undefined), [])
})
