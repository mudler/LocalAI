import { useEffect, useState } from 'react'
import { chatsApi } from '../utils/api'
import { menuChats } from '../utils/chatThread'
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

// The newest chats of the signed-in user, for the sidebar. It reads
// GET /api/chats?limit=N&kind=chat when it starts and whenever `key` changes
// (the sidebar passes the route), and takes the announcements in between.
// A failed read keeps what is shown: the list is a shortcut, not a record.
export function useRecentChats(enabled, limit, key) {
  const [chats, setChats] = useState([])

  useEffect(() => {
    if (!enabled) { setChats([]); return undefined }
    let live = true
    chatsApi.list(limit, 'chat')
      .then((r) => { if (live) setChats(newestChats(menuChats((r?.conversations || []).filter(c => c.kind !== 'subagent' && !pendingChatDeletes.has(c.id))), limit)) })
      .catch(() => {})
    return () => { live = false }
  }, [enabled, limit, key])

  useEffect(() => {
    if (!enabled) return undefined
    const onList = (e) => setChats(newestChats(e.detail, limit))
    window.addEventListener(EVENT, onList)
    return () => window.removeEventListener(EVENT, onList)
  }, [enabled, limit])

  return chats
}
