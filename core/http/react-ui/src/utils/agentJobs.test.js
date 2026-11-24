import assert from 'node:assert/strict'
import test from 'node:test'

import {
  failingTasks, groupByDay, isActive, jobDurationMs, jobLine, jobMark, jobOfRun, jobsOf, statusCounts, stripOfJobs, weekSummary,
} from './agentJobs.js'

const T = (iso) => Date.parse(iso)

test('a start of a trigger reads as a job and keeps the reason of a failure or a skip', () => {
  const job = jobOfRun({ id: 'r1', status: 'failed', reason: 'no model', due: '2026-10-07T07:00:00Z', started: '2026-10-07T07:00:01Z', ended: '2026-10-07T07:00:05Z', chat_id: 'c1' }, 't1')
  assert.equal(job.task_id, 't1')
  assert.equal(job.created_at, '2026-10-07T07:00:00Z')
  assert.equal(job.error, 'no model')
  assert.equal(job.chat_id, 'c1')
  assert.equal(jobDurationMs(job), 4000)
  assert.equal(jobOfRun({ id: 'r2', status: 'done', reason: 'note', due: 'x' }, 't1').error, '')
  assert.equal(jobOfRun({ id: 'r3', status: 'skipped', reason: 'daily cap', due: 'x' }, 't1').error, 'daily cap')
})

test('jobs: mark, activity and the one-line outcome', () => {
  assert.equal(jobMark('done'), 'done')
  assert.equal(jobMark('failed'), 'failed')
  assert.equal(jobMark('skipped'), 'stopped')
  assert.equal(jobMark('running'), 'running')
  assert.equal(jobMark('queued'), 'ready')
  assert.ok(isActive({ status: 'queued' }) && isActive({ status: 'running' }))
  assert.ok(!isActive({ status: 'done' }) && !isActive({ status: 'skipped' }))
  assert.equal(jobDurationMs({ started_at: '2026-10-07T07:00:00Z', completed_at: '2026-10-07T07:01:06Z' }), 66000)
  assert.equal(jobDurationMs({ started_at: '2026-10-07T07:00:00Z' }), null)
  assert.deepEqual(jobLine({ status: 'failed', error: 'read_page: timeout after 20 s' }), { kind: 'error', text: 'read_page: timeout after 20 s' })
  assert.deepEqual(jobLine({ status: 'skipped', reason: 'daily cap' }), { kind: 'skipped', text: 'daily cap' })
  assert.equal(jobLine({ status: 'done' }).kind, 'done')
  assert.equal(jobLine({ status: 'running' }).kind, 'running')
  assert.equal(jobLine({ status: 'queued' }).kind, 'queued')
  assert.ok(jobLine({ status: 'failed', error: 'word '.repeat(60) }).text.endsWith('…'))
})

test('jobs: grouped by day, newest first', () => {
  const now = new Date(2026, 9, 7, 12, 0, 0).getTime()
  const at = (d, h) => new Date(2026, 9, d, h, 0, 0).toISOString()
  const jobs = [
    { id: 'a', created_at: at(5, 7), status: 'done' },
    { id: 'b', created_at: at(7, 7), status: 'failed' },
    { id: 'c', created_at: at(7, 9), status: 'done' },
    { id: 'd', created_at: at(6, 20), status: 'done' },
  ]
  const groups = groupByDay(jobs, now)
  assert.deepEqual(groups.map(g => [g.day, g.jobs.map(j => j.id)]), [['today', ['c', 'b']], ['yesterday', ['d']], [null, ['a']]])
})

test('jobs: the week, per-trigger order, strip and counts', () => {
  const now = T('2026-10-07T12:00:00Z')
  const jobs = [
    { id: '1', task_id: 't1', status: 'done', created_at: '2026-10-07T07:00:00Z' },
    { id: '2', task_id: 't1', status: 'failed', created_at: '2026-10-07T09:00:00Z' },
    { id: '3', task_id: 't2', status: 'running', created_at: '2026-10-07T10:00:00Z' },
    { id: '4', task_id: 't2', status: 'skipped', created_at: '2026-10-05T10:00:00Z' },
    { id: '5', task_id: 't2', status: 'done', created_at: '2026-09-20T10:00:00Z' },
  ]
  assert.deepEqual(weekSummary(jobs, now), { total: 4, finished: 1, failed: 1, stopped: 1, active: 1 })
  assert.deepEqual(jobsOf(jobs, 't1').map(j => j.id), ['2', '1'])
  assert.deepEqual(stripOfJobs(jobsOf(jobs, 't2')).map(s => s.status), ['done', 'stopped', 'running'])
  assert.deepEqual(failingTasks([{ id: 't1', name: 'a' }, { id: 't2', name: 'b' }], jobs).map(t => t.name), ['a'])
  assert.deepEqual(statusCounts(jobs), { all: 5, done: 2, failed: 1, running: 1, skipped: 1 })
})
