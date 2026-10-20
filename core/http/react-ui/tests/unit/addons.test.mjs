import test from 'node:test'
import assert from 'node:assert/strict'
import {
  normalizeQuery, queryKey, queryToParams, paramsToQuery, totalPages, queryToOffset,
  createOperationBook, newOperationId, classifyResponse, pollDelay, slugify,
  validateSourceDraft, draftToSource, sourceToDraft,
} from '../../src/components/catalog/catalogState.js'
import {
  resolveTab, visibleTabs, resolveManage, stateChip, appActions, localizedText, toolSummary,
  shortDigest, updateStatus, formatBytes, rangeText, parseEntryKey,
} from '../../src/components/addons/addonState.js'
import { createAddonsApi, createAddonsCatalogAdapter, AddonsError } from '../../src/utils/addonsApi.js'
import { runLifecycle, settleOperation } from '../../src/components/addons/lifecycleFlow.js'
import { createActionBook, effectOutcome } from '../../src/components/addons/actionBook.js'
import { describeError, describeOutcome } from '../../src/components/addons/errors.js'

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
  assert.deepEqual(visibleTabs(true), ['apps', 'browse', 'installed', 'libraries'])
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
      if (body?.action === 'catalog-search') return json(200, { payload: { releases: [{ addonId: 'notes', name: 'Notes', version: '1.0.0', sourceId: 'main', os: 'linux', arch: 'amd64', digest: 'sha256:a' }], total: 31 } })
      if (body?.action === 'install') return json(503, { error: { type: 'outcome_unknown', operationId: body.operationId } })
      if (body?.action === 'catalog-sources') return json(403, { error: { type: 'addon_error', message: 'Addon administration required' } })
      return json(200, { apps: [{ id: 'notes' }] })
    },
  })
  const adapter = createAddonsCatalogAdapter(api, { pageSize: 10 })
  const page = await adapter.search({ text: 'no', sourceId: 'main', page: 2 })
  assert.equal(calls[0].url, '/base/api/addons/dispatch')
  assert.equal(calls[0].init.method, 'POST')
  assert.equal(calls[0].init.credentials, 'same-origin')
  const sent = JSON.parse(calls[0].init.body)
  assert.deepEqual(sent.arguments, { text: 'no', sourceId: 'main', offset: 10, limit: 10 })
  assert.equal(sent.action, 'catalog-search')
  assert.ok(sent.operationId)
  assert.equal(page.total, 31)
  assert.equal(page.entries[0].key, 'main/notes/1.0.0')
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
