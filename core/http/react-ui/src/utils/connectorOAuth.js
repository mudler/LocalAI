// The rules of the connections that log in with OAuth, as pure functions (no
// React), so a node test drives them. Every word the page shows comes from the
// locale by a fixed key, or from the server (the add-on's scopes, the account
// label); nothing here knows a service.

// The fixed reasons of a reconnect. An unknown one (a newer server) gets the
// generic text.
export const REASONS = new Set(['revoked', 'client_rejected', 'refused', 'needs_approval', 'credential_rejected', 'expired', 'write_failed', 'unreadable', 'scopes_missing', 'endpoint_changed', 'token_too_large'])

// The classes of oauth=error&class=<c> on the page the login returns to.
export const RETURN_CLASSES = new Set(['invalid_state', 'provider_error', 'exchange_failed', 'scopes_missing', 'unavailable', 'token_too_large'])

// Whether a binding logs in with OAuth. A token binding has no auth_kind.
export function isOAuthBinding(c) {
  return c?.auth_kind === 'oauth2'
}

// What the row of an oauth2 binding shows: one state.
//   { kind: 'connected', account, scopes, expiresAt }
//   { kind: 'reconnect', reasonKey }
//   { kind: 'connect' }
//   { kind: 'approval', admin, reasonKey }   an administrator gets a link
// A state the page does not know is shown as not connected, which offers the
// same start the server refuses or accepts.
export function oauthRowState(c, { isAdmin = false } = {}) {
  const o = c?.oauth || {}
  switch (o.state) {
    case 'connected':
      return { kind: 'connected', account: o.account_label || '', scopes: o.scopes || [], expiresAt: o.expires_at || '' }
    case 'reconnect_needed':
      return { kind: 'reconnect', reasonKey: reasonKey(o.reason) }
    case 'needs_approval':
      return { kind: 'approval', admin: !!isAdmin, reasonKey: o.reason === 'needs_approval' ? 'changed' : 'first' }
    default:
      return { kind: 'connect' }
  }
}

// The locale key of a reconnect reason.
export function reasonKey(reason) {
  return REASONS.has(reason) ? reason : 'unknown'
}

// The longest name of a connection the server takes, in characters.
export const MAX_CONNECTION_NAME = 80

// Whether the name of a new connection can be sent: one to
// MAX_CONNECTION_NAME characters once trimmed.
export function canAddConnection(name) {
  const n = [...(name || '').trim()].length
  return n > 0 && n <= MAX_CONNECTION_NAME
}

// The body that adds a connection of a kind whose add-on logs in with OAuth:
// no token and no agent; the person connects it on the page afterwards.
export function newConnectionBody(kind, name) {
  return { kind: kind.kind, name: (name || '').trim(), auth_kind: 'oauth2' }
}

// The optional scopes a person can tick when they connect, from the add-on's
// login in the apps list, each ticked when the current login already has it.
export function scopeChoices(login, granted = []) {
  const optional = Array.isArray(login?.optionalScopes) ? login.optionalScopes : []
  const have = new Set(granted || [])
  return optional.map((scope) => ({ scope, on: have.has(scope) }))
}

// The body of a start: the ticked optional scopes and the page to come back to.
export function startBody(ticked, returnTo) {
  const body = {}
  const scopes = (ticked || []).filter(Boolean)
  if (scopes.length) body.scopes = scopes
  if (returnTo) body.return_to = returnTo
  return body
}

// The page a login comes back to: this page, without a result of an earlier
// login in the query. The server accepts a path under /app only.
export function returnPath(pathname, search = '') {
  const q = new URLSearchParams(search)
  q.delete('oauth')
  q.delete('class')
  const rest = q.toString()
  return `${pathname}${rest ? `?${rest}` : ''}`
}

// The result of a login on the page it came back to, read from ?oauth= and
// ?class=, and the query without them. null when the query has no result.
//   { outcome: 'ok' | 'denied' | 'error', classKey, search }
export function readOAuthReturn(search) {
  const q = new URLSearchParams(search || '')
  const outcome = q.get('oauth')
  if (!outcome) return null
  const klass = q.get('class')
  q.delete('oauth')
  q.delete('class')
  const known = outcome === 'ok' || outcome === 'denied' ? outcome : 'error'
  return {
    outcome: known,
    classKey: known === 'error' ? (RETURN_CLASSES.has(klass) ? klass : 'unknown') : '',
    search: q.toString(),
  }
}

// The locale key of an error of a start, a disconnect or a client save, or ''
// when the error is not one of the typed ones (the page then shows its text).
export function oauthErrorKey(e) {
  const type = e?.body?.error?.type
  if (e?.status === 409 && type === 'base_url_required') return 'baseUrlRequired'
  if (e?.status === 409 && type === 'needs_approval') return 'needsApproval'
  if (e?.status === 409 && type === 'approval_stale') return 'approvalStale'
  if (e?.status === 429 && type === 'too_many_logins') return 'tooManyLogins'
  if (e?.status === 503) return 'unavailable'
  return ''
}

// The text of a typed error under the locale subtree prefix (each page words
// the errors for its own reader), or '' when the error is not a typed one.
export function oauthErrorText(t, e, prefix) {
  const key = oauthErrorKey(e)
  return key ? t(`${prefix}.${key}`) : ''
}

// The current digest of a stale save (409 approval_stale), or ''.
export function staleDigest(e) {
  if (e?.status !== 409 || e?.body?.error?.type !== 'approval_stale') return ''
  return e.body.current_digest || ''
}

// Where the OAuth client of an add-on stands, for the administrator's form:
//   unconfigured  no client saved yet
//   changed       a client is saved, but the login changed since it was approved
//   approved      the saved client approves the login the add-on declares now
export function clientStatus(client) {
  if (!client?.configured) return 'unconfigured'
  return client.approved ? 'approved' : 'changed'
}

// Whether the login of the add-on needs a client secret: every client
// authentication but none (a public client with PKCE alone).
export function needsSecret(client) {
  return client?.descriptor?.client_auth !== 'none'
}

// The body of a client save. The secret is write-only: an empty field keeps
// the stored one, so it is sent only when typed. The digest is the one the
// form showed, so the save approves what the administrator saw.
export function clientBody({ clientId, secret, digest }) {
  const body = { client_id: (clientId || '').trim(), descriptor_digest: digest || '' }
  if (secret) body.client_secret = secret
  return body
}

// Whether the form can be saved: a client ID, a digest, and a secret when the
// login needs one and none is stored.
export function canSaveClient(client, { clientId, secret }) {
  if (!client?.current_digest || !(clientId || '').trim()) return false
  if (needsSecret(client) && !client.has_secret && !secret) return false
  return true
}

// A date and time in the page language, or '' for none.
export function formatWhen(value, language) {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  try {
    return new Intl.DateTimeFormat(language || undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(d)
  } catch {
    return d.toLocaleString()
  }
}

// The connections of a chat's add-on that must be connected again, from the
// add-on list of the chat (connection_state of each connection).
export function reconnectConnections(addon) {
  return (addon?.connections || []).filter((c) => c.connection_state === 'reconnect_needed')
}

// The connections of one kind that log in with OAuth, from a list of the
// person's connections. The kind is { kind, addon }; a connection that names no
// add-on (an older record) matches on the kind alone.
export function loginBindings(bindings, kind) {
  return (bindings || []).filter((b) => isOAuthBinding(b) && b.kind === kind?.kind && (!b.addon || !kind?.addon || b.addon === kind.addon))
}

// Whether the list shows that no administrator approved the add-on's client:
// a connection without a login says so (has_client false). A person with no
// connection yet cannot tell from the list; the start then answers needs_approval.
export function clientMissing(oauthBindings) {
  return (oauthBindings || []).some((b) => b?.oauth && b.oauth.has_client === false)
}
