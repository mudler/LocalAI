import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildPreset, clock, cronWords, failingTasks, fillPrompt, formatKeyValues, groupByDay, jobDurationMs, jobLine, jobMark,
  jobsOf, parseKeyValues, presetOf, promptParams, statusCounts, stripOfJobs, validateCron, weekSummary,
} from './agentJobs.js'

test('cron: valid and invalid expressions', () => {
  for (const ok of ['', '0 7 * * *', '*/30 * * * *', '0 9 * * 1-5', '0 9 * * mon,fri', '15 4 1 jan *', '@hourly', '@every 5m', '@every 1h30m', '0 0 1,15 * *']) {
    assert.equal(validateCron(ok), null, ok)
  }
  assert.deepEqual(validateCron('* * * *'), { id: 'fields', count: 4 })
  assert.deepEqual(validateCron('0 7 * * * *'), { id: 'fields', count: 6 })
  assert.deepEqual(validateCron('61 * * * *'), { id: 'field', field: 'minute' })
  assert.deepEqual(validateCron('0 24 * * *'), { id: 'field', field: 'hour' })
  assert.deepEqual(validateCron('0 7 32 * *'), { id: 'field', field: 'day' })
  assert.deepEqual(validateCron('0 7 * 13 *'), { id: 'field', field: 'month' })
  assert.deepEqual(validateCron('0 7 * * 7'), { id: 'field', field: 'weekday' })
  assert.deepEqual(validateCron('0 7 * * x'), { id: 'field', field: 'weekday' })
  assert.deepEqual(validateCron('5-1 * * * *'), { id: 'field', field: 'minute' })
  assert.deepEqual(validateCron('*/0 * * * *'), { id: 'field', field: 'minute' })
  assert.deepEqual(validateCron('@sometimes'), { id: 'descriptor' })
  assert.deepEqual(validateCron('@every soon'), { id: 'descriptor' })
})

test('cron: plain words for the shapes the form offers', () => {
  assert.deepEqual(cronWords('* * * * *'), { id: 'everyMinute' })
  assert.deepEqual(cronWords('*/30 * * * *'), { id: 'everyMinutes', n: 30 })
  assert.deepEqual(cronWords('*/1 * * * *'), { id: 'everyMinute' })
  assert.deepEqual(cronWords('0 * * * *'), { id: 'hourly', minute: 0 })
  assert.deepEqual(cronWords('15 * * * *'), { id: 'hourly', minute: 15 })
  assert.deepEqual(cronWords('0 */6 * * *'), { id: 'everyHours', n: 6, minute: 0 })
  assert.deepEqual(cronWords('0 7 * * *'), { id: 'daily', time: '07:00' })
  assert.deepEqual(cronWords('30 18 * * *'), { id: 'daily', time: '18:30' })
  assert.deepEqual(cronWords('0 9 * * 1-5'), { id: 'weekdays', time: '09:00' })
  assert.deepEqual(cronWords('0 9 * * mon-fri'), { id: 'weekdays', time: '09:00' })
  assert.deepEqual(cronWords('0 9 * * 1'), { id: 'weekly', days: [1], time: '09:00' })
  assert.deepEqual(cronWords('0 9 * * 1,3'), { id: 'weekly', days: [1, 3], time: '09:00' })
  assert.deepEqual(cronWords('0 8 15 * *'), { id: 'monthly', day: 15, time: '08:00' })
  assert.deepEqual(cronWords('@hourly'), { id: 'hourly', minute: 0 })
  assert.deepEqual(cronWords('@daily'), { id: 'daily', time: '00:00' })
  assert.deepEqual(cronWords('@every 5m'), { id: 'every', n: 5, unit: 'minute' })
})

test('cron: no wording when none fits, and none for an invalid expression', () => {
  assert.equal(cronWords('0,30 9-17 * * 1-5'), null)
  assert.equal(cronWords('0 7 1 6 *'), null)
  assert.equal(cronWords('0 7 * * * *'), null)
  assert.equal(cronWords(''), null)
  assert.equal(cronWords('@every 1h30m'), null)
})

test('presets build and read back', () => {
  assert.equal(buildPreset('hourly'), '0 * * * *')
  assert.equal(buildPreset('daily', '07:00'), '0 7 * * *')
  assert.equal(buildPreset('daily', '18:45'), '45 18 * * *')
  assert.equal(buildPreset('weekdays', '09:30'), '30 9 * * 1-5')
  assert.equal(buildPreset('daily', ''), '0 9 * * *')
  assert.deepEqual(presetOf(''), { kind: 'none' })
  assert.deepEqual(presetOf('0 * * * *'), { kind: 'hourly' })
  assert.deepEqual(presetOf('15 * * * *'), { kind: 'custom' })
  assert.deepEqual(presetOf('0 7 * * *'), { kind: 'daily', time: '07:00' })
  assert.deepEqual(presetOf('0 9 * * 1-5'), { kind: 'weekdays', time: '09:00' })
  assert.deepEqual(presetOf('*/5 * * * *'), { kind: 'custom' })
  assert.equal(clock(7, 5), '07:05')
})

test('parameters: key=value lines, template gaps and the filled prompt', () => {
  assert.deepEqual(parseKeyValues('topic=AI trends\nformat = markdown\nbad line\nurl=a=b'), { topic: 'AI trends', format: 'markdown', url: 'a=b' })
  assert.equal(formatKeyValues({ a: '1', b: '2' }), 'a=1\nb=2')
  assert.equal(formatKeyValues('a=1'), 'a=1')
  const prompt = 'Summarise {{.topic}} in {{ .format }} format, {{.topic}} again.'
  assert.deepEqual(promptParams(prompt), ['topic', 'format'])
  assert.equal(fillPrompt(prompt, { topic: 'news' }), 'Summarise news in {{ .format }} format, news again.')
  assert.equal(fillPrompt('a {{.x}}', { x: '$&' }), 'a $&')
})

const T = (iso) => Date.parse(iso)

test('jobs: mark, duration and the one-line outcome', () => {
  assert.equal(jobMark('completed'), 'done')
  assert.equal(jobMark('failed'), 'failed')
  assert.equal(jobMark('cancelled'), 'stopped')
  assert.equal(jobMark('pending'), 'ready')
  assert.equal(jobDurationMs({ started_at: '2026-10-07T07:00:00Z', completed_at: '2026-10-07T07:01:06Z' }), 66000)
  assert.equal(jobDurationMs({ started_at: '2026-10-07T07:00:00Z' }), null)
  assert.equal(jobDurationMs({}), null)
  assert.deepEqual(jobLine({ status: 'failed', error: 'read_page: timeout after 20 s' }), { kind: 'error', text: 'read_page: timeout after 20 s' })
  assert.deepEqual(jobLine({ status: 'completed', result: '## Digest\n\n* one **item**' }), { kind: 'result', text: 'Digest' })
  assert.equal(jobLine({ status: 'completed', result: '| a | b |\n| - | - |\n\n* a [link](http://x) and **bold**' }).text, 'a link and bold')
  assert.equal(jobLine({ status: 'completed', result: '```\ncode\n```\nThen words.' }).text, 'Then words.')
  assert.deepEqual(jobLine({ status: 'completed', result: '' }), { kind: 'noOutput', text: '' })
  assert.equal(jobLine({ status: 'running' }).kind, 'running')
  assert.equal(jobLine({ status: 'pending' }).kind, 'pending')
  assert.equal(jobLine({ status: 'cancelled' }).kind, 'cancelled')
  assert.ok(jobLine({ status: 'completed', result: 'word '.repeat(60) }).text.endsWith('…'))
})

test('jobs: grouped by day, newest first', () => {
  const now = new Date(2026, 9, 7, 12, 0, 0).getTime()
  const at = (d, h) => new Date(2026, 9, d, h, 0, 0).toISOString()
  const jobs = [
    { id: 'a', created_at: at(5, 7), status: 'completed' },
    { id: 'b', created_at: at(7, 7), status: 'failed' },
    { id: 'c', created_at: at(7, 9), status: 'completed' },
    { id: 'd', created_at: at(6, 20), status: 'completed' },
  ]
  const groups = groupByDay(jobs, now)
  assert.deepEqual(groups.map(g => [g.day, g.jobs.map(j => j.id)]), [['today', ['c', 'b']], ['yesterday', ['d']], [null, ['a']]])
})

test('jobs: the week, per-task order, strip and counts', () => {
  const now = T('2026-10-07T12:00:00Z')
  const jobs = [
    { id: '1', task_id: 't1', status: 'completed', created_at: '2026-10-07T07:00:00Z' },
    { id: '2', task_id: 't1', status: 'failed', created_at: '2026-10-07T09:00:00Z' },
    { id: '3', task_id: 't2', status: 'running', created_at: '2026-10-07T10:00:00Z' },
    { id: '4', task_id: 't2', status: 'cancelled', created_at: '2026-10-05T10:00:00Z' },
    { id: '5', task_id: 't2', status: 'completed', created_at: '2026-09-20T10:00:00Z' },
  ]
  assert.deepEqual(weekSummary(jobs, now), { total: 4, finished: 1, failed: 1, stopped: 1, active: 1 })
  assert.deepEqual(jobsOf(jobs, 't1').map(j => j.id), ['2', '1'])
  assert.deepEqual(stripOfJobs(jobsOf(jobs, 't2')).map(s => s.status), ['done', 'stopped', 'running'])
  assert.deepEqual(failingTasks([{ id: 't1', name: 'a' }, { id: 't2', name: 'b' }], jobs).map(t => t.name), ['a'])
  assert.deepEqual(statusCounts(jobs), { all: 5, completed: 2, failed: 1, running: 1, cancelled: 1 })
})
