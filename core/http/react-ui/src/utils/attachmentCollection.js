// Optional copy of chat attachments into a knowledge collection.
//
// The chat inlines attachments into the completion request in the browser, so
// the original file never reaches the server. When the user picks a
// collection, each original is also uploaded to
// POST /api/agents/collections/:name/upload. The chat request itself does not
// wait for, or depend on, these uploads.

export const ATTACHMENT_COLLECTION_KEY = 'localai_chat_attachment_collection'

function defaultStorage() {
  try { return globalThis.localStorage || null } catch { return null }
}

export function loadAttachmentCollection(storage = defaultStorage()) {
  try { return storage?.getItem(ATTACHMENT_COLLECTION_KEY) || '' } catch { return '' }
}

export function saveAttachmentCollection(name, storage = defaultStorage()) {
  try {
    if (name) storage?.setItem(ATTACHMENT_COLLECTION_KEY, name)
    else storage?.removeItem(ATTACHMENT_COLLECTION_KEY)
  } catch { /* private mode or blocked storage: the choice lasts for the session */ }
}

// The rules the navigation uses for the Memory (collections) item, except that
// the agent pool must be confirmed on (`agents: true` from /api/features), not
// merely "not known to be off": this gate starts a request, so it waits for
// the answer instead of listing collections on a server without them.
export function canSaveAttachments({ features = {}, hasFeature = () => false } = {}) {
  if (features.agents !== true) return false
  if (features.collections === false) return false
  return !!hasFeature('collections')
}

// A stored choice only counts while that collection still exists, so a
// deleted or renamed collection silently falls back to "none" instead of
// producing a failed upload on every message.
export function effectiveCollection(selected, available) {
  if (!selected || !Array.isArray(available)) return ''
  return available.includes(selected) ? selected : ''
}

export function collectionNames(listResponse) {
  const list = Array.isArray(listResponse?.collections) ? listResponse.collections : []
  return list.map(c => (typeof c === 'string' ? c : c?.name)).filter(Boolean)
}

export function base64ToFile(base64, name, type) {
  const binary = atob(base64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return new File([bytes], name, { type: type || 'application/octet-stream' })
}

// The originals behind the composer entries, one per source file. An entry
// carries its original in `source`; the pages of a scanned PDF share one
// source, so the PDF is uploaded once rather than as page images. Entries
// without a source (e.g. handed over from the Home page as JSON) are rebuilt
// from their base64 payload, which holds the file's own bytes.
export function attachmentOriginals(entries = []) {
  const seen = new Set()
  const out = []
  for (const entry of entries) {
    if (!entry) continue
    if (entry.source && typeof entry.source === 'object') {
      if (seen.has(entry.source)) continue
      seen.add(entry.source)
      out.push({ name: entry.source.name || entry.name, file: entry.source })
    } else if (entry.base64) {
      out.push({ name: entry.name, file: base64ToFile(entry.base64, entry.name, entry.type) })
    }
  }
  return out
}

export const UPLOAD_UPLOADING = 'uploading'
export const UPLOAD_SAVED = 'saved'
export const UPLOAD_FAILED = 'failed'

// Uploads each original to `collection`, reporting every state change through
// onStatus(statuses) with a fresh array. Resolves with the final statuses and
// never rejects: a failed upload is a status, not an error for the caller.
// With no collection or no originals it does nothing and calls nothing.
export async function saveAttachmentsToCollection({ collection, entries, upload, onStatus = () => {}, idPrefix = 'up' }) {
  if (!collection) return []
  const originals = attachmentOriginals(entries)
  if (originals.length === 0) return []

  let statuses = originals.map((o, i) => ({
    id: `${idPrefix}-${i}`, name: o.name, collection, state: UPLOAD_UPLOADING, error: '',
  }))
  const set = (i, patch) => {
    statuses = statuses.map((s, j) => (j === i ? { ...s, ...patch } : s))
    onStatus(statuses)
  }
  onStatus(statuses)

  await Promise.all(originals.map(async (o, i) => {
    try {
      const form = new FormData()
      form.append('file', o.file, o.name)
      await upload(collection, form)
      set(i, { state: UPLOAD_SAVED })
    } catch (err) {
      set(i, { state: UPLOAD_FAILED, error: err?.message || String(err) })
    }
  }))
  return statuses
}
