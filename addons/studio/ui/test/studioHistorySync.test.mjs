import assert from 'node:assert/strict'
import test from 'node:test'

import {
  ACCOUNT_KEY, LEGACY_CLAIM_KEY, clearRemote, deleteRemote, fromServerEntry, historyStorageKey, isServerMode,
  mergeHistory, resetStudioHistorySync, saveRemote, setStudioHistoryTransport, syncStudioHistory, toServerEntry,
} from '../src/utils/studioHistorySync.js'
import { MEDIA_STORAGE_KEYS } from '../src/utils/mediaHistoryStore.js'

// A localStorage stand-in, fresh for every test.
function memoryStorage() {
  const data = new Map()
  return {
    getItem: k => (data.has(k) ? data.get(k) : null),
    setItem: (k, v) => data.set(k, String(v)),
    removeItem: k => data.delete(k),
    get size() { return data.size },
  }
}

const entry = (id, createdAt, extra = {}) => ({
  id, createdAt, prompt: `prompt ${id}`, model: 'm', params: {}, results: [{ url: `/generated-videos/${id}.mp4` }], ...extra,
})
const serverEntry = (id, type, createdAt, version = 1) => ({ id, type, createdAt, version, entry: entry(id, createdAt) })

function json(status, body) {
  return { ok: status >= 200 && status < 300, status, headers: { get: () => 'application/json' }, json: async () => body }
}

// A fake /api/studio/history server: one account, its entries and tombstones,
// and a log of the requests it received.
function fakeServer({ entries = [], deleted = [], cacheKey = 'acct1', status = 200, disabled = false } = {}) {
  const calls = []
  const server = { calls, entries, deleted, failDeletes: false, failGetsAfter: Infinity }
  server.transport = async (path, init) => {
    calls.push({ path, method: init?.method || 'GET', body: init?.body ? JSON.parse(init.body) : null })
    if (status !== 200) return json(status, { error: 'nope' })
    // A LocalAI without accounts: only the list, saying there is no history.
    if (disabled) return (init?.method || 'GET') === 'GET' ? json(200, { enabled: false, entries: [], deleted: [] }) : json(404, { error: 'not found' })
    const method = init?.method || 'GET'
    if (method === 'DELETE') {
      if (server.failDeletes) throw new TypeError('Failed to fetch')
      const id = decodeURIComponent(path.split('?')[0].split('/').pop())
      const type = new URLSearchParams(path.split('?')[1] || '').get('type')
      const gone = id === 'history' ? entries.filter(e => !type || e.type === type) : entries.filter(e => e.id === id)
      for (const e of gone) { entries.splice(entries.indexOf(e), 1); deleted.push({ id: e.id, type: e.type, version: 2 }) }
      return json(200, { status: 'ok' })
    }
    // A copy, like a real response: later writes must not change an answer already sent.
    if (method === 'GET') {
      if (calls.filter(c => c.method === 'GET').length > server.failGetsAfter) throw new TypeError('Failed to fetch')
      return json(200, structuredClone({ entries, deleted, cacheKey }))
    }
    if (method === 'PUT') {
      const body = JSON.parse(init.body)
      entries.push({ ...body, version: 1 })
      return json(200, { ...body, version: 1 })
    }
    if (method === 'POST') {
      const body = JSON.parse(init.body)
      for (const e of body.entries) entries.push({ ...e, version: 1 })
      return json(200, { status: 'ok', count: body.entries.length, skipped: [] })
    }
    return json(200, { status: 'ok' })
  }
  return server
}

function setup(server) {
  globalThis.localStorage = memoryStorage()
  resetStudioHistorySync()
  setStudioHistoryTransport(server.transport)
}

test('mergeHistory: tombstones win, the server copy wins, unsynced local entries are uploaded', () => {
  const local = [
    entry('both', 3, { prompt: 'local copy' }),
    entry('draft', 5),
    entry('gone-elsewhere', 2, { serverVersion: 4 }),
    entry('dead', 1),
  ]
  const server = [serverEntry('both', 'video', 3, 2), serverEntry('remote', 'video', 4)]
  const { list, upload } = mergeHistory(local, server, [{ id: 'dead' }])
  assert.deepEqual(list.map(e => e.id), ['draft', 'remote', 'both'])
  assert.equal(list.find(e => e.id === 'both').prompt, 'prompt both')
  assert.equal(list.find(e => e.id === 'both').serverVersion, 2)
  assert.deepEqual(upload.map(e => e.id), ['draft'])
})

test('toServerEntry keeps the Studio record and drops sync-only fields', () => {
  const e = toServerEntry('video', entry('v', 10, { serverVersion: 3, parentId: 'i', edge: 'animate' }))
  assert.equal(e.type, 'video')
  assert.equal(e.createdAt, 10)
  assert.equal(e.entry.serverVersion, undefined)
  assert.equal(e.entry.parentId, 'i')
  assert.equal(fromServerEntry({ ...e, version: 3 }).serverVersion, 3)
})

test('without a server the plain keys stay in use', async () => {
  const server = fakeServer({ status: 404 })
  setup(server)
  globalThis.localStorage.setItem(MEDIA_STORAGE_KEYS.video, JSON.stringify([entry('old', 1)]))
  assert.equal(await syncStudioHistory(), false)
  assert.equal(isServerMode(), false)
  assert.equal(historyStorageKey('video'), MEDIA_STORAGE_KEYS.video)
  assert.equal(await saveRemote('video', entry('x', 2)), null)
  assert.equal(server.calls.length, 1)
})

test('a LocalAI without accounts answers enabled false and the plain keys stay in use', async () => {
  const server = fakeServer({ disabled: true })
  setup(server)
  globalThis.localStorage.setItem(MEDIA_STORAGE_KEYS.video, JSON.stringify([entry('old', 1)]))
  assert.equal(await syncStudioHistory(), false)
  assert.equal(isServerMode(), false)
  assert.equal(historyStorageKey('video'), MEDIA_STORAGE_KEYS.video)
  assert.equal(await saveRemote('video', entry('x', 2)), null)
  await deleteRemote('video', 'old')
  // Only the one list request: no upload, delete or import.
  assert.deepEqual(server.calls.map(c => c.method), ['GET'])
})

test('with a server, the cache is per account and the legacy history is imported once', async () => {
  const server = fakeServer({ entries: [serverEntry('s1', 'image', 50)] })
  setup(server)
  globalThis.localStorage.setItem(MEDIA_STORAGE_KEYS.video, JSON.stringify([entry('old', 1)]))

  assert.equal(await syncStudioHistory(), true)
  assert.equal(historyStorageKey('video'), `${MEDIA_STORAGE_KEYS.video}:acct1`)
  const imports = server.calls.filter(c => c.method === 'POST')
  assert.equal(imports.length, 1)
  assert.deepEqual(imports[0].body.entries.map(e => [e.id, e.type]), [['old', 'video']])
  assert.equal(JSON.parse(globalThis.localStorage.getItem(LEGACY_CLAIM_KEY)), 'acct1')

  const videos = JSON.parse(globalThis.localStorage.getItem(`${MEDIA_STORAGE_KEYS.video}:acct1`))
  const images = JSON.parse(globalThis.localStorage.getItem(`${MEDIA_STORAGE_KEYS.image}:acct1`))
  assert.deepEqual(videos.map(e => e.id), ['old'])
  assert.deepEqual(images.map(e => e.id), ['s1'])
  assert.ok(videos[0].serverVersion > 0)

  // A second account on the same browser does not get the legacy history.
  const other = fakeServer({ cacheKey: 'acct2' })
  resetStudioHistorySync()
  setStudioHistoryTransport(other.transport)
  assert.equal(historyStorageKey('video'), null) // nothing shown until the server says whose cache it is
  assert.equal(await syncStudioHistory(), true)
  assert.equal(other.calls.filter(c => c.method === 'POST').length, 0)
  assert.deepEqual(JSON.parse(globalThis.localStorage.getItem(`${MEDIA_STORAGE_KEYS.video}:acct2`)), [])
  assert.equal(JSON.parse(globalThis.localStorage.getItem(ACCOUNT_KEY)), 'acct2')
})

test('saveRemote uploads a new entry and returns its version', async () => {
  const server = fakeServer()
  setup(server)
  globalThis.localStorage.setItem(LEGACY_CLAIM_KEY, JSON.stringify('acct1'))
  const version = await saveRemote('tts', entry('t1', 7))
  assert.equal(version, 1)
  const put = server.calls.find(c => c.method === 'PUT')
  assert.equal(put.path, '/api/studio/history/t1')
  assert.equal(put.body.type, 'tts')
  assert.equal(put.body.entry.prompt, 'prompt t1')
})

test('after the import, the old history is gone from the plain keys: a later account with no access sees nothing', async () => {
  const alice = fakeServer()
  setup(alice)
  globalThis.localStorage.setItem(MEDIA_STORAGE_KEYS.image, JSON.stringify([entry('alice-old', 1)]))
  assert.equal(await syncStudioHistory(), true)
  assert.equal(globalThis.localStorage.getItem(MEDIA_STORAGE_KEYS.image), null)

  // Bob signs in on the same browser, but his studio_history permission is revoked.
  resetStudioHistorySync()
  setStudioHistoryTransport(fakeServer({ status: 403 }).transport)
  assert.equal(await syncStudioHistory(), false)
  assert.equal(historyStorageKey('image'), null)

  // Offline is the same: the account is unknown.
  resetStudioHistorySync()
  setStudioHistoryTransport(async () => { throw new TypeError('Failed to fetch') })
  assert.equal(await syncStudioHistory(), false)
  assert.equal(historyStorageKey('image'), null)

  // A LocalAI without accounts (404) keeps history in the browser again.
  resetStudioHistorySync()
  setStudioHistoryTransport(fakeServer({ status: 404 }).transport)
  assert.equal(await syncStudioHistory(), false)
  assert.equal(historyStorageKey('image'), MEDIA_STORAGE_KEYS.image)

  // So does one that says it keeps no history.
  resetStudioHistorySync()
  setStudioHistoryTransport(fakeServer({ disabled: true }).transport)
  assert.equal(await syncStudioHistory(), false)
  assert.equal(historyStorageKey('image'), MEDIA_STORAGE_KEYS.image)
})

test('a delete queued offline is sent on the next load and the entry does not come back', async () => {
  const server = fakeServer({ entries: [serverEntry('v1', 'video', 1), serverEntry('v2', 'video', 2)] })
  setup(server)
  globalThis.localStorage.setItem(LEGACY_CLAIM_KEY, JSON.stringify('acct1'))
  await syncStudioHistory()
  server.failDeletes = true
  await deleteRemote('video', 'v1')

  // Next page load, online again.
  server.failDeletes = false
  resetStudioHistorySync()
  assert.equal(await syncStudioHistory(), true)
  assert.deepEqual(server.entries.map(e => e.id), ['v2'])
  const cached = JSON.parse(globalThis.localStorage.getItem(`${MEDIA_STORAGE_KEYS.video}:acct1`))
  assert.deepEqual(cached.map(e => e.id), ['v2'])
})

test('a delete still pending hides the entry even though the server still lists it', async () => {
  const server = fakeServer({ entries: [serverEntry('v1', 'video', 1)] })
  setup(server)
  globalThis.localStorage.setItem(LEGACY_CLAIM_KEY, JSON.stringify('acct1'))
  await syncStudioHistory()
  server.failDeletes = true
  await deleteRemote('video', 'v1')
  resetStudioHistorySync()
  await syncStudioHistory()
  const cached = JSON.parse(globalThis.localStorage.getItem(`${MEDIA_STORAGE_KEYS.video}:acct1`))
  assert.deepEqual(cached, [])
})

test('a Clear that failed offline deletes only what it cleared, not results made later elsewhere', async () => {
  const server = fakeServer({ entries: [serverEntry('old', 'image', 1)] })
  setup(server)
  globalThis.localStorage.setItem(LEGACY_CLAIM_KEY, JSON.stringify('acct1'))
  await syncStudioHistory()
  server.failDeletes = true
  await clearRemote('image', [{ id: 'old', type: 'image' }])

  // Another device makes a new picture before this browser is back.
  server.entries.push(serverEntry('newer', 'image', 5))
  server.failDeletes = false
  resetStudioHistorySync()
  await syncStudioHistory()
  assert.deepEqual(server.entries.map(e => e.id), ['newer'])
  const cached = JSON.parse(globalThis.localStorage.getItem(`${MEDIA_STORAGE_KEYS.image}:acct1`))
  assert.deepEqual(cached.map(e => e.id), ['newer'])
})

test('a queued delete that went through stays deleted even when the list cannot be fetched again', async () => {
  const server = fakeServer({ entries: [serverEntry('v1', 'video', 1), serverEntry('v2', 'video', 2)] })
  setup(server)
  globalThis.localStorage.setItem(LEGACY_CLAIM_KEY, JSON.stringify('acct1'))
  await syncStudioHistory()
  server.failDeletes = true
  await deleteRemote('video', 'v1')

  // Next load: the first GET works, the queued DELETE works, the second GET fails.
  server.failDeletes = false
  server.failGetsAfter = server.calls.filter(c => c.method === 'GET').length + 1
  resetStudioHistorySync()
  assert.equal(await syncStudioHistory(), true)
  const cached = JSON.parse(globalThis.localStorage.getItem(`${MEDIA_STORAGE_KEYS.video}:acct1`))
  assert.deepEqual(cached.map(e => e.id), ['v2'])
})

test('a queued item without an id is dropped, not sent', async () => {
  const server = fakeServer()
  setup(server)
  globalThis.localStorage.setItem(LEGACY_CLAIM_KEY, JSON.stringify('acct1'))
  globalThis.localStorage.setItem('localai_studio_history_pending_deletes:acct1', JSON.stringify([{ all: true, type: 'image' }]))
  await syncStudioHistory()
  assert.equal(server.calls.filter(c => c.method === 'DELETE').length, 0)
  assert.deepEqual(JSON.parse(globalThis.localStorage.getItem('localai_studio_history_pending_deletes:acct1')), [])
})
