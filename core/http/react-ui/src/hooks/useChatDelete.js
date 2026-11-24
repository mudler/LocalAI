import { useCallback, useEffect, useRef, useState } from 'react'

// How long a deleted chat can be brought back before the server deletes it.
export const CHAT_UNDO_MS = 6000

// The chats whose delete waits for its undo offer to end, on every page. A
// list read from the server meanwhile (the sidebar's recent chats) leaves
// them out.
export const pendingChatDeletes = new Set()

// Deleting a chat is a request: the row is hidden at once and the server
// deletes the chat when the undo offer ends (the toast's onExpire calls
// commit), when another chat is deleted, or when the page goes away. Undo
// drops the request and gives it back, so the page can reopen the chat.
//
// `remove(id)` deletes on the server; `onFailed(error)` runs when the server
// refuses, so the page can say why and show the chat again.
export function useChatDelete({ remove, onFailed }) {
  const [pending, setPending] = useState(null)
  const pendingRef = useRef(null)
  const removeRef = useRef(remove)
  removeRef.current = remove
  const failedRef = useRef(onFailed)
  failedRef.current = onFailed

  const commit = useCallback(() => {
    const p = pendingRef.current
    if (!p) return
    pendingRef.current = null
    setPending(null)
    Promise.resolve(removeRef.current(p.id))
      .catch((e) => failedRef.current?.(e))
      .finally(() => pendingChatDeletes.delete(p.id))
  }, [])

  // The page going away ends the offer: the chat is deleted then.
  useEffect(() => () => commit(), [commit])

  // `item` is { id, name, ...anything the page wants back on Undo }.
  const request = useCallback((item) => {
    commit()
    pendingChatDeletes.add(item.id)
    pendingRef.current = item
    setPending(item)
  }, [commit])

  const undo = useCallback(() => {
    const p = pendingRef.current
    if (p) pendingChatDeletes.delete(p.id)
    pendingRef.current = null
    setPending(null)
    return p
  }, [])

  return { pending, request, undo, commit }
}
