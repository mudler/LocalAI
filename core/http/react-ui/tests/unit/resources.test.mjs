import test from 'node:test'
import assert from 'node:assert/strict'
import * as React from 'react'
import { createHostSDK, AddonSdkError } from '../../src/addons/sdk.js'
import { resourceRequest, resourceOperationId, dispatchArgs, chatHeaders } from '../../src/addons/resourceCalls.js'
import { classifyResponse } from '../../src/components/catalog/catalogState.js'
import { createAddonsApi, AddonsError } from '../../src/utils/addonsApi.js'

const RID = 'r0123456789abcdef0123456789abcde'

function make(over = {}) {
  const controller = new AbortController()
  const calls = []
  const host = {
    React,
    components: {},
    Icon: () => null,
    theme: { mode: () => 'light', subscribe: () => () => {} },
    language: () => 'en',
    subscribeLanguage: () => () => {},
    api: {
      async dispatch() { return null },
      async propose() { return null },
      async resource(call, signal) { calls.push({ call, signal }); return { ok: true } },
    },
    ...over,
  }
  const sdk = createHostSDK({ addonId: 'tables', generation: 4, signal: controller.signal, pages: ['databases'], host })
  return { sdk, controller, calls }
}

test('resources: every call carries this add-on and its generation and nothing the add-on chose', async () => {
  const { sdk, calls } = make()
  await sdk.resources.list()
  await sdk.resources.create({ arguments: { name: 'Budget' } })
  await sdk.resources.rename(RID, { name: 'B2', revision: { schema: 1, data: 2 } })
  await sdk.resources.remove(RID, { confirm: 'B2', revision: { schema: 1, data: 2 } })
  await sdk.resources.apply(RID, { tool: 'rows-insert', arguments: { table: 'T', rows: [{ A: 1 }] }, revision: { schema: 1, data: 2 } })
  await sdk.resources.grants(RID)
  await sdk.resources.setGrant(RID, 'user-1', 'viewer')
  await sdk.resources.revokeGrant(RID, 'user-1')
  await sdk.resources.searchUsers(RID, 'ca', { limit: 5 })
  await sdk.resources.operation(RID, 'apply', 'op-1')
  await sdk.resources.selection.get('chat-1')
  await sdk.resources.selection.set('chat-1', RID)
  await sdk.resources.selection.clear('chat-1')
  assert.deepEqual(calls.map(c => c.call.op), ['list', 'create', 'rename', 'delete', 'apply', 'grants', 'grant-set', 'grant-revoke', 'users', 'status', 'selection-get', 'selection-put', 'selection-clear'])
  for (const { call } of calls) {
    assert.equal(call.addonId, 'tables')
    assert.equal(call.generation, 4)
  }
  // Mutations carry an operation identifier of their own, never a repeated one.
  const ops = calls.filter(c => ['create', 'rename', 'delete', 'apply'].includes(c.call.op)).map(c => c.call.operationId)
  assert.equal(new Set(ops).size, 4)
  for (const id of ops) assert.match(id, /^[a-z][a-z0-9-]{0,63}$/)
})

test('resources: an operation identifier chosen by the caller is kept across a retry', async () => {
  const { sdk, calls } = make()
  await sdk.resources.apply(RID, { tool: 'rows-delete', arguments: { table: 'T', ids: [1] }, revision: { schema: 1, data: 1 } }, { operationId: 'op-keep-1' })
  await sdk.resources.apply(RID, { tool: 'rows-delete', arguments: { table: 'T', ids: [1] }, revision: { schema: 1, data: 1 } }, { operationId: 'op-keep-1' })
  assert.deepEqual(calls.map(c => c.call.operationId), ['op-keep-1', 'op-keep-1'])
})

test('resources: nothing is sent once the add-on is withdrawn', async () => {
  const { sdk, controller, calls } = make()
  controller.abort('withdrawn')
  await assert.rejects(sdk.resources.list(), (e) => e instanceof AddonSdkError && e.code === 'withdrawn')
  await assert.rejects(sdk.resources.apply(RID, { tool: 'rows-insert', arguments: {}, revision: { schema: 1, data: 0 } }), AddonSdkError)
  await assert.rejects(sdk.resources.selection.set('c', RID), AddonSdkError)
  assert.equal(calls.length, 0)
})

test('resources: a malformed identifier is refused before any request', async () => {
  const { sdk, calls } = make()
  await assert.rejects(sdk.resources.grants('../x'), TypeError)
  await assert.rejects(sdk.resources.setGrant(RID, '', 'viewer'), TypeError)
  await assert.rejects(sdk.resources.setGrant(RID, 'u', 'owner'), TypeError)
  await assert.rejects(sdk.resources.selection.get('bad id'), TypeError)
  await assert.rejects(sdk.resources.apply(RID, { tool: 'Rows Insert', arguments: {}, revision: { schema: 1, data: 0 } }), TypeError)
  await assert.rejects(sdk.resources.apply(RID, { tool: 'rows-insert', arguments: [], revision: { schema: 1, data: 0 } }), TypeError)
  await assert.rejects(sdk.resources.apply(RID, { tool: 'rows-insert', arguments: {}, revision: {} }), TypeError)
  await assert.rejects(sdk.resources.remove(RID, { confirm: '', revision: { schema: 1, data: 0 } }), TypeError)
  assert.equal(calls.length, 0)
})

test('resources: the request of each call names the route, the method and the own-session confirmation', () => {
  const base = { addonId: 'tables', generation: 4 }
  const list = resourceRequest({ ...base, op: 'list' })
  assert.equal(list.method, 'GET')
  assert.equal(list.path, '/api/addons/resources?addonId=tables')
  assert.equal(list.headers['X-LocalAI-Confirm'], undefined)

  const create = resourceRequest({ ...base, op: 'create', operationId: 'op-1', arguments: { name: 'B' } })
  assert.equal(create.method, 'POST')
  assert.equal(create.path, '/api/addons/resources')
  assert.equal(create.headers['X-LocalAI-Confirm'], 'op-1')
  assert.deepEqual(create.body, { addonId: 'tables', operationId: 'op-1', generation: 4, arguments: { name: 'B' } })

  const apply = resourceRequest({ ...base, op: 'apply', resourceId: RID, operationId: 'op-2', tool: 'rows-update', arguments: { table: 'T' }, revision: { schema: 1, data: 3 } })
  assert.equal(apply.path, `/api/addons/resources/${RID}/apply`)
  assert.equal(apply.headers['X-LocalAI-Confirm'], RID)
  assert.deepEqual(apply.body, { operationId: 'op-2', generation: 4, revision: { schema: 1, data: 3 }, tool: 'rows-update', arguments: { table: 'T' } })

  const rename = resourceRequest({ ...base, op: 'rename', resourceId: RID, operationId: 'op-3', name: 'N', revision: { schema: 1, data: 3 } })
  assert.equal(rename.path, `/api/addons/resources/${RID}/rename`)
  assert.deepEqual(rename.body, { operationId: 'op-3', generation: 4, revision: { schema: 1, data: 3 }, name: 'N' })

  const del = resourceRequest({ ...base, op: 'delete', resourceId: RID, operationId: 'op-4', confirm: 'N', revision: { schema: 1, data: 3 } })
  assert.equal(del.path, `/api/addons/resources/${RID}/delete`)
  assert.deepEqual(del.body, { operationId: 'op-4', generation: 4, revision: { schema: 1, data: 3 }, confirm: 'N' })

  const set = resourceRequest({ ...base, op: 'grant-set', resourceId: RID, userId: 'user:1', role: 'editor', operationId: 'op-5' })
  assert.equal(set.method, 'PUT')
  assert.equal(set.path, `/api/addons/resources/${RID}/grants/user%3A1`)
  assert.deepEqual(set.body, { role: 'editor', operationId: 'op-5' })
  assert.equal(set.headers['X-LocalAI-Confirm'], RID)

  const rev = resourceRequest({ ...base, op: 'grant-revoke', resourceId: RID, userId: 'u1', operationId: 'op-6' })
  assert.equal(rev.method, 'DELETE')
  assert.equal(rev.path, `/api/addons/resources/${RID}/grants/u1?operationId=op-6`)

  const users = resourceRequest({ ...base, op: 'users', resourceId: RID, query: 'Ca rol', limit: 5 })
  assert.equal(users.path, `/api/addons/resources/${RID}/users?q=Ca%20rol&limit=5`)
  assert.equal(users.headers['X-LocalAI-Confirm'], RID)

  const status = resourceRequest({ ...base, op: 'status', resourceId: RID, kind: 'apply', operationId: 'op-7' })
  assert.equal(status.path, `/api/addons/resources/${RID}/operations/op-7?kind=apply`)

  const get = resourceRequest({ ...base, op: 'selection-get', conversationId: 'chat-1' })
  assert.equal(get.path, '/api/addons/conversations/chat-1/selection/tables')
  assert.equal(get.method, 'GET')
  const put = resourceRequest({ ...base, op: 'selection-put', conversationId: 'chat-1', resourceId: RID })
  assert.equal(put.method, 'PUT')
  assert.deepEqual(put.body, { resourceId: RID })
  const clear = resourceRequest({ ...base, op: 'selection-clear', conversationId: 'chat-1' })
  assert.equal(clear.method, 'DELETE')
})

test('resources: an unknown operation and a body the host did not shape are refused', () => {
  assert.throws(() => resourceRequest({ addonId: 'tables', generation: 1, op: 'purge' }), TypeError)
  assert.throws(() => resourceRequest({ addonId: 'Tables', generation: 1, op: 'list' }), TypeError)
  assert.throws(() => resourceRequest({ addonId: 'tables', generation: 0, op: 'create', operationId: 'op-1', arguments: {} }), TypeError)
  assert.match(resourceOperationId(), /^op-[0-9a-f-]{8,}$/)
})

test('resources: typed answers of the host keep their type for the page', () => {
  assert.equal(classifyResponse(409, { error: { type: 'stale' } }).kind, 'stale')
  assert.equal(classifyResponse(409, { error: { type: 'sharing_disabled' } }).kind, 'sharing_disabled')
  assert.equal(classifyResponse(404, { error: { type: 'not_selected' } }).kind, 'not_selected')
  assert.equal(classifyResponse(400, { error: { type: 'invalid_request' } }).kind, 'invalid')
  assert.equal(classifyResponse(403, { error: { type: 'quota_reached' } }).type, 'quota_reached')
  assert.equal(classifyResponse(403, { error: { type: 'outcome_blocked', blockedBy: 'p1' } }).blockedBy, 'p1')
  assert.equal(classifyResponse(409, { error: { type: 'other' } }).kind, 'unavailable')
  assert.equal(classifyResponse(503, { error: { type: 'outcome_unknown', operationId: 'op-9' } }).operationId, 'op-9')
})

test('resources: the client of a stale answer is told it is stale', async () => {
  const api = createAddonsApi({ fetch: async () => ({ ok: false, status: 409, json: async () => ({ error: { type: 'stale', message: 'x' } }) }) })
  await assert.rejects(api.call('/api/addons/resources'), (e) => e instanceof AddonsError && e.kind === 'stale' && e.status === 409)
})

test('resources: a typed read names the resource the person chose and the client sends it', async () => {
  const { sdk, calls } = make()
  await sdk.api.read({ resourceId: RID, arguments: { tool: 'rows-query', table: 'T' } })
  assert.equal(calls.length, 0)
  const sent = []
  const body = { operationId: 'op-r', action: 'read', addonId: 'tables', generation: 4, arguments: { tool: 'rows-query' }, resourceId: RID, extra: 'ignored' }
  assert.deepEqual(dispatchArgs(body), { operationId: 'op-r', action: 'read', addonId: 'tables', generation: 4, args: { tool: 'rows-query' }, resourceId: RID })
  assert.equal('resourceId' in dispatchArgs({ ...body, resourceId: '' }), false)
  const api = createAddonsApi({ fetch: async (url, init) => { sent.push(JSON.parse(init.body)); return { ok: true, status: 200, json: async () => ({ payload: { ok: 1 } }) } } })
  await api.dispatch(dispatchArgs(body))
  assert.equal(sent[0].resourceId, RID)
  assert.deepEqual(sent[0].arguments, { tool: 'rows-query' })
  await api.dispatch({ operationId: 'op-a', action: 'addons-installed' })
  assert.equal('resourceId' in sent[1], false)
})

test('resources: a typed read always carries an operation identifier the owner accepts', async () => {
  const dispatched = []
  const controller = new AbortController()
  const own = createHostSDK({
    addonId: 'tables', generation: 2, signal: controller.signal,
    host: {
      React, components: {}, Icon: () => null, theme: { mode: () => 'light', subscribe: () => () => {} }, language: () => 'en', subscribeLanguage: () => () => {},
      api: { async dispatch(body) { dispatched.push(body.operationId); return {} }, async propose() { return {} } },
    },
  })
  for (let i = 0; i < 200; i++) await own.api.read({ arguments: { tool: 'databases-inspect' } })
  assert.equal(new Set(dispatched).size, 200)
  for (const id of dispatched) assert.match(id, /^[a-z][a-z0-9-]{0,63}$/)
})

test('resources: the conversation header goes on a chat request that uses add-on tools, and only a well-formed one', () => {
  assert.deepEqual(chatHeaders({ id: 'chat-1', addonTools: true }), { 'X-LocalAI-Conversation-Id': 'chat-1' })
  assert.deepEqual(chatHeaders({ id: 'chat-1' }), {})
  assert.deepEqual(chatHeaders({ id: 'chat-1', addonTools: false }), {})
  assert.deepEqual(chatHeaders({ id: 'bad id', addonTools: true }), {})
  assert.deepEqual(chatHeaders({ id: '', addonTools: true }), {})
  assert.deepEqual(chatHeaders({ id: 'x'.repeat(129), addonTools: true }), {})
  assert.deepEqual(chatHeaders(null), {})
})
