import { test } from 'node:test'
import assert from 'node:assert/strict'
import vm from 'node:vm'
import { buildPageSrcdoc, createPageHost } from '../../src/utils/pagesBridge.js'
import { createFrameWatch } from '../../src/utils/frameWatch.js'
import {
  MAX_LIVE_FRAMES, MAX_PAGE_TILES, compactOf, createFrameSlots, eligiblePages, nextFrame, refKey, tilePagePath, tileRef, tileState, tileTitle,
} from '../../src/utils/pageTiles.js'
import { cleanWidgets, widgetList } from '../../src/utils/wellKnown.js'

// The frame's bootstrap in a VM: the attributes it sets on <html>, the API it
// defines and the port it talks over.
function boot(opts, { timers = null, clock = null, before = null } = {}) {
  const attrs = {}
  let onMessage = null
  let capture = null
  const sandbox = {
    console: { warn() {}, error() {}, log() {} },
    setTimeout, Promise,
    setInterval: (fn, ms) => { if (timers) timers.push({ fn, ms }); return 1 }, Response, Request, Blob, TextEncoder, DOMException, JSON, Object, String, Error, TypeError, URL,
    document: {
      baseURI: 'http://localai.test/app', readyState: 'complete',
      documentElement: { setAttribute: (k, v) => { attrs[k] = v }, removeAttribute: (k) => { delete attrs[k] }, style: {} },
      addEventListener() {}, getElementById: () => null, getElementsByName: () => [],
    },
    scrollTo() {},
    addEventListener: (t, fn, c) => { if (t === 'message') { onMessage = fn; capture = c } },
  }
  if (clock) sandbox.Date = { now: () => clock.t }
  sandbox.window = sandbox
  sandbox.parent = {}
  vm.createContext(sandbox)
  vm.runInContext(/<script>([\s\S]*?)<\/script>/.exec(buildPageSrcdoc('', opts))[1], sandbox)
  let deliver = null
  const posted = []
  const port = { postMessage(m) { posted.push(m) }, set onmessage(fn) { deliver = fn } }
  // The page's own script runs after the bridge and before the init.
  before?.(sandbox)
  let stopped = 0
  onMessage({ source: sandbox.parent, data: { type: 'localai:init' }, ports: [port], stopImmediatePropagation() { stopped++ } })
  return { attrs, posted, capture, stopped, localai: sandbox.localai, deliver: (m) => deliver({ data: m }) }
}

test('frame: a page knows its mode and size, and so does its CSS', () => {
  const page = boot({ name: 'clock', title: 'Clock' })
  assert.equal(page.localai.page.mode, 'page')
  assert.equal(page.localai.page.size, '')
  assert.equal(page.attrs['data-mode'], 'page')
  assert.equal(page.attrs['data-size'], undefined)

  const tile = boot({ name: 'clock', title: 'Clock', mode: 'widget', size: 'small' })
  assert.equal(tile.localai.page.mode, 'widget')
  assert.equal(tile.localai.page.size, 'small')
  assert.equal(tile.attrs['data-mode'], 'widget')
  assert.equal(tile.attrs['data-size'], 'small')
  assert.ok(Object.isFrozen(tile.localai.page), 'a page cannot change its mode')

  assert.equal(boot({ name: 'x', mode: 'widget', size: '"><script>' }).localai.page.size, 'medium', 'an unknown size is medium')
  assert.equal(boot({ name: 'x', mode: 'fullscreen', size: 'large' }).localai.page.mode, 'page', 'an unknown mode is page')
  assert.equal(boot({ name: 'x', mode: 'fullscreen', size: 'large' }).localai.page.size, '', 'only a tile has a size')
})

test('frame: a page hears when it goes out of sight and back', () => {
  const tile = boot({ name: 'clock', mode: 'widget', size: 'medium' })
  const heard = []
  const off = tile.localai.on('visibility', (d) => heard.push(d.visible))
  tile.deliver({ event: 'visibility', data: { visible: false } })
  tile.deliver({ event: 'visibility', data: { visible: true } })
  off()
  tile.deliver({ event: 'visibility', data: { visible: false } })
  assert.deepEqual(heard, [false, true])
  assert.throws(() => tile.localai.on('unknown', () => {}), /unknown event/)
})

test('page host: emits files.changed and visibility, and nothing else', () => {
  const sent = []
  const h = createPageHost({ post: (m) => sent.push(m), files: {}, chat: {} })
  h.emit('visibility', { visible: 0 })
  h.emit('visibility', { visible: 'yes', extra: 1 })
  h.emit('files.changed', { paths: ['a.json'] })
  h.emit('navigate', { to: 'x' })
  assert.deepEqual(sent, [
    { event: 'visibility', data: { visible: false } },
    { event: 'visibility', data: { visible: true } },
    { event: 'files.changed', data: { paths: ['a.json'] } },
  ])
})

test('page tiles: the widget names a page, and the tile says what it shows', () => {
  assert.deepEqual(tileRef({ page: 'news' }), { name: 'news', folder: 0 })
  assert.deepEqual(tileRef({ page: 'news', folder: 7 }), { name: 'news', folder: 7 })
  assert.deepEqual(tileRef({ page: 'news', folder: '7' }), { name: 'news', folder: 0 })
  for (const bad of ['', 'News', '../x', 'a/b', 7, undefined]) assert.equal(tileRef({ page: bad }), null)

  assert.equal(tileTitle({ title: ' Mine ' }, { title: 'Page', widget: { title: 'Tile' } }), 'Mine')
  assert.equal(tileTitle({ title: '' }, { title: 'Page', widget: { title: 'Tile' } }), 'Tile')
  assert.equal(tileTitle({}, { title: 'Page', widget: {} }), 'Page')
  assert.equal(tileTitle({ page: 'news' }, null), 'news')
  assert.equal(tilePagePath({ name: 'news', folder: 0 }), '/app/pages/news')
  assert.equal(tilePagePath({ name: 'news', folder: 3 }), '/app/pages/shared/3/news')
  assert.equal(refKey({ name: 'news', folder: 3 }), '3/news')

  const own = { source: 'own', widget: { size: 'small' } }
  assert.equal(tileState({ loading: true }), 'loading')
  assert.equal(tileState({ page: own }), 'ready')
  assert.equal(tileState({ page: { source: 'own' } }), 'noWidget')
  assert.equal(tileState({ error: { status: 404 } }), 'missing')
  assert.equal(tileState({ error: { status: 400 } }), 'missing')
  assert.equal(tileState({ error: { status: 403, type: 'authorization_error' } }), 'missing', 'a page the user can no longer read')
  assert.equal(tileState({ error: { status: 403, type: 'shared_pages_off' } }), 'off')
  assert.equal(tileState({ error: { status: 500 } }), 'error')
  const shared = { source: 'shared', widget: { size: 'large' } }
  assert.equal(tileState({ page: shared, consent: 'none' }), 'needsAllow')
  assert.equal(tileState({ page: shared, consent: 'changed' }), 'needsAllow')
  assert.equal(tileState({ page: shared, consent: 'allowed' }), 'needsAllow', 'opening a page is not running it on Home or in a slot')
  assert.equal(tileState({ page: shared, consent: 'allowed', embed: true }), 'ready')
  assert.equal(tileState({ page: shared, consent: 'changed', embed: true }), 'needsAllow', 'a changed page asks again')
  assert.equal(tileState({ page: { source: 'shared' }, consent: 'allowed' }), 'noWidget')
  // The dock shows any page: no widget block needed, the same consent rules.
  assert.equal(tileState({ page: { source: 'own' } }, { widget: false }), 'ready')
  assert.equal(tileState({ page: { source: 'shared' }, consent: 'allowed' }, { widget: false }), 'needsAllow', 'the dock runs a page without a click on a reload')
  assert.equal(tileState({ page: { source: 'shared' }, consent: 'allowed', embed: true }, { widget: false }), 'ready')
})

test('page tiles: the editor offers the pages with a widget block', () => {
  const list = eligiblePages(
    [{ name: 'clock', widget: { size: 'small' } }, { name: 'notes' }, { name: 'Bad', widget: {} }],
    [{ name: 'standup', folder_id: 4, widget: { size: 'large' } }, { name: 'plain', folder_id: 4 }, { name: 'nofolder', widget: {} }],
  )
  assert.deepEqual(list.map((e) => e.key), ['clock', '4/standup'])
  assert.deepEqual(list[1].ref, { name: 'standup', folder: 4 })
  assert.deepEqual(eligiblePages(null, undefined), [])
})

test('page tiles: the layout keeps page widgets, with or without a title', () => {
  const doc = { data: { widgets: [
    { id: 'clock', type: 'page', page: 'clock' },
    { id: 'news', type: 'page', page: 'news', title: 'News', folder: 3, keep: 1 },
    { id: 't', type: 'list', title: 'Tasks', source: 'data/t.json' },
  ] } }
  const rows = widgetList(doc)
  assert.equal(rows.length, 3)
  assert.equal(rows[0].title, '')
  const saved = cleanWidgets([
    ...rows,
    { id: '', type: 'page', page: 'weather', title: '', source: 'x.json', trigger: 'abc', folder: 0 },
    { id: '', type: 'page', page: '../bad', title: 'Bad' },
    { id: 'l', type: 'list', title: 'L', source: 'a.json', page: 'leftover', folder: 2 },
  ])
  assert.deepEqual(saved, [
    { id: 'clock', type: 'page', page: 'clock' },
    { id: 'news', type: 'page', page: 'news', title: 'News', folder: 3, keep: 1 },
    { id: 't', type: 'list', title: 'Tasks', source: 'data/t.json' },
    { id: 'weather', type: 'page', page: 'weather' },
    { id: 'l', type: 'list', title: 'L', source: 'a.json' },
  ])
})

test('page tiles: a frame loads in view, pauses out of sight and goes far away', () => {
  const none = { alive: false, visible: false }
  // Not yet seen: no frame, however near.
  assert.deepEqual(nextFrame(none, { inView: false, near: true, hidden: false, slot: true }), none)
  // In view, but the tab is hidden or no slot is free: wait.
  assert.deepEqual(nextFrame(none, { inView: true, near: true, hidden: true, slot: true }), none)
  assert.deepEqual(nextFrame(none, { inView: true, near: true, hidden: false, slot: false }), none)
  const live = nextFrame(none, { inView: true, near: true, hidden: false, slot: true })
  assert.deepEqual(live, { alive: true, visible: true })
  // Scrolled a little away, or the tab hidden: kept, paused.
  assert.deepEqual(nextFrame(live, { inView: false, near: true, hidden: false, slot: true }), { alive: true, visible: false })
  assert.deepEqual(nextFrame(live, { inView: true, near: true, hidden: true, slot: true }), { alive: true, visible: false })
  // Far away: taken down; back in view: built again.
  const gone = nextFrame(live, { inView: false, near: false, hidden: false, slot: true })
  assert.deepEqual(gone, none)
  assert.deepEqual(nextFrame(gone, { inView: true, near: true, hidden: false, slot: true }), live)
})

test('page tiles: at most three frames are alive, and a release wakes the waiting', () => {
  assert.equal(MAX_PAGE_TILES, 3)
  const slots = createFrameSlots(3)
  let woke = 0
  const off = slots.subscribe(() => { woke++ })
  assert.ok(slots.acquire('a'))
  assert.ok(slots.acquire('b'))
  assert.ok(slots.acquire('c'))
  assert.ok(slots.acquire('a'), 'a holder asks again')
  assert.equal(slots.acquire('d'), false)
  assert.equal(slots.held(), 3)
  slots.release('x')
  assert.equal(woke, 0, 'releasing nothing wakes nobody')
  slots.release('b')
  assert.equal(woke, 1)
  assert.ok(slots.acquire('d'))
  assert.ok(slots.holds('d'))
  off()
  slots.release('d')
  assert.equal(woke, 1)
})

test('frame cap: the app keeps at most eight live frames, and a click on a static card takes the oldest', () => {
  assert.equal(MAX_LIVE_FRAMES, 8)
  const slots = createFrameSlots()
  assert.equal(slots.max(), 8)
  const evicted = []
  for (let i = 1; i <= 8; i++) assert.ok(slots.acquire(`t${i}`, () => evicted.push(`t${i}`)))
  assert.equal(slots.acquire('t9', () => evicted.push('t9')), false, 'the ninth waits as a static card')
  assert.equal(slots.held(), 8)
  let woke = 0
  slots.subscribe(() => { woke++ })
  // The click on the ninth: the frame that started first stops.
  assert.ok(slots.take('t9', () => evicted.push('t9')))
  assert.deepEqual(evicted, ['t1'])
  assert.equal(slots.holds('t1'), false)
  assert.ok(slots.holds('t9'))
  assert.equal(slots.held(), 8, 'the cap holds')
  assert.equal(woke, 0, 'a take does not wake the waiting: the slot went to the click')
  // The next click takes the next oldest.
  assert.ok(slots.take('t1', () => evicted.push('t1')))
  assert.deepEqual(evicted, ['t1', 't2'])
  assert.ok(slots.take('t1'), 'a holder takes again for free')
  assert.equal(slots.held(), 8)
  // A holder that gave no way to stop it is never taken.
  const fixed = createFrameSlots(1)
  assert.ok(fixed.acquire('a'))
  assert.equal(fixed.take('b', () => {}), false)
  // A release wakes the waiting.
  slots.release('t3')
  assert.equal(woke, 1)
  assert.ok(slots.acquire('t2'))
})

test('compact tiles: a page has a strip only with compact in its widget block', () => {
  assert.equal(compactOf({ widget: { size: 'small' } }), null)
  assert.equal(compactOf(null), null)
  assert.deepEqual(compactOf({ widget: { compact: { width: 160 } } }), { width: 160 })
  assert.deepEqual(compactOf({ widget: { compact: {} } }), { width: 120 })
  assert.deepEqual(compactOf({ widget: { compact: { width: 5 } } }), { width: 72 })
  assert.deepEqual(compactOf({ widget: { compact: { width: 999 } } }), { width: 220 })
})

test('frame: a tile changes size without a reload, and only to known sizes', () => {
  const tile = boot({ name: 'weather', mode: 'widget', size: 'compact' })
  assert.equal(tile.localai.page.size, 'compact')
  assert.equal(tile.attrs['data-size'], 'compact')
  const heard = []
  tile.localai.on('mode', (d) => heard.push(`${d.mode}/${d.size}`))
  tile.deliver({ event: 'mode', data: { mode: 'widget', size: 'medium' } })
  assert.equal(tile.localai.page.size, 'medium', 'localai.page follows')
  assert.equal(tile.attrs['data-size'], 'medium', 'and so does the CSS')
  tile.deliver({ event: 'mode', data: { mode: 'widget', size: 'medium' } })
  tile.deliver({ event: 'mode', data: { mode: 'widget', size: '"><img>' } })
  tile.deliver({ event: 'mode', data: { mode: 'fullscreen', size: 'large' } })
  tile.deliver({ event: 'mode', data: { mode: 'page', size: 'large' } })
  assert.equal(tile.localai.page.size, 'medium', 'an unknown mode or size changes nothing')
  tile.deliver({ event: 'mode', data: { mode: 'widget', size: 'compact' } })
  assert.deepEqual(heard, ['widget/medium', 'widget/compact'], 'one call per change')
  assert.throws(() => { 'use strict'; tile.localai.page.size = 'large' }, TypeError, 'the page cannot set its size')
  assert.equal(boot({ name: 'x', mode: 'widget', size: 'compact' }).localai.page.size, 'compact')
})

test('frame: a frame with a budget beats about once a second, a full page does not', () => {
  const timers = []
  const tile = boot({ name: 'clock', mode: 'widget', size: 'small', beat: true }, { timers })
  assert.equal(timers.length, 1)
  assert.equal(timers[0].ms, 1000)
  timers[0].fn()
  const beat = tile.posted.find((m) => m.beat === 1)
  assert.ok(beat, 'a beat goes over the port')
  assert.equal(typeof beat.busy, 'number')
  assert.equal(beat.id, undefined, 'a beat is not a call')
  const page = []
  boot({ name: 'clock' }, { timers: page })
  assert.equal(page.length, 0)
})

test('frame: a page in the dock keeps beating past the start budget and is not stopped', () => {
  // The dock runs a page in the page mode with a budget: the host watches it,
  // so the bridge must beat, or the watch stops it as noStart after 10 s.
  const clock = { t: 1_000_000 }
  const timers = []
  const dock = boot({ name: 'board', mode: 'page', beat: true }, { timers, clock })
  const watch = createFrameWatch({ now: () => clock.t })
  let seen = 0
  for (let s = 0; s < 13; s++) {
    clock.t += 1000
    for (const tm of timers) tm.fn()
    for (const m of dock.posted.slice(seen)) if (m.beat === 1) watch.beat(m.busy)
    seen = dock.posted.length
    assert.equal(watch.check(true), '', `still running after ${s + 1} s`)
  }
  assert.equal(dock.posted.filter((m) => m.beat === 1).length, 13, 'one beat a second for 13 s')
})

test('page host: a beat goes to onBeat and gets no answer; mode events are checked', async () => {
  const sent = []
  const beats = []
  const h = createPageHost({ post: (m) => sent.push(m), files: {}, chat: {}, onBeat: (b) => beats.push(b) })
  await h.receive({ beat: 1, busy: 120 })
  await h.receive({ beat: 1, busy: -5 })
  await h.receive({ beat: 1, busy: 'x' })
  assert.deepEqual(beats, [120, 0, 0])
  assert.deepEqual(sent, [], 'no answer to a beat')
  h.emit('mode', { mode: 'widget', size: 'compact' })
  h.emit('mode', { mode: 'widget', size: '<b>' })
  h.emit('mode', { mode: 'evil', size: 'large' })
  assert.deepEqual(sent, [
    { event: 'mode', data: { mode: 'widget', size: 'compact' } },
    { event: 'mode', data: { mode: 'widget', size: 'medium' } },
    { event: 'mode', data: { mode: 'page', size: '' } },
  ])
})

test('frame: a page cannot fake its budget with the clock or the timer, nor see the port', () => {
  const clock = { t: 1_000_000 }
  const timers = []
  const tile = boot({ name: 'spin', mode: 'widget', size: 'small', beat: true }, {
    timers, clock,
    // The page lies about the time and takes over the timer after the bridge ran.
    before: (w) => {
      w.Date.now = () => 0
      w.setInterval = () => 0
    },
  })
  assert.equal(tile.capture, true, 'the init is taken in the capture phase')
  assert.equal(tile.stopped, 1, 'and no listener of the page sees it')
  assert.equal(timers.length, 1, 'the beat runs on the timer saved before the page')
  // The page held the thread for 800 ms: the beat fires that much late.
  clock.t += 1800
  timers[0].fn()
  const beat = tile.posted.find((m) => m.beat === 1)
  assert.equal(beat.busy, 800, 'the real clock measures the delay')
})
