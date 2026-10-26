import test from 'node:test'
import assert from 'node:assert/strict'
import {
  PIN_CAP, sidebarPinLinks, tileFromApp, tileFromBuiltin, buildTiles, filterTiles, resolvePins, togglePin, movePin,
} from '../../src/utils/addonTiles.js'
import { primaryDestinations, visiblePrimary } from '../../src/utils/navigationModel.js'
import { showFirstRun } from '../../src/components/addons/addonState.js'
import { createPinsApi } from '../../src/utils/pinsApi.js'

const app = (over = {}) => ({
  id: 'notes', title: 'Notes', description: 'Keep notes.', enabled: true, state: 'running',
  mainPage: '/app/addons/notes/home', generation: 1, ...over,
})

test('tileFromApp: a running add-on with a main page opens it', () => {
  const t = tileFromApp(app())
  assert.equal(t.state, 'running')
  assert.equal(t.target, '/app/addons/notes/home')
  assert.equal(t.detailPath, '')
})

test('tileFromApp never targets the main page of a stopped add-on', () => {
  for (const over of [{ enabled: false, state: 'stopped' }, { state: 'starting' }, { state: 'failed' }, { state: 'stopped' }]) {
    const t = tileFromApp(app(over))
    assert.equal(t.state, 'stopped', JSON.stringify(over))
    assert.equal(t.target, '', JSON.stringify(over))
    assert.equal(t.detailPath, '/app/addons?detail=notes')
  }
})

test('tileFromApp: starting and failed carry a status line, stopped does not', () => {
  assert.equal(tileFromApp(app({ state: 'starting' })).statusLine, 'starting')
  assert.equal(tileFromApp(app({ state: 'failed' })).statusLine, 'failed')
  assert.equal(tileFromApp(app({ enabled: false, state: 'stopped' })).statusLine, '')
})

test('tileFromApp: an add-on without a main page has no target and opens the detail view', () => {
  const t = tileFromApp(app({ mainPage: undefined }))
  assert.equal(t.target, '')
  assert.equal(t.detailPath, '/app/addons?detail=notes')
})

test('tileFromApp: the title falls back to the id', () => {
  assert.equal(tileFromApp(app({ title: '' })).title, 'notes')
})

test('buildTiles: built-in first, then apps by id, a built-in id wins', () => {
  // No add-on is built in today (Studio and Talk are packaged add-ons), so the
  // rule is checked with an entry the test makes up.
  const builtin = [{ id: 'guide', titleKey: 'items.guide', icon: 'book', mainPath: '/app/guide' }]
  const tiles = buildTiles([app({ id: 'zed' }), app({ id: 'guide' }), app({ id: 'abc' })], builtin)
  assert.deepEqual(tiles.map(t => t.id), ['guide', 'abc', 'zed'])
  assert.equal(tiles[0].source, 'builtin')
  assert.equal(tiles[0].target, '/app/guide')
})

test('filterTiles matches title and description, case-insensitive', () => {
  const tiles = [tileFromApp(app()), tileFromApp(app({ id: 'x', title: 'Faces', description: 'Who is it' }))]
  const t = (tile) => tile.title
  assert.deepEqual(filterTiles(tiles, 'NOTE', t).map(x => x.id), ['notes'])
  assert.deepEqual(filterTiles(tiles, 'who', t).map(x => x.id), ['x'])
  assert.equal(filterTiles(tiles, '  ', t).length, 2)
})

test('resolvePins keeps the order and hides a pin without a tile', () => {
  const tiles = buildTiles([app({ id: 'a' }), app({ id: 'b' })], [])
  assert.deepEqual(resolvePins(['b', 'gone', 'a'], tiles).map(t => t.id), ['b', 'a'])
})

test('togglePin enforces the cap of eight and asks the user to unpin one', () => {
  const eight = Array.from({ length: PIN_CAP }, (_, i) => `a${i}`)
  assert.deepEqual(togglePin(eight, 'new'), { ids: eight, needsUnpin: true })
  assert.deepEqual(togglePin(['a'], 'b'), { ids: ['a', 'b'], needsUnpin: false })
  assert.deepEqual(togglePin(['a', 'b'], 'a'), { ids: ['b'], needsUnpin: false })
})

test('movePin moves one place and stops at the ends', () => {
  assert.deepEqual(movePin(['a', 'b', 'c'], 'c', -1), ['a', 'c', 'b'])
  assert.deepEqual(movePin(['a', 'b', 'c'], 'a', -1), ['a', 'b', 'c'])
  assert.deepEqual(movePin(['a', 'b', 'c'], 'c', 1), ['a', 'b', 'c'])
})

const user = { isAdmin: false, authEnabled: true, hasFeature: () => false, features: {}, hasBuiltin: false }

test('Settings is not a primary item and Models is an administrator item', () => {
  assert.deepEqual(primaryDestinations.map(i => i.path), ['/app/chat', '/app/chats', '/app/files', '/app/models', '/app/addons'])
  const admin = visiblePrimary(primaryDestinations, { ...user, isAdmin: true, hasFeature: () => true })
  assert.equal(admin.length, 5)
  const member = visiblePrimary(primaryDestinations, { ...user, hasBuiltin: true })
  assert.deepEqual(member.map(i => i.path), ['/app/chat', '/app/addons'])
})

test('the files entry shows with the chats feature, like the chats entry', () => {
  const paths = (ctx) => visiblePrimary(primaryDestinations, ctx).map(i => i.path)
  assert.ok(paths({ ...user, hasFeature: (f) => f === 'chats' }).includes('/app/files'))
  assert.ok(!paths(user).includes('/app/files'))
})

test('the chats entry shows only to a viewer who holds the chats feature', () => {
  const paths = (a) => visiblePrimary(primaryDestinations, a).map(i => i.path)
  assert.ok(paths({ ...user, hasFeature: (f) => f === 'chats' }).includes('/app/chats'))
  assert.ok(!paths(user).includes('/app/chats'))
  assert.ok(paths({ ...user, isAdmin: true, hasFeature: () => true }).includes('/app/chats'))
})

test('addons entry shows for a built-in tile even without the addons feature, and hides with neither', () => {
  const paths = (a) => visiblePrimary(primaryDestinations, a).map(i => i.path)
  assert.ok(paths({ ...user, hasBuiltin: true }).includes('/app/addons'))
  assert.ok(paths({ ...user, hasFeature: (f) => f === 'addons' }).includes('/app/addons'))
  assert.ok(!paths(user).includes('/app/addons'))
})

test('pins api: reads and writes arrays, sends JSON, never throws on a malformed body', async () => {
  const calls = []
  const fetchImpl = async (url, init) => {
    calls.push([url, init?.method || 'GET', init?.body])
    if (url.endsWith('/api/addons/builtin')) return { ok: true, json: async () => ({ builtin: [{ id: 'guide' }] }) }
    return { ok: true, json: async () => (init?.method === 'PUT' ? JSON.parse(init.body) : ['guide', 7, null]) }
  }
  const api = createPinsApi({ base: (p) => `/base${p}`, fetchImpl })
  assert.deepEqual(await api.listPins(), ['guide'])
  assert.deepEqual(await api.putPins(['a', 'b']), ['a', 'b'])
  assert.deepEqual(await api.listBuiltin(), [{ id: 'guide' }])
  assert.equal(calls[1][0], '/base/api/addons/pins')
  assert.equal(calls[1][1], 'PUT')
  assert.equal(calls[1][2], '["a","b"]')
})

test('pins api: a failed answer is an error with the status', async () => {
  const api = createPinsApi({ base: (p) => p, fetchImpl: async () => ({ ok: false, status: 503, json: async () => ({}) }) })
  await assert.rejects(() => api.listPins(), (e) => e.status === 503)
})

test('sidebarPinLinks: pinned apps are listed in pin order with their main page', () => {
  const tiles = buildTiles([app({ id: 'notes', title: 'Notes' }), app({ id: 'studio', title: 'Studio', mainPage: '/app/addons/studio/home' })], [])
  const links = sidebarPinLinks(['studio', 'notes'], tiles)
  assert.deepEqual(links.map(l => l.id), ['studio', 'notes'])
  assert.deepEqual(links.map(l => l.title), ['Studio', 'Notes'])
  assert.deepEqual(links.map(l => l.to), ['/app/addons/studio/home', '/app/addons/notes/home'])
  assert.ok(links.every(l => l.stopped === false))
})

test('sidebarPinLinks: nothing is listed when no app is pinned', () => {
  const tiles = buildTiles([app()], [])
  assert.deepEqual(sidebarPinLinks([], tiles), [])
  assert.deepEqual(sidebarPinLinks(undefined, tiles), [])
})

test('sidebarPinLinks: a pin that is not installed is not listed', () => {
  const tiles = buildTiles([app({ id: 'notes' })], [])
  assert.deepEqual(sidebarPinLinks(['gone', 'notes', 'also-gone'], tiles).map(l => l.id), ['notes'])
})

test('sidebarPinLinks: a stopped app opens its detail view and is marked stopped', () => {
  const tiles = buildTiles([app({ enabled: false, state: 'stopped' })], [])
  const [link] = sidebarPinLinks(['notes'], tiles)
  assert.equal(link.to, '/app/addons?detail=notes')
  assert.equal(link.stopped, true)
})

test('the first-run card shows only to an installer when nothing is installed', () => {
  assert.equal(showFirstRun({ apps: [], isAdmin: true }), true)
  assert.equal(showFirstRun({ apps: [app()], isAdmin: true }), false)
  assert.equal(showFirstRun({ apps: [], isAdmin: false }), false)
  assert.equal(showFirstRun({ apps: [], isAdmin: true, canList: false }), false)
})

test('sidebar entries own one route each: Apps on /app/addons, never a hub', async () => {
  const { hubs, hubOwnsPath } = await import('../../src/components/hub/hubConfig.js')
  const [build, operate] = hubs
  // /api/features has answered and the legacy switch is off: four-item sidebar.
  const auth = { isAdmin: true, authEnabled: true, hasFeature: () => true, features: { addon_navigation: true } }
  for (const p of ['/app/addons', '/app/addons/voice/voice', '/app/addons/notes/home']) {
    assert.equal(hubOwnsPath(build, p, auth), false, `Build owns ${p}`)
    assert.equal(hubOwnsPath(operate, p, auth), false, `Operate owns ${p}`)
  }
  // Before /api/features answers the Add-ons tab is also closed.
  assert.equal(hubOwnsPath(build, '/app/addons', { ...auth, features: {} }), false)
})

test('sidebar hubs keep their own routes, and Apps does not claim them', async () => {
  const { hubs, hubOwnsPath } = await import('../../src/components/hub/hubConfig.js')
  const [build, operate] = hubs
  const auth = { isAdmin: true, authEnabled: true, hasFeature: () => true, features: { addon_navigation: true } }
  for (const p of ['/app/operate', '/app/settings', '/app/backends', '/app/traces', '/app/nodes']) {
    assert.equal(hubOwnsPath(operate, p, auth), true, `Operate misses ${p}`)
    assert.equal(hubOwnsPath(build, p, auth), false, `Build owns ${p}`)
  }
  assert.equal(hubOwnsPath(build, '/app/build', auth), true)
  assert.equal(hubOwnsPath(build, '/app/agents', auth), true)
  assert.equal(hubOwnsPath(operate, '/app/build', auth), false)
  // The primary Apps entry matches /app/addons and its subpaths only.
  const apps = '/app/addons'
  const appsActive = p => p === apps || p.startsWith(apps + '/')
  assert.equal(appsActive('/app/addons/voice/voice'), true)
  assert.equal(appsActive('/app/operate'), false)
  // Legacy navigation: the Build hub keeps the Add-ons tab.
  const legacy = { ...auth, features: { addon_navigation: false } }
  assert.equal(hubOwnsPath(build, '/app/addons', legacy), true)
})
