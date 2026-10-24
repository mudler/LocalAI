import test from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import {
  normalizeQuery, queryKey, queryToParams, paramsToQuery, totalPages, queryToOffset,
  createOperationBook, newOperationId, classifyResponse, pollDelay, slugify,
  validateSourceDraft, draftToSource, sourceToDraft,
} from '../../src/components/catalog/catalogState.js'
import {
  resolveTab, visibleTabs, resolveManage, stateChip, appActions, localizedText, toolSummary,
  shortDigest, updateStatus, formatBytes, rangeText, parseEntryKey, addedByAddon, screenshotsOf, listState, featuredEntries,
} from '../../src/components/addons/addonState.js'
import { createAddonsApi, createAddonsCatalogAdapter, AddonsError } from '../../src/utils/addonsApi.js'
import { runLifecycle, settleOperation } from '../../src/components/addons/lifecycleFlow.js'
import { createActionBook, effectOutcome } from '../../src/components/addons/actionBook.js'
import { describeError, describeOutcome } from '../../src/components/addons/errors.js'
import { backendState, libraryOrigin, shortDigest as shortBackendDigest } from '../../src/utils/backendRows.js'
import { runInstallAndTurnOn } from '../../src/components/addons/installAndTurnOn.js'
import { installSteps, STEP_ORDER } from '../../src/components/addons/installSteps.js'

const json = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body })

test('query normalization bounds text, library and page', () => {
  assert.deepEqual(normalizeQuery({ text: '  hi ', sourceId: 'Bad ID', page: '-3' }), { text: 'hi', sourceId: '', page: 1 })
  assert.equal(normalizeQuery({ text: 'x'.repeat(999) }).text.length, 256)
  assert.equal(normalizeQuery({ page: '4' }).page, 4)
  assert.equal(queryKey({ text: 'a' }), queryKey({ text: ' a ', page: 1 }))
  assert.notEqual(queryKey({ text: 'a' }), queryKey({ text: 'a', page: 2 }))
  assert.equal(queryToOffset({ page: 3 }, 24), 48)
  assert.equal(totalPages(0), 1)
  assert.equal(totalPages(49, 24), 3)
  const params = queryToParams({ text: 'notes', sourceId: 'main', page: 2 })
  assert.deepEqual(params, { q: 'notes', source: 'main', page: '2' })
  assert.deepEqual(paramsToQuery(k => params[k]), { text: 'notes', sourceId: 'main', page: 2 })
  assert.deepEqual(queryToParams({}), {})
})

test('an operation book keeps one identity per action until it is done', () => {
  let n = 0
  const book = createOperationBook(() => `op-${++n}`)
  assert.equal(book.idFor('start:a:1'), 'op-1')
  assert.equal(book.idFor('start:a:1'), 'op-1')
  assert.equal(book.idFor('stop:a:1'), 'op-2')
  book.done('start:a:1')
  assert.equal(book.idFor('start:a:1'), 'op-3')
  const a = newOperationId()
  assert.match(a, /^[0-9a-f-]{36}$/)
  assert.notEqual(a, newOperationId())
  assert.match(newOperationId({}), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
})

test('responses classify the same way everywhere', () => {
  assert.equal(classifyResponse(401, {}).kind, 'auth')
  assert.equal(classifyResponse(403, { error: { type: 'addon_error', message: 'no' } }).kind, 'denied')
  assert.equal(classifyResponse(400, null).kind, 'invalid')
  const unknown = classifyResponse(503, { error: { type: 'outcome_unknown', operationId: 'op-9' } })
  assert.deepEqual([unknown.kind, unknown.operationId], ['unknown_outcome', 'op-9'])
  assert.equal(classifyResponse(503, { error: { type: 'addon_error' } }).kind, 'unavailable')
  assert.equal(classifyResponse(500, 'x').kind, 'unavailable')
  // A body that only claims success never becomes success.
  assert.equal(classifyResponse(503, { payload: { Phase: 'completed' } }).kind, 'unavailable')
})

test('polling backs off on failure, slows when hidden and stays bounded', () => {
  assert.equal(pollDelay(0, false), 5000)
  assert.equal(pollDelay(0, true), 30000)
  assert.equal(pollDelay(1), 10000)
  assert.equal(pollDelay(2), 20000)
  assert.equal(pollDelay(50), 60000)
  assert.equal(pollDelay(50, true), 60000)
})

test('library drafts accept references and refuse secrets before sending', () => {
  const ok = { id: 'main', name: 'Main', url: 'https://library.example/releases.json', enabled: true }
  assert.deepEqual(validateSourceDraft(ok), {})
  assert.equal(validateSourceDraft({ ...ok, url: 'https://u:pw@library.example/x' }).url, 'urlCredentials')
  assert.equal(validateSourceDraft({ ...ok, url: 'https://library.example/x?token=abc' }).url, 'urlQuery')
  assert.equal(validateSourceDraft({ ...ok, url: 'ftp://library.example/x' }).url, 'url')
  assert.equal(validateSourceDraft({ ...ok, authReference: 'ghp_ABCdef123456' }).authReference, 'reference')
  assert.equal(validateSourceDraft({ ...ok, authReference: 'Bearer abc' }).authReference, 'reference')
  assert.equal(validateSourceDraft({ ...ok, authReference: 'library-token' }).authReference, undefined)
  assert.equal(validateSourceDraft({ ...ok, requireSignature: true }).issuer, 'required')
  assert.equal(validateSourceDraft({ ...ok, id: 'Bad' }).id, 'id')
  const wire = draftToSource({ ...ok, authReference: ' library-token ', requireSignature: true, issuer: 'i', identity: 'd', password: 'x', token: 'y' })
  assert.deepEqual(wire, { id: 'main', name: 'Main', url: 'https://library.example/releases.json', enabled: true, policy: { requireSignature: true, issuer: 'i', identity: 'd' }, authReference: 'library-token' })
  assert.equal(JSON.stringify(wire).includes('password'), false)
  assert.equal(draftToSource({ ...ok, allowLoopbackHTTP: true }).allowLoopbackHTTP, true)
  assert.equal(sourceToDraft({ id: 'a', name: 'A', url: 'https://x.example' }).enabled, true)
  assert.equal(slugify('My Private Library!'), 'my-private-library')
  assert.equal(slugify('9 lives'), 'lib-9-lives')
})

test('tabs: the server decides, the page only avoids showing what it will refuse', () => {
  assert.deepEqual(visibleTabs(false), ['apps'])
  assert.deepEqual(visibleTabs(true), ['apps', 'browse', 'libraries'])
  // The Installed tab is folded into My add-ons; an old address lands there.
  assert.equal(resolveTab('installed', true), 'apps')
  assert.equal(resolveTab('installed', false), 'apps')
  assert.equal(resolveTab('libraries', false), 'apps')
  assert.equal(resolveTab('libraries', true), 'libraries')
  assert.equal(resolveTab('nope', true), 'apps')
  assert.equal(resolveManage(new URLSearchParams('manage=notes'), true), 'notes')
  assert.equal(resolveManage(new URLSearchParams('manage=notes'), false), '')
  assert.equal(resolveManage(new URLSearchParams('manage=../x'), true), '')
})

test('app chips never turn an unknown state into running', () => {
  assert.deepEqual(stateChip({ state: 'running' }), { key: 'running', tone: 'ok' })
  assert.deepEqual(stateChip({ state: 'weird' }), { key: 'failed', tone: 'error' })
  assert.deepEqual(stateChip({}), { key: 'failed', tone: 'error' })
  assert.equal(stateChip({ state: 'stopped' }).tone, 'idle')
  assert.equal(stateChip({ state: 'update-pending' }).key, 'update-pending')
  assert.deepEqual(appActions({ admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=n' } }), { start: true, stop: false, manage: '/app/addons?manage=n' })
  assert.deepEqual(appActions({}), { start: false, stop: false, manage: '' })
})

test('titles follow the language, then the default text, then the id', () => {
  const app = { id: 'n', name: 'n', title: 'Notes', titles: { it: 'Appunti', 'pt-BR': 'Notas' }, description: 'Keep', descriptions: { it: 'Tieni' } }
  assert.equal(localizedText(app, 'title', 'it'), 'Appunti')
  assert.equal(localizedText(app, 'title', 'it-IT'), 'Appunti')
  assert.equal(localizedText(app, 'title', 'de'), 'Notes')
  assert.equal(localizedText({ id: 'x', name: 'x' }, 'title', 'en'), 'x')
  assert.equal(localizedText(app, 'description', 'it'), 'Tieni')
  assert.deepEqual(toolSummary({ tools: ['a', 'b', 'c'], toolCount: 40 }, 2), { shown: ['a', 'b'], more: 38, count: 40 })
  assert.deepEqual(toolSummary({}), { shown: [], more: 0, count: 0 })
})

test('update status comes only from an authoritative check', () => {
  assert.deepEqual(updateStatus({ version: '1.0.0' }), { kind: 'unknown' })
  assert.equal(updateStatus({ update: { available: true, version: '2.0.0' } }).kind, 'available')
  // The owner's own word: a state says current, a bare `available: false` does not.
  assert.equal(updateStatus({ update: { available: false } }).kind, 'unknown')
  assert.equal(updateStatus({ update: { available: false, state: 'up-to-date' } }).kind, 'current')
  assert.equal(updateStatus({ update: { available: false, state: 'check-failed' } }).kind, 'failed')
  assert.equal(shortDigest('sha256:0123456789abcdef0123'), 'sha256:0123456789ab')
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(1536), '1.5 KiB')
  assert.equal(rangeText({ min: 1, max: 1 }), '1')
  assert.equal(rangeText({ min: 1, max: 2 }), '1-2')
  assert.deepEqual(parseEntryKey('main/notes/1.0.0'), { sourceId: 'main', id: 'notes', version: '1.0.0' })
  assert.equal(parseEntryKey('a/b'), null)
})

test('the api sends one dispatch shape and classifies refusals', async () => {
  const calls = []
  const api = createAddonsApi({
    base: p => `/base${p}`,
    fetch: async (url, init) => {
      calls.push({ url, init })
      const body = init.body ? JSON.parse(init.body) : null
      if (body?.action === 'catalog-search') return json(200, { payload: { releases: [{ id: 'notes', name: 'Notes', version: '1.0.0', sourceId: 'main', os: 'linux', arch: 'amd64', digest: 'sha256:a' }], total: 31 } })
      if (body?.action === 'install') return json(503, { error: { type: 'outcome_unknown', operationId: body.operationId } })
      if (url.endsWith('/api/library/sources')) return json(403, { error: { type: 'authorization_error', message: 'Admin access required' } })
      return json(200, { apps: [{ id: 'notes' }] })
    },
  })
  const adapter = createAddonsCatalogAdapter(api, { pageSize: 10 })
  const page = await adapter.search({ text: 'no', sourceId: 'main', page: 2 })
  assert.equal(calls[0].url, '/base/api/addons/dispatch')
  assert.equal(calls[0].init.method, 'POST')
  assert.equal(calls[0].init.credentials, 'same-origin')
  const sent = JSON.parse(calls[0].init.body)
  assert.deepEqual(sent.arguments, { text: 'no', kind: 'all', sourceId: 'main', offset: 10, limit: 10 })
  assert.equal(sent.action, 'catalog-search')
  assert.ok(sent.operationId)
  assert.equal(page.total, 31)
  assert.equal(page.entries[0].key, 'main/notes/1.0.0')
  assert.equal(page.entries[0].kind, 'addon')
  await assert.rejects(adapter.listSources(), e => e instanceof AddonsError && e.kind === 'denied')
  await assert.rejects(
    adapter.submitLifecycle({ operationId: 'op-1', action: 'install', addonId: 'notes', sourceId: 'main', version: '1.0.0' }),
    e => e.kind === 'unknown_outcome' && e.operationId === 'op-1',
  )
  const list = await api.apps.list()
  assert.deepEqual(list, [{ id: 'notes' }])
  // The editor sends a reference and the adapter forwards it untouched.
  await adapter.saveSource({ id: 'p', name: 'P', url: 'https://l.example', enabled: false, authReference: 'library-token' }, undefined, 'op-save')
  const save = JSON.parse(calls.at(-1).init.body)
  assert.equal(save.operationId, 'op-save')
  assert.equal(save.arguments.authReference, 'library-token')
})

test('an unknown outcome is read with the original identity and never resubmitted', async () => {
  const seen = []
  const writes = []
  let reads = 0
  const api = {
    async operationStatus(operationId) {
      seen.push(operationId)
      reads += 1
      return { phase: reads < 3 ? 'prepared' : 'completed' }
    },
  }
  const result = await runLifecycle(api, {
    operationId: 'op-7',
    addonId: 'notes',
    sleep: async () => {},
    submit: async () => { writes.push('op-7'); throw new AddonsError({ kind: 'unknown_outcome', operationId: 'op-7' }) },
  })
  assert.deepEqual(result, { outcome: 'completed', phase: 'completed' })
  assert.deepEqual(writes, ['op-7'])
  assert.deepEqual(seen, ['op-7', 'op-7', 'op-7'])
})

test('a denial is not retried and an exhausted read is reported as unknown', async () => {
  const denied = { async operationStatus() { throw new AddonsError({ kind: 'denied' }) } }
  await assert.rejects(settleOperation(denied, { operationId: 'x', sleep: async () => {} }), e => e.kind === 'denied')
  let reads = 0
  const silent = { async operationStatus() { reads += 1; throw new AddonsError({ kind: 'unavailable' }) } }
  const out = await settleOperation(silent, { operationId: 'x', waits: [1, 1], sleep: async () => {} })
  assert.equal(out.outcome, 'unknown')
  assert.equal(reads, 3)
  const failed = await runLifecycle({}, { operationId: 'x', submit: async () => ({ phase: 'failed' }) })
  assert.equal(failed.outcome, 'failed')
  await assert.rejects(runLifecycle({}, { operationId: 'x', submit: async () => { throw new AddonsError({ kind: 'denied' }) } }), e => e.kind === 'denied')
})

test('an action book binds one identity to one click and its retries', () => {
  let n = 0
  const book = createActionBook(() => `op-${++n}`)
  const first = book.begin('notes', 'start:notes:3')
  assert.deepEqual([first.operationId, first.blocked], ['op-1', undefined])
  // A retry of the same action replays the same operation.
  assert.equal(book.begin('notes', 'start:notes:3').operationId, 'op-1')
  // Another subject has its own identity.
  assert.equal(book.begin('tables', 'start:tables:1').operationId, 'op-2')
  // An action the user abandoned for a different one never replays.
  assert.equal(book.begin('notes', 'stop:notes:3').operationId, 'op-3')
  assert.equal(book.begin('notes', 'start:notes:3').operationId, 'op-4')
})

test('an action book mints a fresh identity once an action is resolved', () => {
  let n = 0
  const book = createActionBook(() => `op-${++n}`)
  assert.equal(book.begin('notes', 'start:notes:3').operationId, 'op-1')
  book.resolve('notes', 'start:notes:3')
  assert.equal(book.pending('notes'), null)
  // The same key is a new click: it must not replay a finished operation.
  assert.equal(book.begin('notes', 'start:notes:3').operationId, 'op-2')
  // Resolving another key leaves the pending identity alone.
  book.resolve('notes', 'stop:notes:3')
  assert.equal(book.begin('notes', 'start:notes:3').operationId, 'op-2')
  assert.equal(book.size(), 1)
})

test('an action book blocks another action on a subject with an unknown outcome until it is resolved or dismissed', () => {
  let n = 0
  const book = createActionBook(() => `op-${++n}`)
  book.begin('notes', 'start:notes:3')
  book.markUnknown('notes', 'start:notes:3')
  assert.equal(book.pending('notes').unknown, true)
  const blocked = book.begin('notes', 'stop:notes:3')
  assert.deepEqual([blocked.blocked, blocked.key, blocked.operationId], [true, 'start:notes:3', 'op-1'])
  // Nothing was minted for the blocked action, and the unknown one is intact.
  assert.equal(book.size(), 1)
  assert.equal(book.pending('notes').operationId, 'op-1')
  // Another add-on is not affected.
  assert.equal(book.begin('tables', 'start:tables:1').blocked, undefined)
  // Resolved (status read as final) or dismissed: the next click is fresh.
  book.resolve('notes', 'start:notes:3')
  assert.equal(book.begin('notes', 'stop:notes:3').operationId, 'op-3')
  book.markUnknown('notes', 'stop:notes:3')
  book.resolve('notes', 'stop:notes:3')
  assert.equal(book.begin('notes', 'start:notes:3').operationId, 'op-4')
})

test('a completed status that left the add-on unchanged is not reported as done', () => {
  assert.equal(effectOutcome('completed', true), 'completed')
  assert.equal(effectOutcome('completed', undefined), 'completed')
  assert.equal(effectOutcome('completed', false), 'not_applied')
  assert.equal(effectOutcome('failed', false), 'failed')
  assert.equal(effectOutcome('unknown', false), 'unknown')
})

test('a refusal shows the server reason and falls back to the generic text', () => {
  const t = key => `T:${key}`
  const withBody = new AddonsError(classifyResponse(403, { error: { type: 'addon_error', message: 'Same-origin browser request required' } }))
  assert.equal(describeError(withBody, t), 'Same-origin browser request required')
  assert.equal(describeOutcome('denied', t, withBody), 'Same-origin browser request required')
  const noBody = new AddonsError(classifyResponse(403, null))
  assert.equal(describeError(noBody, t), 'T:errors.denied')
  assert.equal(describeOutcome('denied', t, noBody), 'T:errors.denied')
  assert.equal(describeOutcome('denied', t), 'T:errors.denied')
  const invalid = new AddonsError(classifyResponse(400, { error: { message: 'Bad id' } }))
  assert.equal(describeError(invalid, t), 'Bad id')
  // Other kinds keep their own words.
  assert.equal(describeError(new AddonsError(classifyResponse(401, { error: { message: 'x' } })), t), 'T:errors.auth')
})

test('a backend entry keeps its kind and installs through the backend route', async () => {
  const calls = []
  const api = createAddonsApi({
    fetch: async (url, init) => {
      calls.push({ url, init })
      const body = init.body ? JSON.parse(init.body) : null
      if (body?.action === 'catalog-search') return json(200, { payload: { releases: [{ kind: 'backend', addonId: 'cpu-local-store', alias: 'local-store', name: 'cpu-local-store', version: '0.0.0-latest.aaaaaaaaaaaa', sourceId: 'official-backends', os: 'linux', arch: 'amd64', digest: 'sha256:a' }], total: 1 } })
      if (url === '/backends/library/install') return json(409, { error: { type: 'backend_library', message: 'backend is already installed from another origin: "cpu-local-store"' } })
      if (url === '/backends') return json(200, [{ Name: 'cpu-local-store', Metadata: { library: 'official-backends' } }, { Name: 'llama-cpp' }])
      return json(200, {})
    },
  })
  const adapter = createAddonsCatalogAdapter(api, { pageSize: 10 })
  const page = await adapter.search({})
  assert.equal(page.entries[0].kind, 'backend')
  assert.equal(page.entries[0].alias, 'local-store')
  assert.equal(page.entries[0].key, 'official-backends/cpu-local-store/0.0.0-latest.aaaaaaaaaaaa')
  assert.deepEqual(await api.backends.installed(), [{ id: 'cpu-local-store', library: 'official-backends', version: '', digest: '' }, { id: 'llama-cpp', library: '', version: '', digest: '' }])
  await assert.rejects(
    api.backends.install({ sourceId: 'official-backends', name: 'cpu-local-store', version: 'v' }),
    e => e instanceof AddonsError && e.kind === 'unavailable' && e.status === 409 && /another origin/.test(e.message),
  )
  const sent = JSON.parse(calls.at(-1).init.body)
  assert.deepEqual(sent, { sourceId: 'official-backends', name: 'cpu-local-store', version: 'v' })
})

test('a library signer is named exactly or by a pattern, never both', () => {
  const ok = { id: 'b', name: 'B', url: 'https://library.example/i.yaml', enabled: true, requireSignature: true, issuer: 'i' }
  assert.equal(validateSourceDraft({ ...ok }).identity, 'required')
  assert.deepEqual(validateSourceDraft({ ...ok, identityRegex: '^a$' }), {})
  assert.equal(validateSourceDraft({ ...ok, identity: 'x', identityRegex: '^a$' }).identityRegex, 'either')
  const wire = draftToSource({ ...ok, identityRegex: ' ^a$ ' })
  assert.deepEqual(wire.policy, { requireSignature: true, issuer: 'i', identityRegex: '^a$' })
  assert.equal(sourceToDraft({ policy: { requireSignature: true, issuer: 'i', identityRegex: '^a$' } }).identityRegex, '^a$')
})

test('a backend install follows its job through the steps and ends with the result', async () => {
  const phases = ['resolving', 'size', 'fetching', 'writing']
  let polls = 0
  const urls = []
  const api = createAddonsApi({
    fetch: async (url) => {
      urls.push(url)
      if (url === '/backends/library/install') return json(200, { id: 'job-1', status_url: '/backends/jobs/job-1' })
      if (url === '/backends/jobs/job-1') {
        const phase = phases[polls++]
        return json(200, phase ? { phase, processed: false, progress: phase === 'fetching' ? 40 : 0 } : { phase: 'done', processed: true, progress: 100, message: 'completed' })
      }
      return json(404, {})
    },
  })
  const seen = []
  const done = await api.backends.install({ sourceId: 's', name: 'n', version: 'v' }, undefined, { pollMs: 1, onStatus: st => seen.push(st.phase) })
  assert.deepEqual(seen, ['resolving', 'size', 'fetching', 'writing', 'done'])
  assert.equal(done.processed, true)
  assert.equal(urls.filter(u => u === '/backends/library/install').length, 1)
})

test('a failed backend install job throws the reason and keeps the step that failed', async () => {
  const api = createAddonsApi({
    fetch: async (url) => (url === '/backends/library/install'
      ? json(200, { id: 'j' })
      : json(200, { phase: 'size', processed: true, error: 'error: image is larger than the size limit' })),
  })
  await assert.rejects(
    api.backends.install({ sourceId: 's', name: 'n', version: 'v' }, undefined, { pollMs: 1 }),
    e => e instanceof AddonsError && e.kind === 'install' && e.phase === 'size' && e.message === 'image is larger than the size limit',
  )
})

test('install steps: left steps are done, the reported one runs, the rest wait', () => {
  assert.deepEqual(STEP_ORDER, ['resolving', 'verifying', 'size', 'fetching', 'writing'])
  const states = job => installSteps(job).map(s => `${s.id}:${s.state}`)
  assert.deepEqual(states(null), STEP_ORDER.map(id => `${id}:pending`))
  assert.deepEqual(states({ phase: 'size' }), ['resolving:done', 'verifying:done', 'size:active', 'fetching:pending', 'writing:pending'])
  assert.deepEqual(states({ phase: 'size', processed: true, error: 'x' }), ['resolving:done', 'verifying:done', 'size:failed', 'fetching:pending', 'writing:pending'])
  assert.deepEqual(states({ phase: 'done', processed: true }), STEP_ORDER.map(id => `${id}:done`))
  // The fetch step carries the percentage of the image.
  assert.equal(installSteps({ phase: 'fetching', progress: 41.6 }).find(s => s.id === 'fetching').progress, 42)
  // A library that needs no signature has no verifying step.
  assert.deepEqual(installSteps({ phase: 'size' }, { signed: false }).map(s => s.id), ['resolving', 'size', 'fetching', 'writing'])
})

test('backends page: a library backend shows its library and the digest it pinned', () => {
  const d = 'sha256:' + 'ab'.repeat(32)
  assert.deepEqual(libraryOrigin({ Name: 'cpu-demo', Metadata: { library: 'official-backends', digest: d, version: '1' } }), { id: 'official-backends', digest: d, version: '1' })
  // A gallery backend, a system backend and one without metadata have no library.
  assert.equal(libraryOrigin({ Name: 'llama-cpp', Metadata: { gallery_url: 'x' } }), null)
  assert.equal(libraryOrigin({ Name: 'llama-cpp' }), null)
  assert.equal(shortBackendDigest(d), 'sha256:abababababab')
  assert.equal(shortBackendDigest(''), '')
})

test('backends page: a newer build in the library is an update state, and a running job wins over it', () => {
  const update = { name: 'cpu-demo', installedVersion: '1', version: '2', digest: 'sha256:b' }
  assert.deepEqual(backendState({ installed: true, libraryUpdate: update }), { kind: 'update', from: '1', to: '', library: true })
  // Without the update the backend is current; the gallery upgrade keeps its own state.
  assert.equal(backendState({ installed: true }).kind, 'current')
  assert.deepEqual(backendState({ installed: true, upgrade: { installed_version: '1', available_version: '3' } }), { kind: 'update', from: '1', to: '3' })
  assert.equal(backendState({ installed: true, libraryUpdate: update, op: { progress: 10, jobID: 'j' } }).kind, 'installing')
})

test('the updates route of the host is read as a list and nothing is sent', async () => {
  const calls = []
  const api = createAddonsApi({
    fetch: async (url, init) => {
      calls.push({ url, method: init.method })
      return json(200, { updates: [{ name: 'cpu-demo', sourceId: 'p', version: '2', digest: 'sha256:b', signed: true }] })
    },
  })
  assert.deepEqual(await api.backends.updates(), [{ name: 'cpu-demo', sourceId: 'p', version: '2', digest: 'sha256:b', signed: true }])
  assert.deepEqual(calls, [{ url: '/backends/library/updates', method: 'GET' }])
})

const localesDir = join(dirname(fileURLToPath(import.meta.url)), '../../public/locales')
const leafKeys = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? leafKeys(v, `${p}${k}.`) : [`${p}${k}`]))
// A plural form is spelled per language (_one, _many, ...), so compare the stem.
const stems = (o) => new Set(leafKeys(o).map(k => k.replace(/_(zero|one|two|few|many|other)$/, '')))

test('every locale carries the add-on and navigation keys of English', () => {
  for (const file of ['addons', 'nav']) {
    const en = stems(JSON.parse(readFileSync(join(localesDir, 'en', `${file}.json`), 'utf8')))
    assert.ok(en.size > 20)
    for (const lang of readdirSync(localesDir)) {
      const have = stems(JSON.parse(readFileSync(join(localesDir, lang, `${file}.json`), 'utf8')))
      const missing = [...en].filter(k => !have.has(k))
      assert.deepEqual(missing, [], `${lang}/${file}.json lacks keys of English`)
    }
  }
})

test('every locale carries the chat tools control keys of English, with the one-line note', () => {
  const read = (lang) => JSON.parse(readFileSync(join(localesDir, lang, 'chat.json'), 'utf8')).addons
  const en = read('en')
  assert.ok(en.note && !en.note.includes('\n'))
  for (const lang of readdirSync(localesDir)) {
    const have = read(lang)
    for (const key of Object.keys(en)) assert.ok(have && have[key], `${lang}/chat.json lacks addons.${key}`)
  }
})

test('the add-on labels use one name and one verb pair', () => {
  const en = JSON.parse(readFileSync(join(localesDir, 'en', 'addons.json'), 'utf8'))
  const nav = JSON.parse(readFileSync(join(localesDir, 'en', 'nav.json'), 'utf8'))
  assert.equal(nav.items.addons, 'Add-ons')
  assert.deepEqual([en.tabs.apps, en.tabs.browse, en.tabs.libraries], ['My add-ons', 'Find add-ons', 'Sources'])
  assert.deepEqual([en.apps.start, en.apps.stop], ['Turn on', 'Turn off'])
  assert.ok(!/owner/i.test(en.supporting), 'the subtitle does not name the owner')
  assert.ok(!/\bapps?\b/i.test(JSON.stringify([...Object.values(en.tabs), en.supporting, nav.items.addons])), 'no label says Apps')
})

test('what an add-on adds comes from its page and its tools, and nothing is guessed', () => {
  assert.deepEqual(addedByAddon({ hasUI: true, tools: ['add', 'list'], toolCount: 2 }), { page: true, tools: 2, toolNames: ['add', 'list'], toolsMore: 0 })
  assert.deepEqual(addedByAddon({ hasUI: false, tools: [], toolCount: 0 }), { page: false, tools: 0, toolNames: [], toolsMore: 0 })
  assert.equal(addedByAddon({ mainPage: '/app/addons/x/home' }).page, true)
  const many = addedByAddon({ tools: ['a', 'b', 'c', 'd'], toolCount: 9 })
  assert.deepEqual([many.tools, many.toolNames.length, many.toolsMore], [9, 4, 5])
  assert.deepEqual(addedByAddon(null), { page: false, tools: 0, toolNames: [], toolsMore: 0 })
})

test('screenshots appear only when the host lists them and they are served by the host', () => {
  assert.deepEqual(screenshotsOf({}), [])
  assert.deepEqual(screenshotsOf({ screenshots: [] }), [])
  assert.deepEqual(screenshotsOf({ screenshots: ['/api/addons/apps/x/shot/0', 'https://elsewhere.example/a.png', '//elsewhere.example/b.png', 7] }), ['/api/addons/apps/x/shot/0'])
  assert.equal(screenshotsOf({ screenshots: Array.from({ length: 12 }, (_, i) => `/s/${i}`) }).length, 6)
})

test('the list says first run, no match or none, and a search never claims that nothing is installed', () => {
  const one = [{ id: 'notes' }]
  assert.equal(listState({ apps: [], shownCount: 0, query: '', isAdmin: true }), 'first-run')
  assert.equal(listState({ apps: [], shownCount: 0, query: '', isAdmin: false }), 'none')
  assert.equal(listState({ apps: [], shownCount: 0, query: '', isAdmin: true, canList: false }), 'none')
  assert.equal(listState({ apps: one, shownCount: 1, query: 'no', isAdmin: true }), 'list')
  assert.equal(listState({ apps: one, shownCount: 0, query: ' zzz ', isAdmin: true }), 'no-match')
  assert.equal(listState({ apps: one, shownCount: 0, query: 'zzz', isAdmin: false }), 'no-match')
  assert.equal(listState({ apps: one, shownCount: 0, query: '', isAdmin: false }), 'none')
})

test('featured add-ons are the first three add-on entries that are not installed', () => {
  const entries = [{ id: 'a', kind: 'addon' }, { id: 'b', kind: 'backend' }, { id: 'c', kind: 'addon' }, { id: 'd', kind: 'addon' }, { id: 'e', kind: 'addon' }]
  assert.deepEqual(featuredEntries(entries).map(e => e.id), ['a', 'c', 'd'])
  assert.deepEqual(featuredEntries(entries, ['a']).map(e => e.id), ['c', 'd', 'e'])
  assert.deepEqual(featuredEntries(null), [])
})

test('install and turn on runs the install, then the turn on, each as its own action', async () => {
  const entry = { key: 'main/tables/1.0.0', id: 'tables', sourceId: 'main', version: '1.0.0' }
  const marks = []
  const calls = []
  const sent = []
  const adapter = { submitLifecycle: async (c) => { sent.push(c); return {} } }
  const api = { installed: async () => [{ id: 'tables', enabled: true }] }
  const run = async (key, { addonId, submit, verify }) => {
    calls.push(key)
    await submit('op-' + key, undefined)
    assert.equal(await verify(), true)
    return { outcome: 'completed', operationId: 'op-' + key }
  }
  await runInstallAndTurnOn({ run, api, adapter, mark: m => marks.push(m.step) }, entry)
  assert.deepEqual(calls, ['install:main/tables/1.0.0', 'enable:tables:0'])
  assert.deepEqual(marks, ['install', 'turn-on', 'done'])
  assert.deepEqual(sent.map(c => [c.action, c.sourceId || '', c.version || '', c.generation || 0]), [['install', 'main', '1.0.0', 0], ['enable', '', '', 0]])
})

test('install and turn on stops at a failed install and retries only the turn on', async () => {
  const entry = { key: 'main/tables/1.0.0', id: 'tables', sourceId: 'main', version: '1.0.0' }
  const adapter = { submitLifecycle: async () => ({}) }
  const api = { installed: async () => [] }
  const marks = []
  const failing = async (key) => (key.startsWith('install') ? { outcome: 'denied', error: { message: 'no' } } : { outcome: 'completed' })
  const calls = []
  const run = (fn) => async (key, o) => { calls.push(key); return fn(key, o) }
  await runInstallAndTurnOn({ run: run(failing), api, adapter, mark: m => marks.push(m) }, entry)
  assert.deepEqual(calls, ['install:main/tables/1.0.0'])
  assert.deepEqual(marks.map(m => m.step), ['install', 'failed'])
  assert.equal(marks[1].failedStep, 'install')
  // The turn on failed after a good install: a retry skips the install.
  calls.length = 0
  marks.length = 0
  const turnOnFails = async (key) => (key.startsWith('enable') ? { outcome: 'unknown' } : { outcome: 'completed' })
  await runInstallAndTurnOn({ run: run(turnOnFails), api, adapter, mark: m => marks.push(m) }, entry)
  assert.deepEqual(marks.map(m => `${m.step}:${m.failedStep || ''}:${m.outcome || ''}`), ['install::', 'turn-on::', 'failed:turn-on:unknown'])
  calls.length = 0
  marks.length = 0
  await runInstallAndTurnOn({ run: run(async () => ({ outcome: 'completed' })), api, adapter, mark: m => marks.push(m) }, entry, { skipInstall: true })
  assert.deepEqual(calls, ['enable:tables:0'])
  assert.deepEqual(marks.map(m => m.step), ['turn-on', 'done'])
})
