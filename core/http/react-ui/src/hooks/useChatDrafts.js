import { useCallback, useEffect, useRef, useState } from 'react'
import { draftsKey, readDrafts, writeDraft } from '../utils/chatDrafts'

// How long after the last key the open chat's draft is written to the storage.
const SAVE_MS = 300
const NO_FILES = Object.freeze([])

function browserStorage() {
  try { return window.localStorage } catch { return null }
}

// The composer's draft of each chat: the text and the staged files. What you
// type or attach belongs to the chat that is open, so another chat shows its
// own draft (or none), and the draft is there again when you come back. The
// text is also kept in the browser storage (utils/chatDrafts.js), so a reload
// keeps it; the files are kept in memory only. The new chat (no id) has a draft
// in memory only, so a new chat starts empty.
//
// Work that ends after the person switched chats names its chat: restore()
// gives a message that could not be sent back to the chat it was written in,
// and append() adds dictated text to the chat the dictation started in.
export function useChatDrafts(chatId, userId) {
  const key = chatId || ''
  const storageKey = draftsKey(userId)
  // The stored texts, read once; every write updates this copy too.
  const storedRef = useRef(null)
  if (storedRef.current === null) storedRef.current = readDrafts(browserStorage(), storageKey)
  const [byChat, setByChat] = useState({})
  const openRef = useRef(key)
  openRef.current = key

  const entryOf = useCallback((map, id) => map[id] || { text: storedRef.current[id]?.text || '', files: NO_FILES }, [])
  const entry = entryOf(byChat, key)

  const persist = useCallback((id, text) => {
    if (!id) return
    if (text && text.trim()) storedRef.current[id] = { text, at: Date.now() }
    else delete storedRef.current[id]
    writeDraft(browserStorage(), storageKey, id, text)
  }, [storageKey])

  const change = useCallback((id, fn) => {
    setByChat((m) => {
      const cur = entryOf(m, id)
      const next = fn(cur)
      return next === cur ? m : { ...m, [id]: next }
    })
  }, [entryOf])
  const apply = (v, cur) => (typeof v === 'function' ? v(cur) : v)

  // The open chat's text is written a moment after the last key; a switch, a
  // closed page or an unmount writes it at once.
  const pendingRef = useRef(null)
  const flush = useCallback(() => {
    const p = pendingRef.current
    if (!p) return
    clearTimeout(p.timer)
    pendingRef.current = null
    persist(p.id, p.text)
  }, [persist])
  useEffect(() => {
    if (!key) return
    const same = (storedRef.current[key]?.text || '') === entry.text
    if (same && !pendingRef.current) return
    clearTimeout(pendingRef.current?.timer)
    pendingRef.current = { id: key, text: entry.text, timer: setTimeout(flush, SAVE_MS) }
  }, [key, entry.text, flush])
  useEffect(() => flush, [key, flush])
  useEffect(() => {
    window.addEventListener('pagehide', flush)
    return () => window.removeEventListener('pagehide', flush)
  }, [flush])
  // A draft changed for a chat that is not open (restore, append) is written now.
  useEffect(() => {
    for (const [id, e] of Object.entries(byChat)) {
      if (!id || id === key) continue
      if ((storedRef.current[id]?.text || '') !== e.text) persist(id, e.text)
    }
  }, [byChat, key, persist])

  const setText = useCallback((v) => change(openRef.current, cur => ({ ...cur, text: apply(v, cur.text) })), [change])
  const setFiles = useCallback((v) => change(openRef.current, cur => ({ ...cur, files: apply(v, cur.files) })), [change])

  // Drops a chat's draft: it was sent, or the chat was deleted.
  const clear = useCallback((id) => {
    const k = id || ''
    if (pendingRef.current?.id === k) { clearTimeout(pendingRef.current.timer); pendingRef.current = null }
    setByChat((m) => {
      if (!(k in m)) return m
      const n = { ...m }
      delete n[k]
      return n
    })
    persist(k, '')
  }, [persist])

  // Gives a message that could not be sent back to its chat, unless the person
  // has typed or attached something else there meanwhile.
  const restore = useCallback((id, text, files = NO_FILES) => {
    change(id || '', cur => ({ text: cur.text || text, files: cur.files.length ? cur.files : files }))
  }, [change])

  const append = useCallback((id, text) => {
    change(id || '', cur => ({ ...cur, text: cur.text.trim() ? `${cur.text.trimEnd()} ${text}` : text }))
  }, [change])

  // Whether the chat is the open one now (for work that ends later).
  const isOpen = useCallback((id) => openRef.current === (id || ''), [])

  return { text: entry.text, files: entry.files, setText, setFiles, clear, restore, append, isOpen }
}
