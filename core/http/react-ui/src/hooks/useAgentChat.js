import { useState, useCallback, useEffect } from 'react'
import { generateId } from '../utils/format'
import { useDebouncedEffect } from './useDebounce'

const STORAGE_KEY_PREFIX = 'localai_agent_chats_'

function storageKey(agentName) {
  return STORAGE_KEY_PREFIX + agentName
}

function loadConversations(agentName) {
  try {
    const stored = localStorage.getItem(storageKey(agentName))
    if (stored) {
      const data = JSON.parse(stored)
      if (data && Array.isArray(data.conversations)) {
        return data
      }
    }
  } catch (_e) {
    localStorage.removeItem(storageKey(agentName))
  }
  return null
}

function saveConversations(agentName, conversations, activeId) {
  try {
    const data = {
      conversations: conversations.map(c => ({
        id: c.id,
        name: c.name,
        messages: c.messages,
        createdAt: c.createdAt,
        updatedAt: c.updatedAt,
      })),
      activeId,
      lastSaved: Date.now(),
    }
    localStorage.setItem(storageKey(agentName), JSON.stringify(data))
  } catch (err) {
    if (err.name === 'QuotaExceededError' || err.code === 22) {
      console.warn('localStorage quota exceeded for agent chats')
    }
  }
}

function createConversation() {
  return {
    id: generateId(),
    name: 'New Chat',
    messages: [],
    createdAt: Date.now(),
    updatedAt: Date.now(),
  }
}

export function useAgentChat(agentName) {
  const [conversations, setConversations] = useState(() => {
    const stored = loadConversations(agentName)
    if (stored && stored.conversations.length > 0) return stored.conversations
    return [createConversation()]
  })

  const [activeId, setActiveId] = useState(() => {
    const stored = loadConversations(agentName)
    if (stored && stored.activeId) return stored.activeId
    return conversations[0]?.id
  })

  const activeConversation = conversations.find(c => c.id === activeId) || conversations[0]

  useDebouncedEffect(() => saveConversations(agentName, conversations, activeId), [agentName, conversations, activeId])

  // Save immediately on unmount
  useEffect(() => {
    return () => {
      saveConversations(agentName, conversations, activeId)
    }
  }, [agentName, conversations, activeId])

  const addConversation = useCallback(() => {
    const conv = createConversation()
    setConversations(prev => [conv, ...prev])
    setActiveId(conv.id)
    return conv
  }, [])

  const switchConversation = useCallback((id) => {
    setActiveId(id)
  }, [])

  const deleteConversation = useCallback((id) => {
    setConversations(prev => {
      if (prev.length <= 1) return prev
      const filtered = prev.filter(c => c.id !== id)
      const newActiveId = id === activeId && filtered.length > 0 ? filtered[0].id : activeId
      if (id === activeId) {
        setActiveId(newActiveId)
      }
      saveConversations(agentName, filtered, newActiveId)
      return filtered
    })
  }, [activeId, agentName])

  const deleteAllConversations = useCallback(() => {
    const conv = createConversation()
    setConversations([conv])
    setActiveId(conv.id)
    saveConversations(agentName, [conv], conv.id)
  }, [agentName])

  const renameConversation = useCallback((id, name) => {
    setConversations(prev => prev.map(c =>
      c.id === id ? { ...c, name, updatedAt: Date.now() } : c
    ))
  }, [])

  const addMessage = useCallback((msg) => {
    setConversations(prev => prev.map(c => {
      if (c.id !== activeId) return c
      const updated = {
        ...c,
        messages: [...c.messages, msg],
        updatedAt: Date.now(),
      }
      // Auto-name from first user message
      if (c.messages.length === 0 && msg.sender === 'user') {
        const text = msg.content || ''
        updated.name = text.slice(0, 40) + (text.length > 40 ? '...' : '')
      }
      return updated
    }))
  }, [activeId])

  // Add a message to a specific conversation by ID, regardless of which is active.
  // Used by SSE handlers to pin responses to the conversation that initiated the request.
  const addMessageToConversation = useCallback((conversationId, msg) => {
    setConversations(prev => prev.map(c => {
      if (c.id !== conversationId) return c
      const updated = {
        ...c,
        messages: [...c.messages, msg],
        updatedAt: Date.now(),
      }
      if (c.messages.length === 0 && msg.sender === 'user') {
        const text = msg.content || ''
        updated.name = text.slice(0, 40) + (text.length > 40 ? '...' : '')
      }
      return updated
    }))
  }, [])

  const clearMessages = useCallback(() => {
    setConversations(prev => {
      const updated = prev.map(c =>
        c.id === activeId ? { ...c, messages: [], updatedAt: Date.now() } : c
      )
      // Save immediately so a page refresh doesn't restore the old messages
      saveConversations(agentName, updated, activeId)
      return updated
    })
  }, [activeId, agentName])

  const getMessages = useCallback(() => {
    return activeConversation?.messages || []
  }, [activeConversation])

  return {
    conversations,
    activeConversation,
    activeId,
    addConversation,
    switchConversation,
    deleteConversation,
    deleteAllConversations,
    renameConversation,
    addMessage,
    addMessageToConversation,
    clearMessages,
    getMessages,
  }
}
