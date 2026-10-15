import assert from 'node:assert/strict'
import test from 'node:test'

import {
  applyStatusLine, applyStreamEvent, clearRuns, effectiveStatus, failTurn, finishTurn, formatDuration,
  historyForRun, legacyRunsFromChats, loadRun, loadRuns, newRun, outcomeLine, recordStats, recordStatus,
  saveRun, stepCount, stripOf, MAX_RUNS_PER_AGENT, STALE_MS,
} from './agentRuns.js'

function memoryStorage() {
  const map = new Map()
  return {
    getItem: k => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => { map.set(k, String(v)) },
    removeItem: k => { map.delete(k) },
  }
}

test.beforeEach(() => { globalThis.localStorage = memoryStorage() })

const T0 = 1_000_000

test('tool calls and results become steps; reasoning folds into a thought at a boundary', () => {
  let turn = newRun('a', 'find it', { now: T0 }).turns[0]
  turn = applyStreamEvent(turn, { type: 'reasoning', content: 'I should ' }, T0 + 1)
  turn = applyStreamEvent(turn, { type: 'reasoning', content: 'search.' }, T0 + 2)
  turn = applyStreamEvent(turn, { type: 'tool_call', tool_name: 'web_search', tool_args: '{"q":' }, T0 + 3)
  turn = applyStreamEvent(turn, { type: 'tool_call', tool_name: '', tool_args: '"x"}' }, T0 + 4)
  turn = applyStreamEvent(turn, { type: 'tool_result', tool_name: 'web_search', tool_result: '8 results' }, T0 + 5)
  assert.deepEqual(turn.steps.map(s => s.kind), ['thought', 'tool'])
  assert.equal(turn.steps[1].args, '{"q":"x"}')
  assert.equal(turn.steps[1].result, '8 results')
  assert.equal(stepCount(turn), 1)
})

test('a done event clears the live text without losing the final answer', () => {
  let turn = newRun('a', 'x', { now: T0 }).turns[0]
  turn = applyStreamEvent(turn, { type: 'content', content: 'draft' }, T0 + 1)
  turn = applyStreamEvent(turn, { type: 'done' }, T0 + 2)
  assert.equal(turn.live.content, '')
  turn = finishTurn(turn, { content: 'The answer.', metadata: { urls: ['https://example.org'] } }, T0 + 9)
  assert.equal(turn.status, 'done')
  assert.equal(turn.outcome, 'The answer.')
  assert.equal(turn.endedAt, T0 + 9)
  assert.deepEqual(turn.metadata, { urls: ['https://example.org'] })
})

test('events after the turn ended change nothing', () => {
  let turn = finishTurn(newRun('a', 'x', { now: T0 }).turns[0], { content: 'ok' }, T0 + 1)
  assert.equal(applyStreamEvent(turn, { type: 'content', content: 'late' }), turn)
  assert.equal(applyStatusLine(turn, 'late'), turn)
})

test('a failed turn keeps the error and the steps taken', () => {
  let turn = newRun('a', 'x', { now: T0 }).turns[0]
  turn = applyStatusLine(turn, 'Thinking', T0 + 1)
  turn = failTurn(turn, 'model did not answer', T0 + 3)
  assert.equal(turn.status, 'failed')
  assert.equal(turn.error, 'model did not answer')
  assert.equal(turn.steps.length, 1)
})

test('a run left running is read as stopped once it is stale', () => {
  const run = newRun('a', 'x', { now: T0 })
  assert.equal(effectiveStatus(run, T0 + 1000), 'running')
  assert.equal(effectiveStatus({ ...run, updatedAt: T0 }, T0 + STALE_MS + 1), 'stopped')
})

test('a follow-up does not change the state the record strip shows', () => {
  let run = newRun('a', 'x', { now: T0 })
  run.turns[0] = finishTurn(run.turns[0], { content: 'ok' }, T0 + 5)
  run = { ...run, turns: [...run.turns, failTurn({ ...run.turns[0], status: 'running', live: { reasoning: '', content: '' }, steps: [] }, 'no', T0 + 9)] }
  assert.equal(recordStatus(run), 'done')
  assert.equal(effectiveStatus(run), 'failed')
})

test('history for a follow-up holds the task and the answers before it', () => {
  let run = newRun('a', 'first', { now: T0 })
  run.turns[0] = finishTurn(run.turns[0], { content: 'one' }, T0 + 1)
  assert.deepEqual(historyForRun(run), [{ role: 'user', content: 'first' }, { role: 'assistant', content: 'one' }])
})

test('save, load, find and clear a run; the log is bounded', () => {
  const run = newRun('agent-x', 'task', { now: T0, id: 'r_one' })
  assert.equal(saveRun(run), true)
  assert.equal(loadRun('agent-x', 'r_one').turns[0].task, 'task')
  assert.equal(loadRun('agent-x', 'r_missing'), null)
  assert.equal(loadRun('other', 'r_one'), null)
  for (let i = 0; i < MAX_RUNS_PER_AGENT + 5; i++) saveRun(newRun('agent-x', `t${i}`, { now: T0 + i + 1, id: `r_${i}` }))
  assert.equal(loadRuns('agent-x').length, MAX_RUNS_PER_AGENT)
  assert.equal(loadRuns('agent-x')[0].id, `r_${MAX_RUNS_PER_AGENT + 4}`)
  clearRuns('agent-x')
  assert.equal(loadRuns('agent-x').length, 0)
})

test('runs of another user are kept apart', () => {
  saveRun(newRun('a', 'mine', { now: T0, id: 'r_a' }))
  saveRun(newRun('a', 'theirs', { now: T0, id: 'r_b', userId: 'u2' }))
  assert.deepEqual(loadRuns('a').map(r => r.id), ['r_a'])
  assert.deepEqual(loadRuns('a', 'u2').map(r => r.id), ['r_b'])
})

test('old stored chats read as runs with stable ids, an answer or none', () => {
  const chats = {
    conversations: [{
      id: 'abcdef123456', createdAt: T0,
      messages: [
        { sender: 'user', content: 'one', timestamp: T0 },
        { sender: 'system', content: 'Thinking', timestamp: T0 + 1 },
        { sender: 'agent', content: 'First answer. More.', timestamp: T0 + 2 },
        { sender: 'user', content: 'two', timestamp: T0 + 3 },
      ],
    }],
  }
  const runs = legacyRunsFromChats('a', undefined, chats)
  assert.equal(runs.length, 2)
  assert.equal(runs[0].turns[0].status, 'done')
  assert.equal(runs[0].turns[0].steps.length, 1)
  assert.equal(runs[1].turns[0].status, 'stopped')
  assert.deepEqual(legacyRunsFromChats('a', undefined, chats).map(r => r.id), runs.map(r => r.id))
  localStorage.setItem('localai_agent_chats_a', JSON.stringify(chats))
  assert.equal(loadRuns('a').length, 2)
  assert.equal(outcomeLine(runs[0]), 'First answer. More.')
})

test('the strip shows the last runs oldest first and the stats count each state', () => {
  const runs = []
  for (let i = 0; i < 20; i++) {
    let r = newRun('a', `t${i}`, { now: T0 + i, id: `r_${i}` })
    r.turns[0] = i % 7 === 3 ? failTurn(r.turns[0], 'x', T0 + i + 2000) : finishTurn(r.turns[0], { content: 'ok' }, T0 + i + 4000)
    runs.unshift(r)
  }
  const strip = stripOf(runs)
  assert.equal(strip.length, 14)
  assert.equal(strip.at(-1).id, 'r_19')
  const stats = recordStats(runs)
  assert.equal(stats.total, 20)
  assert.equal(stats.failed, 3)
  assert.equal(stats.finished, 17)
  assert.equal(stats.medianMs, 4000)
})

test('durations read plainly', () => {
  assert.equal(formatDuration(null), '')
  assert.equal(formatDuration(4200), '4.2 s')
  assert.equal(formatDuration(26100), '26 s')
  assert.equal(formatDuration(66000), '1 min 06 s')
})

import { titleOf } from './agentRuns.js'

test('a run is titled by the first sentence of its task', () => {
  assert.equal(titleOf('Find two sources. Then write a summary.'), 'Find two sources')
  assert.equal(titleOf('One line only'), 'One line only')
  assert.equal(titleOf(`${'word '.repeat(40)}end`).length <= 72, true)
  assert.equal(titleOf(''), '')
})
