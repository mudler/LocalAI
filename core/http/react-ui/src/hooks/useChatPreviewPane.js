import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  PHONE_MAX_WIDTH, autoOpenTarget, closedKey, isClosed, previewableBlocks, setClosed, targetKey,
} from '../utils/previewPane'

function browserStorage() {
  try { return window.localStorage } catch { return null }
}

function phoneNow() {
  try { return window.matchMedia(`(max-width: ${PHONE_MAX_WIDTH}px)`).matches } catch { return false }
}

// The preview pane of the open chat: whether it is open, what it shows, and
// when it opens by itself.
//
// It opens by itself on what a turn produces while this page watches (a file
// the assistant created or changed, a code block that is a result; see
// utils/previewPane.js), never on a phone, and never in a chat whose pane the
// person closed: that choice is kept per chat in the browser storage until
// they open the pane again by hand. Opening by itself never moves the focus;
// announcement is the text for a role="status" region.
//
//   chatId, userId   the open chat and the account (the storage is per account)
//   files            the chat's files (useChatFiles), loaded says they are read
//   messages         the transcript
//   ready            the chat's transcript is read: the watermark is taken then
//   running          a turn runs now (its own messages count as new)
export function useChatPreviewPane({ chatId, userId, files, loaded, messages, ready, running }) {
  const key = closedKey(userId)
  const [open, setOpen] = useState(false)
  const [focus, setFocus] = useState(null)
  const [announcement, setAnnouncement] = useState('')
  const [since, setSince] = useState({ id: null })
  const seen = useRef(new Set())

  const blocks = useMemo(() => previewableBlocks(messages), [messages])

  // Another chat: the pane closes and a new watermark is taken once the chat
  // and its files are read. Everything a chat had then is old.
  useEffect(() => {
    setOpen(false)
    setFocus(null)
    setAnnouncement('')
    seen.current = new Set()
  }, [chatId])
  useEffect(() => {
    if (!chatId || !ready || !loaded || since.id === chatId) return
    const lastTurn = messages.reduce((n, m) => Math.max(n, m.turn || 0), 0)
    const turn = running ? Math.max(0, lastTurn - 1) : lastTurn
    // A running turn's own answers are new: the watermark is the last
    // message of the turns before it.
    const seq = messages.filter(m => (m.turn || 0) <= turn).reduce((n, m) => Math.max(n, m.seq || 0), 0)
    setSince({ id: chatId, turn, seq })
  }, [chatId, ready, loaded, running, messages, since.id])

  const ask = useCallback((target, by) => {
    setFocus(f => ({ path: target.type === 'file' ? target.path : undefined, block: target.type === 'block' ? target.id : undefined, by, n: (f?.n || 0) + 1 }))
  }, [])

  useEffect(() => {
    if (!chatId || since.id !== chatId) return
    const target = autoOpenTarget({ files, messages, since })
    const k = targetKey(target)
    if (!k || seen.current.has(k)) return
    seen.current.add(k)
    if (phoneNow() || isClosed(browserStorage(), key, chatId)) return
    setOpen(true)
    ask(target, 'auto')
    setAnnouncement(target.name)
  }, [chatId, since, files, messages, key, ask])

  // Opening by hand forgets that the pane was closed in this chat.
  const reopen = useCallback(() => {
    setOpen(true)
    setClosed(browserStorage(), key, chatId, false)
  }, [key, chatId])
  const openFile = useCallback((path) => {
    if (!path) return
    reopen()
    ask({ type: 'file', path }, 'user')
  }, [reopen, ask])
  const openBlock = useCallback((id) => {
    if (!id) return
    reopen()
    ask({ type: 'block', id }, 'user')
  }, [reopen, ask])
  const close = useCallback(() => {
    setOpen(false)
    setAnnouncement('')
    setClosed(browserStorage(), key, chatId, true)
  }, [key, chatId])
  const toggle = useCallback(() => { if (open) close(); else reopen() }, [open, close, reopen])
  // A deleted chat leaves nothing behind.
  const forget = useCallback((id) => setClosed(browserStorage(), key, id, false), [key])

  return { open, focus, blocks, announcement, openFile, openBlock, close, toggle, forget }
}
