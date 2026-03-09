import { useState, useCallback, useRef, useEffect } from 'react'
import { API_CONFIG } from '../utils/config'

const thinkingTagRegex = /<thinking>([\s\S]*?)<\/thinking>|<think>([\s\S]*?)<\/think>/g
const openThinkTagRegex = /<thinking>|<think>/
const closeThinkTagRegex = /<\/thinking>|<\/think>/

function extractThinking(text) {
  let regularContent = ''
  let thinkingContent = ''
  let lastIdx = 0
  let match
  thinkingTagRegex.lastIndex = 0
  while ((match = thinkingTagRegex.exec(text)) !== null) {
    regularContent += text.slice(lastIdx, match.index)
    thinkingContent += match[1] || match[2] || ''
    lastIdx = match.index + match[0].length
  }
  regularContent += text.slice(lastIdx)
  return { regularContent, thinkingContent }
}

const CHATS_STORAGE_KEY = 'localai_chats_data'
const SAVE_DEBOUNCE_MS = 500

function generateId() {
  return Date.now().toString(36) + Math.random().toString(36).slice(2)
}

function loadChats() {
  try {
    const stored = localStorage.getItem(CHATS_STORAGE_KEY)
    if (stored) {
      const data = JSON.parse(stored)
      if (data && Array.isArray(data.chats)) {
        return data
      }
    }
  } catch (_e) {
    localStorage.removeItem(CHATS_STORAGE_KEY)
  }
  return null
}

function saveChats(chats, activeChatId) {
  try {
    const data = {
      chats: chats.map(chat => ({
        id: chat.id,
        name: chat.name,
        model: chat.model,
        history: chat.history,
        systemPrompt: chat.systemPrompt,
        mcpMode: chat.mcpMode,
        temperature: chat.temperature,
        topP: chat.topP,
        topK: chat.topK,
        tokenUsage: chat.tokenUsage,
        contextSize: chat.contextSize,
        createdAt: chat.createdAt,
        updatedAt: chat.updatedAt,
      })),
      activeChatId,
      lastSaved: Date.now(),
    }
    localStorage.setItem(CHATS_STORAGE_KEY, JSON.stringify(data))
  } catch (err) {
    if (err.name === 'QuotaExceededError' || err.code === 22) {
      console.warn('localStorage quota exceeded')
    }
  }
}

function createNewChat(model = '', systemPrompt = '', mcpMode = false) {
  return {
    id: generateId(),
    name: 'New Chat',
    model,
    history: [],
    systemPrompt,
    mcpMode,
    temperature: null,
    topP: null,
    topK: null,
    tokenUsage: { prompt: 0, completion: 0, total: 0 },
    contextSize: null,
    createdAt: Date.now(),
    updatedAt: Date.now(),
  }
}

export function useChat(initialModel = '') {
  const [chats, setChats] = useState(() => {
    const stored = loadChats()
    if (stored && stored.chats.length > 0) return stored.chats
    return [createNewChat(initialModel)]
  })

  const [activeChatId, setActiveChatId] = useState(() => {
    const stored = loadChats()
    if (stored && stored.activeChatId) return stored.activeChatId
    return chats[0]?.id
  })

  const [isStreaming, setIsStreaming] = useState(false)
  const [streamingChatId, setStreamingChatId] = useState(null)
  const [streamingContent, setStreamingContent] = useState('')
  const [streamingReasoning, setStreamingReasoning] = useState('')
  const [streamingToolCalls, setStreamingToolCalls] = useState([])
  const [tokensPerSecond, setTokensPerSecond] = useState(null)
  const [maxTokensPerSecond, setMaxTokensPerSecond] = useState(null)
  const abortControllerRef = useRef(null)
  const saveTimerRef = useRef(null)
  const startTimeRef = useRef(null)
  const tokenCountRef = useRef(0)
  const maxTpsRef = useRef(0)

  const activeChat = chats.find(c => c.id === activeChatId) || chats[0]

  // Debounced save
  const debouncedSave = useCallback(() => {
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
    saveTimerRef.current = setTimeout(() => {
      saveChats(chats, activeChatId)
    }, SAVE_DEBOUNCE_MS)
  }, [chats, activeChatId])

  useEffect(() => {
    debouncedSave()
  }, [chats, activeChatId, debouncedSave])

  const addChat = useCallback((model = '', systemPrompt = '', mcpMode = false) => {
    const chat = createNewChat(model, systemPrompt, mcpMode)
    setChats(prev => [chat, ...prev])
    setActiveChatId(chat.id)
    return chat
  }, [])

  const switchChat = useCallback((chatId) => {
    setActiveChatId(chatId)
    setStreamingContent('')
    setStreamingReasoning('')
    setStreamingToolCalls([])
    setTokensPerSecond(null)
    setMaxTokensPerSecond(null)
  }, [])

  const deleteChat = useCallback((chatId) => {
    setChats(prev => {
      if (prev.length <= 1) return prev
      const filtered = prev.filter(c => c.id !== chatId)
      if (chatId === activeChatId && filtered.length > 0) {
        setActiveChatId(filtered[0].id)
      }
      return filtered
    })
  }, [activeChatId])

  const deleteAllChats = useCallback(() => {
    const chat = createNewChat(activeChat?.model || '')
    setChats([chat])
    setActiveChatId(chat.id)
    setStreamingContent('')
    setStreamingReasoning('')
    setStreamingToolCalls([])
    setTokensPerSecond(null)
    setMaxTokensPerSecond(null)
  }, [activeChat?.model])

  const renameChat = useCallback((chatId, name) => {
    setChats(prev => prev.map(c =>
      c.id === chatId ? { ...c, name, updatedAt: Date.now() } : c
    ))
  }, [])

  const updateChatSettings = useCallback((chatId, settings) => {
    setChats(prev => prev.map(c =>
      c.id === chatId ? { ...c, ...settings, updatedAt: Date.now() } : c
    ))
  }, [])

  const getContextUsagePercent = useCallback(() => {
    if (!activeChat || !activeChat.contextSize) return null
    return Math.min(100, (activeChat.tokenUsage.total / activeChat.contextSize) * 100)
  }, [activeChat])

  const sendMessage = useCallback(async (content, files = [], options = {}) => {
    if (!activeChat) return

    const chatId = activeChat.id
    const model = options.model || activeChat.model
    const temperature = activeChat.temperature
    const topP = activeChat.topP
    const topK = activeChat.topK
    const contextSize = activeChat.contextSize

    // Build user message content
    let messageContent
    const userFiles = []
    if (files.length > 0) {
      messageContent = [{ type: 'text', text: content }]
      for (const file of files) {
        if (file.type?.startsWith('image/')) {
          messageContent.push({
            type: 'image_url',
            image_url: { url: `data:${file.type};base64,${file.base64}` },
          })
          userFiles.push({ name: file.name, type: 'image' })
        } else if (file.type?.startsWith('audio/')) {
          messageContent.push({
            type: 'audio_url',
            audio_url: { url: `data:${file.type};base64,${file.base64}` },
          })
          userFiles.push({ name: file.name, type: 'audio' })
        } else {
          // Text/PDF files - append to content
          userFiles.push({ name: file.name, type: 'file', content: file.textContent || '' })
        }
      }
    } else {
      messageContent = content
    }

    const userMessage = { role: 'user', content: messageContent, files: userFiles.length > 0 ? userFiles : undefined }

    // Update chat with user message
    setChats(prev => prev.map(c => {
      if (c.id !== chatId) return c
      const updated = {
        ...c,
        model,
        history: [...c.history, userMessage],
        updatedAt: Date.now(),
      }
      if (c.history.length === 0 && typeof content === 'string') {
        updated.name = content.slice(0, 40) + (content.length > 40 ? '...' : '')
      }
      return updated
    }))

    // Build messages array for API
    const chat = chats.find(c => c.id === chatId)
    const messages = []
    if (chat?.systemPrompt) {
      messages.push({ role: 'system', content: chat.systemPrompt })
    }
    // Filter out thinking/reasoning/tool_call/tool_result messages
    const historyForApi = (chat?.history || []).filter(m =>
      m.role !== 'thinking' && m.role !== 'reasoning' && m.role !== 'tool_call' && m.role !== 'tool_result'
    )
    messages.push(...historyForApi, { role: 'user', content: messageContent })

    const requestBody = { model, messages, stream: true }
    if (temperature !== null && temperature !== undefined) requestBody.temperature = temperature
    if (topP !== null && topP !== undefined) requestBody.top_p = topP
    if (topK !== null && topK !== undefined) requestBody.top_k = topK
    if (contextSize) requestBody.max_tokens = contextSize

    // Choose endpoint
    const endpoint = activeChat.mcpMode
      ? API_CONFIG.endpoints.mcpChatCompletions
      : API_CONFIG.endpoints.chatCompletions

    const controller = new AbortController()
    abortControllerRef.current = controller
    setIsStreaming(true)
    setStreamingChatId(activeChatId)
    setStreamingContent('')
    setStreamingReasoning('')
    setStreamingToolCalls([])
    setTokensPerSecond(null)
    setMaxTokensPerSecond(null)
    startTimeRef.current = Date.now()
    tokenCountRef.current = 0
    maxTpsRef.current = 0

    let usage = {}
    const newMessages = [] // Accumulate messages to add to history

    if (activeChat.mcpMode) {
      // MCP SSE streaming
      try {
        const timeoutId = setTimeout(() => controller.abort(), 300000) // 5 min timeout
        const response = await fetch(endpoint, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(requestBody),
          signal: controller.signal,
        })
        clearTimeout(timeoutId)

        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`)
        }

        const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
        let buffer = ''
        let assistantContent = ''
        let reasoningContent = ''
        let hasReasoningFromAPI = false
        let currentToolCalls = []

        while (true) {
          const { value, done } = await reader.read()
          if (done) break

          buffer += value
          const lines = buffer.split('\n')
          buffer = lines.pop() || ''

          for (const line of lines) {
            if (!line.trim() || line.startsWith(':')) continue
            if (line === 'data: [DONE]') continue
            if (!line.startsWith('data: ')) continue

            try {
              const eventData = JSON.parse(line.slice(6))

              switch (eventData.type) {
                case 'reasoning':
                  hasReasoningFromAPI = true
                  if (eventData.content) {
                    reasoningContent += eventData.content
                    tokenCountRef.current += Math.ceil(eventData.content.length / 4)
                    setStreamingReasoning(reasoningContent)
                    updateTps()
                  }
                  break

                case 'tool_call':
                  if (eventData.name) {
                    const tc = {
                      type: 'tool_call',
                      name: eventData.name,
                      arguments: eventData.arguments || {},
                      reasoning: eventData.reasoning || '',
                    }
                    currentToolCalls.push(tc)
                    setStreamingToolCalls([...currentToolCalls])
                    newMessages.push({ role: 'tool_call', content: JSON.stringify(tc, null, 2), expanded: false })
                  }
                  break

                case 'tool_result':
                  if (eventData.name) {
                    const tr = {
                      type: 'tool_result',
                      name: eventData.name,
                      result: eventData.result || '',
                    }
                    currentToolCalls.push(tr)
                    setStreamingToolCalls([...currentToolCalls])
                    newMessages.push({ role: 'tool_result', content: JSON.stringify(tr, null, 2), expanded: false })
                  }
                  break

                case 'status':
                  // Logged but not displayed
                  break

                case 'assistant':
                  if (eventData.content) {
                    assistantContent += eventData.content
                    tokenCountRef.current += Math.ceil(eventData.content.length / 4)
                    // Handle thinking tags if no API reasoning
                    if (!hasReasoningFromAPI) {
                      const { regularContent, thinkingContent } = extractThinking(assistantContent)
                      if (thinkingContent) {
                        reasoningContent = thinkingContent
                        setStreamingReasoning(reasoningContent)
                      }
                      setStreamingContent(regularContent)
                    } else {
                      setStreamingContent(assistantContent)
                    }
                    updateTps()
                  }
                  break

                case 'error':
                  newMessages.push({ role: 'assistant', content: `Error: ${eventData.message}` })
                  break
              }
            } catch (_e) {
              // skip malformed JSON
            }
          }
        }

        // Final: add accumulated messages
        let finalContent = assistantContent
        if (!hasReasoningFromAPI) {
          const { regularContent, thinkingContent } = extractThinking(assistantContent)
          finalContent = regularContent
          if (thinkingContent && !reasoningContent) reasoningContent = thinkingContent
        }

        if (reasoningContent) {
          newMessages.unshift({ role: 'thinking', content: reasoningContent, expanded: true })
        }
        if (finalContent) {
          newMessages.push({ role: 'assistant', content: finalContent })
        }
      } catch (err) {
        if (err.name !== 'AbortError') {
          newMessages.push({ role: 'assistant', content: `Error: ${err.message}` })
        }
      }
    } else {
      // Regular SSE streaming
      let rawContent = ''
      let reasoningContent = ''
      let hasReasoningFromAPI = false
      let insideThinkTag = false

      try {
        const response = await fetch(endpoint, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(requestBody),
          signal: controller.signal,
        })

        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`)
        }

        const reader = response.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''

        while (true) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() || ''

          for (const line of lines) {
            const trimmed = line.trim()
            if (!trimmed || !trimmed.startsWith('data: ')) continue
            const data = trimmed.slice(6)
            if (data === '[DONE]') continue

            try {
              const parsed = JSON.parse(data)
              const delta = parsed?.choices?.[0]?.delta

              // Handle reasoning field from API
              if (delta?.reasoning) {
                hasReasoningFromAPI = true
                reasoningContent += delta.reasoning
                tokenCountRef.current++
                setStreamingReasoning(reasoningContent)
                updateTps()
              }

              if (delta?.content) {
                rawContent += delta.content
                tokenCountRef.current++

                if (!hasReasoningFromAPI) {
                  // Check thinking tags
                  if (openThinkTagRegex.test(rawContent) && !closeThinkTagRegex.test(rawContent)) {
                    insideThinkTag = true
                  }
                  if (insideThinkTag && closeThinkTagRegex.test(rawContent)) {
                    insideThinkTag = false
                  }

                  const { regularContent, thinkingContent } = extractThinking(rawContent)
                  if (thinkingContent) {
                    reasoningContent = thinkingContent
                  }

                  if (insideThinkTag) {
                    const lastOpen = Math.max(rawContent.lastIndexOf('<thinking>'), rawContent.lastIndexOf('<think>'))
                    if (lastOpen >= 0) {
                      const partial = rawContent.slice(lastOpen).replace(/<thinking>|<think>/, '')
                      setStreamingReasoning(partial)
                      // Only show content before the unclosed think tag (with prior complete pairs removed)
                      const beforeThink = rawContent.slice(0, lastOpen)
                      const { regularContent: contentBeforeThink } = extractThinking(beforeThink)
                      setStreamingContent(contentBeforeThink)
                    } else {
                      setStreamingContent(regularContent)
                    }
                  } else {
                    setStreamingReasoning(reasoningContent)
                    setStreamingContent(regularContent)
                  }
                } else {
                  setStreamingContent(rawContent)
                }

                updateTps()
              }
              if (parsed?.usage) {
                usage = parsed.usage
              }
            } catch (_e) {
              // skip malformed JSON
            }
          }
        }
      } catch (err) {
        if (err.name !== 'AbortError') {
          rawContent += `\n\nError: ${err.message}`
        }
      }

      // Determine final content
      let finalContent = rawContent
      if (!hasReasoningFromAPI) {
        const { regularContent, thinkingContent } = extractThinking(rawContent)
        finalContent = regularContent
        if (thinkingContent && !reasoningContent) reasoningContent = thinkingContent
      }

      if (reasoningContent) {
        newMessages.push({ role: 'thinking', content: reasoningContent, expanded: true })
      }
      if (finalContent) {
        newMessages.push({ role: 'assistant', content: finalContent })
      }
    }

    // Finalize
    setIsStreaming(false)
    setStreamingChatId(null)
    abortControllerRef.current = null
    setStreamingContent('')
    setStreamingReasoning('')
    setStreamingToolCalls([])

    // Set max tokens/sec badge
    if (maxTpsRef.current > 0) {
      setMaxTokensPerSecond(Math.round(maxTpsRef.current * 10) / 10)
    }

    // Add messages to history
    if (newMessages.length > 0) {
      setChats(prev => prev.map(c => {
        if (c.id !== chatId) return c
        return {
          ...c,
          history: [...c.history, ...newMessages],
          tokenUsage: {
            prompt: usage.prompt_tokens || c.tokenUsage.prompt,
            completion: usage.completion_tokens || c.tokenUsage.completion,
            total: usage.total_tokens || c.tokenUsage.total,
          },
          updatedAt: Date.now(),
        }
      }))
    }
  }, [activeChat, chats])

  function updateTps() {
    const elapsed = (Date.now() - startTimeRef.current) / 1000
    if (elapsed > 0) {
      const tps = tokenCountRef.current / elapsed
      setTokensPerSecond(Math.round(tps * 10) / 10)
      if (tps > maxTpsRef.current) {
        maxTpsRef.current = tps
      }
    }
  }

  const stopGeneration = useCallback(() => {
    if (abortControllerRef.current) {
      abortControllerRef.current.abort()
    }
  }, [])

  const clearHistory = useCallback((chatId) => {
    setChats(prev => prev.map(c =>
      c.id === chatId ? { ...c, history: [], tokenUsage: { prompt: 0, completion: 0, total: 0 }, updatedAt: Date.now() } : c
    ))
  }, [])

  const isActiveStreaming = isStreaming && streamingChatId === activeChatId

  return {
    chats,
    activeChat,
    activeChatId,
    isStreaming: isActiveStreaming,
    streamingChatId: isStreaming ? streamingChatId : null,
    streamingContent: isActiveStreaming ? streamingContent : '',
    streamingReasoning: isActiveStreaming ? streamingReasoning : '',
    streamingToolCalls: isActiveStreaming ? streamingToolCalls : [],
    tokensPerSecond,
    maxTokensPerSecond,
    addChat,
    switchChat,
    deleteChat,
    deleteAllChats,
    renameChat,
    updateChatSettings,
    sendMessage,
    stopGeneration,
    clearHistory,
    getContextUsagePercent,
  }
}
