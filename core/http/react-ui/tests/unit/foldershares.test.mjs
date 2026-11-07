// SPDX-License-Identifier: MIT
// The client of the routes that share a folder of the person's home, and the
// share dialog's client over it.
import test from 'node:test'
import assert from 'node:assert/strict'
import { createFolderSharesApi, folderShareRequest } from '../../src/utils/folderSharesApi.js'
import { folderShareClient, SEARCH_LIMIT } from '../../src/components/share/shareClients.js'

test('folder share requests: the path, the method and the confirmation header come from the call', () => {
  assert.deepEqual(folderShareRequest({ op: 'list' }), { method: 'GET', path: '/api/chats/files/shares', headers: {} })
  assert.deepEqual(folderShareRequest({ op: 'find', path: '/Projects' }),
    { method: 'POST', path: '/api/chats/files/shares', headers: {}, body: { path: '/Projects' } })
  assert.deepEqual(folderShareRequest({ op: 'grants', id: 42 }),
    { method: 'GET', path: '/api/chats/files/shares/42/grants', headers: { 'X-LocalAI-Confirm': '42' } })
  assert.deepEqual(folderShareRequest({ op: 'grant-set', id: 42, principal: '@everyone', role: 'read' }),
    { method: 'PUT', path: '/api/chats/files/shares/42/grants/@everyone', headers: { 'X-LocalAI-Confirm': '42' }, body: { role: 'read' } })
  assert.deepEqual(folderShareRequest({ op: 'grant-revoke', id: '42', principal: 'u-1' }),
    { method: 'DELETE', path: '/api/chats/files/shares/42/grants/u-1', headers: { 'X-LocalAI-Confirm': '42' } })
  assert.equal(folderShareRequest({ op: 'users', id: 42, query: '  ali ', limit: 50 }).path, '/api/chats/files/shares/42/users?q=ali&limit=20')
})

test('folder share requests: a bad folder, person, role or query is refused before it is sent', () => {
  for (const call of [
    { op: 'find', path: '' },
    { op: 'grants', id: 0 },
    { op: 'grants', id: '4/2' },
    { op: 'grant-set', id: 1, principal: 'a/b', role: 'read' },
    { op: 'grant-set', id: 1, principal: 'u1', role: 'use' },
    { op: 'users', id: 1, query: 'a' },
    { op: 'users', id: 1, query: 'al' },
    { op: 'nope' },
  ]) {
    assert.throws(() => folderShareRequest(call), TypeError, JSON.stringify(call))
  }
})

test('folder share client: the dialog gets its four calls over the folder routes', async () => {
  const sent = []
  const api = createFolderSharesApi({ call: async (path, opts) => { sent.push({ path, ...opts }); return { grants: [] } } })
  const client = folderShareClient(api, 7)
  await client.grants()
  await client.setGrant('u-2', 'write')
  await client.revoke('@everyone')
  await client.searchUsers('bob')
  assert.deepEqual(sent.map(s => `${s.method} ${s.path}`), [
    'GET /api/chats/files/shares/7/grants',
    'PUT /api/chats/files/shares/7/grants/u-2',
    'DELETE /api/chats/files/shares/7/grants/@everyone',
    `GET /api/chats/files/shares/7/users?q=bob&limit=${SEARCH_LIMIT}`,
  ])
  assert.ok(sent.every(s => s.headers['X-LocalAI-Confirm'] === '7'))
})
