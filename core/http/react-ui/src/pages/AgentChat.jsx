import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useParams, useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { agentsApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { renderMarkdown, highlightAll } from '../utils/markdown'
import { extractCodeArtifacts, extractMetadataArtifacts, renderMarkdownWithArtifacts } from '../utils/artifacts'
import CanvasPanel from '../components/CanvasPanel'
import ResourceCards from '../components/ResourceCards'
import ConfirmDialog from '../components/ConfirmDialog'
import { useAgentChat } from '../hooks/useAgentChat'
import { relativeTime } from '../utils/format'

function getLastMessagePreview(conv) {
  if (!conv.messages || conv.messages.length === 0) return ''
  for (let i = conv.messages.length - 1; i >= 0; i--) {
    const msg = conv.messages[i]
    if (msg.sender === 'user' || msg.sender === 'agent') {
      return (msg.content || '').slice(0, 40).replace(/\n/g, ' ')
    }
  }
  return ''
}

function stripHtml(html) {
  if (!html) return ''
  return html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim()
}

function summarizeStatus(text) {
  const plain = stripHtml(text)
  // Extract a short label from "Thinking: ...", "Reasoning: ...", etc.
  const match = plain.match(/^(Thinking|Reasoning|Action taken|Result)[:\s]*/i)
  if (match) return match[1]
  return plain.length > 60 ? plain.slice(0, 60) + '...' : plain
}

function AgentActivityGroup({ items }) {
  const [expanded, setExpanded] = useState(false)
  if (!items || items.length === 0) return null

  const latest = items[items.length - 1]
  const summary = summarizeStatus(latest.content)

  return (
    <div className="chat-message chat-message-assistant">
      <div className="chat-message-avatar" style={{ background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>
        <i className="fas fa-cogs" />
      </div>
      <div className="chat-activity-group">
        <button className="chat-activity-toggle" onClick={() => setExpanded(!expanded)}>
          <span className="chat-activity-summary">
            {summary}
            {items.length > 1 && <span className="chat-activity-count">+{items.length - 1}</span>}
          </span>
          <i className={`fas fa-chevron-${expanded ? 'up' : 'down'}`} />
        </button>
        {expanded && (
          <div className="chat-activity-details">
            {items.map((item, idx) => (
              <div key={idx} className="chat-activity-item">
                <span className="chat-activity-item-label">{new Date(item.timestamp).toLocaleTimeString()}</span>
                <div className="chat-activity-item-content"
                  dangerouslySetInnerHTML={{ __html: item.content }} />
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

export default function AgentChat() {
  const { name } = useParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [searchParams] = useSearchParams()
  const userId = searchParams.get('user_id') || undefined

  const {
    conversations, activeConversation, activeId,
    addConversation, switchConversation, deleteConversation,
    deleteAllConversations, renameConversation, addMessage, addMessageToConversation, clearMessages,
  } = useAgentChat(name)

  const messages = activeConversation?.messages || []

  const [input, setInput] = useState('')
  const [processingChatId, setProcessingChatId] = useState(null)
  const [canvasMode, setCanvasMode] = useState(false)
  const [canvasOpen, setCanvasOpen] = useState(false)
  const [selectedArtifactId, setSelectedArtifactId] = useState(null)
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [editingName, setEditingName] = useState(null)
  const [editName, setEditName] = useState('')
  const [chatSearch, setChatSearch] = useState('')
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [streamContent, setStreamContent] = useState('')
  const [streamReasoning, setStreamReasoning] = useState('')
  const [streamToolCalls, setStreamToolCalls] = useState([])
  const messagesEndRef = useRef(null)
  const messagesRef = useRef(null)
  const textareaRef = useRef(null)
  const eventSourceRef = useRef(null)
  const messageIdCounter = useRef(0)
  const addMessageRef = useRef(addMessage)
  addMessageRef.current = addMessage
  const addMessageToConvRef = useRef(addMessageToConversation)
  addMessageToConvRef.current = addMessageToConversation
  const activeIdRef = useRef(activeId)
  activeIdRef.current = activeId
  // Tracks which conversation initiated the current request — SSE responses
  // are pinned to this ID so switching tabs doesn't misdirect them.
  const processingChatIdRef = useRef(null)
  // Maps backend messageID → conversationId for robust SSE routing across navigations.
  const pendingRequestsRef = useRef(new Map())

  const processing = processingChatId === activeId

  const nextId = useCallback(() => {
    messageIdCounter.current += 1
    return messageIdCounter.current
  }, [])

  // Connect to SSE endpoint — only reconnect when agent name changes
  useEffect(() => {
    const url = apiUrl(agentsApi.sseUrl(name, userId))
    const es = new EventSource(url)
    eventSourceRef.current = es

    es.addEventListener('json_message', (e) => {
      try {
        const data = JSON.parse(e.data)
        const sender = data.sender || (data.role === 'user' ? 'user' : 'agent')
        // Skip user message echoes — already added locally in handleSend
        if (sender === 'user') return
        const msg = {
          id: nextId(),
          sender,
          content: data.content || data.message || '',
          timestamp: data.timestamp ? Math.floor(data.timestamp / 1e6) : Date.now(),
        }
        if (data.metadata && Object.keys(data.metadata).length > 0) {
          msg.metadata = data.metadata
        }
        // Route to conversation: try messageID mapping first, then processingChatIdRef, then active
        const msgId = data.message_id || ''
        const baseId = msgId.replace(/-agent$/, '')
        const targetId = pendingRequestsRef.current.get(baseId)
          || processingChatIdRef.current
          || activeIdRef.current
        addMessageToConvRef.current(targetId, msg)
        // Clear streaming + processing state when the final agent message arrives
        if (sender === 'agent') {
          pendingRequestsRef.current.delete(baseId)
          processingChatIdRef.current = null
          setProcessingChatId(null)
          setStreamContent('')
          setStreamReasoning('')
          setStreamToolCalls([])
        }
      } catch (_err) {
        // ignore malformed messages
      }
    })

    es.addEventListener('json_message_status', (e) => {
      try {
        const data = JSON.parse(e.data)
        if (data.status === 'processing') {
          // Track which conversation is processing so responses go to the right place.
          // Only set if not already pinned by handleSend (avoids race when user switches conversations).
          if (!processingChatIdRef.current) {
            processingChatIdRef.current = activeIdRef.current
            setProcessingChatId(activeIdRef.current)
          }
          setStreamContent('')
          setStreamReasoning('')
          setStreamToolCalls([])
        } else if (data.status === 'completed') {
          // Don't clear processingChatIdRef, processingChatId, or streaming state here —
          // they'll be cleared when the agent's json_message arrives,
          // so reasoning and tool calls remain visible until the response replaces them
          // and late-arriving messages still route to the correct conversation.
        }
      } catch (_err) {
        // ignore
      }
    })

    es.addEventListener('stream_event', (e) => {
      try {
        const data = JSON.parse(e.data)
        if (data.type === 'reasoning') {
          setStreamReasoning(prev => prev + (data.content || ''))
        } else if (data.type === 'content') {
          setStreamContent(prev => prev + (data.content || ''))
        } else if (data.type === 'tool_call') {
          const name = data.tool_name || ''
          const args = data.tool_args || ''
          setStreamToolCalls(prev => {
            if (name) {
              return [...prev, { name, args }]
            }
            if (prev.length === 0) return prev
            const updated = [...prev]
            updated[updated.length - 1] = { ...updated[updated.length - 1], args: updated[updated.length - 1].args + args }
            return updated
          })
        } else if (data.type === 'tool_result') {
          const tname = data.tool_name || ''
          setStreamToolCalls(prev => {
            const updated = [...prev]
            const idx = updated.findLastIndex(tc => tc.name === tname && !tc.result)
            if (idx >= 0) {
              updated[idx] = { ...updated[idx], result: data.tool_result || 'done' }
            }
            return updated
          })
        } else if (data.type === 'done') {
          // Content will be finalized by json_message event
        }
      } catch (_err) {
        // ignore
      }
    })

    es.addEventListener('status', (e) => {
      const text = e.data
      if (!text) return
      const targetId = processingChatIdRef.current || activeIdRef.current
      addMessageToConvRef.current(targetId, {
        id: nextId(),
        sender: 'system',
        content: text,
        timestamp: Date.now(),
      })
    })

    es.addEventListener('json_error', (e) => {
      try {
        const data = JSON.parse(e.data)
        addToast(data.error || data.message || 'Agent error', 'error')
      } catch (_err) {
        addToast('Agent error', 'error')
      }
      processingChatIdRef.current = null
      setProcessingChatId(null)
    })

    es.onerror = () => {
      addToast('SSE connection lost, attempting to reconnect...', 'warning')
    }

    return () => {
      es.close()
      eventSourceRef.current = null
      processingChatIdRef.current = null
      pendingRequestsRef.current.clear()
    }
  }, [name, userId, addToast, nextId])

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, streamContent, streamReasoning, streamToolCalls])

  // Highlight code blocks
  useEffect(() => {
    if (messagesRef.current) highlightAll(messagesRef.current)
  }, [messages])

  const agentMessages = useMemo(() => messages.filter(m => m.sender === 'agent'), [messages])
  const codeArtifacts = useMemo(
    () => canvasMode ? extractCodeArtifacts(agentMessages, 'sender', 'agent') : [],
    [agentMessages, canvasMode]
  )
  const metaArtifacts = useMemo(
    () => canvasMode ? extractMetadataArtifacts(messages, name) : [],
    [messages, canvasMode, name]
  )
  const artifacts = useMemo(() => [...codeArtifacts, ...metaArtifacts], [codeArtifacts, metaArtifacts])

  const prevArtifactCountRef = useRef(0)
  useEffect(() => {
    prevArtifactCountRef.current = artifacts.length
  }, [activeId])
  useEffect(() => {
    if (artifacts.length > prevArtifactCountRef.current && artifacts.length > 0) {
      setSelectedArtifactId(artifacts[artifacts.length - 1].id)
      if (!canvasOpen) setCanvasOpen(true)
    }
    prevArtifactCountRef.current = artifacts.length
  }, [artifacts])

  // Event delegation for artifact cards
  useEffect(() => {
    const el = messagesRef.current
    if (!el || !canvasMode) return
    const handler = (e) => {
      const openBtn = e.target.closest('.artifact-card-open')
      const downloadBtn = e.target.closest('.artifact-card-download')
      const card = e.target.closest('.artifact-card')
      if (downloadBtn) {
        e.stopPropagation()
        const id = downloadBtn.dataset.artifactId
        const artifact = artifacts.find(a => a.id === id)
        if (artifact?.code) {
          const blob = new Blob([artifact.code], { type: 'text/plain' })
          const url = URL.createObjectURL(blob)
          const a = document.createElement('a')
          a.href = url
          a.download = artifact.title || 'download.txt'
          a.click()
          URL.revokeObjectURL(url)
        }
        return
      }
      if (openBtn || card) {
        const id = (openBtn || card).dataset.artifactId
        if (id) {
          setSelectedArtifactId(id)
          setCanvasOpen(true)
        }
      }
    }
    el.addEventListener('click', handler)
    return () => el.removeEventListener('click', handler)
  }, [canvasMode, artifacts])

  const openArtifactById = useCallback((id) => {
    setSelectedArtifactId(id)
    setCanvasOpen(true)
  }, [])

  const handleSend = useCallback(async () => {
    const msg = input.trim()
    if (!msg || processing) return
    setInput('')
    if (textareaRef.current) textareaRef.current.style.height = 'auto'
    // Add user message locally immediately (like standard chat)
    addMessage({ id: nextId(), sender: 'user', content: msg, timestamp: Date.now() })
    setProcessingChatId(activeId)
    processingChatIdRef.current = activeId
    try {
      const resp = await agentsApi.chat(name, msg, userId)
      // Map backend messageID → conversation so SSE events route correctly
      if (resp && resp.message_id) {
        pendingRequestsRef.current.set(resp.message_id, activeId)
      }
    } catch (err) {
      addToast(`Failed to send message: ${err.message}`, 'error')
      processingChatIdRef.current = null
      setProcessingChatId(null)
    }
  }, [input, processing, name, activeId, addToast, userId, addMessage, nextId])

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const copyMessage = (content) => {
    navigator.clipboard.writeText(content)
    addToast('Copied to clipboard', 'success', 2000)
  }

  const senderToRole = (sender) => {
    if (sender === 'agent') return 'assistant'
    if (sender === 'user') return 'user'
    return 'system'
  }

  const startRename = (id, currentName) => {
    setEditingName(id)
    setEditName(currentName)
  }

  const finishRename = () => {
    if (editingName && editName.trim()) {
      renameConversation(editingName, editName.trim())
    }
    setEditingName(null)
  }

  const filteredConversations = chatSearch.trim()
    ? conversations.filter(c => {
      const q = chatSearch.toLowerCase()
      if ((c.name || '').toLowerCase().includes(q)) return true
      return c.messages?.some(m => {
        return (m.content || '').toLowerCase().includes(q)
      })
    })
    : conversations

  return (
    <div className={`chat-layout${sidebarOpen ? '' : ' chat-sidebar-collapsed'}`}>
      {/* Conversation sidebar */}
      <div className={`chat-sidebar${sidebarOpen ? '' : ' hidden'}`}>
        <div className="chat-sidebar-header">
          <button className="btn btn-primary btn-sm" style={{ flex: 1 }} onClick={() => addConversation()}>
            <i className="fas fa-plus" /> New Chat
          </button>
          <button
            className="btn btn-secondary btn-sm"
            onClick={() => {
              setConfirmDialog({
                title: 'Delete All Conversations',
                message: 'Delete all conversations? This cannot be undone.',
                confirmLabel: 'Delete All',
                danger: true,
                onConfirm: () => { setConfirmDialog(null); deleteAllConversations() },
              })
            }}
            title="Delete all conversations"
            style={{ padding: '6px 8px' }}
          >
            <i className="fas fa-trash" />
          </button>
        </div>

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
          {filteredConversations.map(conv => (
            <div
              key={conv.id}
              className={`chat-list-item ${conv.id === activeId ? 'active' : ''}`}
              onClick={() => switchConversation(conv.id)}
            >
              <i className="fas fa-message" style={{ fontSize: '0.7rem', flexShrink: 0, marginTop: '2px' }} />
              {editingName === conv.id ? (
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
                      onDoubleClick={() => startRename(conv.id, conv.name)}
                    >
                      {processingChatId === conv.id && <i className="fas fa-circle-notch fa-spin" style={{ marginRight: '6px', fontSize: '0.7rem', opacity: 0.7 }} />}
                      {conv.name}
                    </span>
                    <span className="chat-list-item-time">{relativeTime(conv.updatedAt)}</span>
                  </div>
                  <span className="chat-list-item-preview">
                    {getLastMessagePreview(conv) || 'No messages yet'}
                  </span>
                </div>
              )}
              <div className="chat-list-item-actions">
                <button
                  onClick={(e) => { e.stopPropagation(); startRename(conv.id, conv.name) }}
                  title="Rename"
                >
                  <i className="fas fa-edit" />
                </button>
                {conversations.length > 1 && (
                  <button
                    className="chat-list-item-delete"
                    onClick={(e) => { e.stopPropagation(); deleteConversation(conv.id) }}
                    title="Delete conversation"
                  >
                    <i className="fas fa-trash" />
                  </button>
                )}
              </div>
            </div>
          ))}
          {filteredConversations.length === 0 && chatSearch && (
            <div style={{ padding: 'var(--spacing-sm)', textAlign: 'center', color: 'var(--color-text-muted)', fontSize: '0.8rem' }}>
              No conversations match your search
            </div>
          )}
        </div>
      </div>

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
        <span className="chat-header-title">
          <i className="fas fa-robot" style={{ marginRight: 'var(--spacing-xs)' }} />
          {name}
        </span>
        <div className="chat-header-actions">
          <label className="canvas-mode-toggle" title="Extract code blocks and media into a side panel for preview, copy, and download">
            <i className="fas fa-columns" />
            <span className="canvas-mode-label">Canvas</span>
            <span className="toggle">
              <input
                type="checkbox"
                checked={canvasMode}
                onChange={(e) => {
                  setCanvasMode(e.target.checked)
                  if (!e.target.checked) setCanvasOpen(false)
                }}
              />
              <span className="toggle-slider" />
            </span>
          </label>
          {canvasMode && artifacts.length > 0 && !canvasOpen && (
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => { setSelectedArtifactId(artifacts[0]?.id); setCanvasOpen(true) }}
              title="Open canvas panel"
            >
              <i className="fas fa-layer-group" /> {artifacts.length}
            </button>
          )}
          <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/agents/${encodeURIComponent(name)}/status${userId ? `?user_id=${encodeURIComponent(userId)}` : ''}`)} title="View status & observables">
            <i className="fas fa-chart-bar" /> Status
          </button>
          <button className="btn btn-secondary btn-sm" onClick={() => clearMessages()} disabled={messages.length === 0} title="Clear chat history">
            <i className="fas fa-eraser" /> Clear
          </button>
        </div>
      </div>

      {/* Messages */}
      <div className="chat-messages" ref={messagesRef}>
        {messages.length === 0 && !processing && (
          <div className="chat-empty-state">
            <div className="chat-empty-icon">
              <i className="fas fa-robot" />
            </div>
            <h2 className="chat-empty-title">Chat with {name}</h2>
            <p className="chat-empty-text">Send a message to start a conversation with this agent.</p>
            <div className="chat-empty-hints">
              <span><i className="fas fa-keyboard" /> Enter to send</span>
              <span><i className="fas fa-level-down-alt" /> Shift+Enter for newline</span>
            </div>
          </div>
        )}
        {(() => {
          const elements = []
          let systemBuf = []
          const flushSystem = (key) => {
            if (systemBuf.length > 0) {
              elements.push(<AgentActivityGroup key={`sag-${key}`} items={[...systemBuf]} />)
              systemBuf = []
            }
          }
          messages.forEach((msg, idx) => {
            const role = senderToRole(msg.sender)
            if (role === 'system') {
              systemBuf.push(msg)
              return
            }
            flushSystem(idx)
            elements.push(
              <div key={msg.id} className={`chat-message chat-message-${role}`}>
                <div className="chat-message-avatar">
                  <i className={`fas ${role === 'user' ? 'fa-user' : 'fa-robot'}`} />
                </div>
                <div className="chat-message-bubble">
                  <div className="chat-message-content">
                    {role === 'user' ? (
                      <div dangerouslySetInnerHTML={{ __html: msg.content.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\n/g, '<br>') }} />
                    ) : (
                      <div dangerouslySetInnerHTML={{
                        __html: canvasMode
                          ? renderMarkdownWithArtifacts(msg.content, idx)
                          : renderMarkdown(msg.content)
                      }} />
                    )}
                  </div>
                  {role === 'assistant' && msg.metadata && (
                    <ResourceCards
                      metadata={msg.metadata}
                      messageIndex={idx}
                      agentName={name}
                      onOpenArtifact={openArtifactById}
                    />
                  )}
                  <div className="chat-message-actions">
                    <button onClick={() => copyMessage(msg.content)} title="Copy">
                      <i className="fas fa-copy" />
                    </button>
                  </div>
                  <div className="chat-message-timestamp">
                    {new Date(msg.timestamp).toLocaleTimeString()}
                  </div>
                </div>
              </div>
            )
          })
          flushSystem('end')
          return elements
        })()}
        {processing && (streamReasoning || streamContent || streamToolCalls.length > 0) && (
          <div className="chat-message chat-message-assistant">
            <div className="chat-message-avatar">
              <i className="fas fa-robot" />
            </div>
            <div className="chat-message-bubble">
              {streamReasoning && (
                <details className="chat-activity-group" open={!streamContent} style={{ marginBottom: streamContent ? 'var(--spacing-sm)' : 0 }}>
                  <summary className="chat-activity-toggle" style={{ cursor: 'pointer' }}>
                    <span className={`chat-activity-summary${!streamContent ? ' chat-activity-shimmer' : ''}`}>
                      {streamContent ? 'Thinking' : 'Thinking...'}
                    </span>
                  </summary>
                  <div className="chat-activity-details">
                    <div className="chat-activity-item chat-activity-thinking">
                      <div className="chat-activity-item-content chat-activity-live"
                        dangerouslySetInnerHTML={{ __html: renderMarkdown(streamReasoning) }} />
                    </div>
                  </div>
                </details>
              )}
              {streamToolCalls.length > 0 && (
                <div className="chat-activity-group" style={{ marginBottom: 'var(--spacing-sm)' }}>
                  {streamToolCalls.map((tc, idx) => (
                    <details key={idx} className="chat-activity-item chat-activity-tool-call" style={{ padding: 'var(--spacing-xs) var(--spacing-sm)' }} open={!tc.result}>
                      <summary className="chat-activity-item-label" style={{ cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
                        <i className={`fas ${tc.result ? 'fa-check' : 'fa-bolt'}`} />
                        <strong>{tc.name}</strong>
                        <span style={{ opacity: 0.5, fontSize: '0.85em' }}>
                          {tc.result ? 'done' : 'calling...'}
                        </span>
                      </summary>
                      {tc.args && (
                        <pre style={{ margin: '4px 0', fontSize: '0.75rem', opacity: 0.8, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                          {(() => { try { return JSON.stringify(JSON.parse(tc.args), null, 2) } catch { return tc.args } })()}
                        </pre>
                      )}
                      {tc.result && (
                        <pre style={{ margin: '4px 0', fontSize: '0.75rem', opacity: 0.7, whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: '200px', overflow: 'auto' }}>
                          {tc.result}
                        </pre>
                      )}
                    </details>
                  ))}
                </div>
              )}
              {streamContent && (
                <div className="chat-message-content">
                  <span dangerouslySetInnerHTML={{ __html: renderMarkdown(streamContent) }} />
                  <span className="chat-streaming-cursor" />
                </div>
              )}
            </div>
          </div>
        )}
        {processing && !streamReasoning && !streamContent && streamToolCalls.length === 0 && (
          <div className="chat-message chat-message-assistant">
            <div className="chat-message-avatar" style={{ background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>
              <i className="fas fa-cogs" />
            </div>
            <div className="chat-activity-group chat-activity-streaming">
              <div className="chat-activity-toggle" style={{ cursor: 'default' }}>
                <span className="chat-activity-summary chat-activity-shimmer">Working...</span>
              </div>
            </div>
          </div>
        )}
        <div ref={messagesEndRef} />
      </div>

      {/* Input area */}
      <div className="chat-input-area">
        <div className="chat-input-wrapper">
          <textarea
            ref={textareaRef}
            className="chat-input"
            value={input}
            onChange={(e) => {
              setInput(e.target.value)
              const ta = e.target
              ta.style.height = 'auto'
              ta.style.height = Math.min(ta.scrollHeight, 150) + 'px'
            }}
            onKeyDown={handleKeyDown}
            placeholder="Type a message..."
            disabled={processing}
            rows={1}
          />
          <button
            className="chat-send-btn"
            onClick={handleSend}
            disabled={processing || !input.trim()}
          >
            <i className="fas fa-paper-plane" />
          </button>
        </div>
      </div>
    </div>
    {canvasOpen && artifacts.length > 0 && (
      <CanvasPanel
        artifacts={artifacts}
        selectedId={selectedArtifactId}
        onSelect={setSelectedArtifactId}
        onClose={() => setCanvasOpen(false)}
      />
    )}
    <ConfirmDialog
      open={!!confirmDialog}
      title={confirmDialog?.title}
      message={confirmDialog?.message}
      confirmLabel={confirmDialog?.confirmLabel}
      danger={confirmDialog?.danger}
      onConfirm={confirmDialog?.onConfirm}
      onCancel={() => setConfirmDialog(null)}
    />
    </div>
  )
}
