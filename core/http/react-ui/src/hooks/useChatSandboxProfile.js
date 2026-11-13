import { useCallback, useEffect, useRef, useState } from 'react'
import { chatSandboxProfileApi } from '../utils/api'
import { POLL_MS, draftProfileToSave, draftView, errorKey, needsPolling, withDraftChoice } from '../utils/chatSandboxProfile'

const empty = { applies: false, profiles: [] }

// The sandbox profile of a chat (GET /api/chats/{id}/sandbox-profile). A new
// chat with no id shows the default from the list of profiles, and setProfile
// only keeps the person's pick on the screen: saveDraft(id) sends it to the
// chat once it is created. While an image is pulled the view is read again
// every POLL_MS; the timer stops when no pull runs and when the page unmounts.
// For a started chat setProfile changes the chat's choice and keeps the answer;
// the server restarts the sandbox at the next command.
export function useChatSandboxProfile(chatId, tick) {
  const [state, setState] = useState(empty)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')
  const [own, setOwn] = useState(0)
  const [choice, setChoice] = useState('')
  const latest = useRef({ state, choice })
  latest.current = { state, choice }
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
    if (!chatId) { setChoice(profile); return }
    try {
      const res = await chatSandboxProfileApi.set(chatId, profile)
      setState({ ...empty, ...res })
    } catch (e) {
      setError(errorKey(e) || 'saveError')
    }
  }, [chatId])

  // Sends the profile picked in the draft to the chat just created. It returns
  // an error key, or '' when there was nothing to send or it was saved.
  const saveDraft = useCallback(async (id) => {
    const { state: list, choice: picked } = latest.current
    const profile = draftProfileToSave(list, picked)
    setChoice('')
    if (!id || !profile) return ''
    try {
      await chatSandboxProfileApi.set(id, profile)
      return ''
    } catch (e) {
      return errorKey(e) || 'saveError'
    }
  }, [])

  const refresh = useCallback(() => setOwn(n => n + 1), [])
  const view = !chatId && state.draft ? withDraftChoice(state, choice) : state
  return { ...view, loaded, error, setProfile, refresh, saveDraft }
}
