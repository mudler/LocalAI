// SPDX-License-Identifier: MIT
// The share dialog's pure rules, its two clients, the request shaping of
// /api/shares, the SDK binding for add-ons and the first render of the dialog
// and of the sharing line of a list row.
import test from 'node:test'
import { connectionShareStart } from '../../src/components/share/connectionShare.js'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { renderToString } from 'react-dom/server'
import { createSharesApi, shareRequest } from '../../src/utils/sharesApi.js'
import { addonShareClient, kindShareClient, SEARCH_LIMIT } from '../../src/components/share/shareClients.js'
import * as model from '../../src/components/share/shareModel.js'
import { createHostSDK } from '../../src/addons/sdk.js'

const {
  EVERYONE, describeShareError, splitGrants, freshUsers, searchText, cleanRoles, shareRoles,
  cleanReferences, referencesReady, referenceChoices, shareLine, roleTone,
} = model

test('share requests: the path, the method and the confirmation header come from the call', () => {
  const id = 'r0123456789abcdef0123456789abcde'
  assert.deepEqual(shareRequest({ op: 'list', kind: 'example:note' }), { method: 'GET', path: '/api/shares/example:note', headers: {} })
  assert.deepEqual(shareRequest({ op: 'grants', kind: 'example:note', resourceId: id }),
    { method: 'GET', path: `/api/shares/example:note/${id}/grants`, headers: { 'X-LocalAI-Confirm': id } })
  assert.deepEqual(shareRequest({ op: 'grant-set', kind: 'example:note', resourceId: id, principal: '@everyone', role: 'use', operationId: 'share-1' }),
    { method: 'PUT', path: `/api/shares/example:note/${id}/grants/@everyone`, headers: { 'X-LocalAI-Confirm': id }, body: { role: 'use', operationId: 'share-1' } })
  assert.deepEqual(shareRequest({ op: 'grant-revoke', kind: 'example:note', resourceId: id, principal: 'u-1', operationId: 'share-2' }),
    { method: 'DELETE', path: `/api/shares/example:note/${id}/grants/u-1?operationId=share-2`, headers: { 'X-LocalAI-Confirm': id } })
  assert.equal(shareRequest({ op: 'users', kind: 'agent', resourceId: id, query: '  ali ', limit: 50 }).path, `/api/shares/agent/${id}/users?q=ali&limit=20`)
})

test('share requests: a malformed kind, resource, person, role or query is refused before it is sent', () => {
  const id = 'r1'
  for (const call of [
    { op: 'list', kind: 'Bad Kind' },
    { op: 'list', kind: 'a/../b' },
    { op: 'grants', kind: 'agent', resourceId: '../x' },
    { op: 'grant-set', kind: 'agent', resourceId: id, principal: 'a/b', role: 'use' },
    { op: 'grant-set', kind: 'agent', resourceId: id, principal: 'u1', role: 'owner' },
    { op: 'grant-set', kind: 'agent', resourceId: id, principal: 'u1', role: '' },
    { op: 'users', kind: 'agent', resourceId: id, query: 'a' },
    // The picker of a kind searches every account: three characters at least.
    { op: 'users', kind: 'agent', resourceId: id, query: 'al' },
    { op: 'users', kind: 'agent', resourceId: id, query: 'x'.repeat(65) },
    { op: 'other', kind: 'agent', resourceId: id },
  ]) assert.throws(() => shareRequest(call), TypeError, JSON.stringify(call))
})

test('shares api: every change carries an operation identifier and goes through the given client', async () => {
  const sent = []
  const api = createSharesApi({ call: async (path, opts) => { sent.push([path, opts]); return { grants: [] } } })
  await api.setGrant('agent', 'r1', 'u1', 'use')
  await api.revokeGrant('agent', 'r1', 'u1', { operationId: 'share-x' })
  await api.searchUsers('agent', 'r1', 'bob', { limit: 5 })
  assert.equal(sent[0][1].method, 'PUT')
  assert.match(sent[0][1].body.operationId, /^share-/)
  assert.equal(sent[0][1].headers['X-LocalAI-Confirm'], 'r1')
  assert.equal(sent[1][0], '/api/shares/agent/r1/grants/u1?operationId=share-x')
  assert.equal(sent[2][0], '/api/shares/agent/r1/users?q=bob&limit=5')
})

test('share clients: the generic and the add-on client give the dialog the same four calls', async () => {
  const log = []
  const opts = []
  const rec = (name) => async (...args) => {
    log.push([name, ...args.filter(a => typeof a === 'string')])
    opts.push(args.find(a => a && typeof a === 'object'))
    return { ok: name }
  }
  const kind = kindShareClient({ grants: rec('grants'), setGrant: rec('set'), revokeGrant: rec('revoke'), searchUsers: rec('users') }, 'agent', 'r1')
  const addon = addonShareClient({ grants: rec('grants'), setGrant: rec('set'), revokeGrant: rec('revoke'), searchUsers: rec('users') }, 'r2')
  for (const c of [kind, addon]) {
    assert.deepEqual(Object.keys(c).sort(), ['grants', 'revoke', 'searchUsers', 'setGrant'])
    await c.grants()
    await c.setGrant('u1', 'use')
    await c.revoke('u1')
    await c.searchUsers('bo')
  }
  assert.deepEqual(log, [
    ['grants', 'agent', 'r1'], ['set', 'agent', 'r1', 'u1', 'use'], ['revoke', 'agent', 'r1', 'u1'], ['users', 'agent', 'r1', 'bo'],
    ['grants', 'r2'], ['set', 'r2', 'u1', 'use'], ['revoke', 'r2', 'u1'], ['users', 'r2', 'bo'],
  ])
  assert.equal(opts[3].limit, SEARCH_LIMIT)
  assert.equal(opts[7].limit, SEARCH_LIMIT)
})

test('share sdk: an add-on gets the dialog, the badges and a client bound to its own resource', async () => {
  const C = () => null
  const calls = []
  const controller = new AbortController()
  const sdk = createHostSDK({
    addonId: 'tables', generation: 3, signal: controller.signal,
    host: {
      React, Icon: () => null, components: { ShareDialog: C, RoleBadge: C, SharedByBadge: C },
      api: { resource: async (body) => { calls.push(body); return { grants: [] } } },
    },
  })
  for (const n of ['ShareDialog', 'RoleBadge', 'SharedByBadge']) assert.equal(typeof sdk.ui[n], 'function', n)
  assert.ok(Object.isFrozen(sdk.resources))
  const client = sdk.resources.shareClient('r0123456789abcdef0123456789abcde')
  await client.grants()
  await client.setGrant('u1', 'editor')
  await client.revoke('u1')
  await client.searchUsers('bo')
  assert.deepEqual(calls.map(c => [c.op, c.addonId, c.generation, c.resourceId]), [
    ['grants', 'tables', 3, 'r0123456789abcdef0123456789abcde'], ['grant-set', 'tables', 3, 'r0123456789abcdef0123456789abcde'],
    ['grant-revoke', 'tables', 3, 'r0123456789abcdef0123456789abcde'], ['users', 'tables', 3, 'r0123456789abcdef0123456789abcde'],
  ])
  // Add-on resources have no grant to everyone: the host refuses the call
  // before it is sent.
  await assert.rejects(client.setGrant(EVERYONE, 'viewer'), TypeError)
  controller.abort('done')
  await assert.rejects(client.grants(), { code: 'withdrawn' })
})

test('share model: errors say what happened and which ones end sharing here', () => {
  assert.deepEqual(describeShareError({ kind: 'denied' }), { kind: 'denied', key: 'share.error.denied', retry: false, lost: true })
  assert.equal(describeShareError({ kind: 'auth' }).lost, true)
  assert.equal(describeShareError({ code: 'withdrawn' }).lost, true)
  assert.equal(describeShareError({ name: 'AbortError' }).kind, 'withdrawn')
  assert.equal(describeShareError({ kind: 'sharing_disabled' }).key, 'share.disabled.text')
  assert.deepEqual(describeShareError({ kind: 'unknown_outcome' }).retry, true)
  assert.equal(describeShareError({ kind: 'unavailable' }).retry, true)
  assert.equal(describeShareError({ kind: 'busy' }).kind, 'unavailable')
  assert.equal(describeShareError({ kind: 'network' }).retry, true)
  assert.equal(describeShareError({ kind: 'invalid' }).retry, false)
  assert.equal(describeShareError(null).key, 'share.error.generic')
})

test('share model: grants, search results, roles and references are cleaned for the dialog', () => {
  const g = splitGrants({ grants: [{ userId: 'u1', role: 'use' }, { userId: EVERYONE, role: 'use' }, null, { userId: 'u2', role: 'manage' }] })
  assert.equal(g.everyone.userId, EVERYONE)
  assert.deepEqual(g.people.map(x => x.userId), ['u1', 'u2'])
  assert.deepEqual(splitGrants(null), { everyone: null, people: [] })
  // Only an identifier and a name are kept, and people with a grant drop out.
  assert.deepEqual(freshUsers({ users: [{ id: 'u1', displayName: 'A' }, { id: 'u3', displayName: 'C', email: 'c@x' }, { id: EVERYONE }] }, g.people),
    [{ id: 'u3', displayName: 'C' }])
  assert.equal(searchText(' a '), '')
  assert.equal(searchText(' al '), '')
  assert.equal(searchText(' ali '), 'ali')
  assert.equal(searchText('x'.repeat(65)), '')
  assert.deepEqual(cleanRoles(['use', { id: 'manage', label: 'Manage', help: 'h' }, 'owner', 'use', {}]),
    [{ id: 'use', label: 'use', help: '' }, { id: 'manage', label: 'Manage', help: 'h' }])
  assert.deepEqual(shareRoles(['use', 'custom'], (k) => k).map(r => [r.id, r.label]), [['use', 'share.roles.use'], ['custom', 'custom']])
  const refs = [{ id: 'c1', label: 'Docs', kind: 'collection' }, { id: 'k1', label: 'Slack', kind: 'connection', options: ['use', 'none', 'admin'] }, { label: 'no id' }]
  assert.deepEqual(cleanReferences(refs).map(r => [r.id, r.options]), [['c1', ['view', 'use', 'none']], ['k1', ['use', 'none']]])
  assert.equal(referencesReady(refs, {}), false)
  assert.equal(referencesReady(refs, { c1: 'view', k1: 'view' }), false)
  assert.equal(referencesReady(refs, { c1: 'view', k1: 'none' }), true)
  assert.equal(referencesReady([], {}), true)
  assert.deepEqual(referenceChoices(refs, { c1: 'view', k1: 'none', other: 'use' }), { c1: 'view', k1: 'none' })
  // A row the owner cannot share is shown read-only and asks nothing.
  const withOwn = [...refs.slice(0, 2), { id: 'f1', label: 'Projects', kind: 'Folder', readOnly: true, note: 'needs their own' }]
  assert.deepEqual(cleanReferences(withOwn).map(r => [r.id, r.readOnly, r.options]), [['c1', false, ['view', 'use', 'none']], ['k1', false, ['use', 'none']], ['f1', true, []]])
  assert.equal(cleanReferences(withOwn)[2].note, 'needs their own')
  assert.equal(referencesReady(withOwn, { c1: 'view', k1: 'none' }), true)
  assert.deepEqual(referenceChoices(withOwn, { c1: 'view', k1: 'none', f1: 'use' }), { c1: 'view', k1: 'none' })
})

test('share model: a list row says who shared it, with everyone, with how many, or that it is private', () => {
  assert.deepEqual(shareLine({ role: 'owner', sharedWith: 2 }), { key: 'share.line.sharedWith', vars: { count: 2 } })
  assert.deepEqual(shareLine({ role: 'owner' }), { key: 'share.line.private', vars: {} })
  assert.deepEqual(shareLine({ role: 'use', owner: 'Alice' }), { key: 'share.line.sharedBy', vars: { name: 'Alice' } })
  assert.deepEqual(shareLine({ role: 'use', owner: 'Alice', everyone: true }), { key: 'share.line.sharedEveryone', vars: { name: 'Alice' } })
  assert.equal(shareLine({ role: 'use' }), null)
  assert.equal(roleTone('owner'), 'accent')
  assert.equal(roleTone('manage'), 'idle')
})

// Compile a component file with its imports replaced by the given scope.
function compile(path, exportName, scope) {
  const source = readFileSync(new URL(path, import.meta.url), 'utf8')
  const body = source
    .replace(/^import[\s\S]*?from\s+'[^']+'\n/gm, '')
    .replace(/^import\s+'[^']+'\n/gm, '')
    .replace(/^export default function/m, 'function')
    .replace(/^export function/gm, 'function')
  const out = ts.transpileModule(body, {
    compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, fileName: path,
  }).outputText
  return new Function(...Object.keys(scope), `${out}; return { ${exportName} }`)(...Object.values(scope))
}

const en = JSON.parse(readFileSync(new URL('../../public/locales/en/common.json', import.meta.url), 'utf8'))
const translate = (key, vars) => {
  let text = key.split('.').reduce((o, k) => (o && typeof o === 'object' ? o[k] : undefined), en)
  if (typeof text !== 'string') return key
  for (const [k, v] of Object.entries(vars || {})) text = text.replaceAll(`{{${k}}}`, String(v))
  return text
}
const useTranslation = () => ({ t: translate })
const tag = (name) => ({ children, testId, ...rest }) => React.createElement(name, { 'data-testid': testId, 'data-props': Object.keys(rest).sort().join(',') }, children)

// The pattern for a list row of agents, connections or collections (the
// documented example of SharedByBadge): pass the row of the share listing.
test('share badges: a shared row shows the role and "Shared by"; an owned row shows how many', () => {
  const { RoleBadge, SharedByBadge } = compile('../../src/components/share/ShareBadges.jsx', 'RoleBadge, SharedByBadge', {
    React, useTranslation, StatusChip: tag('span'), ...model,
  })
  const shared = renderToString(React.createElement(SharedByBadge, { entry: { id: 'r1', role: 'use', owner: 'Alice' } }))
  assert.match(shared, /data-role="use"/)
  assert.match(shared, /Can use/)
  assert.match(shared, /Shared by Alice/)
  const everyone = renderToString(React.createElement(SharedByBadge, { entry: { id: 'r1', role: 'manage', owner: 'Alice', everyone: true } }))
  assert.match(everyone, /Shared with everyone by Alice/)
  const owned = renderToString(React.createElement(SharedByBadge, { entry: { id: 'r2', role: 'owner', sharedWith: 3 } }))
  assert.match(owned, /Owner/)
  assert.match(owned, /Shared with 3/)
  // An add-on with roles of its own passes its own words.
  assert.match(renderToString(React.createElement(RoleBadge, { role: 'editor', label: 'Editeur' })), /Editeur/)
  assert.equal(renderToString(React.createElement(SharedByBadge, { entry: null })), '')
  assert.equal(renderToString(React.createElement(SharedByBadge, { entry: { role: 'use' }, hideRole: true })), '')
})

function dialog() {
  return compile('../../src/components/share/ShareDialog.jsx', 'ShareDialog', {
    React, useCallback, useEffect, useId, useMemo, useRef, useState, useTranslation, ...model,
    Dialog: ({ title, description, foot, children, testId }) => React.createElement('section', { 'data-testid': testId },
      React.createElement('h3', null, title), description ? React.createElement('p', null, description) : null, children, foot),
    Icon: () => null, LoadingSpinner: () => null,
    Button: ({ children, disabled, ...rest }) => React.createElement('button', { disabled, 'aria-label': rest['aria-label'] }, children),
    Callout: ({ children, testId }) => React.createElement('div', { 'data-testid': testId }, children),
    EmptyState: ({ title, body, testId }) => React.createElement('div', { 'data-testid': testId }, title, ' ', body),
  }).ShareDialog
}

test('share dialog: local-owner mode says sharing is not available and calls nothing', () => {
  const ShareDialog = dialog()
  const client = new Proxy({}, { get: () => { throw new Error('no call in local-owner mode') } })
  const html = renderToString(React.createElement(ShareDialog, { client, resource: { id: 'r1', name: 'Notes' }, sharing: false, roles: ['use'], close() {} }))
  assert.match(html, /Share &quot;Notes&quot;/)
  assert.match(html, /data-testid="share-disabled"/)
  assert.match(html, /there are no other accounts/)
  assert.doesNotMatch(html, /share-search/)
})

test('share dialog: with references, the owner must decide each one before sharing', () => {
  const ShareDialog = dialog()
  const client = { grants: () => new Promise(() => {}), searchUsers: async () => ({ users: [] }), setGrant: async () => ({}), revoke: async () => ({}) }
  const html = renderToString(React.createElement(ShareDialog, {
    client, resource: { id: 'r1', name: 'Helper' }, roles: [{ id: 'use', label: 'Can use' }, { id: 'manage', label: 'Can manage' }],
    references: [{ id: 'c1', label: 'Docs', kind: 'Collection' }, { id: 'k1', label: 'Team chat', kind: 'Connection', options: ['use', 'none'] }],
    close() {},
  }))
  assert.match(html, /Before you share/)
  assert.equal((html.match(/data-testid="share-ref"/g) || []).length, 2)
  assert.match(html, /aria-label="Access to Docs"/)
  assert.match(html, /data-testid="share-refs-pending"/)
  assert.match(html, /aria-label="Role for people you add"/)
  assert.match(html, /aria-label="Find a person by name"|Find a person by name/)
})

test('share dialog: a reference that cannot be shared is a read-only row', () => {
  const ShareDialog = dialog()
  const client = { grants: () => new Promise(() => {}), searchUsers: async () => ({ users: [] }), setGrant: async () => ({}), revoke: async () => ({}) }
  const html = renderToString(React.createElement(ShareDialog, {
    client, resource: { id: 'r1', name: 'Helper' }, roles: [{ id: 'use', label: 'Can use' }],
    references: [{ id: 'f1', label: 'Projects', kind: 'Folder', readOnly: true, note: 'They need access of their own' }],
    close() {},
  }))
  assert.match(html, /data-testid="share-ref-own"/)
  assert.match(html, /They need access of their own/)
  assert.doesNotMatch(html, /aria-label="Access to Projects"/)
  assert.doesNotMatch(html, /data-testid="share-refs-pending"/)
})

test('connection share: the host asks for the ID of the binding itself and refuses a bad binding', async () => {
  const asked = []
  const api = { share: async (id) => { asked.push(id); return { resource_id: 'r0123456789abcdef0123456789abcde', shared_with: 0 } } }
  assert.equal(await connectionShareStart(api, 'b-1'), 'r0123456789abcdef0123456789abcde')
  assert.deepEqual(asked, ['b-1'])
  for (const bad of ['', '../x', 'a/b', null, 7]) {
    await assert.rejects(() => connectionShareStart(api, bad), TypeError, String(bad))
  }
  assert.deepEqual(asked, ['b-1'])
  await assert.rejects(() => connectionShareStart({ share: async () => ({}) }, 'b-2'), /cannot be shared/)
})
