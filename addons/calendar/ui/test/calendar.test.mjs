import test from 'node:test'
import assert from 'node:assert/strict'
import { load, React, renderToString } from './load.mjs'

const lib = () => import('../src/lib/calendar.js')

test('lib: a month grid starts on Monday and covers whole weeks', async () => {
  const { monthGrid, gridRange } = await lib()
  const weeks = monthGrid('2026-11-01', 1)
  assert.equal(weeks.length, 6)
  assert.equal(weeks[0][0], '2026-10-26')
  assert.equal(weeks[5][6], '2026-12-06')
  assert.deepEqual(gridRange('2026-11-01', 1), { from: '2026-10-26', to: '2026-12-06' })
  assert.equal(monthGrid('2027-02-01', 1).length, 4, 'February 2027 fits four weeks')
})

test('lib: dates move across months and years', async () => {
  const { addDays, addMonths, weekday } = await lib()
  assert.equal(addDays('2026-12-31', 1), '2027-01-01')
  assert.equal(addDays('2026-03-01', -1), '2026-02-28')
  assert.equal(addMonths('2026-11-01', 2), '2027-01-01')
  assert.equal(addMonths('2026-01-01', -1), '2025-12-01')
  assert.equal(weekday('2026-11-05'), 4, 'Thursday')
})

test('lib: an event shows on every day it covers, and a midnight end does not spill', async () => {
  const { byDay } = await lib()
  const trip = { id: 'a', allDay: true, start: '2026-11-03', end: '2026-11-05' }
  const late = { id: 'b', allDay: false, start: '2026-11-03T22:00', end: '2026-11-04T00:00' }
  const out = byDay([trip, late], '2026-11-01', '2026-11-30')
  assert.deepEqual(Object.keys(out).sort(), ['2026-11-03', '2026-11-04', '2026-11-05'])
  assert.equal(out['2026-11-04'].length, 1)
  assert.deepEqual(Object.keys(byDay([trip], '2026-11-04', '2026-11-04')), ['2026-11-04'])
})

test('lib: the agenda groups by start day in order', async () => {
  const { agenda } = await lib()
  const g = agenda([{ start: '2026-11-05T09:00' }, { start: '2026-11-05T12:00' }, { start: '2026-11-06' }])
  assert.deepEqual(g.map((d) => [d.date, d.items.length]), [['2026-11-05', 2], ['2026-11-06', 1]])
  assert.deepEqual(agenda([]), [])
})

test('lib: the form becomes the arguments of the tool, and back', async () => {
  const { emptyForm, argsOf, formOf, problem } = await lib()
  const f = { ...emptyForm('2026-11-12', ''), title: ' Dinner ', startTime: '20:00', endTime: '21:30', place: 'Trattoria' }
  assert.deepEqual(argsOf(f), { title: 'Dinner', start: '2026-11-12T20:00', end: '2026-11-12T21:30', allDay: false, notes: '', place: 'Trattoria', attendees: '', repeat: { freq: 'none' } })
  const weekly = { ...f, freq: 'weekly', interval: '2', until: '2026-12-31', id: 'ev-1', zone: 'Europe/Rome' }
  const a = argsOf(weekly)
  assert.deepEqual(a.repeat, { freq: 'weekly', interval: 2, until: '2026-12-31' })
  assert.equal(a.id, 'ev-1')
  assert.equal(a.zone, 'Europe/Rome')
  const day = argsOf({ ...f, allDay: true, endDate: '2026-11-14' })
  assert.deepEqual([day.start, day.end, day.allDay], ['2026-11-12', '2026-11-14', true])
  const back = formOf({ id: 'ev-1', title: 'Yoga', start: '2026-11-05T18:00', end: '2026-11-05T19:00', allDay: false, zone: 'UTC', repeat: { freq: 'weekly', interval: 3 } })
  assert.deepEqual([back.freq, back.interval, back.startTime, back.endTime], ['weekly', '3', '18:00', '19:00'])
  assert.equal(problem({ ...f, title: '' }), 'calendar.form.needTitle')
  assert.equal(problem({ ...f, endTime: '19:00' }), 'calendar.form.endBeforeStart')
  assert.equal(problem({ ...f, freq: 'daily', until: '2026-01-01' }), 'calendar.form.untilBeforeStart')
  assert.equal(problem(f), '')
})

const events = [
  { id: 'ev-1', title: 'Dinner with Sam', start: '2026-11-12T20:00', end: '2026-11-12T21:00', allDay: false, zone: 'Europe/Rome', weekday: 'Thursday', place: 'Trattoria', occurrence: '2026-11-12' },
  { id: 'ev-2', title: 'Yoga', start: '2026-11-12T18:00', end: '2026-11-12T19:00', allDay: false, zone: 'Europe/Rome', weekday: 'Thursday', repeat: { freq: 'weekly', interval: 1 }, occurrence: '2026-11-12' },
]
const make = async (extra) => {
  const { mod, sdk } = await load(extra)
  return mod.createAddon(sdk)
}
const draw = (addon, name, props) => renderToString(React.createElement(addon.components[name], props))

test('page: it draws nothing for a page it does not own, and a loading note for its own', async () => {
  const addon = await make()
  assert.equal(renderToString(React.createElement(addon.Component, { page: 'other' })), '')
  assert.match(renderToString(React.createElement(addon.Component, { page: 'calendar' })), /data-testid="calendar-loading"/)
})

test('page: the month shows the days and the events of each day', async () => {
  const addon = await make()
  const html = draw(addon, 'CalendarMonth', { cursor: '2026-11-01', today: '2026-11-05', occurrences: events, onPick() {}, onDay() {} })
  assert.match(html, /data-testid="calendar-month"/)
  assert.match(html, /data-date="2026-11-12"/)
  assert.match(html, /Dinner with Sam/)
  assert.match(html, /is-today/)
  assert.equal((html.match(/role="gridcell"/g) || []).length, 42)
})

test('page: the agenda lists events by day, or says it is empty', async () => {
  const addon = await make()
  const html = draw(addon, 'CalendarAgenda', { occurrences: events, onPick() {} })
  assert.match(html, /data-testid="calendar-agenda"/)
  assert.match(html, /Yoga/)
  assert.match(draw(addon, 'CalendarAgenda', { occurrences: [], onPick() {} }), /Nothing in the next 60 days/)
})

test('page: the form has every field and a repeat, and the detail of a series offers to skip a day', async () => {
  const addon = await make()
  const { emptyForm } = await lib()
  const form = draw(addon, 'CalendarForm', { initial: emptyForm('2026-11-12', ''), zone: 'Europe/Rome', onSaved() {}, onCancel() {} })
  for (const id of ['title', 'allDay', 'startDate', 'startTime', 'endDate', 'endTime', 'zone', 'place', 'attendees', 'notes', 'freq', 'save']) {
    assert.match(form, new RegExp(`data-testid="calendar-${id}"`), id)
  }
  const detail = draw(addon, 'CalendarDetail', { o: events[1], onEdit() {}, onDeleted() {}, onClose() {} })
  assert.match(detail, /data-testid="calendar-skip"/)
  assert.match(detail, /Edit the series/)
  assert.doesNotMatch(draw(addon, 'CalendarDetail', { o: events[0], onEdit() {}, onDeleted() {}, onClose() {} }), /calendar-skip/)
})

test('page: it reads in Italian, and in English for a language it has no text for', async () => {
  const it = await make({ language: () => 'it' })
  const italian = draw(it, 'CalendarAgenda', { occurrences: [], onPick() {} })
  assert.match(italian, /Niente nei prossimi 60 giorni/)
  const xx = await make({ language: () => 'xx' })
  assert.match(draw(xx, 'CalendarAgenda', { occurrences: [], onPick() {} }), /Nothing in the next 60 days/)
})
