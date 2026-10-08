import assert from 'node:assert/strict'
import test from 'node:test'

import { conversationsFromChats, dayBucket, groupConversations } from './homeConversations.js'

const DAY = 24 * 60 * 60 * 1000
const NOW = new Date(2026, 9, 7, 15, 0, 0).getTime()

const chat = (id, name, history, updatedAt, extra = {}) => ({ id, name, model: 'm', history, updatedAt, ...extra })
const pair = (q, a) => [{ role: 'user', content: q }, { role: 'assistant', content: a }]

test('only chats with a message count, newest first, with the reply as the preview', () => {
  const rows = conversationsFromChats([
    chat('empty', 'New Chat', [], NOW),
    chat('old', 'Older', pair('q1', 'a1'), NOW - 3 * DAY),
    chat('new', 'Newer', [...pair('q2', 'a2'), { role: 'user', content: 'thanks' }], NOW - 1000),
  ])
  assert.deepEqual(rows.map(r => r.id), ['new', 'old'])
  assert.equal(rows[0].preview, 'a2')
  assert.equal(rows[0].count, 3)
  assert.equal(rows[1].title, 'Older')
})

test('a chat that is still called New Chat takes its first question as the title', () => {
  const [row] = conversationsFromChats([chat('c', 'New Chat', pair('How do I size a context?', 'Start at 8k.'), NOW)])
  assert.equal(row.title, 'How do I size a context?')
})

test('an image anywhere in the chat is flagged, and reasoning is left out of the preview', () => {
  const [row] = conversationsFromChats([chat('c', 'Pic', [
    { role: 'user', content: [{ type: 'text', text: 'what is this' }, { type: 'image_url', image_url: { url: 'x' } }] },
    { role: 'assistant', content: '<think>hmm</think>A diagram.' },
  ], NOW)])
  assert.equal(row.hasImage, true)
  assert.equal(row.preview, 'A diagram.')
})

test('days group as today, yesterday, this week and older, keeping the order', () => {
  assert.equal(dayBucket(NOW - 1000, NOW), 'today')
  assert.equal(dayBucket(NOW - DAY, NOW), 'yesterday')
  assert.equal(dayBucket(NOW - 4 * DAY, NOW), 'week')
  assert.equal(dayBucket(NOW - 9 * DAY, NOW), 'older')
  const groups = groupConversations([
    { id: 'a', updatedAt: NOW - 1000 }, { id: 'b', updatedAt: NOW - 2000 },
    { id: 'c', updatedAt: NOW - DAY }, { id: 'd', updatedAt: NOW - 9 * DAY },
  ], NOW)
  assert.deepEqual(groups.map(g => [g.key, g.items.map(i => i.id)]), [
    ['today', ['a', 'b']], ['yesterday', ['c']], ['older', ['d']],
  ])
})
