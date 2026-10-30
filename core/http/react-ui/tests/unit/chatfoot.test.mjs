import test from 'node:test'
import assert from 'node:assert/strict'
import {
  chatFootState, speedValue, liveRate, speedView, averageSpeed, pushUsage, showHint, toolShortName,
} from '../../src/utils/chatFoot.js'
import { contextReading } from '../../src/utils/chatSettings.js'

const live = (over = {}) => ({ text: '', reasoning: '', tools: [], phase: '', ...over })
const base = { activeId: 'c1', noModel: false, running: false, sending: false, lastTurn: 'done', pending: [], live: live(), warm: true }
const key = (over) => chatFootState({ ...base, ...over }).key

test('chat foot: a running turn names its phase', () => {
  assert.equal(key({ running: true }), 'thinking')
  assert.equal(key({ running: true, warm: false }), 'loading')
  assert.equal(key({ running: true, live: live({ phase: 'reasoning', reasoning: 'hm' }) }), 'thinking')
  assert.equal(key({ running: true, live: live({ phase: 'text', text: 'Hi' }) }), 'answering')
  // After a tool call ends the model reads its result: thinking, not answering.
  assert.equal(key({ running: true, live: live({ phase: 'after-tool', text: 'Hi', tools: [{ name: 'bash', state: 'done' }] }) }), 'thinking')
  // A cold model that already streamed is not loading any more.
  assert.equal(key({ running: true, warm: false, live: live({ phase: 'text', text: 'Hi' }) }), 'answering')
})

test('chat foot: a running tool names the tool and its argument', () => {
  const s = chatFootState({ ...base, running: true, live: live({ phase: 'tool', tools: [{ name: 'files__read', summary: 'q2.pdf', state: 'running' }] }) })
  assert.equal(s.key, 'tool')
  assert.equal(s.tool, 'read')
  assert.equal(s.verb, 'read')
  assert.equal(s.arg, 'q2.pdf')
  const other = chatFootState({ ...base, running: true, live: live({ tools: [{ name: 'get-time', summary: '', state: 'running' }] }) })
  assert.equal(other.verb, 'used')
  assert.equal(other.tool, 'get-time')
  // A path argument shows its file name.
  assert.equal(chatFootState({ ...base, running: true, live: live({ tools: [{ name: 'read_file', summary: '/q/q2.pdf', state: 'running' }] }) }).arg, 'q2.pdf')
  assert.equal(s.busy, true)
  assert.equal(s.icon, 'spinner')
  assert.equal(toolShortName('addon__sandbox__bash'), 'bash')
  assert.equal(toolShortName(''), '')
})

test('chat foot: with no turn running, a failure comes first, then an interruption, waiting messages, a stop', () => {
  assert.equal(key({ lastTurn: 'failed', pending: [{ id: 'p' }] }), 'failed')
  assert.equal(key({ lastTurn: 'interrupted', pending: [{ id: 'p' }] }), 'interrupted')
  assert.equal(key({ lastTurn: 'stopped', pending: [{ id: 'p' }] }), 'waiting')
  assert.equal(chatFootState({ ...base, lastTurn: 'stopped', pending: [{ id: 'p' }, { id: 'q' }] }).count, 2)
  assert.equal(key({ lastTurn: 'stopped' }), 'stopped')
  assert.equal(key({ lastTurn: 'done' }), 'ready')
  assert.equal(key({ lastTurn: '' }), 'ready')
  // A running turn wins over how the last one ended.
  assert.equal(key({ running: true, lastTurn: 'failed' }), 'thinking')
})

test('chat foot: a new chat is ready, sending or has no model', () => {
  assert.equal(key({ activeId: null }), 'ready')
  assert.equal(key({ activeId: null, noModel: true }), 'nomodel')
  assert.equal(key({ activeId: null, sending: true }), 'sending')
  assert.equal(chatFootState({ ...base, activeId: null, sending: true }).busy, true)
})

test('chat foot: every state has a tone, and the ones that need attention have an icon', () => {
  for (const k of ['failed', 'interrupted', 'waiting', 'stopped']) {
    const s = chatFootState({ ...base, lastTurn: k === 'waiting' ? 'stopped' : k, pending: k === 'waiting' ? [{ id: 'p' }] : [] })
    assert.equal(s.key, k)
    assert.ok(s.icon, `${k} has an icon`)
  }
  assert.equal(chatFootState({ ...base, lastTurn: 'failed' }).tone, 'error')
  assert.equal(chatFootState(base).tone, 'idle')
})

test('chat foot: the meter warns from 80 % and is hot from 90 %', () => {
  const tone = (used, limit = 1000) => contextReading({ used, limit }).tone
  assert.equal(contextReading({ used: 10 }).tone, 'ok')
  assert.equal(tone(790), 'ok')
  assert.equal(tone(800), 'warn')
  assert.equal(tone(894), 'warn')
  assert.equal(tone(900), 'err')
})

test('chat foot: speeds are whole from 10 tok/s and keep a decimal below', () => {
  assert.equal(speedValue(41.34), 41)
  assert.equal(speedValue(9.46), 9.5)
  assert.equal(speedValue(0), null)
  assert.equal(speedValue(undefined), null)
})

test('chat foot: the live estimate waits for enough pieces and a second', () => {
  assert.equal(liveRate(null), null)
  assert.equal(liveRate({ reqAt: 1000, lastAt: 1500, reqChunks: 20 }), null)
  assert.equal(liveRate({ reqAt: 1000, lastAt: 3000, reqChunks: 3 }), null)
  assert.equal(liveRate({ reqAt: 1000, lastAt: 3000, reqChunks: 41 }), 20)
})

test('chat foot: the speed shown is the estimate while streaming, else the measured figure, stale after the turn', () => {
  assert.deepEqual(speedView({ live: 38.2, measured: 41, running: true }), { value: 38, approx: true, stale: false })
  assert.deepEqual(speedView({ live: null, measured: 41.3, running: true }), { value: 41, approx: false, stale: false })
  assert.deepEqual(speedView({ live: 38, measured: 41.3, running: false }), { value: 41, approx: false, stale: true })
  assert.equal(speedView({ live: null, measured: 0, running: false }), null)
})

test('chat foot: the average needs two figures and keeps the last 10', () => {
  assert.equal(averageSpeed([]), null)
  assert.equal(averageSpeed([{ tokens_per_second: 40 }]), null)
  assert.deepEqual(averageSpeed([{ tokens_per_second: 40 }, { tokens_per_second: 0 }, { tokens_per_second: 20 }]), { value: 30, count: 2 })
  let h = []
  for (let i = 1; i <= 12; i++) h = pushUsage(h, { tokens_per_second: i })
  assert.equal(h.length, 10)
  assert.equal(h[0].tokens_per_second, 3)
})

test('chat foot: the keyboard hint shows only for a focused, empty composer in an idle chat', () => {
  const ready = chatFootState(base)
  assert.equal(showHint({ focused: true, empty: true, state: ready }), true)
  assert.equal(showHint({ focused: false, empty: true, state: ready }), false)
  assert.equal(showHint({ focused: true, empty: false, state: ready }), false)
  assert.equal(showHint({ focused: true, empty: true, state: chatFootState({ ...base, running: true }) }), false)
  assert.equal(showHint({ focused: true, empty: true, state: chatFootState({ ...base, lastTurn: 'failed' }) }), false)
})
