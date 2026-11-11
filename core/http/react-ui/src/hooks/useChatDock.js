import { useCallback, useEffect, useRef, useState } from 'react'
import { chatsApi } from '../utils/api'
import { dockKey, dockToServer, emptyDock, openTab, readDock, sameDock, setDockOpen } from '../utils/dock'
import { setPageOpener } from '../utils/slots'

// A width change while the edge is dragged is saved once the drag rests.
const SAVE_AFTER_MS = 300

// The dock of the open chat: the pages it shows in tabs, whether the panel is
// open and its width. The server keeps it with the chat, so another browser,
// another tab and another frontend show the same dock, and a dock event of
// the chat (a page the assistant opened, a change in another tab) reads it
// again through the chat's detail.
//
// A new chat has no id until its first message: its dock lives here until
// then, and is saved to the chat that message creates.
//
//   chatId      the open chat, or null for a new chat
//   server      the dock of the chat's detail as the server gave it
//   serverFor   the id of the chat that detail belongs to
//   onError     called with the error when a save fails
//
// It returns {dock, update}: update(fn) applies fn to the dock and saves it.
export function useChatDock({ chatId, server, serverFor, onError }) {
  const [dock, setDock] = useState(emptyDock)
  const dockRef = useRef(dock)
  dockRef.current = dock
  const prevChat = useRef(chatId)
  const inflight = useRef(0)
  const waiting = useRef(null) // {id, dock, timer}: a lazy save not sent yet
  const errorRef = useRef(onError)
  errorRef.current = onError

  const send = useCallback((id, next) => {
    inflight.current += 1
    chatsApi.setDock(id, dockToServer(next))
      .catch((e) => errorRef.current?.(e))
      .finally(() => { inflight.current -= 1 })
  }, [])
  // flush sends a lazy save now (another chat opens, or the page goes).
  const flush = useCallback(() => {
    const w = waiting.current
    if (!w) return
    clearTimeout(w.timer)
    waiting.current = null
    send(w.id, w.dock)
  }, [send])
  const save = useCallback((id, next, lazy) => {
    if (waiting.current) {
      clearTimeout(waiting.current.timer)
      waiting.current = null
    }
    if (!lazy) {
      send(id, next)
      return
    }
    const w = { id, dock: next, timer: 0 }
    w.timer = setTimeout(() => { if (waiting.current === w) flush() }, SAVE_AFTER_MS)
    waiting.current = w
  }, [send, flush])
  const busy = () => inflight.current > 0 || waiting.current !== null

  // Another chat: its own dock. The dock of a new chat goes with the chat
  // its first message created.
  useEffect(() => {
    const before = prevChat.current
    prevChat.current = chatId
    if (before === chatId) return
    flush()
    if (!before && chatId && dockRef.current.tabs.length > 0) {
      save(chatId, dockRef.current, false)
      return
    }
    setDock(serverFor === chatId && chatId ? readDock(server) : emptyDock())
  }, [chatId]) // eslint-disable-line react-hooks/exhaustive-deps

  // The server's dock of this chat changed (read, or a dock event): take it,
  // unless a change of this tab is still on its way.
  useEffect(() => {
    if (!chatId || serverFor !== chatId || busy()) return
    const next = readDock(server)
    if (!sameDock(next, dockRef.current)) setDock(next)
  }, [server, serverFor, chatId])

  useEffect(() => flush, [flush])

  const update = useCallback((fn, { lazy = false } = {}) => {
    const next = fn(dockRef.current)
    if (!next || sameDock(next, dockRef.current)) return
    dockRef.current = next
    setDock(next)
    if (chatId) save(chatId, next, lazy)
  }, [chatId, save])

  return { dock, update }
}

const PHONE_QUERY = '(max-width: 639px)'

function usePhone() {
  const read = () => typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(PHONE_QUERY).matches
  const [phone, setPhone] = useState(read)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined
    const mq = window.matchMedia(PHONE_QUERY)
    const on = () => setPhone(mq.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return phone
}

// usePageDock is the dock of the chat page: useChatDock, plus the ways a page
// gets into it. While the chat page shows, the Open of a page button or a
// compact tile in a slot opens the page in the dock (setPageOpener of
// utils/slots.js); Ctrl+\ (Cmd+\ on a Mac) shows and hides it; open(ref) is
// for the Pages list (the location state of the chat) and the + of the tabs.
//
//   enabled   the user has the pages feature: without it there is no dock
//
// It returns {dock, update, open, toggle, fresh, phone}: fresh is the key of
// the page the person just opened, which takes a frame at once.
export function usePageDock({ chatId, server, serverFor, enabled, onError }) {
  const { dock, update } = useChatDock({ chatId, server, serverFor, onError })
  const [fresh, setFresh] = useState('')
  const phone = usePhone()

  const open = useCallback((ref) => {
    update((d) => openTab(d, ref))
    setFresh(dockKey(ref))
  }, [update])
  const toggle = useCallback(() => update((d) => setDockOpen(d, !d.open)), [update])

  useEffect(() => (enabled ? setPageOpener((ref) => { open(ref); return true }) : undefined), [enabled, open])

  const toggleRef = useRef(toggle)
  toggleRef.current = toggle
  useEffect(() => {
    if (!enabled) return undefined
    const onKey = (e) => {
      if (!(e.ctrlKey || e.metaKey) || e.altKey || e.key !== '\\') return
      e.preventDefault()
      toggleRef.current()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [enabled])

  return { dock: enabled ? dock : emptyDock(), update, open, toggle, fresh, phone }
}
