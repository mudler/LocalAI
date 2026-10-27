import test from 'node:test'
import assert from 'node:assert/strict'
import { emptyLive, toolStart, toolEnd, cardResult } from '../../src/utils/chatLive.js'
import {
  threadRows, streamingParts, toolLine, toolOutput, lastUserText, chatMarkdown, markdownFileName, menuChats,
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
})
