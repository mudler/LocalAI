import assert from 'node:assert/strict'
import test from 'node:test'

import {
  effectiveStatus, formatDuration, outcomeLine, recordStats, recordStatus, runOfChat, runOfTriggerRun, stripOf,
  STRIP_LENGTH, titleOf,
} from './agentRuns.js'

const chat = (over) => ({ id: 'c1', title: 'Weekly digest. More text.', created: '2026-10-01T10:00:00Z', updated: '2026-10-01T10:00:30Z', running: false, last_turn: 'done', ...over })

test('a chat of the agent reads as a run: the title is the task, last_turn is the state', () => {
  const run = runOfChat(chat())
  assert.equal(run.chatId, 'c1')
  assert.equal(titleOf(run.turns[0].task), 'Weekly digest')
  assert.equal(recordStatus(run), 'done')
  assert.equal(run.turns[0].endedAt - run.turns[0].startedAt, 30_000)
  assert.equal(recordStatus(runOfChat(chat({ last_turn: 'failed' }))), 'failed')
  assert.equal(recordStatus(runOfChat(chat({ last_turn: 'interrupted' }))), 'failed')
  assert.equal(recordStatus(runOfChat(chat({ last_turn: 'paused' }))), 'done')
  assert.equal(recordStatus(runOfChat(chat({ last_turn: 'stopped' }))), 'stopped')
  assert.equal(recordStatus(runOfChat(chat({ last_turn: '' }))), 'stopped')
})

test('a chat that runs now is running and has no end', () => {
  const run = runOfChat(chat({ running: true, last_turn: 'done' }))
  assert.equal(effectiveStatus(run), 'running')
  assert.equal(run.turns[0].endedAt, null)
})

test('a start of a trigger maps queued and running to running, skipped to stopped, and keeps the reason', () => {
  const r = (status, extra) => runOfTriggerRun({ id: 'r', status, due: '2026-10-01T10:00:00Z', ...extra }, 'Summarise')
  assert.equal(recordStatus(r('queued')), 'running')
  assert.equal(recordStatus(r('running')), 'running')
  assert.equal(recordStatus(r('skipped', { reason: 'daily cap' })), 'stopped')
  const failed = r('failed', { reason: 'no model', chat_id: 'c9', started: '2026-10-01T10:00:01Z', ended: '2026-10-01T10:00:03Z' })
  assert.equal(failed.chatId, 'c9')
  assert.equal(outcomeLine(failed), 'no model')
  assert.equal(failed.turns[0].endedAt - failed.turns[0].startedAt, 2000)
})

test('the record counts finished runs and has a median time of the finished ones', () => {
  const runs = [
    runOfChat(chat({ id: 'a' })),
    runOfChat(chat({ id: 'b', updated: '2026-10-01T10:01:00Z' })),
    runOfChat(chat({ id: 'c', last_turn: 'failed' })),
    runOfChat(chat({ id: 'd', last_turn: 'stopped' })),
  ]
  const s = recordStats(runs)
  assert.deepEqual([s.total, s.finished, s.failed, s.stopped], [4, 2, 1, 1])
  assert.equal(s.medianMs, 60_000)
})

test('the strip keeps the last runs, oldest first', () => {
  const runs = Array.from({ length: 20 }, (_, i) => runOfChat(chat({ id: `r${i}` })))
  const strip = stripOf(runs)
  assert.equal(strip.length, STRIP_LENGTH)
  assert.equal(strip[0].id, `r${STRIP_LENGTH - 1}`)
  assert.equal(strip.at(-1).id, 'r0')
})

test('durations read in seconds and minutes', () => {
  assert.equal(formatDuration(3200), '3.2 s')
  assert.equal(formatDuration(42_000), '42 s')
  assert.equal(formatDuration(125_000), '2 min 05 s')
  assert.equal(formatDuration(null), '')
})
