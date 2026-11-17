// SPDX-License-Identifier: MIT
// The client of the routes that share one of the person's own pages, and the
// share dialog's client over it.
import test from 'node:test'
import assert from 'node:assert/strict'
import { createPageSharesApi, pageShareRequest, readGrants, readUsers } from '../../src/utils/pageSharesApi.js'
import { pageShareClient, SEARCH_LIMIT } from '../../src/components/share/shareClients.js'

test('page share requests: the path, the method and the confirmation header come from the call', () => {
  assert.deepEqual(pageShareRequest({ op: 'grants', name: 'tasks' }),
    { method: 'GET', path: '/api/chats/pages/tasks/shares', headers: { 'X-LocalAI-Confirm': 'tasks' } })
  assert.deepEqual(pageShareRequest({ op: 'grant-set', name: 'tasks', principal: 'u-1', role: 'use' }),
    { method: 'PUT', path: '/api/chats/pages/tasks/shares', headers: { 'X-LocalAI-Confirm': 'tasks' }, body: { principal: 'u-1', role: 'use' } })
  assert.deepEqual(pageShareRequest({ op: 'grant-revoke', name: 'tasks', principal: '@everyone' }),
    { method: 'DELETE', path: '/api/chats/pages/tasks/shares/@everyone', headers: { 'X-LocalAI-Confirm': 'tasks' } })
  assert.equal(pageShareRequest({ op: 'users', name: 'tasks', query: '  ali ', limit: 50 }).path, '/api/chats/pages/shares/search?q=ali&limit=20')
})

test('page share requests: a bad page, person, role or query is refused before it is sent', () => {
  for (const call of [
    { op: 'grants', name: '' },
    { op: 'grants', name: '../x' },
    { op: 'grants', name: 'Tasks' },
    { op: 'grant-set', name: 'tasks', principal: 'a/b', role: 'view' },
    { op: 'grant-set', name: 'tasks', principal: 'u1', role: 'write' },
    { op: 'grant-set', name: 'tasks', principal: '@everyone', role: 'edit' },
    { op: 'users', name: 'tasks', query: 'al' },
    { op: 'nope', name: 'tasks' },
  ]) {
    assert.throws(() => pageShareRequest(call), TypeError, JSON.stringify(call))
  }
})

test('page share answers: names, IDs and the count of people who allowed are read from either spelling', () => {
  const a = readGrants({ grants: [{ id: 'u-1', display_name: 'Ann', role: 'use' }, { userId: 'u-2', displayName: 'Bo', role: 'edit' }, { role: 'view' }], allowed: 1 })
  assert.deepEqual(a.grants.map(g => [g.userId, g.displayName, g.role]), [['u-1', 'Ann', 'use'], ['u-2', 'Bo', 'edit']])
  assert.equal(a.allowed, 1)
  // Without a count for the page, the grants that carry a consent add up.
  const b = readGrants({ grants: [{ principal: 'u-1', role: 'view', consent: 1 }, { principal: 'u-2', role: 'view', consent: 0 }] })
  assert.equal(b.allowed, 1)
  // Nothing says: the count is unknown, not zero.
  assert.equal(readGrants({ grants: [{ principal: 'u-1', role: 'view' }] }).allowed, null)
  // An answer without a list is passed on, so the dialog reads again.
  assert.deepEqual(readGrants({}), {})
  assert.deepEqual(readUsers({ users: [{ id: 'u-1', display_name: 'Ann' }, {}] }).users, [{ id: 'u-1', displayName: 'Ann' }])
})

test('page share client: the dialog gets its four calls over the page routes', async () => {
  const sent = []
  const api = createPageSharesApi({ call: async (path, opts) => { sent.push({ path, ...opts }); return { grants: [] } } })
  const client = pageShareClient(api, 'tasks')
  await client.grants()
  await client.setGrant('u-2', 'edit')
  await client.revoke('@everyone')
  await client.searchUsers('bob')
  assert.deepEqual(sent.map(s => `${s.method} ${s.path}`), [
    'GET /api/chats/pages/tasks/shares',
    'PUT /api/chats/pages/tasks/shares',
    'DELETE /api/chats/pages/tasks/shares/@everyone',
    `GET /api/chats/pages/shares/search?q=bob&limit=${SEARCH_LIMIT}`,
  ])
  assert.deepEqual(sent[1].body, { principal: 'u-2', role: 'edit' })
})
