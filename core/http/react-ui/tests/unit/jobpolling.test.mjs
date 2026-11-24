import test from 'node:test'
import assert from 'node:assert/strict'
import { newPoll, pollStep, canStart, isTerminal, MAX_FAILURES, SILENCE_CAP_MS } from '../../src/utils/jobPolling.js'

const run = (events, start = newPoll(0)) => events.reduce((s, e) => pollStep(s, e), start)

test('job polling: happy path ends in done', () => {
  const s = run([
    { type: 'tick', now: 1000 }, { type: 'ok', now: 1100 },
    { type: 'tick', now: 2000 }, { type: 'ok', now: 2100 },
    { type: 'tick', now: 3000 }, { type: 'done', now: 3100 },
  ])
  assert.equal(s.phase, 'done')
  assert.equal(isTerminal(s), true)
})

test('job polling: a reported failure is terminal', () => {
  assert.equal(run([{ type: 'tick', now: 1 }, { type: 'failed', now: 2 }]).phase, 'failed')
})

test('job polling: repeated errors give up after the limit, one error does not', () => {
  const once = run([{ type: 'tick', now: 1 }, { type: 'error', now: 2 }])
  assert.equal(once.phase, 'polling')
  assert.equal(once.failures, 1)
  const events = []
  for (let i = 0; i < MAX_FAILURES; i++) events.push({ type: 'tick', now: 1000 * i }, { type: 'error', now: 1000 * i + 1 })
  const s = run(events)
  assert.equal(s.phase, 'gave-up')
  assert.equal(s.failures, MAX_FAILURES)
})

test('job polling: a success resets the failure count', () => {
  const events = []
  for (let i = 0; i < MAX_FAILURES - 1; i++) events.push({ type: 'tick', now: i }, { type: 'error', now: i })
  events.push({ type: 'tick', now: 10 }, { type: 'ok', now: 11 })
  for (let i = 0; i < MAX_FAILURES - 1; i++) events.push({ type: 'tick', now: 20 + i }, { type: 'error', now: 20 + i })
  const s = run(events)
  assert.equal(s.phase, 'polling')
  assert.equal(s.failures, MAX_FAILURES - 1)
})

test('job polling: a tick while a request is in flight is skipped', () => {
  const idle = newPoll(0)
  const first = pollStep(idle, { type: 'tick', now: 1000 })
  assert.equal(canStart(idle, first), true)
  const second = pollStep(first, { type: 'tick', now: 2000 })
  assert.equal(canStart(first, second), false)
  assert.equal(second.phase, 'polling')
  const after = pollStep(second, { type: 'ok', now: 2500 })
  assert.equal(canStart(after, pollStep(after, { type: 'tick', now: 3000 })), true)
})

test('job polling: a hung request still ends once the silence cap passes', () => {
  const hung = pollStep(newPoll(0), { type: 'tick', now: 1000 })
  const s = pollStep(hung, { type: 'tick', now: SILENCE_CAP_MS + 1 })
  assert.equal(s.phase, 'gave-up')
  assert.equal(s.inFlight, false)
})

test('job polling: events after a terminal phase change nothing', () => {
  for (const type of ['done', 'failed']) {
    const terminal = run([{ type: 'tick', now: 1 }, { type, now: 2 }])
    for (const late of ['tick', 'ok', 'error', 'done', 'failed']) {
      assert.equal(pollStep(terminal, { type: late, now: 3 }), terminal)
    }
  }
  const gaveUp = run(Array.from({ length: MAX_FAILURES }, (_, i) => ({ type: 'error', now: i })))
  assert.equal(pollStep(gaveUp, { type: 'ok', now: 99 }).phase, 'gave-up')
})
