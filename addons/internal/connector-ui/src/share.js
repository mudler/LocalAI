// SPDX-License-Identifier: MIT
// Sharing a connection from the page of its add-on. The host owns the share:
// the page asks for the ID of the binding in the sharing API
// (POST /api/chats/connectors/{id}/share) and then hands the host's Share
// dialog (sdk.ui.ShareDialog) a client of /api/shares/connection/{id}. A host
// without the dialog, or one that cannot share (local-owner mode), shows no
// Share button. No React and no network of its own: the node unit tests drive
// it directly.

// SHARE_KIND is the kind of a binding in the sharing API.
export const SHARE_KIND = 'connection'

// The roles of a connection: use is the only one, and it is never granted to
// everyone.
export const SHARE_ROLES = Object.freeze(['use'])

const RESOURCE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/
const PRINCIPAL = /^(@everyone|[A-Za-z0-9][A-Za-z0-9._:@-]{0,255})$/

// shareAvailable says whether the page offers sharing: the host draws the
// dialog and the listing says this server shares.
export function shareAvailable(sdk, listing) {
  return Boolean(listing && listing.sharing === true && sdk && sdk.ui && typeof sdk.ui.ShareDialog === 'function')
}

// sharedOfKind keeps the bindings shared with the person that are of the
// kind of this page. A row carries the name, the state and the owner only.
export function sharedOfKind(listing, kind) {
  const rows = Array.isArray(listing?.shared) ? listing.shared : []
  return rows.filter((c) => c && typeof c.id === 'string' && c.kind === kind)
}

// shareRequest returns { path, init } for one call of the dialog's client.
export function shareRequest(op, resourceId, arg = {}) {
  if (!RESOURCE.test(String(resourceId))) throw new TypeError('A resource identifier is required')
  const base = `/api/shares/${SHARE_KIND}/${encodeURIComponent(resourceId)}`
  const headers = { 'Content-Type': 'application/json', 'X-LocalAI-Confirm': resourceId }
  const principal = () => {
    if (!PRINCIPAL.test(String(arg.principal))) throw new TypeError('A person is required')
    return encodeURIComponent(arg.principal).replace(/%40/g, '@')
  }
  switch (op) {
    case 'grants':
      return { path: `${base}/grants`, init: { method: 'GET', headers } }
    case 'set': {
      if (!SHARE_ROLES.includes(arg.role)) throw new TypeError('A role of a connection is required')
      return { path: `${base}/grants/${principal()}`, init: { method: 'PUT', headers, body: JSON.stringify({ role: arg.role }) } }
    }
    case 'revoke':
      return { path: `${base}/grants/${principal()}`, init: { method: 'DELETE', headers } }
    case 'users': {
      const q = typeof arg.query === 'string' ? arg.query.trim() : ''
      if (q.length < 2 || q.length > 64) throw new TypeError('A query of 2 to 64 characters is required')
      return { path: `${base}/users?q=${encodeURIComponent(q)}&limit=8`, init: { method: 'GET', headers } }
    }
  }
  throw new TypeError('Unknown share operation')
}

// classifyShareError gives an error of the page's client the kind that the
// host's dialog reads (see describeShareError of the host).
export function classifyShareError(err) {
  const e = err || {}
  const type = e.body?.error?.type || ''
  let kind = 'error'
  if (e.name === 'AbortError') return e
  if (e.status === 401) kind = 'auth'
  else if (e.status === 403 || e.status === 404) kind = 'denied'
  else if (e.status === 409 && type === 'sharing_disabled') kind = 'sharing_disabled'
  else if (e.status === 503 && type === 'outcome_unknown') kind = 'unknown_outcome'
  else if (e.status === 503) kind = 'unavailable'
  else if (e.status === 400) kind = 'invalid'
  else if (e.status === undefined && e instanceof TypeError) kind = 'network'
  const out = new Error(e.message || kind)
  out.kind = kind
  out.status = e.status
  return out
}

// connectionShareClient is the client of the Share dialog for one binding.
// call is the page's core call: (path, init) => Promise of the JSON answer.
export function connectionShareClient(call, resourceId) {
  const run = async (op, arg) => {
    const r = shareRequest(op, resourceId, arg)
    try {
      return await call(r.path, r.init)
    } catch (err) {
      throw classifyShareError(err)
    }
  }
  return Object.freeze({
    grants: () => run('grants'),
    setGrant: (principal, role) => run('set', { principal, role }),
    revoke: (principal) => run('revoke', { principal }),
    searchUsers: (query) => run('users', { query }),
  })
}
