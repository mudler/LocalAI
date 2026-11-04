import test from 'node:test'
import assert from 'node:assert/strict'
import { emptyLive, toolStart, toolEnd, cardResult } from '../../src/utils/chatLive.js'
import {
  threadRows, placeProposals, streamingParts, toolLine, toolOutput, lastUserText, chatMarkdown, markdownFileName, menuChats,
} from '../../src/utils/chatThread.js'

const app = { server: 's', tool: 'clock', resource_uri: 'ui://c', visibility: ['model', 'app'] }

test('chat thread: reasoning and tools fold into the answer that follows them', () => {
  const rows = threadRows([
    { seq: 1, role: 'user', text: 'hi' },
    { seq: 2, role: 'reasoning', text: 'think' },
    { seq: 3, role: 'tool', text: 'read_file: /a\nbody' },
    { seq: 4, role: 'assistant', text: 'done' },
    { seq: 5, role: 'checkpoint', text: '' },
  ], emptyLive())
  assert.deepEqual(rows.map((r) => r.kind), ['msg', 'msg'])
  assert.equal(rows[1].activity.length, 2)
  assert.equal(rows[0].activity, null)
})

test('chat thread: activity with no answer after it stands alone', () => {
  const rows = threadRows([
    { seq: 1, role: 'user', text: 'hi' },
    { seq: 2, role: 'tool', text: 'bash: ls' },
  ], emptyLive())
  assert.deepEqual(rows.map((r) => r.kind), ['msg', 'activity'])
})

test('chat thread: an app call is its own row with the same key live and stored', () => {
  let live = toolStart(emptyLive(), { name: 'clock', summary: '' })
  live = toolEnd(live, { name: 'clock', ok: true, seq: 3, app })
  live = cardResult(live, 3, { ...app, input: {} })
  const during = threadRows([{ seq: 1, role: 'user', text: 'time?' }], live)
  const after = threadRows([
    { seq: 1, role: 'user', text: 'time?' },
    { seq: 2, role: 'reasoning', text: 'r' },
    { seq: 3, role: 'tool', text: 'clock: now', app },
  ], live)
  const liveApp = during.find((r) => r.kind === 'app')
  const storedApp = after.find((r) => r.kind === 'app')
  assert.equal(liveApp.key, 'tool-3')
  assert.equal(storedApp.key, 'tool-3')
  assert.equal(storedApp.live, false)
  assert.equal(after.filter((r) => r.kind === 'app').length, 1)
  assert.deepEqual(after.map((r) => r.kind), ['msg', 'activity', 'app'])
})

test('chat thread: the streaming turn leaves app calls out', () => {
  let live = toolStart(emptyLive(), { name: 'bash', summary: 'ls' })
  live = toolStart(live, { name: 'clock', summary: '' })
  live = toolEnd(live, { name: 'clock', ok: true, seq: 4, app })
  live = { ...live, text: 'hello', reasoning: 'hmm' }
  const parts = streamingParts(live)
  assert.equal(parts.text, 'hello')
  assert.equal(parts.reasoning, 'hmm')
  assert.deepEqual(parts.tools.map((t) => [t.type, t.name]), [['tool_call', 'bash']])
})

test('chat thread: tool lines, last user text and the markdown export', () => {
  assert.deepEqual(toolLine('read_file: /a/b\nout'), { name: 'read_file', summary: '/a/b' })
  assert.deepEqual(toolLine('bash'), { name: 'bash', summary: '' })
  assert.equal(toolOutput('x: y\nline1\nline2'), 'line1\nline2')
  assert.equal(toolOutput('x'), '')
  assert.equal(lastUserText([{ role: 'user', text: 'a' }, { role: 'assistant', text: 'b' }, { role: 'user', text: 'c' }]), 'c')
  assert.equal(lastUserText([]), '')
  const md = chatMarkdown({ title: 'T', model: 'm' }, [
    { role: 'user', text: 'q' }, { role: 'tool', text: 'bash: ls\nout' }, { role: 'assistant', text: 'a' },
  ])
  assert.match(md, /^# T\n/)
  assert.match(md, /## User\n\nq/)
  assert.match(md, /> Tool: bash: ls\n/)
  assert.doesNotMatch(md, /out/)
  assert.equal(markdownFileName('My chat!'), 'My_chat_.md')
})

test('chat thread: server chats in the menu shape', () => {
  const [c] = menuChats([{ id: 'a', title: 'T', model: 'm', running: true, created: '2026-01-01T00:00:00Z', updated: '2026-01-02T00:00:00Z' }])
  assert.equal(c.name, 'T')
  assert.equal(c.running, true)
  assert.equal(c.updatedAt, Date.parse('2026-01-02T00:00:00Z'))
  assert.deepEqual([c.origin, c.originId, c.lastTurn], ['', '', ''])
  const [b] = menuChats([{ id: 'b', origin: 'connector', origin_id: 'b1', last_turn: 'paused' }])
  assert.deepEqual([b.origin, b.originId, b.lastTurn], ['connector', 'b1', 'paused'])
})

// Where the cards of proposals go: after the words of the turn.
const note = (seq, id) => ({ seq, role: 'tool', text: 'propose_memory: x', card: { kind: 'change_proposal', id } })
const forRow = (r) => (r.proposals || []).map((m) => m.card.id)

test('placeProposals: the card of a turn goes on its last model message, not the first', () => {
  const rows = placeProposals(threadRows([
    { seq: 1, role: 'user', text: 'remember it' },
    note(2, 'a'),
    { seq: 3, role: 'assistant', text: 'I will check first.' },
    { seq: 4, role: 'tool', text: 'read_file: /n\nbody' },
    { seq: 5, role: 'assistant', text: 'Here is the change.' },
  ], emptyLive()))
  assert.deepEqual(rows.map((r) => r.kind), ['msg', 'msg', 'msg'])
  assert.deepEqual(rows.map(forRow), [[], [], ['a']])
  // The tool lines keep their place in the fold.
  assert.equal(rows[1].activity.length, 1)
})

test('placeProposals: several pending cards keep their order', () => {
  const rows = placeProposals(threadRows([
    { seq: 1, role: 'user', text: 'x' },
    note(2, 'a'), { seq: 3, role: 'assistant', text: 'one' },
    note(4, 'b'), { seq: 5, role: 'assistant', text: 'two' },
  ], emptyLive()))
  assert.deepEqual(rows.map(forRow), [[], [], ['a', 'b']])
})

test('placeProposals: a streaming turn gets a row of its own after the text', () => {
  const rows = placeProposals(threadRows([
    { seq: 1, role: 'user', text: 'x' },
    note(2, 'a'), { seq: 3, role: 'assistant', text: 'early' },
  ], emptyLive()), { streaming: true })
  assert.deepEqual(rows.map((r) => r.kind), ['msg', 'msg', 'proposals'])
  assert.deepEqual(rows[2].items.map((m) => m.card.id), ['a'])
  assert.deepEqual(rows.map(forRow), [[], [], []])
})

test('placeProposals: a turn with no message yet gets the row, and turns stay apart', () => {
  const rows = placeProposals(threadRows([
    { seq: 1, role: 'user', text: 'x' },
    note(2, 'a'), { seq: 3, role: 'assistant', text: 'ok' },
    { seq: 4, role: 'user', text: 'y' },
    note(5, 'b'),
  ], emptyLive()))
  assert.deepEqual(rows.map((r) => r.kind), ['msg', 'msg', 'msg', 'activity', 'proposals'])
  assert.deepEqual(forRow(rows[1]), ['a'])
  assert.deepEqual(rows[4].items.map((m) => m.card.id), ['b'])
  assert.deepEqual(forRow(rows[3]), [])
})

test('placeProposals: trigger cards and turns without cards are left alone', () => {
  const plain = threadRows([{ seq: 1, role: 'user', text: 'x' }, { seq: 2, role: 'assistant', text: 'y' }], emptyLive())
  assert.deepEqual(placeProposals(plain).map((r) => r.kind), ['msg', 'msg'])
  const rows = placeProposals(threadRows([
    { seq: 1, role: 'user', text: 'x' },
    { seq: 2, role: 'tool', text: 'propose_trigger: d', card: { kind: 'trigger_proposal', id: 'p1' } },
    { seq: 3, role: 'assistant', text: 'ok' },
  ], emptyLive()))
  assert.deepEqual(forRow(rows[1]), ['p1'])
})
