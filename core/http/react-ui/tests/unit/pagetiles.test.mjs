import { test } from 'node:test'
import assert from 'node:assert/strict'
import vm from 'node:vm'
import { buildPageSrcdoc, createPageHost } from '../../src/utils/pagesBridge.js'
import {
  MAX_PAGE_TILES, createFrameSlots, eligiblePages, nextFrame, refKey, tilePagePath, tileRef, tileSize, tileState, tileTitle,
} from '../../src/utils/pageTiles.js'
import { cleanWidgets, widgetList } from '../../src/utils/wellKnown.js'

// The frame's bootstrap in a VM: the attributes it sets on <html>, the API it
// defines and the port it talks over.
function boot(opts) {
  const attrs = {}
  let onMessage = null
  const sandbox = {
    console: { warn() {}, error() {}, log() {} },
    setTimeout, Promise, Response, Request, Blob, TextEncoder, DOMException, JSON, Object, String, Error, TypeError, URL,
    document: {
      baseURI: 'http://localai.test/app', readyState: 'complete',
      documentElement: { setAttribute: (k, v) => { attrs[k] = v }, style: {} },
      addEventListener() {}, getElementById: () => null, getElementsByName: () => [],
    },
    scrollTo() {},
    addEventListener: (t, fn) => { if (t === 'message') onMessage = fn },
  }
  sandbox.window = sandbox
  sandbox.parent = {}
  vm.createContext(sandbox)
  vm.runInContext(/<script>([\s\S]*?)<\/script>/.exec(buildPageSrcdoc('', opts))[1], sandbox)
  let deliver = null
  const port = { postMessage() {}, set onmessage(fn) { deliver = fn } }
  onMessage({ source: sandbox.parent, data: { type: 'localai:init' }, ports: [port] })
  return { attrs, localai: sandbox.localai, deliver: (m) => deliver({ data: m }) }
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

  assert.equal(tileSize({ widget: { size: 'small' } }), 'small')
  assert.equal(tileSize({ widget: { size: 'huge' } }), 'medium')
  assert.equal(tileSize(undefined), 'medium')
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
  assert.equal(tileState({ page: shared, consent: 'allowed' }), 'ready')
  assert.equal(tileState({ page: { source: 'shared' }, consent: 'allowed' }), 'noWidget')
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
  const slots = createFrameSlots()
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
