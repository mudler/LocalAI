// SPDX-License-Identifier: MIT
// Client of the generic sharing routes, /api/shares/{kind}/... (see
// docs/content/features/sharing.md). They serve the resource kinds that are not
// add-ons; add-on resources keep the add-on routes (sdk.resources). The server
// decides every call: this module only validates the identifiers, shapes the
// path and sets the own-session confirmation header, which names the resource.
//
// It takes the add-on client's `call`, so an answer is classified the same way
// as an add-on grant call and the share dialog reads one error shape.
//
// No React and no network of its own: the node unit tests drive it directly.

import { SEARCH_MAX, SEARCH_MIN } from '../components/share/shareModel.js'

const KIND = /^[a-z][a-z0-9-]{0,63}(:[a-z0-9][a-z0-9-]{0,63})?$/
const RESOURCE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/
const PRINCIPAL = /^(@everyone|[A-Za-z0-9][A-Za-z0-9._:@-]{0,255})$/
const ROLE = /^[a-z][a-z0-9-]{0,63}$/

const need = (ok, what) => { if (!ok) throw new TypeError(what) }

// A path segment. ':' and '@' are valid in a segment and are kept as they are,
// so `example:note` and `@everyone` reach the router unescaped.
const segment = (s) => encodeURIComponent(s).replace(/%3A/gi, ':').replace(/%40/g, '@')

// shareRequest validates one call and returns { path, method, headers, body }.
export function shareRequest(call) {
  const { op, kind } = call || {}
  need(KIND.test(String(kind)), 'A resource kind is required')
  const base = `/api/shares/${segment(kind)}`
  if (op === 'list') return { method: 'GET', path: base, headers: {} }
  need(RESOURCE.test(String(call.resourceId)), 'A resource identifier is required')
  const r = call.resourceId
  const confirm = { 'X-LocalAI-Confirm': r }
  const at = `${base}/${segment(r)}`
  const principal = () => { need(PRINCIPAL.test(String(call.principal)), 'A person or @everyone is required'); return segment(call.principal) }
  const operation = typeof call.operationId === 'string' && call.operationId !== '' ? call.operationId : ''
  switch (op) {
    case 'grants':
      return { method: 'GET', path: `${at}/grants`, headers: confirm }
    case 'grant-set': {
      const p = principal()
      need(ROLE.test(String(call.role)) && call.role !== 'owner', 'A role the kind grants is required')
      return { method: 'PUT', path: `${at}/grants/${p}`, headers: confirm, body: operation ? { role: call.role, operationId: operation } : { role: call.role } }
    }
    case 'grant-revoke': {
      const p = principal()
      return { method: 'DELETE', path: `${at}/grants/${p}${operation ? `?operationId=${encodeURIComponent(operation)}` : ''}`, headers: confirm }
    }
    case 'users': {
      const q = typeof call.query === 'string' ? call.query.trim() : ''
      need(q.length >= SEARCH_MIN && q.length <= SEARCH_MAX, `A query of ${SEARCH_MIN} to ${SEARCH_MAX} characters is required`)
      const limit = Number.isInteger(call.limit) && call.limit > 0 ? `&limit=${Math.min(call.limit, 20)}` : ''
      return { method: 'GET', path: `${at}/users?q=${encodeURIComponent(q)}${limit}`, headers: confirm }
    }
    default:
      throw new TypeError('Unknown share operation')
  }
}

function newOperationId() {
  const c = globalThis.crypto
  const raw = c && typeof c.randomUUID === 'function' ? c.randomUUID() : Math.random().toString(16).slice(2) + Date.now().toString(16)
  return `share-${raw}`
}

// createSharesApi binds the requests to a client with `call(path, { method,
// body, headers, signal })`, normally the add-on client (utils/addonsClient).
export function createSharesApi(client) {
  const send = (spec, signal) => {
    const r = shareRequest(spec)
    return client.call(r.path, { method: r.method, body: r.body, headers: r.headers, signal })
  }
  return {
    // What the caller owns and what was shared with them, of one kind.
    list: (kind, options) => send({ op: 'list', kind }, options?.signal),
    grants: (kind, resourceId, options) => send({ op: 'grants', kind, resourceId }, options?.signal),
    // One operation identifier per change, so an outcome_unknown answer can be
    // repeated as the same call.
    setGrant: (kind, resourceId, principal, role, options) => send({ op: 'grant-set', kind, resourceId, principal, role, operationId: options?.operationId || newOperationId() }, options?.signal),
    revokeGrant: (kind, resourceId, principal, options) => send({ op: 'grant-revoke', kind, resourceId, principal, operationId: options?.operationId || newOperationId() }, options?.signal),
    searchUsers: (kind, resourceId, query, options) => send({ op: 'users', kind, resourceId, query, limit: options?.limit }, options?.signal),
  }
}
