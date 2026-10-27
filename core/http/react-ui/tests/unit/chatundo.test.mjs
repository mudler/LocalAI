import test from 'node:test'
import assert from 'node:assert/strict'
import { createAddonsApi, AddonsError } from '../../src/utils/addonsApi.js'
import { classifyUndo, runUndo, undoMessageKey } from '../../src/utils/addonUndo.js'

const RES = 'r0123456789abcdef0123456789abcde'
const reply = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body })

function api(handler) {
  const calls = []
  const fetch = async (url, init) => { calls.push({ url, init }); return handler(url, init, calls) }
  return { api: createAddonsApi({ fetch }), calls }
}

test('chat undo: the call is a same-origin POST that names the resource in the confirmation header and carries one operation ID', async () => {
  const { api: a, calls } = api(() => reply(200, { resourceId: RES, changeId: 't1', revision: { schema: 2, data: 3 } }))
  const out = await a.changes.undo(RES, 't1', { operationId: 'u-1', generation: 4 })
  assert.equal(out.changeId, 't1')
  assert.equal(calls[0].url, `/api/addons/resources/${RES}/changes/t1/undo`)
  assert.equal(calls[0].init.method, 'POST')
  assert.equal(calls[0].init.credentials, 'same-origin')
  assert.equal(calls[0].init.headers['X-LocalAI-Confirm'], RES)
  assert.deepEqual(JSON.parse(calls[0].init.body), { operationId: 'u-1', generation: 4 })
})

test('chat undo: every typed refusal of the host keeps its own kind and the words of what changed', () => {
  for (const [status, type] of [[409, 'stale'], [404, 'not_found'], [410, 'expired'], [409, 'already_undone'], [409, 'cannot_undo']]) {
    const c = classifyUndo(status, { error: { type, message: 'after this change the database inserted 1 row into People' } })
    assert.equal(c.kind, type)
    assert.match(c.message, /inserted 1 row/)
  }
  assert.equal(classifyUndo(403, { error: { type: 'addon_error', message: 'x' } }).kind, 'denied')
  assert.equal(classifyUndo(503, { error: { type: 'outcome_unknown', operationId: 'u-1' } }).kind, 'unknown_outcome')
  assert.equal(classifyUndo(404, { error: { type: 'something_else' } }).kind, 'unavailable', 'a 404 of another type is not an undo answer')
  assert.equal(classifyUndo(500, null).kind, 'unavailable')
})

test('chat undo: the generation comes from the add-on list, and the result says what happened', async () => {
  const { api: a, calls } = api((url) => (url.startsWith('/api/addons/apps') ? reply(200, { apps: [{ id: 'tables', generation: 7 }] }) : reply(200, { changeId: 't1' })))
  const out = await runUndo({ api: a, addonId: 'tables', resourceId: RES, changeId: 't1', operationId: 'u-1' })
  assert.deepEqual(out, { kind: 'done', deleted: false })
  assert.deepEqual(JSON.parse(calls[1].init.body), { operationId: 'u-1', generation: 7 })
  const deleted = await runUndo({ api: api((url) => (url.startsWith('/api/addons/apps') ? reply(200, { apps: [{ id: 'tables', generation: 7 }] }) : reply(200, { deleted: true }))).api, addonId: 'tables', resourceId: RES, changeId: 't1', operationId: 'u-2' })
  assert.deepEqual(deleted, { kind: 'done', deleted: true })
})

test('chat undo: a refused undo is shown as such, an unknown outcome keeps the operation ID to repeat, and an add-on that is gone is unavailable', async () => {
  const refuse = (status, type, message = 'm') => api((url) => (url.startsWith('/api/addons/apps') ? reply(200, { apps: [{ id: 'tables', generation: 7 }] }) : reply(status, { error: { type, message } }))).api
  const run = (a) => runUndo({ api: a, addonId: 'tables', resourceId: RES, changeId: 't1', operationId: 'u-1' })
  assert.deepEqual(await run(refuse(409, 'stale', 'after this change the database inserted 1 row into People')), { kind: 'stale', message: 'after this change the database inserted 1 row into People' })
  assert.equal((await run(refuse(410, 'expired'))).kind, 'expired')
  assert.equal((await run(refuse(404, 'not_found'))).kind, 'not_found')
  assert.equal((await run(refuse(409, 'already_undone'))).kind, 'already_undone')
  assert.equal((await run(refuse(409, 'cannot_undo', 'the job already finished (completed)'))).message, 'the job already finished (completed)')
  assert.equal((await run(refuse(403, 'addon_error'))).kind, 'denied')
  assert.equal((await run(api((url) => (url.startsWith('/api/addons/apps') ? reply(200, { apps: [] }) : reply(200, {}))).api)).kind, 'unavailable', 'no such add-on in the list')
  assert.equal((await run(api(() => { throw new TypeError('offline') }).api)).kind, 'unavailable')
  const unknown = await run(api((url) => (url.startsWith('/api/addons/apps') ? reply(200, { apps: [{ id: 'tables', generation: 7 }] }) : reply(503, { error: { type: 'outcome_unknown', operationId: 'u-1' } }))).api)
  assert.equal(unknown.kind, 'unknown')
})

test('chat undo: each outcome has its own sentence in the chats locale', () => {
  for (const kind of ['done', 'stale', 'not_found', 'expired', 'already_undone', 'cannot_undo', 'denied', 'unknown', 'unavailable']) {
    assert.match(undoMessageKey(kind), /^tool\.undo[A-Za-z]+$/, kind)
  }
  assert.notEqual(undoMessageKey('stale'), undoMessageKey('expired'))
})

test('AddonsError keeps the typed refusal', () => {
  assert.equal(new AddonsError({ kind: 'stale', message: 'x' }).kind, 'stale')
})
