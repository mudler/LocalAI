// SPDX-License-Identifier: MIT
// Client of the routes with which a person shares a folder of their own home,
// /api/chats/files/shares/... (see docs/content/features/chats.md). The grants
// are the workspace grants of the folder; the server decides every call. This
// module only validates the identifiers, shapes the path and sets the
// own-session confirmation header, which names the folder.
//
// It takes the add-on client's `call`, so an answer is classified like a
// grant call of the other sharing routes and the share dialog reads one error
// shape.
//
// No React and no network of its own: the node unit tests drive it directly.

import { SEARCH_MAX, SEARCH_MIN } from '../components/share/shareModel.js'

const BASE = '/api/chats/files/shares'
const ID = /^[1-9][0-9]{0,18}$/
const PRINCIPAL = /^(@everyone|[A-Za-z0-9][A-Za-z0-9._:@-]{0,255})$/
const ROLES = new Set(['read', 'write'])

const need = (ok, what) => { if (!ok) throw new TypeError(what) }
const segment = (s) => encodeURIComponent(s).replace(/%3A/gi, ':').replace(/%40/g, '@')

// folderShareRequest validates one call and returns { path, method, headers, body }.
export function folderShareRequest(call) {
  const { op } = call || {}
  if (op === 'list') return { method: 'GET', path: BASE, headers: {} }
  if (op === 'find') {
    need(typeof call.path === 'string' && call.path !== '', 'A folder path is required')
    return { method: 'POST', path: BASE, headers: {}, body: { path: call.path } }
  }
  const id = String(call?.id ?? '')
  need(ID.test(id), 'A folder identifier is required')
  const at = `${BASE}/${id}`
  const confirm = { 'X-LocalAI-Confirm': id }
  const principal = () => { need(PRINCIPAL.test(String(call.principal)), 'A person or @everyone is required'); return segment(call.principal) }
  switch (op) {
    case 'grants':
      return { method: 'GET', path: `${at}/grants`, headers: confirm }
    case 'grant-set': {
      const p = principal()
      need(ROLES.has(call.role), 'The role is read or write')
      return { method: 'PUT', path: `${at}/grants/${p}`, headers: confirm, body: { role: call.role } }
    }
    case 'grant-revoke':
      return { method: 'DELETE', path: `${at}/grants/${principal()}`, headers: confirm }
    case 'users': {
      const q = typeof call.query === 'string' ? call.query.trim() : ''
      need(q.length >= SEARCH_MIN && q.length <= SEARCH_MAX, `A query of ${SEARCH_MIN} to ${SEARCH_MAX} characters is required`)
      const limit = Number.isInteger(call.limit) && call.limit > 0 ? `&limit=${Math.min(call.limit, 20)}` : ''
      return { method: 'GET', path: `${at}/users?q=${encodeURIComponent(q)}${limit}`, headers: confirm }
    }
    default:
      throw new TypeError('Unknown folder share operation')
  }
}

// createFolderSharesApi binds the requests to a client with `call(path, {
// method, body, headers, signal })`, normally the add-on client.
export function createFolderSharesApi(client) {
  const send = (spec, signal) => {
    const r = folderShareRequest(spec)
    return client.call(r.path, { method: r.method, body: r.body, headers: r.headers, signal })
  }
  return {
    // The folders of the home that are shared, and whether sharing is on.
    list: (options) => send({ op: 'list' }, options?.signal),
    // The folder at a path of the home, with its ID for the other calls.
    find: (path, options) => send({ op: 'find', path }, options?.signal),
    grants: (id, options) => send({ op: 'grants', id }, options?.signal),
    setGrant: (id, principal, role, options) => send({ op: 'grant-set', id, principal, role }, options?.signal),
    revokeGrant: (id, principal, options) => send({ op: 'grant-revoke', id, principal }, options?.signal),
    searchUsers: (id, query, options) => send({ op: 'users', id, query, limit: options?.limit }, options?.signal),
  }
}
