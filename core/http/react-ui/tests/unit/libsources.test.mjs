import test from 'node:test'
import assert from 'node:assert/strict'
import {
  SOURCE_KINDS, canRefresh, countByKind, draftToLibrarySource, filterSources, searchableSources,
  sourceKind, sourceState, sourceToDraft, validateSourceDraft,
} from '../../src/components/catalog/catalogState.js'
import { createAddonsApi, createAddonsCatalogAdapter, AddonsError, classifyLibrary } from '../../src/utils/addonsApi.js'

const json = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body })

const SOURCES = [
  { kind: 'addon', id: 'official', name: 'Official', url: 'https://a.example/index.yaml', enabled: true, default: true },
  { kind: 'addon', id: 'mine', name: 'Mine', url: 'https://b.example/index.yaml', enabled: false },
  { kind: 'backend', id: 'localai-backends', name: 'Official backends', url: 'https://c.example/backends', enabled: true, default: true, format: 'gallery' },
  { kind: 'model', id: 'localai', name: 'Official models', url: 'https://d.example/models', enabled: false, default: true, removed: true, format: 'gallery' },
  { kind: 'model', id: 'team', name: 'Team', url: 'https://e.example/models', enabled: false, default: true, suspended: true, format: 'gallery' },
  { id: 'legacy', name: 'Legacy', url: 'https://f.example/index.yaml', enabled: true },
]

test('the kind filter lists one kind, or all of them, in the order given', () => {
  assert.deepEqual(SOURCE_KINDS, ['addon', 'backend', 'model'])
  assert.deepEqual(filterSources(SOURCES, 'all').map(s => s.id), SOURCES.map(s => s.id))
  assert.deepEqual(filterSources(SOURCES, 'addon').map(s => s.id), ['official', 'mine', 'legacy'])
  assert.deepEqual(filterSources(SOURCES, 'backend').map(s => s.id), ['localai-backends'])
  assert.deepEqual(filterSources(SOURCES, 'model').map(s => s.id), ['localai', 'team'])
  assert.deepEqual(filterSources([], 'model'), [])
  assert.equal(sourceKind({}), 'addon', 'a record with no kind is an add-on source')
  assert.equal(sourceKind({ kind: 'nonsense' }), 'addon')
})

test('the kind filter counts every kind and the total', () => {
  assert.deepEqual(countByKind(SOURCES), { all: 6, addon: 3, backend: 1, model: 2 })
  assert.deepEqual(countByKind(), { all: 0, addon: 0, backend: 0, model: 0 })
})

test('a source shows one state: removed, suspended, disabled or enabled', () => {
  assert.equal(sourceState(SOURCES[0]), 'enabled')
  assert.equal(sourceState(SOURCES[1]), 'disabled')
  assert.equal(sourceState(SOURCES[3]), 'removed')
  assert.equal(sourceState(SOURCES[4]), 'suspended')
  // Removed wins over suspended: a tombstone is not "off by the switch".
  assert.equal(sourceState({ removed: true, suspended: true }), 'removed')
})

test('only enabled library-format sources are searched and refreshed', () => {
  assert.deepEqual(searchableSources(SOURCES).map(s => s.id), ['official', 'legacy'])
  assert.equal(canRefresh(SOURCES[0]), true)
  assert.equal(canRefresh(SOURCES[1]), false, 'a disabled source is not refreshed')
  assert.equal(canRefresh(SOURCES[2]), false, 'a gallery index is read by the gallery readers')
  assert.equal(canRefresh(SOURCES[4]), false)
})

test('a backend or model source is added with its kind and format, an add-on keeps its own form', () => {
  assert.deepEqual(
    draftToLibrarySource({ kind: 'model', format: 'gallery', name: ' Team ', url: ' https://e.example/models ', id: ' team ' }),
    { kind: 'model', format: 'gallery', name: 'Team', url: 'https://e.example/models', id: 'team' },
  )
  assert.deepEqual(draftToLibrarySource({ kind: 'backend', format: 'zip', url: 'https://x.example', name: '' }), { kind: 'backend', url: 'https://x.example', name: '', format: 'library' })
  assert.equal(sourceToDraft(SOURCES[3]).kind, 'model')
  assert.equal(sourceToDraft(SOURCES[3]).format, 'gallery')
  assert.equal(sourceToDraft(SOURCES[5]).kind, 'addon')
  // The add-on checks stay; the others leave the address to the server.
  assert.ok(validateSourceDraft({ kind: 'addon', id: 'a', name: 'A', url: 'ftp://x' }).url)
  assert.deepEqual(validateSourceDraft({ kind: 'model', id: 'a', name: 'A', url: 'github:org/repo/index.yaml' }), {})
  assert.equal(validateSourceDraft({ kind: 'model', id: 'a', name: 'A', url: ' ' }).url, 'urlEmpty')
})

test('the library routes carry the kind, the toggle and the removal, and refusals keep their reason', async () => {
  const calls = []
  const api = createAddonsApi({
    base: p => `/base${p}`,
    fetch: async (url, init) => {
      calls.push({ url, method: init.method, body: init.body ? JSON.parse(init.body) : null })
      if (url.endsWith('/api/library/sources') && init.method === 'GET') return json(200, { sources: SOURCES, officialLibraries: false })
      if (url.endsWith('/api/library/sources') && init.method === 'POST') return json(409, { error: { code: 409, message: 'x is already a model source (team)' } })
      if (url.endsWith('/sources/ghost/disable')) return json(404, { error: { code: 404, message: 'no source with id ghost' } })
      if (url.endsWith('/sources/team')) return json(403, { error: { type: 'authorization_error', message: 'Admin access required' } })
      return json(200, {})
    },
  })
  assert.equal((await api.library.list()).officialLibraries, false)
  const adapter = createAddonsCatalogAdapter(api)
  assert.equal((await adapter.listSources()).length, 6)
  assert.equal(calls[0].url, '/base/api/library/sources')
  await assert.rejects(adapter.addSource({ kind: 'model', url: 'x' }), e => e instanceof AddonsError && e.kind === 'invalid' && /already a model source/.test(e.message))
  await assert.rejects(adapter.setSourceEnabled('ghost', false), e => e.kind === 'invalid' && e.status === 404)
  await adapter.setSourceEnabled('localai', true)
  assert.deepEqual(calls.at(-1), { url: '/base/api/library/sources/localai/enable', method: 'POST', body: null })
  await assert.rejects(adapter.removeSource('team'), e => e.kind === 'denied')
  assert.equal(calls.at(-1).method, 'DELETE')
  assert.equal(classifyLibrary(409, null).kind, 'invalid')
  assert.equal(classifyLibrary(403, null).kind, 'denied')
})
