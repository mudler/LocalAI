import test from 'node:test'
import assert from 'node:assert/strict'
import {
  emptyLive, toolStart, toolEnd, cardResult, cardFromMessages, conversationItems, toolKey, liveCardWanted,
} from '../../src/utils/chatLive.js'

const ref = { server: 'connector:a', tool: 'get-time', resource_uri: 'ui://clock/app.html', visibility: ['model', 'app'] }
const full = { ...ref, input: { zone: 'UTC' }, result: { content: [{ type: 'text', text: '12:00' }] } }

test('chat live: a tool_end with an app and a seq waits for its card; one without a seq cannot be read', () => {
  let live = toolStart(emptyLive(), { name: 'get-time', summary: '' })
  live = toolEnd(live, { name: 'get-time', ok: true, output: '12:00', seq: 7, app: ref })
  assert.equal(live.tools[0].state, 'done')
  assert.equal(live.tools[0].seq, 7)
  assert.equal(live.tools[0].appState, 'loading')
  assert.equal(liveCardWanted({ name: 'get-time', ok: true, seq: 7, app: ref }), true)
  assert.equal(liveCardWanted({ name: 'get-time', ok: true, seq: 0, app: ref }), false)
  assert.equal(liveCardWanted({ name: 'bash', ok: true, seq: 3 }), false)
  assert.equal(liveCardWanted({ name: 'get-time', ok: false, seq: 3, app: ref }), false)

  let lost = toolStart(emptyLive(), { name: 'get-time', summary: '' })
  lost = toolEnd(lost, { name: 'get-time', ok: true, output: 'x', seq: 0, app: ref })
  assert.equal(lost.tools[0].appState, 'failed')
})

test('chat live: a tool without an app has no card state', () => {
  let live = toolStart(emptyLive(), { name: 'bash', summary: 'ls' })
  live = toolEnd(live, { name: 'bash', ok: true, output: 'a', seq: 3 })
  assert.equal(live.tools[0].appState, '')
  assert.equal(live.tools[0].seq, 3)
})

test('chat live: the card arrives for the call with that seq, or its failure does', () => {
  let live = toolStart(emptyLive(), { name: 'get-time', summary: '' })
  live = toolEnd(live, { name: 'get-time', ok: true, output: 'a', seq: 4, app: ref })
  live = toolStart(live, { name: 'get-time', summary: '' })
  live = toolEnd(live, { name: 'get-time', ok: true, output: 'b', seq: 6, app: ref })
  const ok = cardResult(live, 6, full)
  assert.equal(ok.tools[1].appState, 'ready')
  assert.deepEqual(ok.tools[1].app, full)
  assert.equal(ok.tools[0].appState, 'loading')
  const bad = cardResult(live, 4, null)
  assert.equal(bad.tools[0].appState, 'failed')
  assert.equal(bad.tools[0].app, undefined)
  assert.equal(cardResult(live, 99, full), live) // a call that is not in the turn any more
})

test('chat live: the card is found by seq among the lines read', () => {
  const lines = [{ seq: 6, role: 'tool', app: full }, { seq: 7, role: 'tool' }]
  assert.deepEqual(cardFromMessages(lines, 6), full)
  assert.equal(cardFromMessages(lines, 7), null)
  assert.equal(cardFromMessages(lines, 8), null)
  assert.equal(cardFromMessages(undefined, 6), null)
})

test('chat live: items are the transcript, then what only the live turn has, with stable keys', () => {
  const messages = [
    { seq: 1, role: 'user', text: 'time?' },
    { seq: 2, role: 'checkpoint' },
  ]
  let live = toolStart(emptyLive(), { name: 'bash', summary: 'ls' })
  live = toolEnd(live, { name: 'bash', ok: true, output: '', seq: 3 })
  live = toolStart(live, { name: 'get-time', summary: '' })
  live = toolEnd(live, { name: 'get-time', ok: true, output: '12:00', seq: 4, app: ref })
  live = toolStart(live, { name: 'bash', summary: 'pwd' })
  live = { ...live, reasoning: 'hmm', text: 'It is' }
  const items = conversationItems(messages, live)
  assert.deepEqual(items.map(i => i.key), [1, 2, 'live-reasoning', 'live-tools-0', toolKey(4), 'live-tools-2', 'live-text'])
  assert.deepEqual(items.map(i => i.type), ['message', 'message', 'reasoning', 'tools', 'app', 'tools', 'text'])
})

test('chat live: when the transcript has a call, the live copy is gone and the key is the same', () => {
  let live = toolStart(emptyLive(), { name: 'get-time', summary: '' })
  live = toolEnd(live, { name: 'get-time', ok: true, output: '12:00', seq: 4, app: ref })
  live = cardResult(live, 4, full)
  const before = conversationItems([{ seq: 1, role: 'user', text: 'q' }], live)
  const after = conversationItems([{ seq: 1, role: 'user', text: 'q' }, { seq: 4, role: 'tool', text: 'get-time: \n12:00', app: full }], emptyLive())
  const liveKey = before.find(i => i.type === 'app').key
  const tx = after.find(i => i.type === 'message' && i.m.role === 'tool').key
  assert.equal(liveKey, tx)
  // Read again in mid-turn, the call is in both: it shows once.
  const both = conversationItems([{ seq: 1, role: 'user', text: 'q' }, { seq: 4, role: 'tool', text: 't', app: full }], live)
  assert.deepEqual(both.map(i => i.key), [1, toolKey(4)])
  assert.equal(new Set(both.map(i => i.key)).size, both.length)
})

test('chat live: a call whose entry was not stored keeps a key of its own', () => {
  let live = toolStart(emptyLive(), { name: 'get-time', summary: '' })
  live = toolEnd(live, { name: 'get-time', ok: true, output: 'x', seq: 0, app: ref })
  const items = conversationItems([], live)
  assert.equal(items.length, 1)
  assert.equal(items[0].type, 'app')
  assert.equal(items[0].key, 'live-tool-0')
})
