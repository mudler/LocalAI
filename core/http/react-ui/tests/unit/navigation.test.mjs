import test from 'node:test'
import assert from 'node:assert/strict'
import {
  PIN_CAP, tileFromApp, tileFromBuiltin, buildTiles, filterTiles, resolvePins, togglePin, movePin,
} from '../../src/utils/addonTiles.js'
import { primaryDestinations, visiblePrimary } from '../../src/utils/navigationModel.js'
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
  const builtin = [{ id: 'studio', titleKey: 'items.studio', icon: 'palette', mainPath: '/app/studio' }]
  const tiles = buildTiles([app({ id: 'zed' }), app({ id: 'studio' }), app({ id: 'abc' })], builtin)
  assert.deepEqual(tiles.map(t => t.id), ['studio', 'abc', 'zed'])
  assert.equal(tiles[0].source, 'builtin')
  assert.equal(tiles[0].target, '/app/studio')
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

test('the sidebar has four primary items and Settings and Models are administrator items', () => {
  assert.deepEqual(primaryDestinations.map(i => i.path), ['/app/chat', '/app/models', '/app/addons', '/app/settings'])
  const admin = visiblePrimary(primaryDestinations, { ...user, isAdmin: true, hasFeature: () => true })
  assert.equal(admin.length, 4)
  const member = visiblePrimary(primaryDestinations, { ...user, hasBuiltin: true })
  assert.deepEqual(member.map(i => i.path), ['/app/chat', '/app/addons'])
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
    if (url.endsWith('/api/addons/builtin')) return { ok: true, json: async () => ({ builtin: [{ id: 'studio' }] }) }
    return { ok: true, json: async () => (init?.method === 'PUT' ? JSON.parse(init.body) : ['studio', 7, null]) }
  }
  const api = createPinsApi({ base: (p) => `/base${p}`, fetchImpl })
  assert.deepEqual(await api.listPins(), ['studio'])
  assert.deepEqual(await api.putPins(['a', 'b']), ['a', 'b'])
  assert.deepEqual(await api.listBuiltin(), [{ id: 'studio' }])
  assert.equal(calls[1][0], '/base/api/addons/pins')
  assert.equal(calls[1][1], 'PUT')
  assert.equal(calls[1][2], '["a","b"]')
})

test('pins api: a failed answer is an error with the status', async () => {
  const api = createPinsApi({ base: (p) => p, fetchImpl: async () => ({ ok: false, status: 503, json: async () => ({}) }) })
  await assert.rejects(() => api.listPins(), (e) => e.status === 503)
})
