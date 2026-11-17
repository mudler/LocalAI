// SPDX-License-Identifier: MIT
// Client of the routes with which a person shares one of their own pages,
// /api/chats/pages/{name}/shares (see the Pages guide). The server decides
// every call. This module validates the identifiers, shapes the path, sets the
// own-session confirmation header, which names the page, and reads the
// answers into the shape the share dialog draws:
//
//   grants(name)   -> { grants: [{ userId, displayName, role }], allowed }
//   searchUsers    -> { users: [{ id, displayName }] }
//
// `allowed` is how many people allowed the version of the page that is
// current, or null when the server does not say.
//
// It takes the add-on client's `call`, so an answer is classified like a
// grant call of the other sharing routes and the share dialog reads one error
// shape. No React and no network of its own: the node unit tests drive it.

import { SEARCH_MAX, SEARCH_MIN } from '../components/share/shareModel.js'

const BASE = '/api/chats/pages'
const NAME = /^[a-z0-9][a-z0-9-]{0,63}$/
const PRINCIPAL = /^(@everyone|[A-Za-z0-9][A-Za-z0-9._:@-]{0,255})$/
export const PAGE_ROLES = Object.freeze(['view', 'use', 'edit'])
const ROLES = new Set(PAGE_ROLES)

const need = (ok, what) => { if (!ok) throw new TypeError(what) }
const segment = (s) => encodeURIComponent(s).replace(/%3A/gi, ':').replace(/%40/g, '@')

// pageShareRequest validates one call and returns { path, method, headers, body }.
export function pageShareRequest(call) {
  const { op } = call || {}
  const name = String(call?.name ?? '')
  need(NAME.test(name), 'A page name is required')
  const confirm = { 'X-LocalAI-Confirm': name }
  const at = `${BASE}/${encodeURIComponent(name)}/shares`
  const principal = () => { need(PRINCIPAL.test(String(call.principal)), 'A person or @everyone is required'); return segment(call.principal) }
  switch (op) {
    case 'grants':
      return { method: 'GET', path: at, headers: confirm }
    case 'grant-set':
      need(PRINCIPAL.test(String(call.principal)), 'A person or @everyone is required')
      need(ROLES.has(call.role), 'The role is view, use or edit')
      need(call.principal !== '@everyone' || call.role !== 'edit', 'Everyone cannot edit')
      return { method: 'PUT', path: at, headers: confirm, body: { principal: call.principal, role: call.role } }
    case 'grant-revoke':
      return { method: 'DELETE', path: `${at}/${principal()}`, headers: confirm }
    case 'users': {
      const q = typeof call.query === 'string' ? call.query.trim() : ''
      need(q.length >= SEARCH_MIN && q.length <= SEARCH_MAX, `A query of ${SEARCH_MIN} to ${SEARCH_MAX} characters is required`)
      const limit = Number.isInteger(call.limit) && call.limit > 0 ? `&limit=${Math.min(call.limit, 20)}` : ''
      // The picker has no page in its path: the server expects the word "pages".
      return { method: 'GET', path: `${BASE}/shares/search?q=${encodeURIComponent(q)}${limit}`, headers: { 'X-LocalAI-Confirm': 'pages' } }
    }
    default:
      throw new TypeError('Unknown page share operation')
  }
}

// readGrants reads the answer of the grants route, and of every change (they
// have one shape): { name, enabled, hash, allowedCount, grants: [{ userId,
// displayName, role, consent }] }. `consent` is none, allowed or changed, and
// is absent for everyone. The dialog gets { grants, allowed, enabled, hash }:
// `allowed` is how many people allowed the current version, null when the
// server does not say. An answer without a grants list is returned as it came,
// so the dialog reads again.
const CONSENTS = new Set(['none', 'allowed', 'changed'])
export function readGrants(raw) {
  if (!raw || !Array.isArray(raw.grants)) return raw
  const grants = raw.grants
    .filter(g => g && typeof g.userId === 'string' && g.userId !== '')
    .map(g => ({
      userId: g.userId,
      displayName: typeof g.displayName === 'string' ? g.displayName : '',
      role: ROLES.has(g.role) ? g.role : 'view',
      consent: CONSENTS.has(g.consent) ? g.consent : '',
    }))
  return {
    ...raw,
    grants,
    allowed: Number.isFinite(raw.allowedCount) ? raw.allowedCount : null,
    enabled: raw.enabled !== false,
    hash: typeof raw.hash === 'string' ? raw.hash : '',
  }
}

export function readUsers(raw) {
  if (!raw || !Array.isArray(raw.users)) return raw
  return { ...raw, users: raw.users.filter(u => u && typeof u.id === 'string' && u.id !== '').map(u => ({ id: u.id, displayName: typeof u.displayName === 'string' ? u.displayName : '' })) }
}

// createPageSharesApi binds the requests to a client with `call(path, {
// method, body, headers, signal })`, normally the add-on client.
export function createPageSharesApi(client) {
  const send = async (spec, signal) => {
    const r = pageShareRequest(spec)
    return client.call(r.path, { method: r.method, body: r.body, headers: r.headers, signal })
  }
  return {
    grants: async (name, options) => readGrants(await send({ op: 'grants', name }, options?.signal)),
    setGrant: async (name, principal, role, options) => readGrants(await send({ op: 'grant-set', name, principal, role }, options?.signal)),
    revokeGrant: async (name, principal, options) => readGrants(await send({ op: 'grant-revoke', name, principal }, options?.signal)),
    searchUsers: async (name, query, options) => readUsers(await send({ op: 'users', name, query, limit: options?.limit }, options?.signal)),
  }
}
