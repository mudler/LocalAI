// The chats the old chat page kept in this browser. It stored them in
// localStorage under localai_chats_data, or localai_chats_data:<account> with
// accounts, as { chats: [...] }. POST /api/chats/import/browser takes that value
// as it is and skips a chat it has already, so an import can be repeated. The
// page offers it once per browser; the copy here is removed only when nothing
// failed.

export const BROWSER_CHATS_KEY = 'localai_chats_data'
export const BROWSER_IMPORT_DISMISSED = 'localai_chats_import_dismissed'

const isStoreKey = (k) => k === BROWSER_CHATS_KEY || (typeof k === 'string' && k.startsWith(`${BROWSER_CHATS_KEY}:`))

// The stores this browser holds, with the number of chats in each. An
// unreadable value or one with no chats is left out.
export function browserChatStores(storage) {
  const out = []
  if (!storage) return out
  try {
    for (let i = 0; i < storage.length; i++) {
      const key = storage.key(i)
      if (!isStoreKey(key)) continue
      const raw = storage.getItem(key)
      let count = 0
      try {
        const v = JSON.parse(raw)
        count = Array.isArray(v?.chats) ? v.chats.length : 0
      } catch {
        count = 0
      }
      if (count > 0) out.push({ key, raw, count })
    }
  } catch {
    return []
  }
  return out
}

// Sends every store and adds up what the server said. A store is removed from
// the browser only when none of its chats failed.
export async function importBrowserChats(storage, stores, send) {
  const total = { imported: 0, skipped: 0, failed: 0 }
  for (const s of stores) {
    const res = await send(s.raw)
    total.imported += res?.imported || 0
    total.skipped += res?.skipped || 0
    total.failed += res?.failed || 0
    if (!(res?.failed > 0)) {
      try { storage.removeItem(s.key) } catch { /* storage blocked: the import can run again */ }
    }
  }
  return total
}
