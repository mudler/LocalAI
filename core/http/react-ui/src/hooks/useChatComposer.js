import { useCallback, useEffect, useState } from 'react'
import { chatAgentsApi } from '../utils/api'
import { CAP_CHAT } from '../utils/capabilities'
import { useChatDrafts } from './useChatDrafts'
import { useChatSwitches } from './useChatSwitches'
import { useModels } from './useModels'

// The state of the chat composer, shared by the chat page and Home: the draft
// (text and files, useChatDrafts), the model and the saved agent of a new
// chat, the chat models, the switches of the Tools and Skills menus
// (useChatSwitches, a draft before the chat exists) and what attaching and
// pasting a file do. components/chat/ChatComposer draws it.
//
//   chatId       the open chat, or null for a chat that does not exist yet
//   draftKey     where the draft of a new chat is kept: '' keeps it in memory
//                only (the empty chat); a key keeps its text across a reload
//                (Home)
//   activeChat   the open chat's record ({ model, agent_id }), or null
//   filesOn      files work on this server: attaching and the skills need it
//   initialModel, initialAgent   the model and the agent a new chat starts on
export function useChatComposer({ chatId = null, draftKey = '', userId, activeChat = null, filesOn = true, canAgents = false, initialModel = '', initialAgent = '' }) {
  // The id the draft is kept under (useChatDrafts): the chat, or draftKey.
  const draftId = chatId || draftKey || ''
  const drafts = useChatDrafts(draftId || null, userId)
  const { setFiles } = drafts
  const [draftModel, setDraftModel] = useState(initialModel)
  const [agentId, setAgentId] = useState(initialAgent)
  const [agents, setAgents] = useState([])
  const { models, loading: modelsLoading } = useModels(CAP_CHAT)

  // Agents are best effort: a server without them leaves the picker out.
  useEffect(() => {
    let live = true
    if (canAgents) chatAgentsApi.list().then((r) => { if (live) setAgents(r.agents || []) }).catch(() => { if (live) setAgents([]) })
    return () => { live = false }
  }, [canAgents])

  const pickedAgent = agents.find((a) => a.id === (activeChat ? activeChat.agent_id : agentId))
  // The model of a new chat comes from the agent when it names one.
  const effectiveModel = activeChat ? activeChat.model : (pickedAgent?.model || draftModel)
  const switches = useChatSwitches(chatId, filesOn, chatId ? null : { model: effectiveModel, agentId })

  // Files go to the server with the message that starts a turn and are saved
  // in the chat's folder of the workspace.
  const attach = useCallback((_kind, list) => {
    setFiles(prev => [...prev, ...list])
  }, [setFiles])

  const paste = useCallback((e) => {
    if (!filesOn) return
    const items = e.clipboardData?.items
    if (!items) return
    const images = Array.from(items)
      .filter(item => item.kind === 'file' && item.type.startsWith('image/'))
      .map(item => item.getAsFile())
      .filter(Boolean)
    if (images.length === 0) return
    // A pasted image attaches as a file rather than inserting into the text.
    // Clipboard images arrive unnamed or as "image.png"; give each a unique name.
    e.preventDefault()
    const stamp = Date.now()
    const named = images.map((file, i) => {
      const name = (file.name && file.name !== 'image.png')
        ? file.name
        : `pasted-image-${stamp}-${i + 1}.${(file.type.split('/')[1] || 'png').replace('+xml', '')}`
      return new File([file], name, { type: file.type })
    })
    setFiles(prev => [...prev, ...named])
  }, [filesOn, setFiles])

  return {
    drafts, draftId, draftModel, setDraftModel, agentId, setAgentId, agents, pickedAgent, effectiveModel,
    models, modelsLoading, switches, attach, paste,
  }
}
