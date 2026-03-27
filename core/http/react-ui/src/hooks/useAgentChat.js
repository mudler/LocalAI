import { useState, useCallback, useRef, useEffect } from 'react'
import { generateId } from '../utils/format'

const STORAGE_KEY_PREFIX = 'localai_agent_chats_'
const SAVE_DEBOUNCE_MS = 500

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

  const saveTimerRef = useRef(null)

  const activeConversation = conversations.find(c => c.id === activeId) || conversations[0]

  // Debounced save
  const debouncedSave = useCallback(() => {
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
    saveTimerRef.current = setTimeout(() => {
      saveConversations(agentName, conversations, activeId)
    }, SAVE_DEBOUNCE_MS)
  }, [agentName, conversations, activeId])

  useEffect(() => {
    debouncedSave()
    return () => {
      if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
    }
  }, [conversations, activeId, debouncedSave])

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
      if (id === activeId && filtered.length > 0) {
        setActiveId(filtered[0].id)
      }
      return filtered
    })
  }, [activeId])

  const deleteAllConversations = useCallback(() => {
    const conv = createConversation()
    setConversations([conv])
    setActiveId(conv.id)
  }, [])

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

  const clearMessages = useCallback(() => {
    setConversations(prev => prev.map(c =>
      c.id === activeId ? { ...c, messages: [], updatedAt: Date.now() } : c
    ))
  }, [activeId])

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
    clearMessages,
    getMessages,
  }
}
