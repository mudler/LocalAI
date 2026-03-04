import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import { useChat } from '../hooks/useChat'
import ModelSelector from '../components/ModelSelector'
import { renderMarkdown, highlightAll } from '../utils/markdown'
import { fileToBase64, modelsApi } from '../utils/api'

function ThinkingMessage({ msg, onToggle }) {
  const contentRef = useRef(null)

  useEffect(() => {
    if (msg.expanded && contentRef.current) {
      highlightAll(contentRef.current)
    }
  }, [msg.expanded, msg.content])

  return (
    <div className="chat-thinking-box">
      <button className="chat-thinking-header" onClick={onToggle}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
          <i className="fas fa-brain" style={{ color: 'var(--color-primary)' }} />
          <span className="chat-thinking-label">Thinking</span>
          <span style={{ fontSize: '0.7rem', color: 'var(--color-text-muted)' }}>
            ({(msg.content || '').split('\n').length} lines)
          </span>
        </div>
        <i className={`fas fa-chevron-${msg.expanded ? 'up' : 'down'}`} style={{ color: 'var(--color-primary)', fontSize: '0.75rem' }} />
      </button>
      {msg.expanded && (
        <div
          ref={contentRef}
          className="chat-thinking-content"
          dangerouslySetInnerHTML={{ __html: renderMarkdown(msg.content || '') }}
        />
      )}
    </div>
  )
}

function ToolCallMessage({ msg, onToggle }) {
  let parsed = null
  try { parsed = JSON.parse(msg.content) } catch (_e) { /* ignore */ }
  const isCall = msg.role === 'tool_call'

  return (
    <div className={`chat-tool-box chat-tool-box-${isCall ? 'call' : 'result'}`}>
      <button className="chat-tool-header" onClick={onToggle}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
          <i className={`fas ${isCall ? 'fa-wrench' : 'fa-check-circle'}`}
            style={{ color: isCall ? 'var(--color-accent)' : 'var(--color-success)' }} />
          <span className="chat-tool-label">
            {isCall ? 'Tool Call' : 'Tool Result'}: {parsed?.name || 'unknown'}
          </span>
        </div>
        <i className={`fas fa-chevron-${msg.expanded ? 'up' : 'down'}`}
          style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem' }} />
      </button>
      {msg.expanded && (
        <div className="chat-tool-content">
          <pre><code>{msg.content}</code></pre>
        </div>
      )}
    </div>
  )
}

function StreamingToolCalls({ toolCalls }) {
  if (!toolCalls || toolCalls.length === 0) return null
  return toolCalls.map((tc, i) => {
    const isCall = tc.type === 'tool_call'
    return (
      <div key={i} className={`chat-tool-box chat-tool-box-${isCall ? 'call' : 'result'}`}>
        <div className="chat-tool-header" style={{ cursor: 'default' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
            <i className={`fas ${isCall ? 'fa-wrench' : 'fa-check-circle'}`}
              style={{ color: isCall ? 'var(--color-accent)' : 'var(--color-success)' }} />
            <span className="chat-tool-label">
              {isCall ? 'Tool Call' : 'Tool Result'}: {tc.name}
            </span>
            <span className="chat-streaming-cursor" />
          </div>
        </div>
        <div className="chat-tool-content">
          <pre><code>{JSON.stringify(isCall ? tc.arguments : tc.result, null, 2)}</code></pre>
        </div>
      </div>
    )
  })
}

function UserMessageContent({ content, files }) {
  const text = typeof content === 'string' ? content : content?.[0]?.text || ''
  return (
    <>
      <div dangerouslySetInnerHTML={{ __html: text.replace(/\n/g, '<br>') }} />
      {files && files.length > 0 && (
        <div className="chat-message-files">
          {files.map((f, i) => (
            <span key={i} className="chat-file-inline">
              <i className={`fas ${f.type === 'image' ? 'fa-image' : f.type === 'audio' ? 'fa-headphones' : 'fa-file'}`} />
              {f.name}
            </span>
          ))}
        </div>
      )}
      {/* Inline images from multimodal content */}
      {Array.isArray(content) && content.filter(c => c.type === 'image_url').map((img, i) => (
        <img key={i} src={img.image_url.url} alt="attached" className="chat-inline-image" />
      ))}
    </>
  )
}

export default function Chat() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const {
    chats, activeChat, activeChatId, isStreaming, streamingContent, streamingReasoning,
    streamingToolCalls, tokensPerSecond, maxTokensPerSecond,
    addChat, switchChat, deleteChat, deleteAllChats, renameChat, updateChatSettings,
    sendMessage, stopGeneration, clearHistory, getContextUsagePercent,
  } = useChat(urlModel || '')

  const [input, setInput] = useState('')
  const [files, setFiles] = useState([])
  const [showSettings, setShowSettings] = useState(false)
  const [editingName, setEditingName] = useState(null)
  const [editName, setEditName] = useState('')
  const [mcpAvailable, setMcpAvailable] = useState(false)
  const [chatSearch, setChatSearch] = useState('')
  const messagesEndRef = useRef(null)
  const fileInputRef = useRef(null)
  const messagesRef = useRef(null)
  const thinkingBoxRef = useRef(null)

  // Check MCP availability
  useEffect(() => {
    const model = activeChat?.model
    if (!model) { setMcpAvailable(false); return }
    let cancelled = false
    modelsApi.getConfig(model).then(cfg => {
      if (cancelled) return
      const hasMcp = !!(cfg?.mcp?.remote || cfg?.mcp?.stdio)
      setMcpAvailable(hasMcp)
      if (!hasMcp && activeChat?.mcpMode) {
        updateChatSettings(activeChat.id, { mcpMode: false })
      }
    }).catch(() => { if (!cancelled) setMcpAvailable(false) })
    return () => { cancelled = true }
  }, [activeChat?.model])

  // Load initial message from home page
  useEffect(() => {
    const stored = localStorage.getItem('localai_index_chat_data')
    if (stored) {
      try {
        const data = JSON.parse(stored)
        localStorage.removeItem('localai_index_chat_data')
        if (data.message) {
          setInput(data.message)
          if (data.files) setFiles(data.files)
          if (data.model && activeChat) {
            updateChatSettings(activeChat.id, { model: data.model })
          }
          setTimeout(() => {
            const submitBtn = document.getElementById('chat-submit-btn')
            submitBtn?.click()
          }, 100)
        }
      } catch (_e) { /* ignore */ }
    }
  }, [])

  // Auto-scroll
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [activeChat?.history, streamingContent, streamingReasoning, streamingToolCalls])

  // Scroll streaming thinking box
  useEffect(() => {
    if (thinkingBoxRef.current) {
      thinkingBoxRef.current.scrollTop = thinkingBoxRef.current.scrollHeight
    }
  }, [streamingReasoning])

  // Highlight code blocks
  useEffect(() => {
    if (messagesRef.current) {
      highlightAll(messagesRef.current)
    }
  }, [activeChat?.history, streamingContent])

  const handleFileChange = useCallback(async (e) => {
    const newFiles = []
    for (const file of e.target.files) {
      const base64 = await fileToBase64(file)
      const entry = { name: file.name, type: file.type, base64 }
      // For text/PDF files, read text content
      if (!file.type.startsWith('image/') && !file.type.startsWith('audio/')) {
        entry.textContent = await file.text().catch(() => '')
      }
      newFiles.push(entry)
    }
    setFiles(prev => [...prev, ...newFiles])
    e.target.value = ''
  }, [])

  const handleSend = useCallback(async () => {
    const msg = input.trim()
    if (!msg && files.length === 0) return
    if (!activeChat?.model) {
      addToast('Please select a model', 'warning')
      return
    }
    setInput('')
    setFiles([])
    await sendMessage(msg, files)
  }, [input, files, activeChat, sendMessage, addToast])

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const startRename = (chatId, currentName) => {
    setEditingName(chatId)
    setEditName(currentName)
  }

  const finishRename = () => {
    if (editingName && editName.trim()) {
      renameChat(editingName, editName.trim())
    }
    setEditingName(null)
  }

  const copyMessage = (content) => {
    const text = typeof content === 'string' ? content : content?.[0]?.text || ''
    navigator.clipboard.writeText(text)
    addToast('Copied to clipboard', 'success', 2000)
  }

  // Filter chats by search
  const filteredChats = chatSearch.trim()
    ? chats.filter(c => {
      const q = chatSearch.toLowerCase()
      if ((c.name || '').toLowerCase().includes(q)) return true
      return c.history?.some(m => {
        const t = typeof m.content === 'string' ? m.content : m.content?.[0]?.text || ''
        return t.toLowerCase().includes(q)
      })
    })
    : chats

  const contextPercent = getContextUsagePercent()

  if (!activeChat) return null

  return (
    <div className="chat-layout">
      {/* Chat sidebar */}
      <div className="chat-sidebar">
        <div className="chat-sidebar-header">
          <button className="btn btn-primary btn-sm" style={{ flex: 1 }} onClick={() => addChat(activeChat.model)}>
            <i className="fas fa-plus" /> New Chat
          </button>
          <button
            className="btn btn-secondary btn-sm"
            onClick={() => {
              if (confirm('Delete all chats? This cannot be undone.')) deleteAllChats()
            }}
            title="Delete all chats"
            style={{ padding: '6px 8px' }}
          >
            <i className="fas fa-trash" />
          </button>
        </div>

        {/* Chat search */}
        <div style={{ padding: '0 var(--spacing-sm)' }}>
          <div className="chat-search-wrapper">
            <i className="fas fa-search chat-search-icon" />
            <input
              className="chat-search-input"
              type="text"
              value={chatSearch}
              onChange={(e) => setChatSearch(e.target.value)}
              placeholder="Search conversations..."
            />
            {chatSearch && (
              <button className="chat-search-clear" onClick={() => setChatSearch('')}>
                <i className="fas fa-times" />
              </button>
            )}
          </div>
        </div>

        <div className="chat-list">
          {filteredChats.map(chat => (
            <div
              key={chat.id}
              className={`chat-list-item ${chat.id === activeChatId ? 'active' : ''}`}
              onClick={() => switchChat(chat.id)}
            >
              <i className="fas fa-message" style={{ fontSize: '0.7rem' }} />
              {editingName === chat.id ? (
                <input
                  className="input"
                  value={editName}
                  onChange={(e) => setEditName(e.target.value)}
                  onBlur={finishRename}
                  onKeyDown={(e) => e.key === 'Enter' && finishRename()}
                  autoFocus
                  onClick={(e) => e.stopPropagation()}
                  style={{ padding: '2px 4px', fontSize: '0.8125rem' }}
                />
              ) : (
                <span
                  className="chat-list-item-name"
                  onDoubleClick={() => startRename(chat.id, chat.name)}
                >
                  {chat.name}
                </span>
              )}
              <div className="chat-list-item-actions">
                <button
                  onClick={(e) => { e.stopPropagation(); startRename(chat.id, chat.name) }}
                  title="Rename"
                >
                  <i className="fas fa-edit" />
                </button>
                {chats.length > 1 && (
                  <button
                    className="chat-list-item-delete"
                    onClick={(e) => { e.stopPropagation(); deleteChat(chat.id) }}
                    title="Delete chat"
                  >
                    <i className="fas fa-trash" />
                  </button>
                )}
              </div>
            </div>
          ))}
          {filteredChats.length === 0 && chatSearch && (
            <div style={{ padding: 'var(--spacing-sm)', textAlign: 'center', color: 'var(--color-text-muted)', fontSize: '0.8rem' }}>
              No conversations match your search
            </div>
          )}
        </div>
      </div>

      {/* Chat main area */}
      <div className="chat-main">
        {/* Header */}
        <div className="chat-header">
          <ModelSelector
            value={activeChat.model}
            onChange={(model) => updateChatSettings(activeChat.id, { model })}
          />
          <div className="chat-header-actions">
            {mcpAvailable && (
              <label className={`chat-mcp-toggle ${activeChat.mcpMode ? 'active' : ''}`}>
                <input
                  type="checkbox"
                  checked={activeChat.mcpMode || false}
                  onChange={(e) => updateChatSettings(activeChat.id, { mcpMode: e.target.checked })}
                  style={{ display: 'none' }}
                />
                <i className="fas fa-plug" style={{ fontSize: '0.625rem' }} />
                MCP
              </label>
            )}
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => clearHistory(activeChat.id)}
              title="Clear chat history"
            >
              <i className="fas fa-eraser" />
            </button>
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => setShowSettings(!showSettings)}
              title="Settings"
            >
              <i className="fas fa-sliders-h" />
            </button>
          </div>
        </div>

        {/* Context window progress bar */}
        {contextPercent !== null && (
          <div className="chat-context-bar">
            <div className="chat-context-progress"
              style={{
                width: `${contextPercent}%`,
                background: contextPercent > 90 ? 'var(--color-error)' : contextPercent > 70 ? 'var(--color-warning)' : 'var(--color-primary)',
              }}
            />
            <span className="chat-context-label">
              Context: {Math.round(contextPercent)}%
              {activeChat.tokenUsage.total > 0 && ` (${activeChat.tokenUsage.total} tokens)`}
            </span>
          </div>
        )}

        {/* Settings panel */}
        {showSettings && (
          <div className="chat-settings-panel">
            <div className="chat-settings-grid">
              <div className="form-group" style={{ gridColumn: '1 / -1' }}>
                <label className="form-label">System Prompt</label>
                <textarea
                  className="textarea"
                  value={activeChat.systemPrompt || ''}
                  onChange={(e) => updateChatSettings(activeChat.id, { systemPrompt: e.target.value })}
                  rows={2}
                  placeholder="You are a helpful assistant..."
                />
              </div>
              <div className="form-group">
                <label className="form-label">
                  Temperature {activeChat.temperature !== null ? `(${activeChat.temperature})` : ''}
                </label>
                <input
                  type="range" min="0" max="2" step="0.1"
                  value={activeChat.temperature ?? 0.7}
                  onChange={(e) => updateChatSettings(activeChat.id, { temperature: parseFloat(e.target.value) })}
                  className="chat-slider"
                />
                <div className="chat-slider-labels"><span>0</span><span>2</span></div>
              </div>
              <div className="form-group">
                <label className="form-label">
                  Top P {activeChat.topP !== null ? `(${activeChat.topP})` : ''}
                </label>
                <input
                  type="range" min="0" max="1" step="0.05"
                  value={activeChat.topP ?? 0.9}
                  onChange={(e) => updateChatSettings(activeChat.id, { topP: parseFloat(e.target.value) })}
                  className="chat-slider"
                />
                <div className="chat-slider-labels"><span>0</span><span>1</span></div>
              </div>
              <div className="form-group">
                <label className="form-label">
                  Top K {activeChat.topK !== null ? `(${activeChat.topK})` : ''}
                </label>
                <input
                  type="range" min="1" max="100" step="1"
                  value={activeChat.topK ?? 40}
                  onChange={(e) => updateChatSettings(activeChat.id, { topK: parseInt(e.target.value) })}
                  className="chat-slider"
                />
                <div className="chat-slider-labels"><span>1</span><span>100</span></div>
              </div>
              <div className="form-group">
                <label className="form-label">Context Size</label>
                <input
                  type="number"
                  className="input"
                  value={activeChat.contextSize || ''}
                  onChange={(e) => updateChatSettings(activeChat.id, { contextSize: parseInt(e.target.value) || null })}
                  placeholder="2048"
                />
              </div>
            </div>
          </div>
        )}

        {/* Messages */}
        <div className="chat-messages" ref={messagesRef}>
          {activeChat.history.length === 0 && !isStreaming && (
            <div className="empty-state">
              <div className="empty-state-icon"><i className="fas fa-comments" /></div>
              <h2 className="empty-state-title">Start a conversation</h2>
              <p className="empty-state-text">Type a message below to begin chatting with {activeChat.model || 'the AI'}.</p>
            </div>
          )}
          {activeChat.history.map((msg, i) => {
            if (msg.role === 'thinking' || msg.role === 'reasoning') {
              return (
                <ThinkingMessage key={i} msg={msg} onToggle={() => {
                  const newHistory = [...activeChat.history]
                  newHistory[i] = { ...newHistory[i], expanded: !newHistory[i].expanded }
                  updateChatSettings(activeChat.id, { history: newHistory })
                }} />
              )
            }
            if (msg.role === 'tool_call' || msg.role === 'tool_result') {
              return (
                <ToolCallMessage key={i} msg={msg} onToggle={() => {
                  const newHistory = [...activeChat.history]
                  newHistory[i] = { ...newHistory[i], expanded: !newHistory[i].expanded }
                  updateChatSettings(activeChat.id, { history: newHistory })
                }} />
              )
            }
            return (
              <div key={i} className={`chat-message chat-message-${msg.role}`}>
                <div className="chat-message-avatar">
                  <i className={`fas ${msg.role === 'user' ? 'fa-user' : 'fa-robot'}`} />
                </div>
                <div className="chat-message-content">
                  {msg.role === 'user' ? (
                    <UserMessageContent content={msg.content} files={msg.files} />
                  ) : (
                    <div dangerouslySetInnerHTML={{
                      __html: renderMarkdown(typeof msg.content === 'string' ? msg.content : '')
                    }} />
                  )}
                </div>
                <button className="chat-message-copy" onClick={() => copyMessage(msg.content)} title="Copy">
                  <i className="fas fa-copy" />
                </button>
              </div>
            )
          })}

          {/* Streaming reasoning box */}
          {isStreaming && streamingReasoning && (
            <div className="chat-thinking-box chat-thinking-box-streaming">
              <div className="chat-thinking-header" style={{ cursor: 'default' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
                  <i className="fas fa-brain" style={{ color: 'var(--color-primary)' }} />
                  <span className="chat-thinking-label">Thinking</span>
                  <span className="chat-streaming-cursor" />
                </div>
              </div>
              <div
                ref={thinkingBoxRef}
                className="chat-thinking-content"
                style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}
              >
                {streamingReasoning}
              </div>
            </div>
          )}

          {/* Streaming tool calls */}
          {isStreaming && <StreamingToolCalls toolCalls={streamingToolCalls} />}

          {/* Streaming message */}
          {isStreaming && streamingContent && (
            <div className="chat-message chat-message-assistant">
              <div className="chat-message-avatar">
                <i className="fas fa-robot" />
              </div>
              <div className="chat-message-content">
                <span dangerouslySetInnerHTML={{ __html: renderMarkdown(streamingContent) }} />
                <span className="chat-streaming-cursor" />
              </div>
            </div>
          )}
          {isStreaming && !streamingContent && !streamingReasoning && streamingToolCalls.length === 0 && (
            <div className="chat-message chat-message-assistant">
              <div className="chat-message-avatar">
                <i className="fas fa-robot" />
              </div>
              <div className="chat-message-content" style={{ color: 'var(--color-text-muted)' }}>
                <i className="fas fa-circle-notch fa-spin" /> Thinking...
              </div>
            </div>
          )}
          <div ref={messagesEndRef} />
        </div>

        {/* Token info bar */}
        {(tokensPerSecond || maxTokensPerSecond || activeChat.tokenUsage?.total > 0) && (
          <div className="chat-token-info">
            {tokensPerSecond !== null && <span><i className="fas fa-tachometer-alt" /> {tokensPerSecond} tok/s</span>}
            {maxTokensPerSecond !== null && !isStreaming && (
              <span className="chat-max-tps-badge">
                <i className="fas fa-bolt" /> Peak: {maxTokensPerSecond} tok/s
              </span>
            )}
            {activeChat.tokenUsage?.total > 0 && (
              <span>
                <i className="fas fa-coins" /> {activeChat.tokenUsage.prompt}p + {activeChat.tokenUsage.completion}c = {activeChat.tokenUsage.total}
              </span>
            )}
          </div>
        )}

        {/* File badges */}
        {files.length > 0 && (
          <div className="chat-files">
            {files.map((f, i) => (
              <span key={i} className="chat-file-badge">
                <i className={`fas ${f.type?.startsWith('image/') ? 'fa-image' : f.type?.startsWith('audio/') ? 'fa-headphones' : 'fa-file'}`} />
                {f.name}
                <button onClick={() => setFiles(prev => prev.filter((_, idx) => idx !== i))}>
                  <i className="fas fa-xmark" />
                </button>
              </span>
            ))}
          </div>
        )}

        {/* Input area */}
        <div className="chat-input-area">
          <div className="chat-input-wrapper">
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => fileInputRef.current?.click()}
              title="Attach file"
            >
              <i className="fas fa-paperclip" />
            </button>
            <input
              ref={fileInputRef}
              type="file"
              multiple
              accept="image/*,audio/*,application/pdf,.txt,.md,.csv,.json"
              style={{ display: 'none' }}
              onChange={handleFileChange}
            />
            <textarea
              className="chat-input"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Type a message... (Enter to send, Shift+Enter for newline)"
              rows={1}
              disabled={isStreaming}
            />
            {isStreaming ? (
              <button className="chat-stop-btn" onClick={stopGeneration} title="Stop generating">
                <i className="fas fa-stop" />
              </button>
            ) : (
              <button
                id="chat-submit-btn"
                className="chat-send-btn"
                onClick={handleSend}
                disabled={!input.trim() && files.length === 0}
              >
                <i className="fas fa-paper-plane" />
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
