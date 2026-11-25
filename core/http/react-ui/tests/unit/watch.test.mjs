import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { jobLine, jobMark } from '../../src/utils/agentJobs.js'
import { runTone } from '../../src/utils/chatTrigger.js'
import { deniedWords, emptyWatch, localInput, offsetParts, retimePatch, undoPatch, watchForm, watchPayload, watchProblem, whenText } from '../../src/utils/watch.js'

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
  assert.equal(whenText('2026-11-26T14:45:00Z', 'UTC', now, 'en-GB'), 'Thu 14:45')
  assert.equal(whenText('2026-11-26T14:45:00Z', 'Europe/Rome', now, 'en-GB'), 'Thu 15:45')
  assert.match(whenText('2026-12-10T14:45:00Z', 'UTC', now, 'en-GB'), /10 Dec/)
  assert.equal(whenText('', 'UTC', now), '')
  assert.equal(localInput('2026-11-26T14:45:00Z', 'Europe/Rome'), '2026-11-26T15:45')
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
    const texts = walk({ watch: chats.watch, proposal: chats.proposal }, lang, [])
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
