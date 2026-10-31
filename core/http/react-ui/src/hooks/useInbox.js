import { useCallback, useEffect, useRef, useState } from 'react'
import { chatNotificationsApi } from '../utils/api'
import { retryDelay } from '../utils/streamRetry'

const RECONNECT_MS = 2000 // first wait; it doubles up to 30 s
const PAGE = 50 // items per page of the list; "show more" adds one page
const CHAT_LINK = /^\/app\/chats\/([^/?#]+)/

// The inbox of the chats: the newest notifications, how many are unread, and
// which chats have an unread one. New ones arrive as server-sent events; the
// list is read again whenever the stream reconnects, because events are live
// only; a change elsewhere (read, delete or clear in another tab) arrives as
// notifications_changed and reads the list again. A server without an inbox
// answers 409: the hook then stays empty and leaves the stream alone.
export function useInbox() {
  const [items, setItems] = useState([])
  const [unread, setUnread] = useState(0)
  const [total, setTotal] = useState(0)
  const limitRef = useRef(PAGE)
  const [off, setOff] = useState(false)
  const sourceRef = useRef(null)
  const timerRef = useRef(null)
  // The reconnect loop reads this, not the state: it outlives a render.
  const offRef = useRef(false)
  const attemptRef = useRef(0)

  const refresh = useCallback(async () => {
    try {
      const res = await chatNotificationsApi.list(false, limitRef.current)
      setItems(res.notifications || [])
      setUnread(res.unread || 0)
      setTotal(res.total ?? (res.notifications || []).length)
      offRef.current = false
      setOff(false)
    } catch (e) {
      if (e?.status === 409 || e?.status === 404) {
        offRef.current = true
        setOff(true)
        clearTimeout(timerRef.current)
        sourceRef.current?.close()
        sourceRef.current = null
      }
    }
  }, [])

  useEffect(() => {
    let closed = false
    const connect = () => {
      if (closed || offRef.current || typeof EventSource === 'undefined') return
      const es = new EventSource(chatNotificationsApi.eventsUrl())
      sourceRef.current = es
      es.addEventListener('notification', () => { refresh() })
      es.addEventListener('notifications_changed', () => { refresh() })
      es.onopen = () => { attemptRef.current = 0; refresh() }
      es.onerror = () => {
        es.close()
        if (closed || offRef.current) return
        timerRef.current = setTimeout(connect, retryDelay(attemptRef.current++, RECONNECT_MS))
      }
    }
    refresh()
    connect()
    return () => {
      closed = true
      clearTimeout(timerRef.current)
      sourceRef.current?.close()
    }
  }, [refresh])

  const markRead = useCallback(async (ids) => {
    await chatNotificationsApi.read({ ids })
    await refresh()
  }, [refresh])
  const markAllRead = useCallback(async () => {
    await chatNotificationsApi.read({ all: true })
    await refresh()
  }, [refresh])
  const remove = useCallback(async (ids) => {
    await chatNotificationsApi.remove(ids)
    await refresh()
  }, [refresh])

  // Removes every notification, or only the read ones. It returns how many went.
  const clear = useCallback(async (readOnly) => {
    const res = await chatNotificationsApi.clear(readOnly)
    await refresh()
    return res?.deleted || 0
  }, [refresh])
  const showMore = useCallback(async () => {
    limitRef.current += PAGE
    await refresh()
  }, [refresh])

  const unreadChatIds = new Set()
  for (const n of items) {
    const m = !n.read && CHAT_LINK.exec(n.link || '')
    if (m) unreadChatIds.add(m[1])
  }
  return { items, unread, total, unreadChatIds, off, refresh, markRead, markAllRead, remove, clear, showMore }
}
