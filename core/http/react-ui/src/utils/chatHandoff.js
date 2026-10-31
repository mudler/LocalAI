// Hand-off from Home to the Chat page: the message composed on Home, its
// attachments and the chosen model/MCP settings.
//
// It is kept in memory. Home and Chat are routes of the same single-page app,
// so a module value survives the client-side navigation between them. It used
// to travel through localStorage, which put the attachments (base64) into a
// shared, size-limited store: when the browser cache of earlier chats had
// filled it, setItem threw QuotaExceededError inside Send's click handler and
// Send did nothing at all.
//
// Chat still reads the old localStorage entry when nothing is pending in
// memory, so a hand-off written by an older page (or seeded by a test) keeps
// working.

export const LEGACY_HANDOFF_KEY = 'localai_index_chat_data'

let pending = null

export function handOffToChat(data) {
  pending = data
}

// Returns the pending hand-off once and forgets it, or null.
export function takeChatHandoff() {
  const fromMemory = pending
  pending = null
  let legacy = null
  try {
    legacy = localStorage.getItem(LEGACY_HANDOFF_KEY)
    // Drop a left-over entry either way, so it cannot open a chat later.
    if (legacy !== null) localStorage.removeItem(LEGACY_HANDOFF_KEY)
  } catch { /* storage unavailable: nothing to read */ }
  if (fromMemory) return fromMemory
  if (!legacy) return null
  try {
    return JSON.parse(legacy)
  } catch {
    return null
  }
}
