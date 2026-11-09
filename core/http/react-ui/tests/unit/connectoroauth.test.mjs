// The connections that log in with OAuth: the rules of the row, the result of
// a login on the page it comes back to, the administrator's form, the Tools
// menu row, and the rendered row of each state. The names are made up: the host
// knows no service.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React, { useState } from 'react'
import { renderToString } from 'react-dom/server'
import {
  canSaveClient, clientBody, clientStatus, formatWhen, isOAuthBinding, needsSecret, oauthErrorKey, oauthErrorText, oauthRowState,
  readOAuthReturn, reasonKey, loginBindings, clientMissing, reconnectConnections, REASONS, RETURN_CLASSES, returnPath, scopeChoices, staleDigest, startBody, newConnectionBody, canAddConnection,
} from '../../src/utils/connectorOAuth.js'
import { hasAddonSettings, hasOAuthLogin } from '../../src/addons/hostControls.js'

const binding = (oauth) => ({ id: 'b1', name: 'Work', kind: 'providera', addon: 'providera', auth_kind: 'oauth2', enabled: true, oauth })
const httpError = (status, body) => Object.assign(new Error(body?.error?.message || `HTTP ${status}`), { status, body })

test('oauth: a binding logs in only when the server says oauth2', () => {
  assert.equal(isOAuthBinding(binding({ state: 'connected' })), true)
  assert.equal(isOAuthBinding({ id: 'b2', has_token: true }), false)
  assert.equal(isOAuthBinding(null), false)
})

test('oauth: the row shows one state for each login state', () => {
  assert.deepEqual(oauthRowState(binding({ state: 'connected', account_label: 'ana@example.test', scopes: ['read'], expires_at: '2026-11-04T10:00:00Z' })),
    { kind: 'connected', account: 'ana@example.test', scopes: ['read'], expiresAt: '2026-11-04T10:00:00Z' })
  assert.deepEqual(oauthRowState(binding({ state: 'connected' })), { kind: 'connected', account: '', scopes: [], expiresAt: '' })
  assert.deepEqual(oauthRowState(binding({ state: 'reconnect_needed', reason: 'revoked' })), { kind: 'reconnect', reasonKey: 'revoked' })
  assert.deepEqual(oauthRowState(binding({ state: 'not_connected', has_client: true })), { kind: 'connect' })
  assert.deepEqual(oauthRowState(binding({ state: 'needs_approval' })), { kind: 'approval', admin: false, reasonKey: 'first' })
  assert.deepEqual(oauthRowState(binding({ state: 'needs_approval', reason: 'needs_approval' }), { isAdmin: true }), { kind: 'approval', admin: true, reasonKey: 'changed' })
  // A state of a newer server offers the start, which the server decides.
  assert.deepEqual(oauthRowState(binding({ state: 'later_state' })), { kind: 'connect' })
  assert.deepEqual(oauthRowState(binding(undefined)), { kind: 'connect' })
})

test('oauth: every reconnect reason has its own text, an unknown one the generic text', () => {
  for (const r of ['revoked', 'client_rejected', 'refused', 'needs_approval', 'credential_rejected', 'expired', 'write_failed', 'unreadable', 'scopes_missing', 'endpoint_changed', 'token_too_large']) assert.equal(reasonKey(r), r)
  assert.equal(reasonKey('something_new'), 'unknown')
  assert.equal(reasonKey(undefined), 'unknown')
})

test('oauth: the optional scopes to tick come from the apps list, ticked when the login has them', () => {
  assert.deepEqual(scopeChoices({ requiredScopes: ['read'], optionalScopes: ['write', 'admin'] }, ['read', 'write']),
    [{ scope: 'write', on: true }, { scope: 'admin', on: false }])
  assert.deepEqual(scopeChoices(null, ['write']), [])
  assert.deepEqual(scopeChoices({ optionalScopes: 'write' }), [])
})

test('oauth: a start sends the ticked scopes and the page to come back to', () => {
  assert.deepEqual(startBody(['write'], '/app/chats/connectors'), { scopes: ['write'], return_to: '/app/chats/connectors' })
  assert.deepEqual(startBody([], '/app/chats/connectors'), { return_to: '/app/chats/connectors' })
  assert.deepEqual(startBody(null, ''), {})
  assert.equal(returnPath('/app/chats/connectors', '?oauth=ok&tab=x'), '/app/chats/connectors?tab=x')
  assert.equal(returnPath('/app/chats/connectors', '?oauth=error&class=invalid_state'), '/app/chats/connectors')
})

test('oauth: the page reads the result of a login once and strips it from the query', () => {
  assert.equal(readOAuthReturn(''), null)
  assert.equal(readOAuthReturn('?tab=x'), null)
  assert.deepEqual(readOAuthReturn('?oauth=ok'), { outcome: 'ok', classKey: '', search: '' })
  assert.deepEqual(readOAuthReturn('?oauth=denied&tab=x'), { outcome: 'denied', classKey: '', search: 'tab=x' })
  for (const c of ['invalid_state', 'provider_error', 'exchange_failed', 'scopes_missing', 'unavailable', 'token_too_large']) {
    assert.deepEqual(readOAuthReturn(`?oauth=error&class=${c}`), { outcome: 'error', classKey: c, search: '' })
  }
  // Text the service may have put in the address is never shown: an unknown
  // class or outcome gets the generic text.
  assert.deepEqual(readOAuthReturn('?oauth=error&class=<b>hi</b>'), { outcome: 'error', classKey: 'unknown', search: '' })
  assert.deepEqual(readOAuthReturn('?oauth=whatever'), { outcome: 'error', classKey: 'unknown', search: '' })
})

test('oauth: the typed refusals have their own text', () => {
  assert.equal(oauthErrorKey(httpError(409, { error: { type: 'base_url_required' } })), 'baseUrlRequired')
  assert.equal(oauthErrorKey(httpError(409, { error: { type: 'needs_approval' } })), 'needsApproval')
  assert.equal(oauthErrorKey(httpError(409, { error: { type: 'approval_stale' } })), 'approvalStale')
  assert.equal(oauthErrorKey(httpError(503, { error: { type: 'unavailable' } })), 'unavailable')
  assert.equal(oauthErrorKey(httpError(429, { error: { type: 'too_many_logins' } })), 'tooManyLogins')
  assert.equal(oauthErrorKey(httpError(400, { error: { type: 'invalid_request' } })), '')
  assert.equal(oauthErrorKey(new Error('offline')), '')
  const t = (key) => `t:${key}`
  assert.equal(oauthErrorText(t, httpError(409, { error: { type: 'needs_approval' } }), 'oauthClient.errors'), 't:oauthClient.errors.needsApproval')
  assert.equal(oauthErrorText(t, new Error('offline'), 'oauthClient.errors'), '', 'an untyped error keeps the page fallback')
  assert.equal(staleDigest(httpError(409, { error: { type: 'approval_stale' }, current_digest: 'sha256:new' })), 'sha256:new')
  assert.equal(staleDigest(httpError(409, { error: { type: 'needs_approval' } })), '')
})

test('oauth: the administrator form says where the client stands and sends the digest it showed', () => {
  assert.equal(clientStatus({ configured: false }), 'unconfigured')
  assert.equal(clientStatus({ configured: true, approved: false }), 'changed')
  assert.equal(clientStatus({ configured: true, approved: true }), 'approved')
  assert.equal(clientStatus(null), 'unconfigured')
  assert.equal(needsSecret({ descriptor: { client_auth: 'basic' } }), true)
  assert.equal(needsSecret({ descriptor: { client_auth: 'none' } }), false)
  // The secret is write-only: an empty field keeps the stored one.
  assert.deepEqual(clientBody({ clientId: ' id-1 ', secret: '', digest: 'sha256:a' }), { client_id: 'id-1', descriptor_digest: 'sha256:a' })
  assert.deepEqual(clientBody({ clientId: 'id-1', secret: 's3', digest: 'sha256:a' }), { client_id: 'id-1', client_secret: 's3', descriptor_digest: 'sha256:a' })
  const fresh = { current_digest: 'sha256:a', has_secret: false, descriptor: { client_auth: 'post' } }
  assert.equal(canSaveClient(fresh, { clientId: 'id', secret: '' }), false, 'a confidential client needs a secret')
  assert.equal(canSaveClient(fresh, { clientId: 'id', secret: 's' }), true)
  assert.equal(canSaveClient({ ...fresh, has_secret: true }, { clientId: 'id', secret: '' }), true, 'a stored secret is kept')
  assert.equal(canSaveClient({ ...fresh, descriptor: { client_auth: 'none' } }, { clientId: 'id', secret: '' }), true, 'a public client has none')
  assert.equal(canSaveClient(fresh, { clientId: '  ', secret: 's' }), false)
  assert.equal(canSaveClient({ ...fresh, current_digest: '' }, { clientId: 'id', secret: 's' }), false)
  assert.match(formatWhen('2026-11-04T10:00:00Z', 'en'), /2026/)
  assert.equal(formatWhen('', 'en'), '')
  assert.equal(formatWhen('nope', 'en'), '')
})

test('oauth: the Settings tab draws the approval form for any add-on with a login, whatever its ID', () => {
  assert.equal(hasOAuthLogin({ id: 'providera', oauth: { requiredScopes: [], optionalScopes: [] } }), true)
  assert.equal(hasOAuthLogin({ id: 'providerb' }), false)
  assert.equal(hasAddonSettings({ id: 'providera', oauth: { requiredScopes: ['read'], optionalScopes: [] } }), true)
  assert.equal(hasAddonSettings({ id: 'plain' }), false)
})

test('oauth: the Tools menu says which connection must be connected again and links to the Connectors page', async () => {
  const { hookHarness } = await import('./chatcommands-lifecycle.mjs')
  const imports = Object.assign({}, ...await Promise.all(['chatMCP', 'chatCommands', 'toolOptions'].map((n) => import(`../../src/utils/${n}.js`))))
  const harness = hookHarness('../../src/hooks/useToolGroups.js', 'useToolGroups', {
    useTranslation: () => ({ t: (key, o) => (o?.names ? `${key}:${o.names}` : key) }), ...imports,
    reconnectConnections,
  }, (source) => source.replace(/export const/g, 'const'))
  const base = { builtins: [], servers: [], connectors: [], commands: {}, setBuiltinsOn() {}, setAddonsOn() {}, setMCPOn() {}, setConnections() {} }
  const addon = {
    id: 'providera', title: 'Provider A', tools: 2, credentialed: true, connection_by: 'chat', enabled_connections: ['b1', 'b2'], connection_state: 'reconnect_needed',
    connections: [{ id: 'b1', name: 'Work', connection_state: 'reconnect_needed' }, { id: 'b2', name: 'Home', connection_state: 'connected' }, { id: 'b3', name: 'Old', connection_state: 'reconnect_needed' }],
  }
  const row = (a) => harness.render({ ...base, addons: [a] }, {}).find((g) => g.key === 'addons').rows[0]
  const stale = row(addon)
  assert.equal(stale.warn, true)
  assert.equal(stale.note, 'tools.connection.reconnectNeeded:Work, Old')
  assert.deepEqual(stale.pick.link, { to: '/app/chats/connectors', label: 'tools.connection.reconnect' })
  const fine = row({ ...addon, connection_state: 'connected', connections: addon.connections.map((c) => ({ ...c, connection_state: 'connected' })) })
  assert.equal(fine.warn, undefined)
  assert.equal(fine.note, '')
  assert.equal(fine.pick.link, undefined)
  // A connection the chat does not use does not warn on its own.
  const unused = row({ ...addon, connection_state: 'connected', enabled_connections: ['b2'] })
  assert.equal(unused.warn, undefined)
  // A connection another person shared: its login is the owner's, so the row
  // says the owner must connect it again and offers the person no login.
  const sharedEnded = row({ ...addon, enabled_connections: ['b1'], connections: [{ id: 'b1', name: 'Team drive', shared: true, owner: 'Ana', connection_state: 'reconnect_needed' }] })
  assert.equal(sharedEnded.warn, true)
  assert.equal(sharedEnded.note, 'tools.connection.ownerReconnectNeeded:Team drive')
  assert.notEqual(sharedEnded.pick.link?.label, 'tools.connection.reconnect')
  const mixed = row({ ...addon, enabled_connections: ['b1', 'b2'], connections: [{ id: 'b1', name: 'Work', connection_state: 'reconnect_needed' }, { id: 'b2', name: 'Team drive', shared: true, connection_state: 'reconnect_needed' }] })
  assert.equal(mixed.note, 'tools.connection.reconnectNeeded:Work tools.connection.ownerReconnectNeeded:Team drive')
  assert.deepEqual(mixed.pick.link, { to: '/app/chats/connectors', label: 'tools.connection.reconnect' })
})

// The row of each state, rendered from the production component with the UI
// primitives as plain stand-ins, so the test reads what each state draws.
const source = readFileSync(new URL('../../src/components/connectors/OAuthConnection.jsx', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source.replace(/^import [^\n]*\n/gm, '').replace('export default function', 'function'), {
  compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, fileName: 'OAuthConnection.jsx',
}).outputText
const h = React.createElement
const scope = {
  React, useState,
  useTranslation: () => ({ t: (key, o) => (o ? `${key}${JSON.stringify(o)}` : key), i18n: { language: 'en' } }),
  Icon: () => null,
  Button: ({ children, to, busy, disabled, onClick, ...rest }) => h(to ? 'a' : 'button', { href: to, 'data-testid': rest['data-testid'], 'data-busy': busy ? 'true' : undefined, disabled: disabled || undefined }, children),
  Callout: ({ title, children, actions, testId, tone }) => h('div', { 'data-testid': testId, 'data-tone': tone }, title, children, actions),
  addonSettingsAddress: (id) => `/app/addons?detail=${id}&view=settings`,
  formatWhen, oauthRowState, scopeChoices,
}
const OAuthConnection = new Function(...Object.keys(scope), `${compiled}; return OAuthConnection`)(...Object.values(scope))
const draw = (props) => renderToString(h(OAuthConnection, props))

test('oauth: the row draws one state with its action', () => {
  const login = { requiredScopes: ['read'], optionalScopes: ['write'] }
  const connected = draw({ binding: binding({ state: 'connected', account_label: 'ana@example.test', scopes: ['read', 'write'], expires_at: '2026-11-04T10:00:00Z' }), login })
  assert.match(connected, /data-state="connected"/)
  assert.match(connected, /connectorsPage\.oauth\.connectedAs\{&quot;account&quot;:&quot;ana@example.test&quot;\}/)
  assert.match(connected, /connectorsPage\.oauth\.access\{&quot;scopes&quot;:&quot;read, write&quot;\}/)
  assert.match(connected, /connectorsPage\.oauth\.expires/)
  assert.match(connected, /data-testid="oauth-disconnect"/)
  assert.doesNotMatch(connected, /oauth-connect"/)

  const reconnect = draw({ binding: binding({ state: 'reconnect_needed', reason: 'expired', scopes: ['write'] }), login, starting: true })
  assert.match(reconnect, /data-tone="warn"/)
  assert.match(reconnect, /connectorsPage\.oauth\.reason\.expired/)
  assert.match(reconnect, /data-testid="oauth-connect" data-busy="true"/)
  assert.match(reconnect, /data-scope="write" checked=""/, 'a scope the login had stays ticked')

  const connect = draw({ binding: binding({ state: 'not_connected', has_client: true }), login })
  assert.match(connect, /data-state="not_connected"/)
  assert.match(connect, /connectorsPage\.oauth\.alsoAllow/)
  assert.match(connect, /data-scope="write"/)
  assert.doesNotMatch(connect, /data-scope="read"/, 'a required scope is not a choice')
  assert.match(connect, /data-testid="oauth-connect"/)

  const user = draw({ binding: binding({ state: 'needs_approval' }), login, isAdmin: false })
  assert.match(user, /connectorsPage\.oauth\.approvalUser</)
  assert.doesNotMatch(user, /oauth-approval-link/)
  assert.doesNotMatch(user, /oauth-connect"/)

  const admin = draw({ binding: binding({ state: 'needs_approval', reason: 'needs_approval' }), login, isAdmin: true })
  assert.match(admin, /connectorsPage\.oauth\.approvalAdminChanged/)
  assert.match(admin, /href="\/app\/addons\?detail=providera&amp;view=settings"/)
})

test('oauth: the body that adds a connection that logs in, and when it can be sent', () => {
  assert.deepEqual(newConnectionBody({ kind: 'providera' }, '  Work  '), { kind: 'providera', name: 'Work', auth_kind: 'oauth2' })
  assert.equal(canAddConnection('  '), false)
  assert.equal(canAddConnection('Work'), true)
  assert.equal(canAddConnection('é'.repeat(80)), true)
  assert.equal(canAddConnection('x'.repeat(81)), false, 'the server takes 80 characters')
})

// The fixed codes come from the server. The page words each one by a key, so a
// code the server adds and the page lacks would read as "unknown". This reads
// the Go sources and fails when the two lists differ.
const goSource = (path) => readFileSync(new URL(`../../../../../${path}`, import.meta.url), 'utf8')

test('oauth: the reconnect reasons of the page are the grant reasons of the server', () => {
  const server = [...goSource('core/services/studio/connectoroauth.go').matchAll(/\tReason\w+\s+GrantReason = "([a-z_]+)"/g)].map((m) => m[1])
  assert.ok(server.length > 0, 'no grant reason found in the Go source')
  assert.deepEqual([...REASONS].sort(), server.sort())
})

test('oauth: the return classes of the page are the error classes of the callback', () => {
  const login = goSource('core/services/studio/connectorlogin.go')
  const valueOf = (name) => new RegExp(`\\b${name}\\s*=\\s*"([a-z_]+)"`).exec(login)?.[1]
  const endpoint = goSource('core/http/endpoints/studio/connectoroauth.go')
  const block = /var oauthErrorClasses = map\[string\]bool\{([^}]*)\}/.exec(endpoint)?.[1] || ''
  const server = [...block.matchAll(/studiosvc\.(Login\w+):/g)].map((m) => valueOf(m[1]))
  assert.ok(server.length > 0 && server.every(Boolean), 'no error class found in the Go source')
  assert.deepEqual([...RETURN_CLASSES].sort(), server.sort())
})

test('oauth login on an add-on page: the connections of the kind that log in, and whether the client is missing', () => {
  const kind = { kind: 'providera', addon: 'providera' }
  const list = [
    binding({ state: 'not_connected', has_client: true }),
    { ...binding({ state: 'connected' }), id: 'b2', kind: 'other', addon: 'other' },
    { id: 'b3', name: 'Token', kind: 'providera', addon: 'providera', has_token: true },
    { ...binding({ state: 'connected' }), id: 'b4', addon: undefined },
  ]
  assert.deepEqual(loginBindings(list, kind).map((b) => b.id), ['b1', 'b4'])
  assert.deepEqual(loginBindings(null, kind), [])
  assert.equal(clientMissing(loginBindings(list, kind)), false)
  assert.equal(clientMissing([binding({ state: 'needs_approval', has_client: false })]), true)
  // No connection yet: the list cannot say, so the start answers needs_approval.
  assert.equal(clientMissing([]), false)
})
