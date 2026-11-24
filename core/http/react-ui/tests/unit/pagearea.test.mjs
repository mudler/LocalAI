import { test } from 'node:test'
import assert from 'node:assert/strict'
import vm from 'node:vm'
import { buildPageSrcdoc, createPageHost } from '../../src/utils/pagesBridge.js'
import { AREA_DEFAULT_PX, AREA_IDLE, AREA_MAX_PX, AREA_MIN_PX, AREA_RESIZE_MS, areaHeight, areaKey, areaStep, areaTarget, areaValue, areaView, createAreaSizer, revertTarget } from '../../src/utils/pageArea.js'
import { MAX_LIVE_FRAMES, createFrameSlots, tileState } from '../../src/utils/pageTiles.js'
import { pinsOf, startOf, withStart } from '../../src/utils/pages.js'

// The frame's bootstrap in a VM, as in pagetiles.test.mjs.
function boot(opts) {
  const attrs = {}
  const timers = []
  let onMessage = null
  const sandbox = {
    console: { warn() {}, error() {}, log() {} },
    setTimeout, Promise,
    setInterval: (fn, ms) => { timers.push({ fn, ms }); return 1 }, Response, Request, Blob, TextEncoder, DOMException, JSON, Object, String, Error, TypeError, URL,
    document: {
      baseURI: 'http://localai.test/app', readyState: 'complete',
      documentElement: { setAttribute: (k, v) => { attrs[k] = v }, removeAttribute: (k) => { delete attrs[k] }, style: {} },
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
  const posted = []
  const port = { postMessage(m) { posted.push(m) }, set onmessage(fn) { deliver = fn } }
  onMessage({ source: sandbox.parent, data: { type: 'localai:init' }, ports: [port] })
  return { attrs, posted, timers, localai: sandbox.localai, deliver: (m) => deliver({ data: m }) }
}

test('page area: the frame knows the mode area, with no size, and beats like a tile', () => {
  const area = boot({ name: 'today', mode: 'area', size: 'large', beat: true })
  assert.equal(area.localai.page.mode, 'area')
  assert.equal(area.localai.page.size, '', 'the area is the full width: no size')
  assert.equal(area.attrs['data-mode'], 'area')
  assert.equal(area.attrs['data-size'], undefined)
  assert.ok(area.timers.some((t) => t.ms === 1000), 'the area is watched like a tile')
  const page = boot({ name: 'today' })
  assert.equal(page.timers.length, 0, 'a full page with no budget is not watched')
})

test('page area: a page moves between the area, a tile and its own screen without a reload', () => {
  const page = boot({ name: 'today', mode: 'widget', size: 'small' })
  const heard = []
  page.localai.on('mode', (d) => heard.push(d))
  page.deliver({ event: 'mode', data: { mode: 'area', size: '' } })
  assert.deepEqual(JSON.parse(JSON.stringify(heard)), [{ mode: 'area', size: '' }])
  assert.equal(page.attrs['data-mode'], 'area')
  assert.equal(page.attrs['data-size'], undefined)
  page.deliver({ event: 'mode', data: { mode: 'area', size: 'large' } })
  assert.equal(heard.length, 1, 'an area with a size is not a mode')
})

test('page area: the host sends mode area and drops a size', () => {
  const sent = []
  const h = createPageHost({ post: (m) => sent.push(m), files: {}, chat: {} })
  h.emit('mode', { mode: 'area', size: 'large' })
  assert.deepEqual(sent, [{ event: 'mode', data: { mode: 'area', size: '' } }])
})

test('page area: areaTarget reads the pageArea of home.json', () => {
  assert.equal(areaTarget(undefined).kind, 'none')
  assert.equal(areaTarget(null).kind, 'none')
  assert.deepEqual(areaTarget('today'), { kind: 'page', ref: { name: 'today', folder: 0 } })
  assert.deepEqual(areaTarget({ page: 'today', folder: 3 }), { kind: 'page', ref: { name: 'today', folder: 3 } })
  assert.deepEqual(areaTarget({ page: 'today' }), { kind: 'page', ref: { name: 'today', folder: 0 } })
  for (const bad of ['', '../x', 'Today', 7, ['today'], {}, { page: 7 }, true]) assert.equal(areaTarget(bad).kind, 'invalid', JSON.stringify(bad))
})

test('page area: areaValue is what home.json stores', () => {
  assert.equal(areaValue(null), null)
  assert.equal(areaValue({ name: 'today', folder: 0 }), 'today')
  assert.deepEqual(areaValue({ name: 'today', folder: 3 }), { page: 'today', folder: 3 })
  assert.equal(areaKey({ name: 'today', folder: 3 }), '3/today')
  assert.equal(areaKey(null), '')
})

test('page area: the failure state machine, from signals the shell sees', () => {
  let s = AREA_IDLE
  s = areaStep(s, { type: 'error', detail: {} })
  assert.equal(s.phase, 'idle', 'no frame, no failure')
  s = areaStep(s, { type: 'start' })
  assert.equal(s.phase, 'starting')
  s = areaStep(s, { type: 'ready' })
  assert.equal(s.phase, 'running')
  s = areaStep(s, { type: 'error', detail: {} })
  assert.equal(s.phase, 'running', 'an error after the first beat is the page\'s business')
  s = areaStep(s, { type: 'timeout' })
  assert.equal(s.phase, 'running', 'the timer is for the start only')
  s = areaStep(s, { type: 'stopped', reason: 'silent' })
  assert.deepEqual([s.phase, s.reason], ['failed', 'silent'])
  // Failed is sticky: nothing retries by itself.
  for (const ev of [{ type: 'start' }, { type: 'ready' }, { type: 'timeout' }, { type: 'error' }, { type: 'navigated' }]) assert.equal(areaStep(s, ev), s)
  s = areaStep(s, { type: 'reload' })
  assert.deepEqual([s.phase, s.reason], ['starting', ''])
})

test('page area: each signal gives its own reason', () => {
  const from = (...events) => events.reduce(areaStep, AREA_IDLE)
  assert.equal(from({ type: 'start' }, { type: 'error', detail: { message: 'x is not defined', line: 41 } }).reason, 'script')
  assert.equal(from({ type: 'start' }, { type: 'error', detail: { message: 'x is not defined', line: 41 } }).detail.line, 41)
  assert.equal(from({ type: 'start' }, { type: 'timeout' }).reason, 'noPaint')
  assert.equal(from({ type: 'start' }, { type: 'navigated' }).reason, 'navigated')
  assert.equal(from({ type: 'start' }, { type: 'stopped', reason: 'busy' }).reason, 'busy')
  assert.equal(from({ type: 'start' }, { type: 'stopped', reason: 'noStart' }).reason, 'noStart')
  assert.equal(from({ type: 'start' }, { type: 'stopped', reason: '<b>' }).reason, 'busy', 'an unknown reason is the budget')
  // A late timer after a first beat does nothing.
  assert.equal(from({ type: 'start' }, { type: 'ready' }, { type: 'timeout' }).phase, 'running')
  assert.equal(from({ type: 'start' }, { type: 'reset' }).phase, 'idle')
})

test('page area: what the area shows', () => {
  const page = areaTarget('today')
  const life = AREA_IDLE
  const view = (o) => areaView({ target: page, enabled: true, read: 'ready', life, ...o })
  assert.equal(view({ target: areaTarget(null) }).view, 'none')
  assert.equal(view({ target: areaTarget(undefined), enabled: false }).view, 'none', 'nothing to switch off')
  assert.equal(view({ enabled: false }).view, 'off', 'the administrator switch beats the value')
  assert.equal(view({ enabled: false, target: areaTarget(7) }).view, 'off')
  assert.equal(view({ enabled: null }).view, 'loading', 'no frame before the switch is known')
  assert.deepEqual(view({ target: areaTarget(7) }), { view: 'failed', reason: 'invalid' })
  assert.equal(view({ read: 'loading' }).view, 'loading')
  assert.deepEqual(view({ read: 'missing' }), { view: 'failed', reason: 'missing' })
  assert.deepEqual(view({ read: 'error' }), { view: 'failed', reason: 'read' })
  assert.deepEqual(view({ read: 'off' }), { view: 'failed', reason: 'sharedOff' })
  assert.equal(view({ read: 'needsAllow' }).view, 'allow', 'a shared page asks before it runs')
  assert.equal(view().view, 'run')
  assert.deepEqual(view({ life: { phase: 'failed', reason: 'noPaint' } }), { view: 'failed', reason: 'noPaint' })
  assert.equal(view({ life: { phase: 'running', reason: '' } }).view, 'run')
})

test('page area: a page without a widget block is ready, a tile still needs one', () => {
  const read = { loading: false, page: { name: 'today', source: 'own' } }
  assert.equal(tileState(read), 'noWidget')
  assert.equal(tileState(read, { widget: false }), 'ready')
  const shared = { loading: false, page: { name: 'today', source: 'shared' }, consent: 'allowed', embed: false }
  assert.equal(tileState(shared, { widget: false }), 'needsAllow', 'a shared page still needs the consent for Home')
})

test('page area: Revert restores home.json for a bad value, the page for an own page, nothing for a shared page', () => {
  assert.equal(revertTarget('invalid', null), 'home')
  assert.equal(revertTarget('invalid', { name: 'a', folder: 3 }), 'home')
  assert.equal(revertTarget('missing', { name: 'a', folder: 0 }), 'page')
  assert.equal(revertTarget('script', { name: 'a', folder: 0 }), 'page')
  assert.equal(revertTarget('script', { name: 'a', folder: 3 }), '')
})

test('start page: the first pinned page, and the order the switch writes', () => {
  assert.equal(startOf([]), '')
  assert.equal(startOf(['board', 'list']), 'board')
  const ui = { pinnedPages: ['board', 'list', 'notes'], theme: 'dark', look: { radius: 'round' } }
  assert.deepEqual(withStart(ui, 'notes', true).pinnedPages, ['notes', 'board', 'list'])
  assert.deepEqual(withStart(ui, 'board', true).pinnedPages, ['board', 'list', 'notes'], 'already first')
  assert.deepEqual(withStart(ui, 'board', false).pinnedPages, ['list', 'notes', 'board'], 'off hands the start to the next pin')
  assert.deepEqual(withStart(ui, 'list', false).pinnedPages, ['board', 'notes', 'list'])
  assert.deepEqual(withStart({ pinnedPages: ['solo'] }, 'solo', false).pinnedPages, ['solo'], 'the only pin stays')
  assert.deepEqual(withStart(ui, 'fresh', true).pinnedPages, ['fresh', 'board', 'list', 'notes'], 'a page that is not pinned is pinned')
  assert.deepEqual(withStart(ui, 'fresh', false).pinnedPages, ['board', 'list', 'notes'], 'turning off a page that is not pinned pins nothing')
  assert.equal(withStart(ui, 'notes', true).theme, 'dark', 'the other keys stay')
  assert.deepEqual(withStart(ui, 'notes', true).look, { radius: 'round' })
  assert.deepEqual(withStart(null, 'a', true), { pinnedPages: ['a'] })
  const many = { pinnedPages: Array.from({ length: 20 }, (_, i) => `p${i}`) }
  assert.equal(withStart(many, 'new', true).pinnedPages.length, 20, 'the limit holds')
  assert.equal(pinsOf(withStart(many, 'new', true))[0], 'new')
})

test('page area: it holds one of the eight live frames; with all taken it is a card that runs on a click', () => {
  const slots = createFrameSlots(MAX_LIVE_FRAMES)
  const evicted = []
  for (let i = 0; i < 7; i++) assert.equal(slots.acquire(`tile-${i}`, () => evicted.push(i)), true)
  assert.equal(slots.acquire('area-1', () => evicted.push('area')), true, 'the area is the eighth frame')
  assert.equal(slots.held(), 8)
  assert.equal(slots.acquire('tile-8', () => {}), false, 'the next frame waits')
  slots.release('area-1')
  assert.equal(slots.acquire('tile-8', () => {}), true, 'a freed slot goes to the next frame')
  assert.equal(slots.acquire('area-2', () => evicted.push('area2')), false, 'the area waits like a tile: a static card')
  assert.equal(slots.take('area-2', () => evicted.push('area2')), true, 'the click runs it and stops the first frame')
  assert.deepEqual(evicted, [0])
  assert.equal(slots.held(), 8)
})

test('page area: a reported height is clamped to whole pixels between the limits', () => {
  assert.equal(AREA_DEFAULT_PX, 240)
  assert.equal(AREA_MIN_PX, 160)
  assert.equal(areaHeight(300), 300)
  assert.equal(areaHeight(300.2), 301)
  assert.equal(areaHeight(12), AREA_MIN_PX)
  assert.equal(areaHeight(0), AREA_MIN_PX)
  assert.equal(areaHeight(1e9), AREA_MAX_PX)
  for (const bad of [-1, NaN, Infinity, '300', null, undefined, {}, [300]]) assert.equal(areaHeight(bad), null, String(bad))
})

test('page area: the sizer applies at most one height per period, the last one, and skips small steps', () => {
  let t = 1000
  const timers = []
  const applied = []
  const sizer = createAreaSizer({
    apply: (h) => applied.push(h), now: () => t,
    timer: (fn, ms) => { timers.push({ fn, at: t + ms }); return timers.length }, clear: (id) => { timers[id - 1] = null },
  })
  const run = () => { for (const k of timers.splice(0)) if (k) { t = Math.max(t, k.at); k.fn() } }
  // The first report applies at once.
  assert.equal(sizer.offer(320), 320)
  assert.deepEqual(applied, [320])
  // A burst within the period: only the last applies, when the period ends.
  assert.equal(sizer.offer(500), 500)
  sizer.offer(900)
  sizer.offer(410)
  assert.deepEqual(applied, [320])
  assert.equal(timers.filter(Boolean).length, 1, 'one timer for the burst')
  run()
  assert.deepEqual(applied, [320, 410])
  assert.ok(t >= 1000 + AREA_RESIZE_MS)
  // A step of a few pixels changes nothing: no layout for a jitter.
  t += AREA_RESIZE_MS
  sizer.offer(412)
  run()
  assert.deepEqual(applied, [320, 410])
  // A value that is not a height is refused and changes nothing.
  assert.equal(sizer.offer('tall'), null)
  t += AREA_RESIZE_MS
  run()
  assert.deepEqual(applied, [320, 410])
  // After stop nothing applies.
  t += AREA_RESIZE_MS
  sizer.offer(600)
  sizer.offer(700)
  sizer.stop()
  run()
  assert.deepEqual(applied, [320, 410, 600])
})

test('page area: the bridge sends page.resize with the height asked, or the height of the document', () => {
  const area = boot({ name: 'today', mode: 'area', beat: true })
  area.localai.page.resize(333)
  const m = area.posted.find((p) => p.method === 'page.resize')
  assert.deepEqual(JSON.parse(JSON.stringify(m.params)), { height: 333 })
  area.localai.page.resize()
  const all = area.posted.filter((p) => p.method === 'page.resize')
  assert.equal(all.length, 2)
  assert.equal(typeof all[1].params.height, 'number', 'with no argument the bridge measures the document')
  assert.equal(Object.isFrozen(area.localai.page), true)
})

test('page area: the host passes a height to the frame that sizes it, and answers null elsewhere', async () => {
  const sent = []
  const asked = []
  const h = createPageHost({ post: (m) => sent.push(m), files: {}, chat: {}, resize: (v) => { asked.push(v); return 280 } })
  await h.receive({ id: 1, method: 'page.resize', params: { height: 275.5 } })
  assert.deepEqual(asked, [275.5])
  assert.deepEqual(sent.at(-1), { id: 1, result: { height: 280 } })
  await h.receive({ id: 2, method: 'page.resize', params: { height: 'tall' } })
  assert.equal(sent.at(-1).error.code, 'invalid')
  await h.receive({ id: 3, method: 'page.resize', params: { height: -4 } })
  assert.equal(sent.at(-1).error.code, 'invalid')
  assert.equal(asked.length, 1)
  const other = []
  const tile = createPageHost({ post: (m) => other.push(m), files: {}, chat: {} })
  await tile.receive({ id: 1, method: 'page.resize', params: { height: 300 } })
  assert.deepEqual(other.at(-1), { id: 1, result: { height: null } })
})
