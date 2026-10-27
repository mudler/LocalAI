import test from 'node:test'
import assert from 'node:assert/strict'
import { eventPayload, retryDelay, RETRY_CAP_MS } from '../../src/utils/streamRetry.js'

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
