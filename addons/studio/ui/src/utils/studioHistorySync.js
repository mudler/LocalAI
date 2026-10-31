// Server-side Studio history (/api/studio/history), modeled on the chat
// history sync of the host (core/http/react-ui/src/hooks/useChat.js).
//
// The server is the record when it answers: LocalAI runs with accounts (the
// store exists only then) and the user has the studio_history feature. The
// browser then keeps a cache per account, under the media key plus the
// opaque cacheKey the server hands out (the Studio is not told who is signed
// in), so two people sharing a browser never see each other's work. When the
// server does not answer (no accounts: 404; feature revoked: 403; offline)
// the Studio keeps using the plain localStorage keys, as before.
//
// Merge rules, as for chats: a server tombstone always wins; an entry on both
// sides takes the server's copy; an entry only in the browser is uploaded if
// it was never synced (no serverVersion) and dropped if it was (deleted or
// trimmed elsewhere); an entry only on the server is added.
import { MEDIA_STORAGE_KEYS } from './mediaHistoryStore.js'

export const HISTORY_ENDPOINT = '/api/studio/history'
// The account whose cache this browser showed last. Read before the server
// answers only to know that a scoped cache exists; it is not shown until the
// server confirms the same account.
export const ACCOUNT_KEY = 'localai_studio_history_account'
// Set once the entries kept under the plain keys (before server-side history)
// were imported into an account, so they go to one account only.
export const LEGACY_CLAIM_KEY = 'localai_studio_history_legacy_claimed'
const PENDING_DELETES_PREFIX = 'localai_studio_history_pending_deletes'
export const SYNC_EVENT = 'localai-studio-history-synced'
const CACHE_LIMIT = 100

// What the Studio keeps on a cached entry for the sync only; never uploaded.
const SYNC_FIELDS = ['serverVersion']

let verifiedKey = null // cacheKey confirmed by the server on this page load
let serverMode = null // null: not asked yet, true: server is the record, false: local only
// True when this browser has held a server-side history before but the server
// cannot say whose it is now (signed out, permission revoked, offline): nothing
// is shown or kept, rather than one account's cache or old history in another's
// session.
let locked = false
let syncPromise = null
let transport = null // the core-route client (path, init), set by the hook: the SDK's fetchUrl

// setStudioHistoryTransport gives the sync its fetch for core routes: the
// SDK's same-origin fetch in the Studio, a fake in tests.
export function setStudioHistoryTransport(fn) {
  transport = typeof fn === 'function' ? fn : null
}

function storage() {
  try { return globalThis.localStorage || null } catch { return null }
}

function readJSON(key, fallback) {
  const ls = storage()
  try {
    const raw = ls?.getItem(key)
    return raw ? JSON.parse(raw) : fallback
  } catch { return fallback }
}

function writeJSON(key, value) {
  try { storage()?.setItem(key, JSON.stringify(value)) } catch { /* quota or no storage */ }
}

// The localStorage key the media type's entries live under right now, or
// null while a scoped cache exists but the server has not confirmed whose it
// is (show nothing rather than someone else's work).
export function historyStorageKey(mediaType) {
  const base = MEDIA_STORAGE_KEYS[mediaType]
  if (!base) return null
  if (verifiedKey) return `${base}:${verifiedKey}`
  if (locked) return null
  if (serverMode === false) return base
  return readJSON(ACCOUNT_KEY, null) ? null : base
}

export function isServerMode() {
  return serverMode === true
}

// The entry as stored on the server: the Studio's own record minus the
// sync-only fields.
export function toServerEntry(mediaType, entry) {
  const body = { ...entry }
  for (const f of SYNC_FIELDS) delete body[f]
  const createdAt = Number(entry.createdAt) || Date.now()
  return { id: entry.id, type: mediaType, entry: body, createdAt, updatedAt: Number(entry.updatedAt) || createdAt }
}

export function fromServerEntry(e) {
  return { ...(e.entry || {}), id: e.id, createdAt: e.createdAt || e.entry?.createdAt, serverVersion: e.version }
}

// mergeHistory applies the merge rules for one media type. It returns the
// merged list (newest first) and the local entries still to upload.
export function mergeHistory(local, server, tombstones) {
  const dead = new Set((tombstones || []).map(t => t.id))
  const remote = new Map()
  for (const e of server || []) if (!dead.has(e.id)) remote.set(e.id, fromServerEntry(e))
  const merged = new Map(remote)
  const upload = []
  for (const e of local || []) {
    if (!e || !e.id || dead.has(e.id) || remote.has(e.id)) continue
    if (e.serverVersion) continue // synced before, gone from the server since
    merged.set(e.id, e)
    upload.push(e)
  }
  const list = [...merged.values()].sort((a, b) => (Number(b.createdAt) || 0) - (Number(a.createdAt) || 0))
  return { list, upload }
}

async function api(path, init) {
  if (!transport) throw new Error('studio history: no transport')
  const response = await transport(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...(init?.headers || {}) },
  })
  if (!response.ok) {
    const err = new Error(`HTTP ${response.status}`)
    err.status = response.status
    throw err
  }
  const type = response.headers.get('content-type') || ''
  return type.includes('application/json') ? response.json() : null
}

function notify() {
  try { globalThis.dispatchEvent?.(new Event(SYNC_EVENT)) } catch { /* no window */ }
}

function pendingKey() {
  return `${PENDING_DELETES_PREFIX}:${verifiedKey}`
}

// Deletes the server has not confirmed yet survive a reload and are sent again
// on the next sync, one {id, type} per entry. A Clear that could not reach the
// server is queued as the ids it removed, never as "everything": replayed
// later, it must not delete results made on another device in between.
function queueDeletes(items) {
  if (!verifiedKey || items.length === 0) return
  writeJSON(pendingKey(), [...readJSON(pendingKey(), []), ...items])
}

// flushDeletes sends the queued deletes and returns the ones the server took
// and the ones still pending. Both count as deleted in the merge that follows.
async function flushDeletes() {
  if (!verifiedKey) return { sent: [], pending: [] }
  const queue = readJSON(pendingKey(), [])
  const pending = []
  const sent = []
  for (const item of queue) {
    if (typeof item?.id !== 'string' || !item.id) continue // not a queued delete: drop it
    try {
      await api(`${HISTORY_ENDPOINT}/${encodeURIComponent(item.id)}?type=${encodeURIComponent(item.type || '')}`, { method: 'DELETE' })
      sent.push(item)
    } catch { pending.push(item) }
  }
  writeJSON(pendingKey(), pending)
  return { sent, pending }
}

// The entries this browser kept under the plain keys before the server held
// the history, for the one-time import into the first account that signs in.
function legacyEntries() {
  const out = []
  for (const [mediaType, key] of Object.entries(MEDIA_STORAGE_KEYS)) {
    for (const e of readJSON(key, [])) if (e?.id) out.push(toServerEntry(mediaType, e))
  }
  return out
}

async function claimLegacy(cacheKey) {
  if (readJSON(LEGACY_CLAIM_KEY, null)) return false
  const entries = legacyEntries()
  if (entries.length === 0) {
    writeJSON(LEGACY_CLAIM_KEY, cacheKey)
    return false
  }
  try {
    await api(`${HISTORY_ENDPOINT}/import`, { method: 'POST', body: JSON.stringify({ entries }) })
    writeJSON(LEGACY_CLAIM_KEY, cacheKey)
    // The entries now belong to that account (server and its cache). Leaving
    // them under the plain keys would show them to whoever uses this browser
    // next without server-side history.
    for (const key of Object.values(MEDIA_STORAGE_KEYS)) {
      try { storage()?.removeItem(key) } catch { /* no storage */ }
    }
    return true
  } catch {
    return false // tried again on the next load
  }
}

function localOnly(isLocked) {
  serverMode = false
  verifiedKey = null
  locked = isLocked
  notify()
  return false
}

async function runSync() {
  let list
  try {
    list = await api(HISTORY_ENDPOINT, { method: 'GET' })
  } catch (err) {
    // 404: a server without the list at all, the same as enabled false.
    // Anything else on a browser that held an account's history before: the
    // account is unknown, so show and keep nothing.
    return localOnly(err?.status !== 404 && Boolean(readJSON(ACCOUNT_KEY, null)))
  }
  // This LocalAI keeps no history (no accounts): the browser is the record.
  if (!list || list.enabled === false) return localOnly(false)
  verifiedKey = String(list.cacheKey || 'account')
  serverMode = true
  locked = false
  writeJSON(ACCOUNT_KEY, verifiedKey)
  const { sent, pending } = await flushDeletes()
  const imported = await claimLegacy(verifiedKey)
  if (sent.length > 0 || imported) {
    // The first answer predates those writes: it would bring back what the
    // queued deletes just removed.
    try { list = await api(HISTORY_ENDPOINT, { method: 'GET' }) } catch { /* keep the first answer */ }
  }

  const byType = {}
  for (const e of list.entries || []) (byType[e.type] ||= []).push(e)
  const deadByType = {}
  for (const t of list.deleted || []) (deadByType[t.type] ||= []).push(t)
  // Deletes just sent (in case the list could not be fetched again) and those
  // still waiting for the server count as deleted here.
  for (const p of [...sent, ...pending]) (deadByType[p.type] ||= []).push({ id: p.id })

  for (const mediaType of Object.keys(MEDIA_STORAGE_KEYS)) {
    const key = historyStorageKey(mediaType)
    const { list: merged, upload } = mergeHistory(readJSON(key, []), byType[mediaType], deadByType[mediaType])
    const synced = await uploadEntries(mediaType, upload)
    const final = merged.map(e => synced.get(e.id) || e).slice(0, CACHE_LIMIT)
    writeJSON(key, final)
  }
  notify()
  return true
}

async function uploadEntries(mediaType, entries) {
  const synced = new Map()
  for (const e of entries) {
    try {
      const saved = await api(`${HISTORY_ENDPOINT}/${encodeURIComponent(e.id)}`, { method: 'PUT', body: JSON.stringify(toServerEntry(mediaType, e)) })
      synced.set(e.id, { ...e, serverVersion: saved?.version || 1 })
    } catch { /* stays unsynced, uploaded on the next sync */ }
  }
  return synced
}

// syncStudioHistory asks the server once per page load (later calls share the
// answer) and resolves true when the server holds the history.
export function syncStudioHistory() {
  if (!syncPromise) {
    syncPromise = runSync()
    if (typeof globalThis.addEventListener === 'function') {
      globalThis.addEventListener('online', () => { syncPromise = runSync() })
    }
  }
  return syncPromise
}

// saveRemote uploads a new or changed entry and resolves its server version,
// or null when the server does not hold the history or the upload failed (the
// entry then stays unsynced and goes up with the next sync).
export async function saveRemote(mediaType, entry) {
  if (!(await syncStudioHistory())) return null
  try {
    const saved = await api(`${HISTORY_ENDPOINT}/${encodeURIComponent(entry.id)}`, { method: 'PUT', body: JSON.stringify(toServerEntry(mediaType, entry)) })
    return saved?.version || 1
  } catch (err) {
    return err?.status === 410 ? -1 : null // -1: deleted on another browser
  }
}

export async function deleteRemote(mediaType, id) {
  if (!(await syncStudioHistory())) return
  try {
    await api(`${HISTORY_ENDPOINT}/${encodeURIComponent(id)}?type=${encodeURIComponent(mediaType)}`, { method: 'DELETE' })
  } catch {
    queueDeletes([{ id, type: mediaType }])
  }
}

// clearRemote clears one workspace, or all of them without a type. `removed`
// is what the browser showed, [{id, type}]: when the server cannot be reached,
// those entries (and only those) are queued for deletion.
export async function clearRemote(mediaType, removed) {
  if (!(await syncStudioHistory())) return
  const path = mediaType ? `${HISTORY_ENDPOINT}?type=${encodeURIComponent(mediaType)}` : HISTORY_ENDPOINT
  try {
    await api(path, { method: 'DELETE' })
  } catch {
    queueDeletes((removed || []).filter(r => r?.id))
  }
}

// For tests: forget what this page load learnt from the server.
export function resetStudioHistorySync() {
  verifiedKey = null
  serverMode = null
  locked = false
  syncPromise = null
}
