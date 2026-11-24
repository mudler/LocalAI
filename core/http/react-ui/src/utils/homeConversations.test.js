import assert from 'node:assert/strict'
import test from 'node:test'

import { dayBucket, groupConversations, resumeItems } from './homeConversations.js'

const DAY = 24 * 60 * 60 * 1000
const NOW = new Date(2026, 9, 7, 15, 0, 0).getTime()

test('the resume rows are the chats a person started, newest first, with a title', () => {
  const rows = resumeItems([
    { id: 'old', title: 'Older', model: 'm', updated: new Date(NOW - 2 * DAY).toISOString() },
    { id: 'sub', title: 'Run', kind: 'subagent', updated: new Date(NOW).toISOString() },
    { id: 'new', title: '  ', model: 'm2', updated: new Date(NOW - 1000).toISOString() },
  ])
  assert.deepEqual(rows.map(r => [r.id, r.title, r.model]), [['new', 'New chat', 'm2'], ['old', 'Older', 'm']])
  assert.equal(rows[0].updatedAt, NOW - 1000)
  assert.deepEqual(resumeItems(undefined), [])
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
