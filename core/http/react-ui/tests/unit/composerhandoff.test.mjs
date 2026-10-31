import test from 'node:test'
import assert from 'node:assert/strict'
import {
  offerHandoff, peekHandoff, clearHandoff, handoffSeed, handoffMessage, withHandoff, whenArrived, markArrived, HANDOFF_ROW,
} from '../../src/utils/composerHandoff.js'
import { threadRows } from '../../src/utils/chatThread.js'
import { emptyLive } from '../../src/utils/chatLive.js'

const chat = { id: 'c1', model: 'qwen3-8b-instruct', title: 'New chat', kind: 'chat' }
const files = [
  { name: 'notes.txt', size: 17, type: 'text/plain' },
  { name: 'shot.png', size: 900, type: 'image/png', url: 'blob:local/1' },
]

test('composer handoff: one hand-off, read by its chat only, without being taken', () => {
  offerHandoff({ chat, text: 'hi', files: [], started: true }, 1000)
  assert.equal(peekHandoff('other', 1000), null)
  assert.equal(peekHandoff('c1', 1001).text, 'hi')
  // A second render reads it again.
  assert.equal(peekHandoff('c1', 1002).text, 'hi')
  clearHandoff('other')
  assert.ok(peekHandoff('c1', 1003))
  clearHandoff('c1')
  assert.equal(peekHandoff('c1', 1004), null)
})

test('composer handoff: a newer hand-off replaces the old one and an old one expires', () => {
  offerHandoff({ chat, text: 'one' }, 0)
  offerHandoff({ chat: { ...chat, id: 'c2' }, text: 'two' }, 0)
  assert.equal(peekHandoff('c1', 0), null)
  assert.equal(peekHandoff('c2', 0).text, 'two')
  assert.equal(peekHandoff('c2', 10_001), null)
  // Without a chat id there is nothing to hand over.
  assert.equal(offerHandoff({ text: 'x' }), null)
  clearHandoff()
})

test('composer handoff: the seed opens the chat with the turn the send started', () => {
  assert.equal(handoffSeed(null), null)
  const seed = handoffSeed({ chat, text: 'hi', started: true })
  assert.equal(seed.id, 'c1')
  assert.equal(seed.detail.running, true)
  assert.equal(seed.detail.model, 'qwen3-8b-instruct')
  assert.deepEqual(seed.detail.pending, [])
  assert.equal(handoffSeed({ chat, text: 'hi', started: false }).detail.running, false)
})

test('composer handoff: the message is drawn until the transcript holds it', () => {
  const h = { chat, text: 'What changed?', files }
  const before = withHandoff([], h, 'c1')
  assert.equal(before.length, 1)
  assert.equal(before[0].role, 'user')
  assert.equal(before[0].text, 'What changed?')
  assert.equal(before[0].rowKey, HANDOFF_ROW)
  assert.deepEqual(before[0].attachments.map(a => [a.name, a.path, a.url]), [['notes.txt', '', undefined], ['shot.png', '', 'blob:local/1']])
  assert.deepEqual(handoffMessage(h), before[0])
})

test('composer handoff: the server copy takes the row and keeps the local image', () => {
  const h = { chat, text: 'What changed?', files }
  const server = [
    { seq: 4, turn: 1, role: 'user', text: 'What changed?', attachments: [{ path: '/Chats/a/shot.png', name: 'shot.png', mime: 'image/png' }] },
    { seq: 5, turn: 1, role: 'assistant', text: 'Lots.' },
  ]
  const after = withHandoff(server, h, 'c1')
  assert.equal(after.length, 2)
  assert.equal(after[0].seq, 4)
  assert.equal(after[0].rowKey, HANDOFF_ROW)
  assert.equal(after[0].attachments[0].path, '/Chats/a/shot.png')
  assert.equal(after[0].attachments[0].url, 'blob:local/1')
  assert.equal(after[1], server[1])
  // The input is not changed.
  assert.equal(server[0].rowKey, undefined)
  // The row key is the same before and after: React keeps the row.
  const rowsBefore = threadRows(withHandoff([], h, 'c1'), emptyLive())
  const rowsAfter = threadRows(after, emptyLive())
  assert.equal(rowsBefore[0].key, rowsAfter[0].key)
  assert.equal(rowsAfter[1].key, 'm-5')
})

test('composer handoff: another chat, or none, is left as it is', () => {
  const list = [{ seq: 1, role: 'user', text: 'x' }]
  assert.equal(withHandoff(list, null, 'c1'), list)
  assert.equal(withHandoff(list, { chat, text: 'y' }, 'c9'), list)
  assert.deepEqual(withHandoff(undefined, null, 'c1'), [])
})

test('composer handoff: the wait for the chat page ends when it arrives or times out', async () => {
  const arrived = whenArrived('c1', 1000)
  markArrived('other')
  markArrived('c1')
  assert.equal(await arrived, true)
  assert.equal(await whenArrived('c1', 5), false)
  // A second wait for the same chat releases the first.
  const first = whenArrived('c1', 1000)
  const second = whenArrived('c1', 1000)
  assert.equal(await first, false)
  markArrived('c1')
  assert.equal(await second, true)
})
