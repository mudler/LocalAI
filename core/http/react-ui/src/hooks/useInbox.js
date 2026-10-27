import { useCallback, useEffect, useRef, useState } from 'react'
import { chatNotificationsApi } from '../utils/api'

const RECONNECT_MS = 2000
const CHAT_LINK = /^\/app\/chats\/([^/?#]+)/

// The inbox of the chats: the newest notifications, how many are unread, and
// which chats have an unread one. New ones arrive as server-sent events; the
// list is read again whenever the stream reconnects, because events are live
// only. A server without an inbox answers 409: the hook then stays empty.
export function useInbox() {
  const [items, setItems] = useState([])
  const [unread, setUnread] = useState(0)
  const [off, setOff] = useState(false)
  const sourceRef = useRef(null)
  const timerRef = useRef(null)

  const refresh = useCallback(async () => {
    try {
      const res = await chatNotificationsApi.list(false)
      setItems(res.notifications || [])
      setUnread(res.unread || 0)
      setOff(false)
    } catch (e) {
      if (e?.status === 409 || e?.status === 404) setOff(true)
    }
  }, [])

  useEffect(() => {
    let closed = false
    const connect = () => {
      if (closed || typeof EventSource === 'undefined') return
      const es = new EventSource(chatNotificationsApi.eventsUrl())
      sourceRef.current = es
      es.addEventListener('notification', () => { refresh() })
      es.onopen = () => { refresh() }
      es.onerror = () => {
        es.close()
        if (!closed) timerRef.current = setTimeout(connect, RECONNECT_MS)
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

  const unreadChatIds = new Set()
  for (const n of items) {
    const m = !n.read && CHAT_LINK.exec(n.link || '')
    if (m) unreadChatIds.add(m[1])
  }
  return { items, unread, unreadChatIds, off, refresh, markRead, markAllRead, remove }
}
