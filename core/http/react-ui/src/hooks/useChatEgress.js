import { useCallback, useEffect, useState } from 'react'
import { chatEgressApi } from '../utils/api'

const empty = { preset: 'off', ceiling: 'off', effective: 'off', applies: false, applies_reason: '', registries: [], rules: [], cards: [] }

// The network of a chat's Sandbox commands (GET /api/chats/{id}/egress), read
// again when tick changes (an egress_consent or egress_card event). setPreset
// and removeRule change it on the server and keep the answer.
export function useChatEgress(chatId, tick) {
  const [state, setState] = useState(empty)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')
  const [own, setOwn] = useState(0)

  useEffect(() => {
    if (!chatId) { setState(empty); setLoaded(false); return undefined }
    let live = true
    chatEgressApi.get(chatId)
      .then((res) => { if (live) { setState({ ...empty, ...res }); setLoaded(true); setError('') } })
      .catch(() => { if (live) { setState(empty); setLoaded(true) } })
    return () => { live = false }
  }, [chatId, tick, own])

  const setPreset = useCallback(async (preset) => {
    setError('')
    try {
      const res = await chatEgressApi.setPreset(chatId, preset)
      setState({ ...empty, ...res })
    } catch (e) {
      setError(e.message || 'error')
    }
  }, [chatId])

  const removeRule = useCallback(async (host, port) => {
    setError('')
    try {
      await chatEgressApi.deleteRule(chatId, host, port)
      setState(s => ({ ...s, rules: s.rules.filter(r => !(r.host === host && r.port === port)) }))
    } catch (e) {
      setError(e.message || 'error')
    }
  }, [chatId])

  // Read the view again, for a card that was answered from somewhere else or
  // refused while the page showed it pending.
  const refresh = useCallback(() => setOwn(n => n + 1), [])

  return { ...state, loaded, error, setPreset, removeRule, refresh }
}
