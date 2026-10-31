import { useCallback, useEffect, useRef, useState } from 'react'
import { chatNotificationsApi } from '../utils/api'
import { retryDelay } from '../utils/streamRetry'

const RECONNECT_MS = 2000 // first wait; it doubles up to 30 s
const PAGE = 50 // items per page of the list; "show more" adds one page
const ALL_KEPT = 500 // the server keeps at most this many per user
const CHAT_LINK = /^\/app\/chats\/([^/?#]+)/

// The inbox of the chats: the newest notifications, how many are unread, and
// which chats have an unread one. New ones arrive as server-sent events; the
// list is read again whenever the stream reconnects, because events are live
// only; a change elsewhere (read, delete or clear in another tab) arrives as
// notifications_changed and reads the list again. A server without an inbox
// answers 409: the hook then stays empty and leaves the stream alone.
//
// status is loading until the first answer, then ready, or error when the
// last read failed (the items of the read before stay). filter is all,
// unread (the server sends only those) or failed (the whole inbox is read,
// the panel picks the failures). arrived lists the items that came in since
// the read before, for the panel to point out.
export function useInbox() {
  const [items, setItems] = useState([])
  const [unread, setUnread] = useState(0)
  const [total, setTotal] = useState(0)
  const [status, setStatus] = useState('loading')
  const [error, setError] = useState(null)
  const [filter, setFilterState] = useState('all')
  const [arrived, setArrived] = useState([])
  const limitRef = useRef(PAGE)
  const filterRef = useRef('all')
  // The highest id seen; anything above it on the next read is new.
  const seenRef = useRef(0)
  const loadedRef = useRef(false)
  const [off, setOff] = useState(false)
  const sourceRef = useRef(null)
  const timerRef = useRef(null)
  // The reconnect loop reads this, not the state: it outlives a render.
  const offRef = useRef(false)
  const attemptRef = useRef(0)

  const refresh = useCallback(async () => {
    const f = filterRef.current
    try {
      const res = await chatNotificationsApi.list(f === 'unread', f === 'failed' ? ALL_KEPT : limitRef.current)
      if (f !== filterRef.current) return
      const list = res.notifications || []
      let top = seenRef.current
      for (const n of list) if (n.id > top) top = n.id
      if (loadedRef.current) {
        const fresh = list.filter(n => n.id > seenRef.current)
        if (fresh.length > 0) setArrived(fresh)
      }
      seenRef.current = top
      loadedRef.current = true
      setItems(list)
      setUnread(res.unread || 0)
      setTotal(res.total ?? list.length)
      setStatus('ready')
      setError(null)
      offRef.current = false
      setOff(false)
    } catch (e) {
      if (e?.status === 409 || e?.status === 404) {
        offRef.current = true
        setOff(true)
        clearTimeout(timerRef.current)
        sourceRef.current?.close()
        sourceRef.current = null
        return
      }
      setError(e)
      setStatus('error')
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
  const setFilter = useCallback((f) => {
    if (f === filterRef.current) return Promise.resolve()
    filterRef.current = f
    limitRef.current = PAGE
    setFilterState(f)
    return refresh()
  }, [refresh])

  const unreadChatIds = new Set()
  for (const n of items) {
    const m = !n.read && CHAT_LINK.exec(n.link || '')
    if (m) unreadChatIds.add(m[1])
  }
  // How many the current view could show, for "Showing 50 of 120".
  const listTotal = filter === 'unread' ? unread : filter === 'failed' ? items.length : total
  return {
    items, unread, total, listTotal, unreadChatIds, off, status, error, filter, arrived,
    refresh, markRead, markAllRead, remove, clear, showMore, setFilter,
  }
}
