import test from 'node:test'
import assert from 'node:assert/strict'
import { chatsListPath } from '../../src/utils/chatsQuery.js'
import { resumeItems } from '../../src/utils/homeConversations.js'

test('chats list: a request for chats asks the server to leave sub-agent runs out', () => {
  assert.equal(chatsListPath('/api/chats', 1, 'chat'), '/api/chats?limit=1&kind=chat')
  assert.equal(chatsListPath('/api/chats', 200, 'chat'), '/api/chats?limit=200&kind=chat')
  assert.equal(chatsListPath('/api/chats'), '/api/chats')
  assert.equal(chatsListPath('/api/chats', 5), '/api/chats?limit=5')
})

test('chats list: resume rows skip a sub-agent run that is the newest record', () => {
  const rows = resumeItems([
    { id: 'run', kind: 'subagent', parent_id: 'c1', updated: '2026-10-27T10:00:00Z' },
    { id: 'c1', title: 'Parent', kind: 'chat', updated: '2026-10-27T09:00:00Z' },
  ])
  assert.deepEqual(rows.map(r => r.id), ['c1'])
})
