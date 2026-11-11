import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import {
  addonRows, addonSource, ageParts, applyLocal, backendRows, badgeOf, needsPersonRows, breakdownOf, bulkItemsOf, currentBackendRows,
  filterCounts, filterRows, galleryUpdateMap, groupOf, groupRows, headlineOf, normalizeFilter, sourceViews, trackJob, verdictOf, addonStillListed,
} from '../../src/utils/updates.js'

const okSource = (extra = {}) => ({ state: 'ok', checkedAt: '2026-11-08T10:00:00Z', lastSuccessAt: '2026-11-08T10:00:00Z', ...extra })
const snapshot = (items, extra = {}) => ({
  checkedAt: '2026-11-08T10:00:00Z',
  sources: { gallery: okSource(), library: okSource() },
  autoUpdate: { backends: false },
  items,
  ...extra,
})
const gallery = (name, extra = {}) => ({ id: `gallery:${name}`, kind: 'gallery', name, state: 'update', installedVersion: '1.0.0', availableVersion: '1.1.0', ...extra })
const library = (name, extra = {}) => ({ id: `library:official/${name}`, kind: 'library', name, state: 'update', sourceId: 'official', signed: true, installedVersion: '0.1', availableVersion: '0.2', ...extra })

const addon = (id, update, extra = {}) => ({ id, title: id, version: '1.0.0', generation: 1, digest: 'sha256:aa', update, ...extra })
const available = { state: 'update-available', version: '1.1.0', digest: 'sha256:bb', available: true, checkedAt: '2026-11-08T10:00:00Z' }

test('updates: a backend row is ready, with its versions and its origin', () => {
  const rows = backendRows(snapshot([gallery('llama-cpp'), library('piper')]))
  assert.equal(rows.length, 2)
  assert.deepEqual([rows[0].state, rows[0].from, rows[0].to, rows[0].kind], ['ready', 'v1.0.0', 'v1.1.0', 'gallery'])
  assert.equal(rows[1].sourceId, 'official')
  assert.equal(rows[1].signed, true)
})

test('updates: a digest stands in for a version the registry does not name', () => {
  const [row] = backendRows(snapshot([gallery('x', { installedVersion: '', availableVersion: '', installedDigest: 'sha256:aaaaaaaaaaaaaaaa', availableDigest: 'sha256:bbbbbbbbbbbbbbbb' })]))
  assert.equal(row.from, 'sha256:aaaaaaaaaaaa')
  assert.equal(row.to, 'sha256:bbbbbbbbbbbb')
})

test('updates: an operation in the list makes the row updating, an error makes it failed', () => {
  const s = snapshot([gallery('a'), gallery('b'), gallery('c')])
  const ops = [
    { name: 'a', jobID: 'ja', progress: 62.4, message: 'downloading', cancellable: true },
    { name: 'b', jobID: 'jb', error: 'download interrupted' },
  ]
  const byName = Object.fromEntries(backendRows(s, { operations: ops }).map(r => [r.name, r]))
  assert.equal(byName.a.state, 'updating')
  assert.equal(byName.a.progress, 62)
  assert.equal(byName.a.cancellable, true)
  assert.equal(byName.b.state, 'failed')
  assert.equal(byName.b.error, 'download interrupted')
  assert.equal(byName.c.state, 'ready')
})

test('updates: a queued operation is updating, a deletion is not an update', () => {
  const s = snapshot([gallery('a'), gallery('b')])
  const ops = [{ name: 'a', jobID: 'ja', isQueued: true }, { name: 'b', jobID: 'jb', isDeletion: true }]
  const byName = Object.fromEntries(backendRows(s, { operations: ops }).map(r => [r.name, r]))
  assert.deepEqual([byName.a.state, byName.a.phase], ['updating', 'queued'])
  assert.equal(byName.b.state, 'ready')
})

test('updates: when installs are off every backend is blocked and none can start', () => {
  const rows = backendRows(snapshot([gallery('a')], { installsDisabled: true }))
  assert.deepEqual([rows[0].state, rows[0].blocked, rows[0].startable], ['blocked', 'installsDisabled', false])
})

test('updates: the automatic switch marks backend rows as covered', () => {
  assert.equal(backendRows(snapshot([gallery('a')]), { auto: true })[0].covered, true)
  assert.equal(backendRows(snapshot([gallery('a')]), { auto: false })[0].covered, false)
})

test('updates: the pages that show a badge read the gallery backends with an update from the same rows', () => {
  const rows = backendRows(snapshot([
    gallery('a', { nodes: [{ nodeId: 'n1', nodeName: 'one', drift: true, version: '0.9' }, { nodeId: 'n2', drift: false }] }),
    gallery('b'),
    library('piper'),
    gallery('c', { state: 'current' }),
  ]), { operations: [{ name: 'c', jobID: 'j', isDeletion: false, error: 'x' }] })
  const map = galleryUpdateMap(rows)
  // The library backend is not a gallery one, and a failed row is not "ready".
  assert.deepEqual(Object.keys(map), ['a', 'b'])
  assert.deepEqual(map.a, {
    backend_name: 'a', installed_version: '1.0.0', available_version: '1.1.0',
    node_drift: [{ node_id: 'n1', node_name: 'one', version: '0.9' }],
  })
  assert.deepEqual(galleryUpdateMap(null), {})
  // Blocked rows (installs off) still show: the page says why it cannot start.
  assert.deepEqual(Object.keys(galleryUpdateMap(backendRows(snapshot([gallery('a')], { installsDisabled: true })))), ['a'])
})

test('updates: a source that failed keeps its list and marks the rows old', () => {
  const s = snapshot([gallery('a')], { sources: { gallery: { state: 'failed', error: 'timeout', stale: true, lastSuccessAt: '2026-11-07T10:00:00Z', checkedAt: '2026-11-08T10:00:00Z' }, library: okSource() } })
  assert.equal(backendRows(s)[0].old, true)
})

test('updates: nodes and drift come through for a cluster row', () => {
  const [row] = backendRows(snapshot([gallery('a', { nodes: [{ nodeId: 'n1' }, { nodeId: 'n2' }, { nodeId: 'n3' }], driftNodes: 2, state: 'drift' })]))
  assert.deepEqual([row.nodeCount, row.driftNodes, row.subtype], [3, 2, 'drift'])
})

test('updates: local state shows a started update before the list does, and a failed end as failed', () => {
  const rows = backendRows(snapshot([gallery('a'), gallery('b'), gallery('c')]))
  const out = applyLocal(rows, {
    'gallery:a': { phase: 'running', progress: 10, message: 'x' },
    'gallery:b': { phase: 'failed', message: 'The update ended but the version did not change.' },
  })
  assert.equal(out[0].state, 'updating')
  assert.equal(out[1].state, 'failed')
  assert.equal(out[1].error, 'The update ended but the version did not change.')
  assert.equal(out[2].state, 'ready')
  // A real operation wins over what the page thinks.
  const live = backendRows(snapshot([gallery('a')]), { operations: [{ name: 'a', jobID: 'j', progress: 50 }] })
  assert.equal(applyLocal(live, { 'gallery:a': { phase: 'failed', message: 'old' } })[0].state, 'updating')
})

test('updates: add-on states', () => {
  const rows = addonRows({
    installed: [
      addon('ready', available),
      addon('current', { state: 'up-to-date', checkedAt: '2026-11-08T10:00:00Z' }),
      addon('unknown', undefined),
      addon('broken', { state: 'check-failed', error: 'source-unavailable' }),
      addon('nofit', { state: 'incompatible', version: '2.0.0', digest: 'sha256:cc', reasons: ['platform'] }),
      addon('integrity', { state: 'integrity-error' }),
      addon('needscheck', { state: 'update-available', version: '1.2.0', available: true }),
    ],
  })
  const by = Object.fromEntries(rows.map(r => [r.addonId, r]))
  assert.equal(by.ready.state, 'ready')
  assert.equal(by.ready.startable, true)
  assert.equal(by.current.state, 'current')
  assert.equal(by.unknown, undefined, 'an unknown status is never "up to date" and is not a row')
  assert.equal(by.broken, undefined, 'a failed check is not a row, it is a source state')
  assert.deepEqual([by.nofit.state, by.nofit.blocked], ['blocked', 'incompatible'])
  assert.equal(by.integrity.blocked, 'integrity-error')
  assert.equal(by.needscheck.state, 'ready')
  assert.equal(by.needscheck.startable, false)
  assert.equal(by.needscheck.needsCheck, 'needs-check')
})

test('updates: a held add-on is held, a failed or running upgrade of this browser wins over the list', () => {
  const grants = { addons: [{ id: 'tables', title: 'Tables', version: '2.3.0', sourceId: 'official', pendingUpgrade: { version: '2.4.0', delta: ['read_files'] } }, { id: 'quiet', pendingUpgrade: { version: '1', delta: [] } }] }
  const rows = addonRows({
    installed: [addon('tables', available), addon('quiet', { state: 'up-to-date' }), addon('mail', available), addon('notes', available)],
    grants,
    live: [
      { addonId: 'mail', state: 'failed', errorKey: 'upgrade_failed', jobID: 'j1' },
      { addonId: 'notes', state: 'running', phase: 'migrating', jobID: 'j2', cancellable: true },
    ],
  })
  const by = Object.fromEntries(rows.map(r => [r.addonId, r]))
  assert.deepEqual([by.tables.state, by.tables.to, by.tables.heldCount], ['held', 'v2.4.0', 1])
  assert.equal(by.quiet.state, 'current', 'an empty delta holds nothing')
  assert.deepEqual([by.mail.state, by.mail.errorKey], ['failed', 'upgrade_failed'])
  assert.deepEqual([by.notes.state, by.notes.phase, by.notes.cancellable], ['updating', 'migrating', true])
})

test('updates: a held upgrade of an add-on the records do not list still shows', () => {
  const rows = addonRows({ installed: [], grants: { addons: [{ id: 'gone', title: 'Gone', version: '1', pendingUpgrade: { version: '2', delta: ['a', 'b'] } }] } })
  assert.deepEqual([rows.length, rows[0].state, rows[0].heldCount], [1, 'held', 2])
})

test('updates: groups put what needs a person first, failed before held before blocked', () => {
  const rows = [
    { id: '1', name: 'r', state: 'ready' }, { id: '2', name: 'b', state: 'blocked' }, { id: '3', name: 'h', state: 'held' },
    { id: '4', name: 'f', state: 'failed' }, { id: '5', name: 'u', state: 'updating' }, { id: '6', name: 'c', state: 'current' },
  ]
  const g = groupRows(rows)
  assert.deepEqual(g.needs.map(r => r.state), ['failed', 'held', 'blocked'])
  assert.deepEqual([g.updating.length, g.ready.length, g.current.length], [1, 1, 1])
  assert.equal(groupOf({ state: 'whatever' }), 'current')
})

test('updates: filters and their counts', () => {
  const rows = [{ id: '1', name: 'a', state: 'ready' }, { id: '2', name: 'b', state: 'failed' }, { id: '3', name: 'c', state: 'current' }]
  assert.deepEqual(filterCounts(rows), { all: 3, ready: 1, 'needs-you': 1, updating: 0, current: 1 })
  assert.deepEqual(filterRows(rows, 'needs-you').map(r => r.id), ['2'])
  assert.equal(filterRows(rows, 'all').length, 3)
  assert.equal(normalizeFilter('needs-you'), 'needs-you')
  assert.equal(normalizeFilter('nonsense'), 'all')
  assert.deepEqual(breakdownOf(rows), { ready: 1, updating: 0, needs: 1 })
})

test('badge: it counts what needs a person and not what an automatic setting will apply', () => {
  const rows = [
    { state: 'ready', covered: false }, { state: 'ready', covered: true }, { state: 'ready', covered: false },
    { state: 'updating' }, { state: 'current' },
  ]
  assert.deepEqual(badgeOf(rows), { count: 2, failed: false })
})

test('badge: failed, held and blocked count whatever the setting says, and failed makes it red', () => {
  const rows = [
    { state: 'failed', covered: true }, { state: 'held', covered: true }, { state: 'blocked', blocked: 'incompatible', covered: true },
    { state: 'ready', covered: true },
  ]
  assert.deepEqual(badgeOf(rows), { count: 3, failed: true })
  assert.deepEqual(badgeOf([{ state: 'held' }]), { count: 1, failed: false })
})

test('badge: a block by policy is not something a person can act on', () => {
  assert.deepEqual(badgeOf([{ state: 'blocked', blocked: 'installsDisabled' }, { state: 'blocked', blocked: 'integrity-error' }]), { count: 1, failed: false })
  assert.deepEqual(badgeOf([]), { count: 0, failed: false })
})

test('sources: the add-on source is derived from the owner records and never reads unknown as current', () => {
  assert.deepEqual(addonSource({ status: 'off' }), { state: 'disabled', reason: 'off' })
  assert.equal(addonSource({ status: 'loading' }).state, 'never')
  assert.equal(addonSource({ status: 'ready', installed: [] }).empty, true)
  assert.equal(addonSource({ status: 'ready', installed: [addon('a', undefined)] }).state, 'never')
  assert.deepEqual(addonSource({ status: 'ready', installed: [addon('a', { state: 'up-to-date', checkedAt: '2026-11-08T09:00:00Z' }), addon('b', { state: 'up-to-date', checkedAt: '2026-11-08T10:00:00Z' })] }), { state: 'ok', checkedAt: '2026-11-08T10:00:00Z' })
  const failed = addonSource({ status: 'ready', installed: [addon('a', { state: 'check-failed', error: 'source-unavailable', checkedAt: '2026-11-08T09:00:00Z' })] })
  assert.deepEqual([failed.state, failed.error], ['failed', 'source-unavailable'])
  assert.equal(addonSource({ status: 'error', installed: [addon('a', available)], error: 'network' }).stale, true)
})

test('sources: views list gallery, library, add-ons, and hide an add-on source with nothing installed', () => {
  const views = sourceViews(snapshot([]), { state: 'ok', empty: true })
  assert.deepEqual(views.map(v => v.id), ['gallery', 'library'])
  assert.deepEqual(sourceViews(snapshot([]), { state: 'ok' }).map(v => v.id), ['gallery', 'library', 'addons'])
  assert.deepEqual(sourceViews(null, null), [])
})

test('headline: "no updates" is never said while a source failed or was never checked', () => {
  const ok = { id: 'gallery', state: 'ok' }
  const failed = { id: 'library', state: 'failed' }
  const never = { id: 'addons', state: 'never' }
  const off = { id: 'x', state: 'disabled' }
  assert.deepEqual(headlineOf({ rows: [], sources: [ok, ok] }), { kind: 'current' })
  assert.deepEqual(headlineOf({ rows: [], sources: [ok, failed] }), { kind: 'noneButFailed', sources: ['library'] })
  assert.deepEqual(headlineOf({ rows: [], sources: [ok, never] }), { kind: 'noneButNever', sources: ['addons'] })
  assert.deepEqual(headlineOf({ rows: [], sources: [failed] }), { kind: 'couldNot' })
  assert.deepEqual(headlineOf({ rows: [], sources: [never, never] }), { kind: 'never' })
  assert.deepEqual(headlineOf({ rows: [], sources: [off] }), { kind: 'unavailable' })
  assert.deepEqual(headlineOf({ rows: [], sources: [], loading: true }), { kind: 'loading' })
  assert.deepEqual(headlineOf({ rows: [], sources: [], loadError: true }), { kind: 'loadError' })
})

test('headline: a failed source does not hide the updates that are listed, and current rows do not count', () => {
  const rows = [{ state: 'ready' }, { state: 'failed' }, { state: 'current' }]
  assert.deepEqual(headlineOf({ rows, sources: [{ id: 'a', state: 'failed' }] }), { kind: 'updates', count: 2 })
})

test('current backends: only those whose source answered, never system or listed ones', () => {
  const snap = snapshot([gallery('listed')], { sources: { gallery: okSource(), library: { state: 'failed' } } })
  const installed = [
    { Name: 'listed', Metadata: { version: '1' } },
    { Name: 'quiet', Metadata: { version: '2.0' } },
    { Name: 'system', IsSystem: true },
    { Name: 'meta', IsMeta: true },
    { Name: 'fromlib', Metadata: { library: 'official', version: '1' } },
  ]
  const rows = currentBackendRows(installed, snap)
  assert.deepEqual(rows.map(r => r.name), ['quiet'], 'a failed library says nothing about library backends')
  assert.equal(rows[0].from, 'v2.0')
})

test('bulk items: only ready rows, startable ones can run, order follows the list', () => {
  const rows = [
    { id: 'b', name: 'b', state: 'ready', startable: true, from: '1', to: '2' },
    { id: 'a', name: 'a', state: 'ready', startable: false, needsCheck: 'needs-check', from: '1', to: '2' },
    { id: 'h', name: 'h', state: 'held', startable: false },
    { id: 'f', name: 'f', state: 'failed', startable: false },
  ]
  const items = bulkItemsOf(rows)
  assert.deepEqual(items.map(i => [i.id, i.startable]), [['a', false], ['b', true]])
  assert.equal(items[0].reason, 'needs-check')
})

test('track: an operation that is listed is waited for, one that left it has ended', () => {
  let t = { seen: false, since: 0 }
  let out = trackJob(t, [{ jobID: 'j' }], 'j', 100)
  assert.equal(out.verdict, 'wait')
  t = out.track
  assert.equal(t.seen, true)
  assert.equal(trackJob(t, [], 'j', 200).verdict, 'ended')
})

test('track: an operation with an error is failed, while it is still listed', () => {
  const out = trackJob({ seen: false, since: 0 }, [{ jobID: 'j', error: 'boom' }], 'j', 10)
  assert.deepEqual([out.verdict, out.error], ['failed', 'boom'])
})

test('track: an operation never seen is waited for a moment, then taken as ended', () => {
  const t = { seen: false, since: 1000 }
  assert.equal(trackJob(t, [], 'j', 1500, 4000).verdict, 'wait')
  assert.equal(trackJob(t, [{ jobID: 'other' }], 'j', 5100, 4000).verdict, 'ended')
})

test('verdict: done needs the fresh check to stop listing the item', () => {
  assert.equal(verdictOf({ listed: false }), 'done')
  assert.equal(verdictOf({ listed: true }), 'unchanged')
  assert.equal(verdictOf({ listed: false, sourceFailed: true }), 'unconfirmed')
  assert.equal(verdictOf({ listed: true, sourceFailed: true }), 'unconfirmed')
})

test('verdict: a throttled answer is asked again until the tries are used', () => {
  assert.equal(verdictOf({ listed: true, throttled: true, tries: 0 }), 'retry')
  assert.equal(verdictOf({ listed: true, throttled: true, tries: 3, maxTries: 4 }), 'retry')
  assert.equal(verdictOf({ listed: true, throttled: true, tries: 4, maxTries: 4 }), 'unchanged')
  assert.equal(verdictOf({ listed: false, throttled: true }), 'done')
})

test('add-on done: the record must no longer carry the update the row showed', () => {
  const row = { to: 'v1.1.0' }
  assert.equal(addonStillListed(addon('a', available), row), true)
  assert.equal(addonStillListed(addon('a', { state: 'up-to-date' }), row), false)
  assert.equal(addonStillListed(addon('a', { ...available, version: '1.2.0' }), row), false, 'a newer candidate is another update')
  assert.equal(addonStillListed(undefined, row), false)
})

test('age: the largest fitting unit, and nothing for a bad time', () => {
  const now = Date.parse('2026-11-08T12:00:00Z')
  assert.deepEqual(ageParts('2026-11-08T11:59:55Z', now), { value: 5, unit: 'second', justNow: true })
  assert.deepEqual(ageParts('2026-11-08T11:48:00Z', now), { value: 12, unit: 'minute' })
  assert.deepEqual(ageParts('2026-11-08T07:00:00Z', now), { value: 5, unit: 'hour' })
  assert.deepEqual(ageParts('2026-11-04T12:00:00Z', now), { value: 4, unit: 'day' })
  assert.equal(ageParts('', now), null)
  assert.equal(ageParts('not a time', now), null)
})

// The server counts the digest of the inbox with backendupdates.PendingUpdates.
// Both read the same cases, so the badge and the digest cannot drift apart.
test('updates: the badge counts the backend items like the server digest', () => {
  const fixture = JSON.parse(readFileSync(new URL('../../../../services/backendupdates/testdata/badge-parity.json', import.meta.url), 'utf8'))
  assert.ok(fixture.cases.length > 0)
  for (const c of fixture.cases) {
    const rows = backendRows(c.snapshot, { auto: c.auto })
    assert.equal(badgeOf(rows).count, c.count, c.name)
  }
})

test('needsPersonRows: backends and add-ons are in one list, and its length is the badge', () => {
  const rows = [
    { id: 'gallery:llama-cpp', kind: 'gallery', name: 'llama-cpp', state: 'ready' },
    { id: 'addon:notes', kind: 'addon', name: 'Notes sync', state: 'ready' },
    { id: 'addon:tables', kind: 'addon', name: 'Tables', state: 'held' },
    { id: 'addon:mail', kind: 'addon', name: 'Mail', state: 'ready', covered: true },
    { id: 'gallery:whisper', kind: 'gallery', name: 'whisper', state: 'current' },
  ]
  const list = needsPersonRows(rows)
  assert.deepEqual(list.map(r => r.name), ['llama-cpp', 'Notes sync', 'Tables'])
  assert.equal(list.length, badgeOf(rows).count)
  assert.deepEqual(needsPersonRows(null), [])
})
