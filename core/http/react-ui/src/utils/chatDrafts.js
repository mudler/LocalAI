// The unsent drafts of the chats, kept in the browser storage so that a draft
// survives a reload or a closed tab. One entry per chat id: { text, at }. Only
// the text is kept; staged files live in the page's memory (a File cannot be
// stored). The new chat, which has no id yet, is never written: a new chat
// starts empty.
//
// Each write reads the stored drafts again and changes only its own chat, so
// two tabs with different chats open do not drop each other's drafts.

const PREFIX = 'localai_chat_drafts'

// The most drafts kept; the oldest go first. Chats deleted in another tab
// leave their draft behind, and this cap is what removes it eventually.
export const MAX_DRAFTS = 50
// The longest draft kept, in characters; a longer one is cut.
export const MAX_DRAFT_CHARS = 20000

// The storage key of an account's drafts: another account in the same browser
// does not see them.
export function draftsKey(userId) {
  return userId ? `${PREFIX}:${userId}` : PREFIX
}

export function readDrafts(storage, key) {
  let raw
  try { raw = storage?.getItem(key) } catch { return {} }
  if (!raw) return {}
  let parsed
  try { parsed = JSON.parse(raw) } catch { return {} }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
  const out = {}
  for (const [id, d] of Object.entries(parsed)) {
    if (d && typeof d.text === 'string' && d.text) out[id] = { text: d.text, at: Number(d.at) || 0 }
  }
  return out
}

function save(storage, key, drafts) {
  try {
    if (Object.keys(drafts).length === 0) storage.removeItem(key)
    else storage.setItem(key, JSON.stringify(drafts))
  } catch { /* private mode or full: the draft stays in memory only */ }
}

// Writes the draft of one chat; an empty or blank text removes it.
export function writeDraft(storage, key, id, text, now = Date.now()) {
  if (!storage || !id) return
  const drafts = readDrafts(storage, key)
  if (!text || !text.trim()) {
    if (!(id in drafts)) return
    delete drafts[id]
    save(storage, key, drafts)
    return
  }
  drafts[id] = { text: text.slice(0, MAX_DRAFT_CHARS), at: now }
  const ids = Object.keys(drafts)
  if (ids.length > MAX_DRAFTS) {
    ids.sort((a, b) => drafts[b].at - drafts[a].at)
    for (const old of ids.slice(MAX_DRAFTS)) delete drafts[old]
  }
  save(storage, key, drafts)
}

export function clearDraft(storage, key, id) {
  writeDraft(storage, key, id, '')
}
