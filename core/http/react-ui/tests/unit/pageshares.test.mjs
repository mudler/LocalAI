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
  const users = pageShareRequest({ op: 'users', name: 'tasks', query: '  ali ', limit: 50 })
  assert.equal(users.path, '/api/chats/pages/shares/search?q=ali&limit=20')
  assert.deepEqual(users.headers, { 'X-LocalAI-Confirm': 'pages' }, 'the picker is confirmed with the word pages')
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

test('page share answers: the grants, the count of people who allowed, the switch and the version are read', () => {
  const a = readGrants({
    name: 'tasks', enabled: true, hash: 'h1', allowedCount: 1,
    grants: [{ userId: 'u-1', displayName: 'Ann', role: 'use', consent: 'allowed' }, { userId: '@everyone', displayName: '', role: 'view' }, { role: 'view' }],
  })
  assert.deepEqual(a.grants.map(g => [g.userId, g.displayName, g.role, g.consent]), [['u-1', 'Ann', 'use', 'allowed'], ['@everyone', '', 'view', '']])
  assert.equal(a.allowed, 1)
  assert.equal(a.enabled, true)
  assert.equal(a.hash, 'h1')
  // The switch is off when the server says so; a count that is missing is unknown, not zero.
  const b = readGrants({ enabled: false, grants: [{ userId: 'u-1', role: 'edit', consent: 'changed' }] })
  assert.equal(b.enabled, false)
  assert.equal(b.allowed, null)
  assert.equal(b.grants[0].consent, 'changed')
  // An unknown consent word is dropped, and the numbers of a grant are never added up.
  assert.equal(readGrants({ grants: [{ userId: 'u-1', role: 'view', consent: 1 }] }).grants[0].consent, '')
  // An answer without a list is passed on, so the dialog reads again.
  assert.deepEqual(readGrants({}), {})
  assert.deepEqual(readUsers({ users: [{ id: 'u-1', displayName: 'Ann' }, {}] }).users, [{ id: 'u-1', displayName: 'Ann' }])
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
