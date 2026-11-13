import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  SLOT_CAPS, SLOT_NAMES, cardSize, cleanSlots, itemLabel, mergeSlots, openSlotPage, readSlots, resolveCommand, setPageOpener, slotItemOk, slotKey, tileMode, toolArguments, toolPrompt,
} from '../../src/utils/slots.js'
import { expandCommand, readCommands } from '../../src/utils/userCommands.js'
import { BUSY_MS, SILENT_MS, START_MS, WINDOW_MS, createFrameWatch } from '../../src/utils/frameWatch.js'

const page = (name, extra = {}) => ({ type: 'page', page: name, ...extra })
const tool = (name, extra = {}) => ({ type: 'tool', tool: name, label: name, ...extra })

test('slots: the three slots and their caps match the server', () => {
  assert.deepEqual(SLOT_NAMES, ['chatHeader', 'sidebarFooter', 'composerToolbar'])
  assert.deepEqual(SLOT_CAPS, { chatHeader: 4, sidebarFooter: 3, composerToolbar: 6 })
})

test('slots: an item shows only with the fields of its type', () => {
  assert.ok(slotItemOk(page('weather')))
  assert.ok(slotItemOk(page('standup', { folder: 4, label: 'Standup', icon: 'users' })))
  assert.ok(slotItemOk(tool('summarize_chat', { arguments: { words: 120, bullets: true } })))
  assert.ok(slotItemOk({ type: 'command', command: 'translate' }))
  for (const bad of [
    null, 'page', {}, { type: 'iframe', url: 'https://x' },
    page(''), page('../etc'), page('Weather'), page('a/b'), page('x', { folder: '4' }), page('x', { folder: 0 }),
    page('x', { tool: 'y' }), page('x', { label: '' }), page('x', { label: 'l'.repeat(41) }), page('x', { icon: '<svg>' }),
    { type: 'tool', tool: 'x' }, tool('a b'), tool('x', { arguments: { nested: { a: 1 } } }), tool('x', { arguments: [1] }),
    tool('x', { arguments: { t: 'x'.repeat(2100) } }), { type: 'command', command: '/translate' },
  ]) assert.equal(slotItemOk(bad), false, JSON.stringify(bad))
  assert.ok(toolArguments(undefined))
  assert.equal(toolArguments(Object.fromEntries(Array.from({ length: 17 }, (_, i) => [`k${i}`, i]))), false)
})

test('slots: the file is read slot by slot, each item once, within the cap', () => {
  const s = readSlots({ slots: {
    chatHeader: [page('a'), page('a'), page('b'), { type: 'bad' }, page('c'), page('d'), page('e')],
    sidebarFooter: 'nope',
    footer: [page('x')],
  } })
  assert.deepEqual(s.chatHeader.map((i) => i.page), ['a', 'b', 'c', 'd'])
  assert.deepEqual(s.sidebarFooter, [])
  assert.deepEqual(s.composerToolbar, [])
  assert.equal(s.footer, undefined)
  assert.deepEqual(readSlots(null), { chatHeader: [], sidebarFooter: [], composerToolbar: [] })
  assert.notEqual(slotKey(tool('t', { arguments: { a: 1, b: 2 } })), slotKey(tool('t', { arguments: { a: 2 } })))
  assert.equal(slotKey(tool('t', { arguments: { a: 1, b: 2 } })), slotKey(tool('t', { label: 'Other', arguments: { b: 2, a: 1 } })))
  assert.equal(slotKey(page('a')), slotKey(page('a', { folder: 0 })))
})

test('slots: a save writes the known slots and drops empty ones', () => {
  assert.deepEqual(cleanSlots({ chatHeader: [page('a', { label: '' }), { type: 'page' }], sidebarFooter: [], composerToolbar: [tool('t')] }), {
    chatHeader: [{ type: 'page', page: 'a' }],
    composerToolbar: [{ type: 'tool', tool: 't', label: 't' }],
  })
  assert.deepEqual(cleanSlots({}, { future: [1] }), { future: [1] }, 'a slot this version does not know is kept')
})

test('slots: an edit merges with a change the assistant made meanwhile', () => {
  const base = { chatHeader: [page('a'), page('b')] }
  const mine = { chatHeader: [page('b'), page('c')], sidebarFooter: [], composerToolbar: [] } // removed a, added c
  const theirs = { chatHeader: [page('a'), page('b'), page('d')], composerToolbar: [tool('t')] } // added d and t
  const out = mergeSlots(base, mine, theirs)
  assert.deepEqual(out.chatHeader.map((i) => i.page), ['b', 'c', 'd'])
  assert.deepEqual(out.composerToolbar.map((i) => i.tool), ['t'])
  // The other writer removed b: it stays removed.
  const gone = mergeSlots(base, { chatHeader: [page('a'), page('b')] }, { chatHeader: [page('a')] })
  assert.deepEqual(gone.chatHeader.map((i) => i.page), ['a'])
  // The cap holds after the merge.
  const full = mergeSlots({}, { chatHeader: [page('a'), page('b'), page('c')] }, { chatHeader: [page('x'), page('y')] })
  assert.equal(full.chatHeader.length, 4)
})

test('slots: labels, the tool request and the modes of a tile', () => {
  assert.equal(itemLabel(page('weather'), 'Weather'), 'Weather')
  assert.equal(itemLabel(page('weather', { label: ' Sky ' }), 'Weather'), 'Sky')
  assert.equal(itemLabel(page('weather')), 'weather')
  assert.equal(itemLabel({ type: 'command', command: 'tr' }), '/tr')
  assert.equal(toolPrompt(tool('summarize_chat')), 'Run the tool summarize_chat.')
  assert.equal(toolPrompt(tool('translate', { arguments: { to: 'it' } })), 'Run the tool translate with the arguments {"to":"it"}.')
  assert.deepEqual(tileMode(false, { widget: { size: 'large' } }), { mode: 'widget', size: 'compact' })
  assert.deepEqual(tileMode(true, { widget: { size: 'large' } }), { mode: 'widget', size: 'large' })
  assert.equal(cardSize({ widget: { size: 'huge' } }), 'medium')
})

test('slots: a page button opens the page until a dock registers its opener', () => {
  const opened = []
  assert.equal(openSlotPage({ name: 'a', folder: 0 }, (r) => opened.push(`page:${r.name}`)), 'page')
  const off = setPageOpener((r) => { opened.push(`dock:${r.name}`) })
  assert.equal(openSlotPage({ name: 'b', folder: 0 }, (r) => opened.push(`page:${r.name}`)), 'dock')
  off()
  assert.equal(openSlotPage({ name: 'c', folder: 0 }, (r) => opened.push(`page:${r.name}`)), 'page')
  assert.deepEqual(opened, ['page:a', 'dock:b', 'page:c'])
})

function clock() {
  let t = 1000
  return { now: () => t, tick: (ms) => { t += ms } }
}

test('frame budget: a quiet page keeps running', () => {
  const c = clock()
  const w = createFrameWatch({ now: c.now })
  for (let i = 0; i < 60; i++) {
    c.tick(1000)
    w.beat(80) // a clock that redraws: well inside the budget
    assert.equal(w.check(), '')
  }
})

test('frame budget: a page that keeps the thread busy is stopped', () => {
  const c = clock()
  const w = createFrameWatch({ now: c.now })
  let why = ''
  for (let i = 0; i < 10 && !why; i++) {
    c.tick(1000)
    w.beat(600)
    why = w.check()
  }
  assert.equal(why, 'busy')
  assert.ok(BUSY_MS < WINDOW_MS)
  assert.equal(w.check(), 'busy', 'a stopped frame stays stopped')
})

test('frame budget: a page that stops answering is stopped, but not while out of sight', () => {
  const c = clock()
  const w = createFrameWatch({ now: c.now })
  c.tick(1000)
  w.beat(0)
  c.tick(SILENT_MS + 500)
  assert.equal(w.check(false), '', 'a hidden frame is slowed by the browser')
  w.resume()
  c.tick(1000)
  assert.equal(w.check(true), '', 'the silence clock starts again when it is back')
  c.tick(SILENT_MS)
  assert.equal(w.check(true), 'silent')
})

test('frame budget: a page whose first beat never comes is stopped', () => {
  const c = clock()
  const w = createFrameWatch({ now: c.now })
  c.tick(START_MS - 100)
  assert.equal(w.check(), '')
  c.tick(200)
  assert.equal(w.check(), 'noStart')
})

test('frame budget: a beat cannot report more than the window', () => {
  const c = clock()
  const w = createFrameWatch({ now: c.now })
  c.tick(1000)
  w.beat(Number.POSITIVE_INFINITY)
  w.beat(-50)
  w.beat(Number.NaN)
  assert.equal(w.check(), '', 'only a finite busy time counts')
  w.beat(1e9)
  assert.equal(w.check(), 'busy')
})

test('slots: a command item finds its command in commands.json, and a gone one shows nothing', () => {
  const commands = readCommands({ commands: [
    { name: 'standup', description: 'Write my standup', type: 'prompt', template: 'Standup for the last {{days}} day(s).', args: [{ name: 'days', default: 1 }] },
    { name: 'summarize', description: 'Summarize this chat', type: 'tool', tool: 'summarize_chat' },
    { name: 'broken', description: 'No template', type: 'prompt' },
  ] })
  const item = (command) => ({ type: 'command', command })
  assert.equal(resolveCommand(item('standup'), commands).name, 'standup')
  assert.equal(resolveCommand(item('summarize'), commands).tool, 'summarize_chat')
  assert.equal(resolveCommand(item('gone'), commands), null, 'a command that is not in the file')
  assert.equal(resolveCommand(item('broken'), commands), null, 'a command the file cannot use')
  assert.equal(resolveCommand(item('standup'), null), null, 'no commands.json')
  assert.equal(resolveCommand({ type: 'tool', tool: 'standup', label: 'x' }, commands), null, 'only a command item')
  // The text that goes in the message box is the one of the slash menu.
  assert.equal(expandCommand(resolveCommand(item('standup'), commands)), 'Standup for the last 1 day(s).')
  // A name a command cannot have is not a command item.
  for (const bad of ['1standup', 'Standup', 'a'.repeat(33)]) assert.equal(slotItemOk(item(bad)), false, bad)
  assert.ok(slotItemOk(item('a'.repeat(32))))
})
