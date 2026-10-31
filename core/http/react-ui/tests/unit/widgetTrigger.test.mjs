import test from 'node:test'
import assert from 'node:assert/strict'
import { cadenceOf, footerState, linkedTrigger, plainSummary, scheduledCount } from '../../src/utils/widgetTrigger.js'

const daily = { id: 't1', kind: 'schedule', schedule: 'daily 08:00', enabled: true, plan: { widget: 'inbox' } }

test('a widget finds its trigger by the id in home.json first, then by the plan', () => {
  const other = { ...daily, id: 't2', plan: { widget: 'other' } }
  assert.equal(linkedTrigger({ id: 'inbox', trigger: 't2' }, [daily, other], []).trigger.id, 't2')
  assert.equal(linkedTrigger({ id: 'inbox' }, [daily, other], []).trigger.id, 't1')
  assert.equal(linkedTrigger({ id: 'inbox', trigger: 'gone' }, [daily], []).trigger.id, 't1', 'a stale id falls back to the plan')
  const prop = { id: 'p1', plan: { widget: 'tasks' }, source_chat_id: 'c1' }
  const got = linkedTrigger({ id: 'tasks' }, [daily], [prop])
  assert.equal(got.trigger, null)
  assert.equal(got.proposal.id, 'p1')
  assert.deepEqual(linkedTrigger({ id: 'x' }, [daily], [prop]), { trigger: null, proposal: null })
})

test('the cadence tag of a trigger', () => {
  assert.equal(cadenceOf(daily), 'daily')
  assert.equal(cadenceOf({ ...daily, schedule: 'weekdays 09:00' }), 'weekdays')
  assert.equal(cadenceOf({ ...daily, schedule: 'weekly fri 08:00' }), 'weekly')
  assert.equal(cadenceOf({ ...daily, schedule: '0 * * * *' }), 'hourly')
  assert.equal(cadenceOf({ ...daily, schedule: 'monthly 1 08:00' }), 'monthly')
  assert.equal(cadenceOf({ ...daily, schedule: '30 9 * * 1' }), '')
  assert.equal(cadenceOf({ ...daily, enabled: false }), 'paused')
  assert.equal(cadenceOf({ kind: 'file', enabled: true }), 'file')
  assert.equal(cadenceOf(null), '')
})

test('the footer of a widget', () => {
  const ok = { state: 'ok', items: [{ label: 'a' }], value: null, source: null }
  const empty = { state: 'empty', items: [], value: null, source: null }
  assert.equal(footerState({ data: ok }), 'file')
  assert.equal(footerState({ data: { ...ok, source: { kind: 'page', page: 'tasks' } } }), 'page')
  assert.equal(footerState({ data: empty }), 'missing')
  assert.equal(footerState({ data: { ...ok, state: 'stale' } }), 'invalid')
  assert.equal(footerState({ data: ok, proposal: { id: 'p' } }), 'waiting')
  assert.equal(footerState({ data: ok, trigger: daily }), 'firstRun')
  assert.equal(footerState({ data: ok, trigger: { ...daily, last_run: { status: 'done' } } }), 'fresh')
  assert.equal(footerState({ data: ok, trigger: { ...daily, last_run: { status: 'running' } } }), 'running')
  assert.equal(footerState({ data: ok, trigger: { ...daily, last_run: { status: 'queued' } } }), 'running')
  assert.equal(footerState({ data: ok, trigger: { ...daily, last_run: { status: 'failed' } } }), 'stale')
  assert.equal(footerState({ data: empty, trigger: { ...daily, last_run: { status: 'failed' } } }), 'failedEmpty')
  assert.equal(footerState({ data: ok, trigger: { ...daily, enabled: false, last_run: { status: 'failed' } } }), 'paused')
  assert.equal(footerState({ data: { ...ok, state: 'stale' }, trigger: { ...daily, last_run: { status: 'done' } } }), 'invalid', 'a broken file wins over a good run')
  assert.equal(footerState({ data: ok, trigger: { ...daily, last_run: { status: 'skipped' } } }), 'fresh', 'a skipped run leaves the data as it was')
})

test('the count of widgets that update on a schedule', () => {
  const ws = [{ id: 'inbox' }, { id: 'tasks' }, { id: 'week', trigger: 't9' }]
  assert.equal(scheduledCount(ws, [daily, { ...daily, id: 't9', plan: {} }, { ...daily, id: 't3', enabled: false, plan: { widget: 'tasks' } }]), 2)
})

test('the summary of a trigger without its zone', () => {
  assert.equal(plainSummary({ summary: 'Every day at 08:00 (Europe/Rome)', time_zone: 'Europe/Rome' }), 'Every day at 08:00')
  assert.equal(plainSummary({ summary: 'Every day at 08:00', time_zone: '' }), 'Every day at 08:00')
  assert.equal(plainSummary({ schedule: 'daily 08:00' }), 'daily 08:00')
})
