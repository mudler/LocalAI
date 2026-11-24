import test from 'node:test'
import assert from 'node:assert/strict'
import { newOptimistic, adoptedSeq, withOptimistic } from '../../src/utils/optimisticSend.js'

const history = [
  { seq: 1, role: 'user', text: 'one' },
  { seq: 2, role: 'assistant', text: 'two' },
]

test('optimistic send: the message is the last line until the server has it', () => {
  const entry = newOptimistic({ chatId: 'c1', text: 'three', base: 1 })
  const out = withOptimistic(history, entry, 'c1')
  assert.equal(out.length, 3)
  assert.equal(out[2].text, 'three')
  assert.equal(out[2].rowKey, entry.key)
  assert.equal(out[2].sending, true)
})

test('optimistic send: the server line takes the row key, and the message shows once', () => {
  const entry = newOptimistic({ chatId: 'c1', text: 'three', base: 1 })
  const server = [...history, { seq: 3, role: 'user', text: 'three' }]
  const out = withOptimistic(server, entry, 'c1')
  assert.equal(out.length, 3)
  assert.equal(out[2].seq, 3)
  assert.equal(out[2].rowKey, entry.key)
  assert.equal(out[2].sending, false)
  assert.equal(adoptedSeq(entry, server), 3)
})

test('optimistic send: once the line is known the key follows it, whatever the count does', () => {
  const entry = { ...newOptimistic({ chatId: 'c1', text: 'three', base: 1 }), seq: 3, status: 'sent' }
  const server = [{ seq: 3, role: 'user', text: 'three' }]
  assert.equal(withOptimistic(server, entry, 'c1')[0].rowKey, entry.key)
  assert.equal(withOptimistic(history, entry, 'c1').length, 2)
})

test('optimistic send: a message that waits for a running turn leaves the transcript alone', () => {
  const entry = { ...newOptimistic({ chatId: 'c1', text: 'later', base: 1 }), status: 'sent' }
  assert.deepEqual(withOptimistic(history, entry, 'c1'), history)
})

test('optimistic send: another chat does not show it, a new chat does until it has an id', () => {
  const entry = newOptimistic({ chatId: null, text: 'first' })
  assert.equal(withOptimistic([], entry, null).length, 1)
  assert.equal(withOptimistic([], entry, 'c9').length, 0)
  const bound = { ...entry, chatId: 'c9' }
  assert.equal(withOptimistic([], bound, 'c9').length, 1)
  assert.equal(withOptimistic([], bound, 'c1').length, 0)
})

test('optimistic send: a message that failed stays as a fading line, with its files', () => {
  const entry = { ...newOptimistic({ chatId: 'c1', text: 'x', files: [{ name: 'a.txt', size: 3, type: 'text/plain' }], base: 1 }), status: 'failed' }
  const line = withOptimistic(history, entry, 'c1')[2]
  assert.equal(line.failed, true)
  assert.equal(line.sending, false)
  assert.equal(line.attachments[0].name, 'a.txt')
})
