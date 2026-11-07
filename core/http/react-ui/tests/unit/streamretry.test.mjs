import test from 'node:test'
import assert from 'node:assert/strict'
import { eventPayload, retryDelay, resumeUrl, RETRY_CAP_MS } from '../../src/utils/streamRetry.js'

test('stream retry: the wait doubles and stops at 30 seconds', () => {
  assert.deepEqual([0, 1, 2, 3, 4, 5, 6, 50].map(n => retryDelay(n)), [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000])
  assert.equal(retryDelay(0, 2000), 2000)
  assert.equal(retryDelay(3, 2000), 16000)
  assert.equal(retryDelay(-1), 1000)
  assert.equal(RETRY_CAP_MS, 30000)
})

test('stream retry: an event without data is not a message of the server', () => {
  assert.equal(eventPayload({ type: 'error' }), null)
  assert.equal(eventPayload({ data: '' }), null)
  assert.equal(eventPayload({ data: 'not json' }), null)
  assert.equal(eventPayload({ data: '3' }), null)
  assert.deepEqual(eventPayload({ data: '{"text":"boom"}' }), { text: 'boom' })
})

test('stream retry: a reconnect asks for the events after the last id of the same chat', () => {
  const url = '/api/chats/c1/events'
  assert.equal(resumeUrl(url, { chat: 'c1', id: 'ab12.57' }, 'c1'), '/api/chats/c1/events?after=ab12.57')
  assert.equal(resumeUrl('/x?y=1', { chat: 'c1', id: 'a b' }, 'c1'), '/x?y=1&after=a%20b')
})

test('stream retry: no resume point, or one of another chat, opens the plain stream', () => {
  const url = '/api/chats/c1/events'
  assert.equal(resumeUrl(url, null, 'c1'), url)
  assert.equal(resumeUrl(url, { chat: 'c1', id: '' }, 'c1'), url)
  assert.equal(resumeUrl(url, { chat: 'c2', id: 'ab12.57' }, 'c1'), url)
})
