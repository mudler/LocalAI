import { useCallback, useEffect, useState } from 'react'
import { chatSandboxProfileApi } from '../utils/api'
import { POLL_MS, draftView, errorKey, needsPolling } from '../utils/chatSandboxProfile'

const empty = { applies: false, profiles: [] }

// The sandbox profile of a chat (GET /api/chats/{id}/sandbox-profile). A new
// chat with no id shows the default from the list of profiles. While an image
// is pulled the view is read again every POLL_MS; the timer stops when no pull
// runs and when the page unmounts. setProfile changes the chat's choice and
// keeps the answer; the server restarts the sandbox at the next command.
export function useChatSandboxProfile(chatId, tick) {
  const [state, setState] = useState(empty)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')
  const [own, setOwn] = useState(0)
  const polling = needsPolling(state)

  useEffect(() => {
    let live = true
    const read = chatId ? chatSandboxProfileApi.get(chatId) : chatSandboxProfileApi.list().then(draftView)
    read
      .then((res) => { if (live) { setState({ ...empty, ...res }); setLoaded(true) } })
      .catch(() => { if (live) { setState(empty); setLoaded(true) } })
    return () => { live = false }
  }, [chatId, tick, own])

  useEffect(() => {
    if (!polling) return undefined
    const id = setInterval(() => setOwn(n => n + 1), POLL_MS)
    return () => clearInterval(id)
  }, [polling])

  const setProfile = useCallback(async (profile) => {
    setError('')
    try {
      const res = await chatSandboxProfileApi.set(chatId, profile)
      setState({ ...empty, ...res })
    } catch (e) {
      setError(errorKey(e) || 'saveError')
    }
  }, [chatId])

  const refresh = useCallback(() => setOwn(n => n + 1), [])
  return { ...state, loaded, error, setProfile, refresh }
}
