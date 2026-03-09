import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useOutletContext, useNavigate } from 'react-router-dom'
import { useChat } from '../hooks/useChat'
import ModelSelector from '../components/ModelSelector'
import { renderMarkdown, highlightAll } from '../utils/markdown'
import { fileToBase64, modelsApi } from '../utils/api'

function relativeTime(ts) {
  if (!ts) return ''
  const diff = Date.now() - ts
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return 'Just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d ago`
  return new Date(ts).toLocaleDateString()
}

function getLastMessagePreview(chat) {
  if (!chat.history || chat.history.length === 0) return ''
  for (let i = chat.history.length - 1; i >= 0; i--) {
    const msg = chat.history[i]
    if (msg.role === 'user' || msg.role === 'assistant') {
      const text = typeof msg.content === 'string' ? msg.content : msg.content?.[0]?.text || ''
      return text.slice(0, 40).replace(/\n/g, ' ')
    }
  }
  return ''
}

function exportChatAsMarkdown(chat) {
  let md = `# ${chat.name}\n\n`
  md += `Model: ${chat.model || 'Unknown'}\n`
  md += `Date: ${new Date(chat.createdAt).toLocaleString()}\n\n---\n\n`
  for (const msg of chat.history) {
    if (msg.role === 'user') {
      const text = typeof msg.content === 'string' ? msg.content : msg.content?.[0]?.text || ''
      md += `## User\n\n${text}\n\n`
    } else if (msg.role === 'assistant') {
      md += `## Assistant\n\n${msg.content}\n\n`
    } else if (msg.role === 'thinking' || msg.role === 'reasoning') {
      md += `<details><summary>Thinking</summary>\n\n${msg.content}\n\n</details>\n\n`
    }
  }
  const blob = new Blob([md], { type: 'text/markdown' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${chat.name.replace(/[^a-zA-Z0-9]/g, '_')}.md`
  a.click()
  URL.revokeObjectURL(url)
}

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
      {Array.isArray(content) && content.filter(c => c.type === 'image_url').map((img, i) => (
        <img key={i} src={img.image_url.url} alt="attached" className="chat-inline-image" />
      ))}
    </>
  )
}

export default function Chat() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const {
    chats, activeChat, activeChatId, isStreaming, streamingChatId, streamingContent,
    streamingReasoning, streamingToolCalls, tokensPerSecond, maxTokensPerSecond,
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
  const [modelInfo, setModelInfo] = useState(null)
  const [showModelInfo, setShowModelInfo] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const messagesEndRef = useRef(null)
  const fileInputRef = useRef(null)
  const messagesRef = useRef(null)
  const thinkingBoxRef = useRef(null)
  const textareaRef = useRef(null)

  // Check MCP availability and fetch model config
  useEffect(() => {
    const model = activeChat?.model
    if (!model) { setMcpAvailable(false); setModelInfo(null); return }
    let cancelled = false
    modelsApi.getConfigJson(model).then(cfg => {
      if (cancelled) return
      setModelInfo(cfg)
      const hasMcp = !!(cfg?.mcp?.remote || cfg?.mcp?.stdio)
      setMcpAvailable(hasMcp)
      if (!hasMcp && activeChat?.mcpMode) {
        updateChatSettings(activeChat.id, { mcpMode: false })
      }
    }).catch(() => { if (!cancelled) { setMcpAvailable(false); setModelInfo(null) } })
    return () => { cancelled = true }
  }, [activeChat?.model])

  // Load initial message from home page
  const homeDataProcessed = useRef(false)
  useEffect(() => {
    if (homeDataProcessed.current) return
    const stored = localStorage.getItem('localai_index_chat_data')
    if (stored) {
      homeDataProcessed.current = true
      try {
        const data = JSON.parse(stored)
        localStorage.removeItem('localai_index_chat_data')
        if (data.message) {
          // Create a new chat when coming from home
          let targetChat = activeChat
          if (data.newChat) {
            targetChat = addChat(data.model || '', '', data.mcpMode || false)
          } else {
            if (data.model && activeChat) {
              updateChatSettings(activeChat.id, { model: data.model })
            }
            if (data.mcpMode && activeChat) {
              updateChatSettings(activeChat.id, { mcpMode: true })
            }
          }
          setInput(data.message)
          if (data.files) setFiles(data.files)
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

  // Auto-grow textarea
  const autoGrowTextarea = useCallback(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 200) + 'px'
  }, [])

  useEffect(() => {
    autoGrowTextarea()
  }, [input, autoGrowTextarea])

  const handleFileChange = useCallback(async (e) => {
    const newFiles = []
    for (const file of e.target.files) {
      const base64 = await fileToBase64(file)
      const entry = { name: file.name, type: file.type, base64 }
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

  const handleRegenerate = useCallback(async () => {
    if (!activeChat || isStreaming) return
    const history = activeChat.history
    let lastUserMsg = null
    let lastUserFiles = null
    for (let i = history.length - 1; i >= 0; i--) {
      if (history[i].role === 'user') {
        lastUserMsg = typeof history[i].content === 'string' ? history[i].content : history[i].content?.[0]?.text || ''
        lastUserFiles = history[i].files || []
        break
      }
    }
    if (!lastUserMsg) return

    // Remove everything after and including the last user message
    const newHistory = []
    let foundLastUser = false
    for (let i = history.length - 1; i >= 0; i--) {
      if (!foundLastUser && history[i].role === 'user') {
        foundLastUser = true
        continue
      }
      if (foundLastUser) {
        newHistory.unshift(history[i])
      }
    }
    updateChatSettings(activeChat.id, { history: newHistory })
    await sendMessage(lastUserMsg, lastUserFiles)
  }, [activeChat, isStreaming, sendMessage, updateChatSettings])

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
    <div className={`chat-layout${sidebarOpen ? '' : ' chat-sidebar-collapsed'}`}>
      {/* Chat sidebar */}
      <div className={`chat-sidebar${sidebarOpen ? '' : ' hidden'}`}>
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
              <i className="fas fa-message" style={{ fontSize: '0.7rem', flexShrink: 0, marginTop: '2px' }} />
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
                <div className="chat-list-item-info">
                  <div className="chat-list-item-top">
                    <span
                      className="chat-list-item-name"
                      onDoubleClick={() => startRename(chat.id, chat.name)}
                    >
                      {streamingChatId === chat.id && <i className="fas fa-circle-notch fa-spin" style={{ marginRight: '6px', fontSize: '0.7rem', opacity: 0.7 }} />}
                      {chat.name}
                    </span>
                    <span className="chat-list-item-time">{relativeTime(chat.updatedAt)}</span>
                  </div>
                  <span className="chat-list-item-preview">
                    {getLastMessagePreview(chat) || 'No messages yet'}
                  </span>
                </div>
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
          <button
            className="btn btn-secondary btn-sm"
            onClick={() => setSidebarOpen(prev => !prev)}
            title={sidebarOpen ? 'Hide chat list' : 'Show chat list'}
            style={{ flexShrink: 0 }}
          >
            <i className={`fas fa-${sidebarOpen ? 'angles-left' : 'angles-right'}`} />
          </button>
          <span className="chat-header-title">{activeChat.name}</span>
          <ModelSelector
            value={activeChat.model}
            onChange={(model) => updateChatSettings(activeChat.id, { model })}
            capability="FLAG_CHAT"
          />
          {activeChat.model && (
            <>
              <button
                className="btn btn-secondary btn-sm"
                onClick={() => setShowModelInfo(!showModelInfo)}
                title="Model info"
              >
                <i className="fas fa-info-circle" />
              </button>
              <button
                className="btn btn-secondary btn-sm"
                onClick={() => navigate(`/model-editor/${encodeURIComponent(activeChat.model)}`)}
                title="Edit model config"
              >
                <i className="fas fa-edit" />
              </button>
            </>
          )}
          {mcpAvailable && (
            <label className="chat-mcp-switch" title="Toggle MCP mode">
              <span className="chat-mcp-switch-label">MCP</span>
              <span className="toggle">
                <input
                  type="checkbox"
                  checked={activeChat.mcpMode || false}
                  onChange={(e) => updateChatSettings(activeChat.id, { mcpMode: e.target.checked })}
                />
                <span className="toggle-slider" />
              </span>
            </label>
          )}
          <div className="chat-header-actions">
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => exportChatAsMarkdown(activeChat)}
              title="Export chat as Markdown"
            >
              <i className="fas fa-download" />
            </button>
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => clearHistory(activeChat.id)}
              title="Clear chat history"
            >
              <i className="fas fa-eraser" />
            </button>
            <button
              className={`btn btn-secondary btn-sm${showSettings ? ' active' : ''}`}
              onClick={() => setShowSettings(!showSettings)}
              title="Settings"
            >
              <i className="fas fa-sliders-h" />
            </button>
          </div>
        </div>

        {/* Model info panel */}
        {showModelInfo && modelInfo && (
          <div className="chat-model-info-panel">
            <div className="chat-model-info-header">
              <span>Model Info: {activeChat.model}</span>
              <button className="btn btn-secondary btn-sm" onClick={() => setShowModelInfo(false)}>
                <i className="fas fa-times" />
              </button>
            </div>
            <div className="chat-model-info-body">
              {modelInfo.backend && <div className="chat-model-info-row"><span>Backend</span><span>{modelInfo.backend}</span></div>}
              {modelInfo.parameters?.model && <div className="chat-model-info-row"><span>Model file</span><span>{modelInfo.parameters.model}</span></div>}
              {modelInfo.context_size > 0 && <div className="chat-model-info-row"><span>Context size</span><span>{modelInfo.context_size}</span></div>}
              {modelInfo.threads > 0 && <div className="chat-model-info-row"><span>Threads</span><span>{modelInfo.threads}</span></div>}
              {(modelInfo.mcp?.remote || modelInfo.mcp?.stdio) && <div className="chat-model-info-row"><span>MCP</span><span className="badge badge-success">Configured</span></div>}
              {modelInfo.template?.chat_message && <div className="chat-model-info-row"><span>Chat template</span><span>Yes</span></div>}
              {modelInfo.gpu_layers > 0 && <div className="chat-model-info-row"><span>GPU layers</span><span>{modelInfo.gpu_layers}</span></div>}
            </div>
          </div>
        )}

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

        {/* Settings slide-out panel */}
        <div className={`chat-settings-overlay${showSettings ? ' open' : ''}`} onClick={() => setShowSettings(false)} />
        <div className={`chat-settings-drawer${showSettings ? ' open' : ''}`}>
          <div className="chat-settings-drawer-header">
            <span>Chat Settings</span>
            <button className="btn btn-secondary btn-sm" onClick={() => setShowSettings(false)}>
              <i className="fas fa-times" />
            </button>
          </div>
          <div className="chat-settings-drawer-body">
            <div className="form-group">
              <label className="form-label">System Prompt</label>
              <textarea
                className="textarea"
                value={activeChat.systemPrompt || ''}
                onChange={(e) => updateChatSettings(activeChat.id, { systemPrompt: e.target.value })}
                rows={3}
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

        {/* Messages */}
        <div className="chat-messages" ref={messagesRef}>
          {activeChat.history.length === 0 && !isStreaming && (
            <div className="chat-empty-state">
              <div className="chat-empty-icon">
                <i className="fas fa-comments" />
              </div>
              <h2 className="chat-empty-title">Start a conversation</h2>
              <p className="chat-empty-text">Type a message below to begin chatting{activeChat.model ? ` with ${activeChat.model}` : ''}.</p>
              <div className="chat-empty-hints">
                <span><i className="fas fa-keyboard" /> Enter to send</span>
                <span><i className="fas fa-level-down-alt" /> Shift+Enter for newline</span>
                <span><i className="fas fa-paperclip" /> Attach files</span>
              </div>
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
                <div className="chat-message-bubble">
                  {msg.role === 'assistant' && activeChat.model && (
                    <span className="chat-message-model">{activeChat.model}</span>
                  )}
                  <div className="chat-message-content">
                    {msg.role === 'user' ? (
                      <UserMessageContent content={msg.content} files={msg.files} />
                    ) : (
                      <div dangerouslySetInnerHTML={{
                        __html: renderMarkdown(typeof msg.content === 'string' ? msg.content : '')
                      }} />
                    )}
                  </div>
                  <div className="chat-message-actions">
                    <button onClick={() => copyMessage(msg.content)} title="Copy">
                      <i className="fas fa-copy" />
                    </button>
                    {msg.role === 'assistant' && i === activeChat.history.length - 1 && !isStreaming && (
                      <button onClick={handleRegenerate} title="Regenerate">
                        <i className="fas fa-rotate" />
                      </button>
                    )}
                  </div>
                </div>
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
              <div className="chat-message-bubble">
                {activeChat.model && (
                  <span className="chat-message-model">{activeChat.model}</span>
                )}
                <div className="chat-message-content">
                  <span dangerouslySetInnerHTML={{ __html: renderMarkdown(streamingContent) }} />
                  <span className="chat-streaming-cursor" />
                </div>
              </div>
            </div>
          )}
          {isStreaming && !streamingContent && !streamingReasoning && streamingToolCalls.length === 0 && (
            <div className="chat-message chat-message-assistant">
              <div className="chat-message-avatar">
                <i className="fas fa-robot" />
              </div>
              <div className="chat-message-bubble">
                <div className="chat-message-content" style={{ color: 'var(--color-text-muted)' }}>
                  <i className="fas fa-circle-notch fa-spin" /> Thinking...
                </div>
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
              className="btn btn-secondary btn-sm chat-attach-btn"
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
              ref={textareaRef}
              className="chat-input"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Type a message..."
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
