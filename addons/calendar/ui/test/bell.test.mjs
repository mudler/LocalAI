import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { hostUi, load, React } from './load.mjs'

// The bell of an event that has a reminder, and the reminder in the event's
// details: Skip asks the host to skip it, Change time… opens the watch.
const require = createRequire(join(hostUi, 'package.json'))
const { JSDOM } = require('jsdom')
const dom = new JSDOM('<div id="root"></div>', { url: 'http://localhost/' })
globalThis.window = dom.window
globalThis.document = dom.window.document
globalThis.IS_REACT_ACT_ENVIRONMENT = true
const { createRoot } = require('react-dom/client')
const { act } = React

const yoga = { id: 'ev-2', title: 'Yoga', start: '2026-11-12T18:00', end: '2026-11-12T19:00', allDay: false, zone: 'UTC', occurrence: '2026-11-12' }
const dinner = { id: 'ev-1', title: 'Dinner', start: '2026-11-12T20:00', end: '2026-11-12T21:00', allDay: false, zone: 'UTC', occurrence: '2026-11-12' }
const bell = { id: 'r1', itemId: 'ev-2', watchId: 'w1', due: '2026-11-12T17:45:00Z', skipped: false }

async function mount(extra, name, props) {
  const { mod, sdk } = await load(extra)
  const addon = mod.createAddon(sdk)
  const el = document.createElement('div')
  document.body.appendChild(el)
  const root = createRoot(el)
  await act(async () => root.render(React.createElement(addon.components[name], props)))
  return { el, async close() { await act(async () => root.unmount()); el.remove() } }
}

test('bell: an event with a reminder shows the bell and its time; others show none', async () => {
  const m = await mount({}, 'CalendarAgenda', { occurrences: [yoga, dinner], onPick() {}, bells: { 'ev-2': bell }, zone: 'UTC' })
  const lines = [...m.el.querySelectorAll('[data-testid="calendar-event"]')]
  const withBell = lines.filter((l) => l.querySelector('[data-testid="calendar-bell"]'))
  assert.equal(withBell.length, 1)
  assert.match(withBell[0].textContent, /Yoga/)
  assert.match(withBell[0].querySelector('[data-testid="calendar-bell"]').textContent, /17:45/)
  await m.close()
})

test('bell: Skip asks the host to skip the reminder of this add-on, then reads the bells again', async () => {
  const skipped = []
  let changed = 0
  const m = await mount({ skipReminder: async (source, w, r) => { skipped.push([source, w, r]); return {} } }, 'CalendarDetail',
    { o: yoga, onEdit() {}, onDeleted() {}, onClose() {}, bell, zone: 'UTC', onBellChanged: () => { changed++ } })
  const box = m.el.querySelector('[data-testid="calendar-reminder"]')
  assert.match(box.textContent, /17:45/)
  await act(async () => box.querySelector('[data-testid="calendar-reminder-skip"]').click())
  assert.deepEqual(skipped, [['calendar', 'w1', 'r1']])
  assert.equal(changed, 1)
  assert.equal(m.el.querySelector('[role="alert"]'), null)
  await m.close()
})

test('bell: a failed skip says so and keeps the reminder', async () => {
  let changed = 0
  const m = await mount({ skipReminder: async () => { throw new Error('refused') } }, 'CalendarDetail',
    { o: yoga, onEdit() {}, onDeleted() {}, onClose() {}, bell, zone: 'UTC', onBellChanged: () => { changed++ } })
  await act(async () => m.el.querySelector('[data-testid="calendar-reminder-skip"]').click())
  assert.ok(m.el.querySelector('[role="alert"]'), 'the page says the skip failed')
  assert.equal(changed, 0)
  await m.close()
})

test('bell: Change time… opens the watch, where the time is changed', async () => {
  const went = []
  const m = await mount({ navigate: (to) => went.push(to) }, 'CalendarDetail',
    { o: yoga, onEdit() {}, onDeleted() {}, onClose() {}, bell, zone: 'UTC', onBellChanged() {} })
  const btn = m.el.querySelector('[data-testid="calendar-reminder-watch"]')
  assert.match(btn.textContent, /Change time/)
  await act(async () => btn.click())
  assert.deepEqual(went, ['/app/triggers/w1'])
  await m.close()
})

test('bell: an event without a reminder has no reminder in its details', async () => {
  const m = await mount({}, 'CalendarDetail', { o: dinner, onEdit() {}, onDeleted() {}, onClose() {} })
  assert.equal(m.el.querySelector('[data-testid="calendar-reminder"]'), null)
  await m.close()
})

test('bell: a repeating event shows the bell only on the occurrence its reminder is for', async () => {
  const day1 = { ...yoga, occurrence: '2026-11-12', repeat: { freq: 'daily' } }
  const day2 = { ...yoga, start: '2026-11-13T18:00', end: '2026-11-13T19:00', occurrence: '2026-11-13', repeat: { freq: 'daily' } }
  const forDay2 = { ...bell, due: '2026-11-13T17:45:00Z', itemTime: '2026-11-13T18:00:00Z' }
  const m = await mount({}, 'CalendarAgenda', { occurrences: [day1, day2], onPick() {}, bells: { 'ev-2': forDay2 }, zone: 'UTC' })
  const lines = [...m.el.querySelectorAll('[data-testid="calendar-event"]')]
  assert.equal(lines.length, 2)
  assert.equal(lines[0].querySelector('[data-testid="calendar-bell"]'), null, 'the first occurrence has no reminder')
  assert.ok(lines[1].querySelector('[data-testid="calendar-bell"]'))
  await m.close()
})
