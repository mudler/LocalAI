// Pure state helpers for the reusable catalog views. No React, no DOM and no
// network: the node unit tests import this file directly.

// The page size of a catalog listing. The owner caps a page at 100.
export const PAGE_SIZE = 24

const SOURCE_ID = /^[a-z][a-z0-9-]{0,63}$/
// A credential reference is the name of a credential the owner configured.
const REFERENCE = /^[a-z][a-z0-9-]{0,63}$/

// A query is what the URL carries: free text, one library and a 1-based page.
export function normalizeQuery(raw = {}) {
  const page = Number.parseInt(raw.page, 10)
  return {
    text: typeof raw.text === 'string' ? raw.text.trim().slice(0, 256) : '',
    sourceId: typeof raw.sourceId === 'string' && SOURCE_ID.test(raw.sourceId) ? raw.sourceId : '',
    page: Number.isFinite(page) && page > 1 ? page : 1,
  }
}

export function queryToOffset(query, pageSize = PAGE_SIZE) {
  return (normalizeQuery(query).page - 1) * pageSize
}

export function totalPages(total, pageSize = PAGE_SIZE) {
  return Math.max(1, Math.ceil((Number(total) || 0) / pageSize))
}

// Two queries that ask the same question have the same key, so a re-render
// with an equal query does not search again.
export function queryKey(query) {
  const q = normalizeQuery(query)
  return JSON.stringify([q.text, q.sourceId, q.page])
}

// The URL form of a query: empty values are left out.
export function queryToParams(query) {
  const q = normalizeQuery(query)
  const out = {}
  if (q.text) out.q = q.text
  if (q.sourceId) out.source = q.sourceId
  if (q.page > 1) out.page = String(q.page)
  return out
}

export function paramsToQuery(get) {
  return normalizeQuery({ text: get('q') || '', sourceId: get('source') || '', page: get('page') || '1' })
}

// ---- Operation identity ----------------------------------------------------

// One fresh identifier per user action. The caller keeps it across retries of
// the same action and drops it only when the action reached a final answer.
export function newOperationId(cryptoLike = globalThis.crypto) {
  if (cryptoLike && typeof cryptoLike.randomUUID === 'function') return cryptoLike.randomUUID()
  const bytes = new Uint8Array(16)
  if (cryptoLike && typeof cryptoLike.getRandomValues === 'function') cryptoLike.getRandomValues(bytes)
  else for (let i = 0; i < bytes.length; i += 1) bytes[i] = Math.floor(Math.random() * 256)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

// Remembers the operation ID of each pending user action. The same action key
// gives the same ID until `done` is called, so a retry replays the operation
// and never becomes a second one.
export function createOperationBook(makeId = newOperationId) {
  const ids = new Map()
  return {
    idFor(key) {
      if (!ids.has(key)) ids.set(key, makeId())
      return ids.get(key)
    },
    has: key => ids.has(key),
    done: key => { ids.delete(key) },
    size: () => ids.size,
  }
}

// ---- Errors ----------------------------------------------------------------

// The server is the authority. A response is one of a few kinds and the page
// treats each kind the same everywhere:
//   auth             401, sign in again
//   denied           403, the server refused; nothing was done
//   invalid          400, the request was malformed
//   unknown_outcome  503 with type outcome_unknown; poll the operation, never
//                    submit a new one
//   unavailable      any other 503 or an unexpected status
export function classifyResponse(status, body) {
  const err = body && typeof body === 'object' ? body.error : null
  const type = err && typeof err === 'object' ? err.type : ''
  const message = err && typeof err === 'object' && typeof err.message === 'string' ? err.message : ''
  if (status === 401) return { kind: 'auth', status, message }
  if (status === 403) return { kind: 'denied', status, message, type, blockedBy: err?.blockedBy || '' }
  if (status === 400) return { kind: 'invalid', status, message, type }
  // The typed answers of the resource management routes.
  if (status === 409 && type === 'stale') return { kind: 'stale', status, message, type }
  if (status === 409 && type === 'sharing_disabled') return { kind: 'sharing_disabled', status, message, type }
  if (status === 404 && type === 'not_selected') return { kind: 'not_selected', status, message, type }
  if (status === 503 && type === 'outcome_unknown') {
    return { kind: 'unknown_outcome', status, message, operationId: typeof err.operationId === 'string' ? err.operationId : '' }
  }
  return { kind: 'unavailable', status, message }
}

// ---- Polling ---------------------------------------------------------------

export const POLL_BASE_MS = 5000
export const POLL_MAX_MS = 60000
export const POLL_HIDDEN_MS = 30000

// The delay before the next refresh: a steady cadence, longer in a hidden tab
// and doubling with every failure up to a bound.
export function pollDelay(failures = 0, hidden = false) {
  const base = hidden ? POLL_HIDDEN_MS : POLL_BASE_MS
  if (!failures) return base
  return Math.min(POLL_MAX_MS, base * 2 ** Math.min(failures, 8))
}

// ---- Libraries -------------------------------------------------------------

export function slugify(text) {
  const slug = String(text || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64)
  return /^[a-z]/.test(slug) ? slug : (slug ? `lib-${slug}`.slice(0, 64) : '')
}

// What the editor can know before it asks the server. The server decides.
// Returns a map from field to an error code; an empty map means "send it".
export function validateSourceDraft(draft = {}) {
  const errors = {}
  if (!SOURCE_ID.test(draft.id || '')) errors.id = 'id'
  const name = String(draft.name || '').trim()
  if (!name || name.length > 256) errors.name = 'name'
  let url = null
  try { url = new URL(String(draft.url || '').trim()) } catch { url = null }
  if (!url || (url.protocol !== 'https:' && url.protocol !== 'http:')) errors.url = 'url'
  else if (url.username || url.password) errors.url = 'urlCredentials'
  else if (url.search || url.hash) errors.url = 'urlQuery'
  const ref = String(draft.authReference || '').trim()
  if (ref && !REFERENCE.test(ref)) errors.authReference = 'reference'
  if (draft.requireSignature) {
    if (!String(draft.issuer || '').trim()) errors.issuer = 'required'
    if (!String(draft.identity || '').trim()) errors.identity = 'required'
  }
  return errors
}

// The wire form of a library. The editor sends a reference by name and never a
// secret; a field the owner did not configure is dropped here.
export function draftToSource(draft) {
  const source = {
    id: draft.id,
    name: String(draft.name).trim(),
    url: String(draft.url).trim(),
    enabled: Boolean(draft.enabled),
    policy: { requireSignature: Boolean(draft.requireSignature) },
  }
  if (draft.requireSignature) {
    source.policy.issuer = String(draft.issuer).trim()
    source.policy.identity = String(draft.identity).trim()
  }
  const ref = String(draft.authReference || '').trim()
  if (ref) source.authReference = ref
  // Only an existing opt-in is carried; the editor cannot create one.
  if (draft.allowLoopbackHTTP) source.allowLoopbackHTTP = true
  return source
}

export function sourceToDraft(source = {}) {
  return {
    id: source.id || '',
    name: source.name || '',
    url: source.url || '',
    enabled: source.enabled !== false,
    requireSignature: Boolean(source.policy?.requireSignature),
    issuer: source.policy?.issuer || '',
    identity: source.policy?.identity || '',
    authReference: source.authReference || '',
    allowLoopbackHTTP: Boolean(source.allowLoopbackHTTP),
  }
}
