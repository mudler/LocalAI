import test from 'node:test'
import assert from 'node:assert/strict'
import {
  emptyForm, formOf, joinTimes, presetOf, scheduleText, triggerPayload, urlBase64ToBytes, runTone,
} from '../../src/utils/chatTrigger.js'

const form = (over) => ({ ...emptyForm(), ...over })

test('chat triggers: the text of every preset', () => {
  assert.equal(scheduleText(form({ preset: 'daily', time: '07:05' })), 'daily 07:05')
  assert.equal(scheduleText(form({ preset: 'weekdays' })), 'weekdays 08:00')
  assert.equal(scheduleText(form({ preset: 'weekly', days: ['thu', 'mon'], time: '09:30' })), 'weekly mon,thu 09:30')
  assert.equal(scheduleText(form({ preset: 'monthly', day: 15 })), 'monthly 15 08:00')
  assert.equal(scheduleText(form({ preset: 'once', date: '2026-12-24', time: '18:00' })), 'once 2026-12-24 18:00')
  assert.equal(scheduleText(form({ preset: 'hourly', minute: 15 })), '15 * * * *')
  assert.equal(scheduleText(form({ preset: 'everyHours', hours: 6 })), '0 */6 * * *')
  assert.equal(scheduleText(form({ preset: 'everyMinutes', minutes: 15 })), '*/15 * * * *')
  assert.equal(scheduleText(form({ preset: 'cron', cron: '  0 8 * * 1-5 ' })), '0 8 * * 1-5')
})

test('chat triggers: an incomplete form gives text the server refuses with a reason', () => {
  assert.equal(scheduleText(form({ preset: 'weekly', days: [] })), 'weekly  08:00')
  assert.equal(scheduleText(form({ preset: 'once', date: '' })), 'once  08:00')
})

test('chat triggers: a text becomes a preset only when the preset writes the same text', () => {
  for (const f of [
    form({ preset: 'daily', time: '07:05' }), form({ preset: 'weekdays', time: '18:00' }),
    form({ preset: 'weekly', days: ['mon', 'thu'], time: '09:30' }), form({ preset: 'monthly', day: 31, time: '00:10' }),
    form({ preset: 'once', date: '2026-12-24', time: '18:00' }), form({ preset: 'hourly', minute: 45 }),
    form({ preset: 'everyHours', hours: 12 }), form({ preset: 'everyMinutes', minutes: 5 }),
    form({ preset: 'cron', cron: '*/15 9-17 * * 1-5' }),
  ]) {
    const back = formOf(scheduleText(f))
    assert.equal(scheduleText(back), scheduleText(f))
    assert.equal(back.preset, f.preset)
  }
  for (const text of ['weekly MON,Thu 09:30', 'weekly mon,xyz 09:30', 'weekly thu,mon 09:30', 'monthly 0 08:00', 'monthly 05 08:00', 'daily  08:00', 'daily 25:00', 'once 2026-12-24 24:00', 'daily 8:00', '']) {
    assert.equal(formOf(text).preset, 'cron', text)
    assert.equal(scheduleText(formOf(text)), text.trim(), text)
  }
})

test('chat triggers: a stored file trigger is the file preset', () => {
  assert.equal(presetOf({ kind: 'file' }).preset, 'file')
  assert.equal(presetOf({ kind: 'schedule', schedule: 'daily 08:00' }).preset, 'daily')
})

test('chat triggers: the request body of a schedule and of a file trigger', () => {
  const base = { name: ' Digest ', prompt: 'Go.', enabled: true, dailyCap: '', agentId: '', model: 'm', sourceChatId: '', zone: 'Europe/Rome', folder: '', quiet: '' }
  assert.deepEqual(triggerPayload({ ...base, schedule: form({ preset: 'weekdays', time: '09:00' }) }), {
    name: 'Digest', prompt: 'Go.', kind: 'schedule', enabled: true, daily_cap: 0, agent_id: undefined, model: 'm', source_chat_id: undefined,
    schedule: 'weekdays 09:00', time_zone: 'Europe/Rome',
  })
  const file = triggerPayload({ ...base, schedule: form({ preset: 'file' }), folder: ' /Contracts ', quiet: '45', dailyCap: '5', agentId: 'a1' })
  assert.equal(file.kind, 'file')
  assert.equal(file.folder, '/Contracts')
  assert.equal(file.quiet_seconds, 45)
  assert.equal(file.daily_cap, 5)
  assert.equal(file.agent_id, 'a1')
  assert.equal('schedule' in file, false)
})

test('chat triggers: times are joined, and a bad time is left out', () => {
  assert.equal(joinTimes(['not a date'], 'UTC'), '')
  assert.match(joinTimes(['2026-10-27T08:00:00Z', '2026-10-28T08:00:00Z'], 'UTC'), /08:00.*; .*08:00/)
})

test('chat triggers: a run is done, failed, skipped or still going', () => {
  assert.deepEqual(['done', 'failed', 'skipped', 'running', 'queued'].map(runTone), ['done', 'failed', 'skipped', 'going', 'going'])
})

test('chat triggers: the push key is read from URL-safe base64', () => {
  assert.deepEqual([...urlBase64ToBytes('AQID')], [1, 2, 3])
  assert.deepEqual([...urlBase64ToBytes('-_8')], [251, 255])
})

test('chat triggers: a stored trigger fills the form and the form writes it back', async () => {
  const { emptyTrigger, triggerForm } = await import('../../src/utils/chatTrigger.js')
  assert.equal(emptyTrigger().enabled, true)
  assert.equal(emptyTrigger().schedule.preset, 'daily')
  const stored = { id: 't1', name: 'Digest', prompt: 'Go.', enabled: false, kind: 'schedule', schedule: 'weekdays 09:00', time_zone: 'Europe/Rome', daily_cap: 3, agent_id: 'a1', model: '' }
  const f = triggerForm(stored)
  assert.equal(f.schedule.preset, 'weekdays')
  assert.equal(f.zone, 'Europe/Rome')
  assert.equal(f.dailyCap, '3')
  const body = triggerPayload(f)
  assert.equal(body.schedule, 'weekdays 09:00')
  assert.equal(body.enabled, false)
  assert.equal(body.agent_id, 'a1')
})
