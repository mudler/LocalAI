import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { jobLine, jobMark } from '../../src/utils/agentJobs.js'
import { runTone } from '../../src/utils/chatTrigger.js'
import { emptyWatch, localInput, offsetParts, offsetWords, reminderOffsetWords, retimePatch, ruleOffsetWords, undoPatch, watchForm, watchPayload, watchProblem, whenText } from '../../src/utils/watch.js'

// Words the owner must never read: internal terms of the host. A watch is
// never called a trigger, except as the name of the page.
const DENIED_WORDS = ['payload', 'reconcil', 'actor', 'dedupe', 'event id', ' key', 'webhook']

// The denied words a text holds, lower case; empty when it is clean.
function deniedWords(text) {
  const lower = ` ${String(text || '').toLowerCase()}`
  return DENIED_WORDS.filter((w) => lower.includes(w))
}

test('a watch rule goes to the form and back unchanged', () => {
  const stored = { source: 'calendar', watch: { offset_minutes: -30, all_day: 'none', quiet_from: '22:00', quiet_to: '07:00', tasks: 'brief for calls', types: ['calendar.event.created'] } }
  const f = watchForm(stored)
  assert.equal(f.offset, '-30')
  assert.deepEqual(watchPayload(f), { offset_minutes: -30, no_default: undefined, all_day: 'none', quiet_from: '22:00', quiet_to: '07:00', tasks: 'brief for calls', types: ['calendar.event.created'] })
})

test('no default reminder and the defaults of a new watch', () => {
  const f = { ...emptyWatch(), source: 'calendar', offset: 'none' }
  assert.equal(watchPayload(f).no_default, true)
  assert.equal(watchPayload(f).offset_minutes, 0)
  assert.equal(watchPayload(emptyWatch()).offset_minutes, -15)
  assert.equal(watchPayload(emptyWatch()).all_day, undefined, 'morning is the server default')
  assert.equal(watchForm({ source: 'calendar', watch: { no_default: true } }).offset, 'none')
})

test('quiet hours need both ends, and the source must be one the server lists', () => {
  const srcs = [{ id: 'calendar' }]
  assert.equal(watchProblem({ ...emptyWatch(), source: '' }, srcs), 'source')
  assert.equal(watchProblem({ ...emptyWatch(), source: 'mail' }, srcs), 'source')
  assert.equal(watchProblem({ ...emptyWatch(), source: 'calendar', quietFrom: '22:00' }, srcs), 'quiet')
  assert.equal(watchProblem({ ...emptyWatch(), source: 'calendar' }, srcs), '')
  assert.equal(watchPayload({ ...emptyWatch(), quietFrom: '22:00' }).quiet_from, undefined, 'one end alone is not sent')
})

test('an offset is said in the largest whole unit', () => {
  assert.deepEqual(offsetParts(-15), { key: 'minutesBefore', count: 15 })
  assert.deepEqual(offsetParts(-60), { key: 'hoursBefore', count: 1 })
  assert.deepEqual(offsetParts(-1440), { key: 'daysBefore', count: 1 })
  assert.deepEqual(offsetParts(30), { key: 'minutesAfter', count: 30 })
  assert.deepEqual(offsetParts(0), { key: 'atTime', count: 0 })
})

test('a due time is the weekday and time in the zone, with the date when far away', () => {
  const now = new Date('2026-11-25T09:00:00Z')
  assert.equal(whenText('2026-11-26T14:45:00Z', 'UTC', now, 'en-GB', 'UTC'), 'Thu 14:45')
  assert.equal(whenText('2026-11-26T14:45:00Z', 'Europe/Rome', now, 'en-GB', 'Europe/Rome'), 'Thu 15:45')
  assert.match(whenText('2026-12-10T14:45:00Z', 'UTC', now, 'en-GB', 'UTC'), /10 Dec/)
  assert.equal(whenText('', 'UTC', now), '')
  assert.equal(localInput('2026-11-26T14:45:00Z', 'Europe/Rome'), '2026-11-26T15:45')
})

test('a due time after midnight reads 00, never 24', () => {
  const now = new Date('2026-11-25T09:00:00Z')
  assert.equal(whenText('2026-11-26T00:05:00Z', 'UTC', now, 'en-US', 'UTC'), 'Thu 00:05')
  assert.equal(whenText('2026-11-26T00:05:00Z', 'UTC', now, 'it', 'UTC').endsWith('00:05'), true)
  assert.equal(localInput('2026-11-26T00:05:00Z', 'UTC'), '2026-11-26T00:05')
})

test('a due time names its zone when the viewer\'s clock reads another time', () => {
  const now = new Date('2026-11-25T09:00:00Z')
  const there = whenText('2026-11-26T14:45:00Z', 'Europe/Rome', now, 'en-GB', 'America/New_York')
  assert.match(there, /15:45/, there)
  assert.match(there, /CET|GMT\+1/, there)
  assert.equal(whenText('2026-11-26T14:45:00Z', 'Europe/Berlin', now, 'en-GB', 'Europe/Rome'), 'Thu 15:45', 'the same clock needs no zone')
})

test('the offset of a rule and of a reminder are said the same way everywhere', () => {
  const t = (key, o) => (o && 'count' in o ? `${key}:${o.count}` : key)
  assert.equal(offsetWords(t, -15), 'watch.minutesBefore:15')
  assert.equal(offsetWords(t, -60, 'chats:'), 'chats:watch.hoursBefore:1')
  assert.equal(ruleOffsetWords(t, { no_default: true }), 'watch.none')
  assert.equal(ruleOffsetWords(t, { offset_minutes: -1440 }), 'watch.daysBefore:1')
  assert.equal(reminderOffsetWords(t, { adjusted: 'quiet', offset_minutes: -15 }), 'watch.adjQuiet')
  assert.equal(reminderOffsetWords(t, { adjusted: 'all_day' }), 'watch.adjAllDay')
  assert.equal(reminderOffsetWords(t, { offset_minutes: 0 }), 'watch.atTime:0')
})

test('each action has an undo that sends the old values back', () => {
  assert.deepEqual(undoPatch({ snoozed_to: null }, 'snooze'), { snoozed_to: '' })
  assert.deepEqual(undoPatch({ snoozed_to: '2026-11-26T10:00:00Z' }, 'snooze'), { snoozed_to: '2026-11-26T10:00:00Z' })
  assert.deepEqual(undoPatch({ skipped: false }, 'skip'), { skip: false })
  assert.deepEqual(undoPatch({ offset_minutes: -15 }, 'retime'), { offset_minutes: -15 })
  assert.deepEqual(undoPatch({ at: '2026-11-26T10:00:00Z', offset_minutes: -15 }, 'retime'), { at: '2026-11-26T10:00:00Z' })
  assert.deepEqual(retimePatch('2026-11-26T14:45'), { at: '2026-11-26 14:45' })
})

// Every string of the watch pages and of the proposal card, in every locale,
// is free of the host's internal words.
test('the words of the watch and the proposal card hold no internal term', () => {
  const root = join(dirname(fileURLToPath(import.meta.url)), '../../public/locales')
  const walk = (v, path, out) => {
    if (typeof v === 'string') out.push([path, v])
    else if (v && typeof v === 'object') for (const [k, x] of Object.entries(v)) walk(x, `${path}.${k}`, out)
    return out
  }
  for (const lang of readdirSync(root)) {
    const chats = JSON.parse(readFileSync(join(root, lang, 'chats.json'), 'utf8'))
    const texts = walk({ watch: chats.watch, proposal: chats.proposal, inboxWatch: chats.inbox?.codes?.watch }, lang, [])
    for (const [path, text] of texts) {
      assert.deepEqual(deniedWords(text), [], `${path}: ${text}`)
      if (path.includes('.watch.')) assert.ok(!/\btrigger/i.test(text) || lang !== 'en', `${path} calls a watch a trigger: ${text}`)
    }
  }
  assert.deepEqual(deniedWords('Moved the reminder for "Standup" to Thu 15:45.'), [])
  assert.deepEqual(deniedWords('the payload key'), ['payload', ' key'])
})

test('a row of what a watch did shows as done, with its line', () => {
  assert.equal(jobMark('handled'), 'done')
  assert.equal(runTone('handled'), 'done')
  assert.deepEqual(jobLine({ status: 'handled', reason: 'Moved the reminder for "Standup" to Thu 15:45.' }), { kind: 'done', text: 'Moved the reminder for "Standup" to Thu 15:45.' })
})
