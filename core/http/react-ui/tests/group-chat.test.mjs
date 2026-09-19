// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { buildMessages, readCompletion, GroupRun, validParticipants } from '../src/utils/groupChat.js'
const participants = [{ name: 'Planner', model: 'alpha' }, { name: 'Critic', model: 'beta' }]
const event = data => `data: ${JSON.stringify(data)}\n\n`
const reply = text => event({ choices: [{ delta: { content: text, reasoning_content: 'secret' } }] })
const stream = (...chunks) => new ReadableStream({ start(c) { chunks.forEach(s => c.enqueue(new TextEncoder().encode(s))); c.close() } })
const complete = () => stream(reply('hello'), 'data: [DONE]\n\n')
test('projects all completed contributions as attributed context with a user turn', () => {
  const messages = buildMessages('Plan a garden', participants[1], [
    { name: 'Moderator', content: 'Native plants', status: 'complete' },
    { ...participants[0], content: 'Use oak', reasoning: 'secret', status: 'complete' },
    { ...participants[1], content: 'unfinished', status: 'incomplete' },
  ])
  assert.equal(messages[0].role, 'system')
  assert.match(messages[0].content, /Critic/)
  assert.match(messages[0].content, /Plan a garden/)
  assert.equal(messages.at(-1).role, 'user')
  assert.match(messages.at(-1).content, /Planner \(alpha\)/)
  assert.match(messages.at(-1).content, /Use oak/)
  assert.doesNotMatch(JSON.stringify(messages), /secret|unfinished/)
  assert.equal(messages.some(m => m.role === 'assistant'), false)
})
test('validates participant bounds and unique trimmed names', () => {
  assert.equal(validParticipants(participants), true)
  for (const p of [[], participants.slice(0, 1), [...participants, { name: ' planner ', model: 'a' }], [{ name: ' ', model: 'a' }, participants[1]], Array(7).fill(participants[0])]) assert.equal(validParticipants(p), false)
})
test('decodes fragmented SSE, ignores reasoning and accepts CRLF', async () => {
  let text = ''
  const s = reply('héllo').replaceAll('\n', '\r\n') + 'data: [DONE]\r\n\r\n'
  assert.equal(await readCompletion(stream(...[...s]), new AbortController().signal, value => { text = value }), 'héllo')
  assert.equal(text, 'héllo')
})
for (const [name, chunks, pattern] of [
  ['SSE error', [event({ error: { message: 'Stream failed' } })], /Stream failed/],
  ['malformed', ['data: not-json\n\n'], /Malformed/],
  ['truncated', [reply('partial')], /ended/],
  ['empty', ['data: [DONE]\n\n'], /no text/],
  ['length limit', [event({ choices: [{ delta: { content: 'partial' }, finish_reason: 'length' }] }), 'data: [DONE]\n\n'], /limit/],
]) test(name, async () => assert.rejects(readCompletion(stream(...chunks), new AbortController().signal), pattern))
test('runs ordered rounds with attributed prior turns', async () => {
  const requests = []
  const runner = new GroupRun(async body => { requests.push(body); return complete() })
  const history = await runner.run({ participants, rounds: 2, objective: 'Discuss', transcript: [] })
  assert.deepEqual(requests.map(r => r.model), ['alpha', 'beta', 'alpha', 'beta'])
  assert.equal(history.length, 4)
  assert.match(requests[1].messages[1].content, /Planner \(alpha\)/)
  assert.equal(history.every(e => e.status === 'complete'), true)
})
test('bounds rounds and stops on provider HTTP failure', async () => {
  let calls = 0
  const runner = new GroupRun(async () => { calls++; throw Error('HTTP 500') })
  for (const rounds of [0, 11, 1.5, NaN]) await assert.rejects(runner.run({ participants, rounds, transcript: [] }), /rounds/)
  await assert.rejects(runner.run({ participants, rounds: 2, transcript: [] }), /HTTP 500/)
  assert.equal(calls, 1)
})
for (const partial of [false, true]) test(`abort ${partial ? 'after partial' : 'before content'} prevents future turns and overlap`, async () => {
  let calls = 0, started
  const ready = new Promise(resolve => { started = resolve })
  let latest
  const runner = new GroupRun(async () => {
    calls++
    return new ReadableStream({ start(c) { if (partial) c.enqueue(new TextEncoder().encode(reply('partial'))); started() } })
  })
  const run = runner.run({ participants, rounds: 2, transcript: [], onUpdate: entries => { latest = entries } })
  await ready
  await assert.rejects(runner.run({ participants, rounds: 1, transcript: [] }), /already/)
  await new Promise(resolve => setImmediate(resolve))
  runner.stop()
  await assert.rejects(run, { name: 'AbortError' })
  assert.equal(calls, 1)
  assert.equal(latest[0].status, 'incomplete')
  assert.equal(latest[0].content, partial ? 'partial' : '')
  assert.doesNotMatch(JSON.stringify(buildMessages('', participants[1], latest)), /partial/)
  assert.equal(runner.running, false)
})
test('manual grant runs only the selected participant', async () => {
  const runner = new GroupRun(async body => { assert.equal(body.model, 'beta'); return complete() })
  const history = await runner.run({ participants, speaker: 1, transcript: [] })
  assert.equal(history.length, 1)
  assert.equal(history[0].name, 'Critic')
})
test('stream failures mark partial output incomplete and stop the round', async () => {
  let count = 0, latest
  const runner = new GroupRun(async () => { count++; return stream(reply('partial')) })
  await assert.rejects(runner.run({ participants, rounds: 2, transcript: [], onUpdate: entries => { latest = entries } }), /ended/)
  assert.equal(count, 1)
  assert.equal(latest[0].status, 'incomplete')
  assert.equal(latest[0].content, 'partial')
})
test('already-aborted reader emits no content', async () => {
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(readCompletion(complete(), controller.signal, () => assert.fail('stale content')), { name: 'AbortError' })
})
