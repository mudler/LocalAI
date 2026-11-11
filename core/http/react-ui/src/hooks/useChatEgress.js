import { useCallback, useEffect, useRef, useState } from 'react'
import { chatEgressApi } from '../utils/api'
import { draftEgressView, draftPresetToSave } from '../utils/chatEgress'

const empty = { preset: 'off', ceiling: 'off', effective: 'off', applies: false, applies_reason: '', registries: [], allowed_hosts: [], rules: [], cards: [] }

// The network of a chat's isolated commands (GET /api/chats/{id}/egress), read
// again when tick changes (an egress_consent or egress_card event). setPreset,
// removeRule and answer change it on the server and keep the answer.
//
// Before the chat exists (no chatId) draft, { model, agentId, profilesApply },
// names the new chat: the view comes from GET /api/chats/draft/egress, setPreset
// only keeps the choice on the screen, and saveDraft(id) sends it to the chat
// once it is created. Without draft a chat with no id has no view.
export function useChatEgress(chatId, tick, draft = null) {
  const [state, setState] = useState(empty)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')
  const [own, setOwn] = useState(0)
  const [choice, setChoice] = useState('')
  const draftOn = !chatId && !!draft
  const draftModel = draft?.model || ''
  const draftAgent = draft?.agentId || ''
  const latest = useRef({ state, choice })
  latest.current = { state, choice }

  useEffect(() => {
    if (!chatId && !draftOn) { setState(empty); setLoaded(false); return undefined }
    let live = true
    const read = chatId ? chatEgressApi.get(chatId) : chatEgressApi.draft(draftModel, draftAgent)
    read
      .then((res) => { if (live) { setState({ ...empty, ...res }); setLoaded(true); setError('') } })
      .catch(() => { if (live) { setState(empty); setLoaded(!draftOn) } })
    return () => { live = false }
  }, [chatId, draftOn, draftModel, draftAgent, tick, own])

  const setPreset = useCallback(async (preset) => {
    setError('')
    if (!chatId) { setChoice(preset); return }
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

  // answer answers a card (an expired one too) and reads the state again: an
  // answer may add a rule.
  const answer = useCallback(async (id, decision) => {
    setError('')
    try {
      await chatEgressApi.consent(chatId, id, decision)
      const res = await chatEgressApi.get(chatId)
      setState({ ...empty, ...res })
    } catch (e) {
      setError(e.message || 'error')
    }
  }, [chatId])

  // Sends the preset picked in the draft to the chat just created. It returns
  // an error text, or '' when there was nothing to send or it was saved.
  const saveDraft = useCallback(async (id) => {
    const { state: res, choice: picked } = latest.current
    const preset = draftPresetToSave(res, picked)
    setChoice('')
    if (!id || !preset) return ''
    try {
      await chatEgressApi.setPreset(id, preset)
      return ''
    } catch (e) {
      return e.message || 'error'
    }
  }, [])

  const view = draftOn ? draftEgressView({ ...state }, Boolean(draft?.profilesApply), choice) : state
  return { ...view, loaded, error, setPreset, removeRule, refresh, answer, saveDraft }
}
