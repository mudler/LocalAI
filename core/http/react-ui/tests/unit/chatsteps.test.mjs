import test from 'node:test'
import assert from 'node:assert/strict'
import {
  emptyLive, reasoningDelta, textDelta, toolStart, toolEnd, liveSteps, groupSteps, groupFacts, thoughtGist, turnFacts, GROUP_MIN,
} from '../../src/utils/chatLive.js'
import { threadRows, streamingParts } from '../../src/utils/chatThread.js'

// One round of the turn: reasoning, a word about it, one call that ends.
function round(live, i, { ok = true, now = i * 1000 } = {}) {
  let l = reasoningDelta(live, `Thought ${i}. `, now)
  l = reasoningDelta(l, 'More.', now)
  l = textDelta(l, `Note ${i}.`, now)
  l = toolStart(l, { name: 'bash', summary: `cmd ${i}` }, now + 100)
  return toolEnd(l, { name: 'bash', ok, output: `out ${i}`, seq: i }, now + 600)
}

const kinds = (steps) => steps.map((s) => s.kind)

test('chat steps: three rounds keep their order, the notes and the calls', () => {
  let live = emptyLive()
  for (let i = 1; i <= 3; i++) live = round(live, i)
  const { steps, answer } = liveSteps(live)
  assert.deepEqual(kinds(steps), ['thought', 'note', 'tool', 'thought', 'note', 'tool', 'thought', 'note', 'tool'])
  assert.deepEqual(steps.filter((s) => s.kind === 'note').map((s) => s.text), ['Note 1.', 'Note 2.', 'Note 3.'])
  assert.equal(steps[0].text, 'Thought 1. More.', 'chunks of one round are one thought')
  assert.equal(answer, '', 'nothing after the last call yet')
  assert.equal(live.beats.length, 3)
})

test('chat steps: the text after the last call is the answer, and it is not a note', () => {
  let live = round(emptyLive(), 1)
  live = reasoningDelta(live, 'Now I can answer.', 5000)
  live = textDelta(live, 'The ', 5100)
  live = textDelta(live, 'answer.', 5200)
  const { steps, answer } = liveSteps(live)
  assert.equal(answer, 'The answer.')
  assert.deepEqual(kinds(steps), ['thought', 'note', 'tool', 'thought'])
  assert.equal(steps[3].open, false, 'a thought stops being open once the answer streams')
})

test('chat steps: the text that is the answer so far has the key of the note it becomes', () => {
  let live = reasoningDelta(emptyLive(), 'hm', 1)
  live = textDelta(live, 'I will look.', 2)
  const before = liveSteps(live)
  live = toolStart(live, { name: 'bash', summary: 'ls' }, 3)
  const after = liveSteps(live)
  const note = after.steps.find((s) => s.kind === 'note')
  assert.equal(note.text, 'I will look.', 'what was said is kept')
  assert.equal(note.key, before.answerKey, 'same element, so it changes style and does not remount')
  assert.equal(after.answer, '')
  assert.equal(after.steps.find((s) => s.kind === 'thought').key, before.steps[0].key)
})

test('chat steps: reasoning after prose closes the round, so prose is never glued across rounds', () => {
  let live = textDelta(emptyLive(), 'first.', 1)
  live = reasoningDelta(live, 'again', 2)
  live = textDelta(live, 'Notes found', 3)
  const { steps, answer } = liveSteps(live)
  assert.deepEqual(steps.map((s) => [s.kind, s.text]), [['note', 'first.'], ['thought', 'again']])
  assert.equal(answer, 'Notes found')
})

test('chat steps: empty reasoning and empty rounds leave nothing', () => {
  let live = reasoningDelta(emptyLive(), '', 1)
  assert.equal(live, emptyLive() && live, 'no change for an empty piece')
  assert.equal(live.reasoning, '')
  live = reasoningDelta(live, '  \n', 2)
  live = toolStart(live, { name: 'bash', summary: 'ls' }, 3)
  assert.equal(live.beats.length, 0, 'a blank round leaves no beat')
  assert.deepEqual(kinds(liveSteps(live).steps), ['tool'])
  assert.deepEqual(liveSteps(emptyLive()), { steps: [], answer: '', answerKey: 't0' })
})

test('chat steps: a call with no thought before it is not a round', () => {
  let live = toolStart(emptyLive(), { name: 'a' }, 1)
  live = toolStart(live, { name: 'b' }, 2)
  assert.equal(live.beats.length, 0)
  assert.deepEqual(kinds(liveSteps(live).steps), ['tool', 'tool'])
})

test('chat steps: tool_end out of order changes only the call it names', () => {
  let live = toolStart(emptyLive(), { name: 'read_file', summary: 'a' }, 1)
  live = toolStart(live, { name: 'bash', summary: 'ls' }, 2)
  const same = toolEnd(live, { name: 'nothing', ok: true, output: 'x' }, 3)
  assert.deepEqual(same.tools.map((t) => t.state), ['running', 'running'], 'an end for a call that never started is ignored')
  live = toolEnd(live, { name: 'bash', ok: true, output: 'a', seq: 5 }, 4)
  assert.deepEqual(live.tools.map((t) => t.state), ['running', 'done'], 'the second call ended first')
  assert.equal(live.tools[1].endedAt - live.tools[1].startedAt, 2)
  const early = toolEnd(emptyLive(), { name: 'bash', ok: true }, 1)
  assert.equal(early.tools.length, 0, 'an end before any start adds nothing')
})

const tools = (n) => Array.from({ length: n }, (_, i) => ({ kind: 'tool', key: `l${i}`, tool: { state: 'done' }, ms: 100 }))

test('chat steps: calls in a row group from three, fewer stay rows', () => {
  assert.equal(GROUP_MIN, 3)
  for (const [n, expected] of [[1, ['tool']], [2, ['tool', 'tool']], [3, ['group']], [6, ['group']]]) {
    assert.deepEqual(kinds(groupSteps(tools(n))), expected, `${n} calls`)
  }
  assert.equal(groupSteps(tools(6))[0].tools.length, 6)
})

test('chat steps: a thought or a note between calls ends the run', () => {
  const mix = [...tools(3), { kind: 'note', key: 'n', text: 'x' }, ...tools(2).map((s, i) => ({ ...s, key: `m${i}` })), { kind: 'thought', key: 'h', text: 'y' }, ...tools(4).map((s, i) => ({ ...s, key: `o${i}` }))]
  assert.deepEqual(kinds(groupSteps(mix)), ['group', 'note', 'tool', 'tool', 'thought', 'group'])
  assert.equal(groupSteps(mix)[0].key, 'g-l0', 'a group is keyed by its first call')
})

test('chat steps: a group says what runs, what failed and its time', () => {
  let live = emptyLive()
  for (let i = 1; i <= 5; i++) live = toolStart(live, { name: 'bash', summary: `c${i}` }, i * 10)
  live = toolEnd(live, { name: 'bash', ok: true, output: '' }, 55)
  let facts = groupFacts(groupSteps(liveSteps(live).steps)[0].tools)
  assert.equal(facts.count, 5)
  assert.ok(facts.active >= 0, 'a call still runs')
  assert.equal(facts.total, null, 'no total while one runs')
  for (let i = 0; i < 3; i++) live = toolEnd(live, { name: 'bash', ok: true, output: '' }, 100 + i)
  live = toolEnd(live, { name: 'bash', ok: false, output: 'boom' }, 110)
  facts = groupFacts(groupSteps(liveSteps(live).steps)[0].tools)
  assert.equal(facts.active, -1)
  assert.equal(facts.failed, 1)
  assert.equal(Number.isFinite(facts.total), true)
})

test('chat steps: a failed call inside a group stays in the group, counted', () => {
  const steps = tools(4)
  steps[2] = { ...steps[2], tool: { state: 'failed' } }
  const [group] = groupSteps(steps)
  const facts = groupFacts(group.tools)
  assert.equal(facts.failed, 1)
  assert.equal(group.tools[2].tool.state, 'failed')
})

test('chat steps: calls with an app are rows of their own and not steps', () => {
  let live = toolStart(emptyLive(), { name: 'clock' }, 1)
  live = toolEnd(live, { name: 'clock', ok: true, app: { server: 's' }, seq: 3 }, 2)
  live = toolStart(live, { name: 'bash' }, 3)
  assert.deepEqual(liveSteps(live).steps.map((s) => s.tool.name), ['bash'])
})

test('chat steps: the gist ends at a sentence or a word and says when it cut', () => {
  assert.deepEqual(thoughtGist('Short one.'), { text: 'Short one.', cut: false, more: false })
  assert.equal(thoughtGist('One.\n\nTwo.').more, true, 'a second paragraph is behind a click')
  assert.equal(thoughtGist('# Plan\nread it').text, 'Plan', 'a Markdown heading has no marks')
  assert.equal(thoughtGist('**Notes** found a `roadmap` and [a skill](http://x/y_z)').text, 'Notes found a roadmap and a skill')
  assert.equal(thoughtGist('read my_file_name.txt').text, 'read my_file_name.txt', 'underscores in names stay')
  const long = 'Notes found a roadmap and a recap skill. Reading the next steps file now to see which features landed in the last release and what is planned for the one after that, in order.'
  const g = thoughtGist(long)
  assert.equal(g.text, 'Notes found a roadmap and a recap skill. Reading the next steps file now to see which features landed in the last release and what is planned for the one after that, in order.'.slice(0, g.text.length))
  assert.ok(g.text.length <= 140)
  assert.match(g.text, /[.…]$/)
  const words = thoughtGist('word '.repeat(80))
  assert.equal(words.cut, true)
  assert.match(words.text, /word…$/, 'ends on a whole word, then the ellipsis')
  const noSpace = thoughtGist('x'.repeat(300))
  assert.equal(noSpace.text.endsWith('…'), true)
  assert.equal(thoughtGist('').text, '')
  const tail = thoughtGist('first line\n' + 'alpha beta gamma '.repeat(20), { tail: true })
  assert.match(tail.text, /^….*gamma$/)
})

test('chat steps: the stored turn draws the same order, with the narration as a note', () => {
  const m = (seq, role, text, extra = {}) => ({ seq, role, text, turn: 1, at: '2026-11-24T10:00:0' + seq + 'Z', ...extra })
  const rows = threadRows([
    m(1, 'user', 'go'),
    m(2, 'reasoning', 'think A'), m(3, 'assistant', 'I will look.'), m(4, 'tool', 'bash: ls\nout'),
    m(5, 'reasoning', 'think B'), m(6, 'assistant', 'Now the file.'), m(7, 'tool', 'read_file: a\nbody'),
    m(8, 'assistant', 'Done: it works.'),
  ], emptyLive())
  assert.deepEqual(rows.map((r) => r.kind), ['msg', 'msg'])
  assert.equal(rows[1].msg.text, 'Done: it works.', 'the answer is the text after the last call')
  assert.deepEqual(rows[1].activity.map((i) => i.role), ['reasoning', 'note', 'tool', 'reasoning', 'note', 'tool'])
})

test('chat steps: a turn that stopped after a call has no answer, so its messages stay messages', () => {
  // A failed or interrupted turn: folding its notes would hide all it wrote.
  const stopped = [
    { seq: 1, role: 'user', text: 'go' },
    { seq: 2, role: 'assistant', text: 'I will look.' }, { seq: 3, role: 'tool', text: 'bash: ls\nout' },
    { seq: 4, role: 'assistant', text: 'Now the file.' }, { seq: 5, role: 'tool', text: 'read_file: a\nbody' },
  ]
  const failed = threadRows([...stopped, { seq: 6, role: 'failure', text: 'x' }, { seq: 7, role: 'checkpoint', text: '' }], emptyLive())
  assert.deepEqual(failed.map((r) => r.kind), ['msg', 'msg', 'msg', 'activity'])
  assert.deepEqual(failed.slice(1, 3).map((r) => r.msg.text), ['I will look.', 'Now the file.'])
  // Resumed, the turn gets its answer after the failure line: the earlier
  // messages become notes of its fold.
  const resumed = threadRows([...stopped, { seq: 6, role: 'failure', text: 'x' }, { seq: 8, role: 'assistant', text: 'Done.' }], emptyLive())
  assert.deepEqual(resumed.map((r) => r.kind), ['msg', 'msg'])
  assert.deepEqual(resumed[1].activity.map((i) => i.role), ['note', 'tool', 'note', 'tool'])
  // While the turn streams, its answer is on its way: the stored messages are notes.
  const live = { ...emptyLive(), text: 'Almost' }
  assert.deepEqual(threadRows(stopped, live).map((r) => r.kind), ['msg', 'activity'])
})

test('chat steps: an assistant message with no call after it stays a message', () => {
  const rows = threadRows([
    { seq: 1, role: 'user', text: 'a' }, { seq: 2, role: 'assistant', text: 'plain answer' },
    { seq: 3, role: 'user', text: 'b' }, { seq: 4, role: 'tool', text: 'bash: ls\nx' },
  ], emptyLive())
  assert.deepEqual(rows.map((r) => r.kind), ['msg', 'msg', 'msg', 'activity'])
  assert.equal(rows[1].msg.text, 'plain answer')
})

test('chat steps: the streaming parts carry the steps in order', () => {
  let live = round(emptyLive(), 1)
  live = textDelta(live, 'Answer', 9000)
  const parts = streamingParts(live)
  assert.equal(parts.text, 'Answer')
  assert.deepEqual(kinds(parts.steps), ['thought', 'note', 'tool'])
  assert.equal(parts.tools.length, 1)
})

test('chat steps: the line of a finished turn counts the thoughts and the calls, and times them', () => {
  let live = emptyLive()
  for (let i = 1; i <= 2; i++) live = round(live, i)
  live = reasoningDelta(live, 'Last thought.', 9000)
  live = textDelta(live, 'Answer.', 9100)
  live = { ...live, ended: true, endedAt: 9500 }
  const facts = turnFacts(live)
  assert.deepEqual([facts.thoughts, facts.calls, facts.steps], [3, 2, 5], 'a note is not a step; the open thought is')
  assert.equal(facts.steps, liveSteps(live).steps.filter((s) => s.kind !== 'note').length, 'the same steps the list shows')
  assert.equal(facts.ms, 9500 - 1000, 'from the first start to the last end')
  assert.deepEqual(turnFacts(emptyLive()), { steps: 0, thoughts: 0, calls: 0, ms: null })
  assert.equal(emptyLive().ended, false)
})

test('chat steps: three calls in a group count as three steps', () => {
  let live = emptyLive()
  for (let i = 1; i <= 3; i++) { live = toolStart(live, { name: 'bash', summary: `c${i}` }, i * 10); live = toolEnd(live, { name: 'bash', ok: true }, i * 10 + 5) }
  assert.equal(turnFacts(live).steps, 3)
  assert.equal(groupSteps(liveSteps(live).steps)[0].tools.length, 3)
})

// The thought that is still arriving keeps one line of height while its words
// stream. When it could wrap to two lines and back, the thread under it jumped
// up and down on each token.
test('chat steps: the live thought is one line high', async () => {
  const { readFileSync } = await import('node:fs')
  const css = readFileSync(new URL('../../src/pages/chat.css', import.meta.url), 'utf8')
  const rule = (sel) => {
    const at = css.indexOf(`${sel} {`)
    assert.ok(at >= 0, `${sel} has a rule`)
    return css.slice(at, css.indexOf('}', at))
  }
  const line = rule('.cx-step--thought.cx-step--live .cx-step__line')
  assert.match(line, /white-space:\s*nowrap/)
  assert.match(line, /display:\s*flex/)
  assert.match(rule('.cx-step--thought.cx-step--live .cx-step__gist'), /overflow:\s*hidden/)
  const jsx = readFileSync(new URL('../../src/components/chat/ActivityFold.jsx', import.meta.url), 'utf8')
  assert.match(jsx, /cx-step--thought\$\{live \? ' cx-step--live' : ''\}/, 'ThoughtStep marks the live thought')
})
