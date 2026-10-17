// The browser cache of the Chat page's conversations. Chat writes it, Home
// reads it.
//
// Signed in, every account has its own entry: a draft one account could not
// upload must never be found, and uploaded, by the next account that signs in
// on the same browser. Without an account (auth disabled) the original key is
// used, so existing browser-only histories keep working.
//
// Besides the chats the entry holds `pendingDeletes`: ids the user deleted
// that the server has not confirmed yet ('*' = delete all). They survive a
// reload and are sent again until the server answers.

export const CHATS_STORAGE_KEY = 'localai_chats_data'

export function chatsStorageKey(accountId) {
  return accountId ? `${CHATS_STORAGE_KEY}:${accountId}` : CHATS_STORAGE_KEY
}

export function isChatsStorageKey(key) {
  return key === CHATS_STORAGE_KEY || (typeof key === 'string' && key.startsWith(`${CHATS_STORAGE_KEY}:`))
}

export function readChatStore(key) {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    const data = JSON.parse(raw)
    if (!data || !Array.isArray(data.chats)) return null
    return { ...data, pendingDeletes: Array.isArray(data.pendingDeletes) ? data.pendingDeletes : [] }
  } catch {
    return null
  }
}

export function writeChatStore(key, data) {
  try {
    localStorage.setItem(key, JSON.stringify({ ...data, lastSaved: Date.now() }))
    return true
  } catch (err) {
    if (err?.name === 'QuotaExceededError' || err?.code === 22) {
      console.warn('localStorage quota exceeded')
    }
    return false
  }
}
