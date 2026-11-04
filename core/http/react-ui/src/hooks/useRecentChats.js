import { useEffect, useState } from 'react'
import { chatNotificationsApi, chatsApi } from '../utils/api'
import { menuChats } from '../utils/chatThread'
import { needsYouIds } from '../utils/originChats'
import { pendingChatDeletes } from './useChatDelete'

const EVENT = 'localai:chats-list'

// The pages that hold the whole chat list (a transcript, All chats) announce
// it after a change (a new chat, a rename, a delete, an Undo), with the rows
// they hide while an undo is offered already left out. The sidebar follows
// the announcement instead of asking the server again.
export function announceChats(chats) {
  window.dispatchEvent(new CustomEvent(EVENT, { detail: chats }))
}

export function newestChats(chats, limit) {
  return [...(chats || [])].sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0)).slice(0, limit)
}

// The newest chats of the signed-in user, for the sidebar. Only the chats a
// person started: the ones a trigger, a connector or a page started have their
// own area (All chats, second tab). It reads
// GET /api/chats?limit=N&kind=chat&origin=person when it starts and whenever `key` changes
// (the sidebar passes the route), and takes the announcements in between.
// A failed read keeps what is shown: the list is a shortcut, not a record.
export function useRecentChats(enabled, limit, key) {
  const [chats, setChats] = useState([])

  useEffect(() => {
    if (!enabled) { setChats([]); return undefined }
    let live = true
    chatsApi.list(limit, 'chat', 'person')
      .then((r) => { if (live) setChats(newestChats(menuChats((r?.conversations || []).filter(c => c.kind !== 'subagent' && !pendingChatDeletes.has(c.id))).filter(c => !c.origin), limit)) })
      .catch(() => {})
    return () => { live = false }
  }, [enabled, limit, key])

  useEffect(() => {
    if (!enabled) return undefined
    const onList = (e) => setChats(newestChats((e.detail || []).filter(c => !c.origin), limit))
    window.addEventListener(EVENT, onList)
    return () => window.removeEventListener(EVENT, onList)
  }, [enabled, limit])

  return chats
}

// The chats that a trigger, a connector, a job or a page started, and what
// needs the person among them, for the badge on the sidebar's All chats entry.
// It reads GET /api/chats?kind=chat&origin=automated (and the unread inbox
// items, which say which chats ask for the person) when it starts and whenever
// `key` changes (the sidebar passes the route) and when the tab gets focus
// back. A failed read keeps what is shown.
export function useAutomatedChats(enabled, key) {
  const [state, setState] = useState({ chats: [], needsYou: new Set() })

  useEffect(() => {
    if (!enabled) return undefined
    let live = true
    const load = () => {
      Promise.all([
        chatsApi.list(200, 'chat', 'automated'),
        chatNotificationsApi.list(true, 200).catch(() => null),
      ]).then(([r, inbox]) => {
        if (!live) return
        setState({
          chats: menuChats((r?.conversations || []).filter(c => c.kind !== 'subagent' && c.origin)),
          needsYou: needsYouIds(inbox?.notifications),
        })
      }).catch(() => {})
    }
    load()
    const onVisible = () => { if (document.visibilityState === 'visible') load() }
    document.addEventListener('visibilitychange', onVisible)
    return () => { live = false; document.removeEventListener('visibilitychange', onVisible) }
  }, [enabled, key])

  return state
}
