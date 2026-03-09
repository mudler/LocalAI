import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { agentsApi } from '../utils/api'
import { renderMarkdown, highlightAll } from '../utils/markdown'
import DOMPurify from 'dompurify'

export default function AgentChat() {
  const { name } = useParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [messages, setMessages] = useState([])
  const [input, setInput] = useState('')
  const [processing, setProcessing] = useState(false)
  const messagesEndRef = useRef(null)
  const messagesRef = useRef(null)
  const textareaRef = useRef(null)
  const eventSourceRef = useRef(null)
  const messageIdCounter = useRef(0)

  const nextId = useCallback(() => {
    messageIdCounter.current += 1
    return messageIdCounter.current
  }, [])

  // Connect to SSE endpoint
  useEffect(() => {
    const url = `/api/agents/${encodeURIComponent(name)}/sse`
    const es = new EventSource(url)
    eventSourceRef.current = es

    es.addEventListener('json_message', (e) => {
      try {
        const data = JSON.parse(e.data)
        setMessages(prev => [...prev, {
          id: nextId(),
          sender: data.sender || (data.role === 'user' ? 'user' : 'agent'),
          content: data.content || data.message || '',
          timestamp: data.timestamp || Date.now(),
        }])
      } catch (_err) {
        // ignore malformed messages
      }
    })

    es.addEventListener('json_message_status', (e) => {
      try {
        const data = JSON.parse(e.data)
        if (data.status === 'processing') {
          setProcessing(true)
        } else if (data.status === 'completed') {
          setProcessing(false)
        }
      } catch (_err) {
        // ignore
      }
    })

    es.addEventListener('status', (e) => {
      const text = e.data
      if (!text) return
      setMessages(prev => [...prev, {
        id: nextId(),
        sender: 'system',
        content: text,
        timestamp: Date.now(),
      }])
    })

    es.addEventListener('json_error', (e) => {
      try {
        const data = JSON.parse(e.data)
        addToast(data.error || data.message || 'Agent error', 'error')
      } catch (_err) {
        addToast('Agent error', 'error')
      }
      setProcessing(false)
    })

    es.onerror = () => {
      addToast('SSE connection lost, attempting to reconnect...', 'warning')
    }

    return () => {
      es.close()
      eventSourceRef.current = null
    }
  }, [name, addToast, nextId])

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // Highlight code blocks
  useEffect(() => {
    if (messagesRef.current) highlightAll(messagesRef.current)
  }, [messages])

  const handleSend = useCallback(async () => {
    const msg = input.trim()
    if (!msg || processing) return
    setInput('')
    if (textareaRef.current) textareaRef.current.style.height = 'auto'
    setProcessing(true)
    try {
      await agentsApi.chat(name, msg)
    } catch (err) {
      addToast(`Failed to send message: ${err.message}`, 'error')
      setProcessing(false)
    }
  }, [input, processing, name, addToast])

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

  return (
    <div className="chat-main">
      {/* Header */}
      <div className="chat-header">
        <span className="chat-header-title">
          <i className="fas fa-robot" style={{ marginRight: 'var(--spacing-xs)' }} />
          {name}
        </span>
        <div className="chat-header-actions">
          <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/agents/${encodeURIComponent(name)}/status`)} title="View status & observables">
            <i className="fas fa-chart-bar" /> Status
          </button>
          <button className="btn btn-secondary btn-sm" onClick={() => setMessages([])} disabled={messages.length === 0} title="Clear chat history">
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
        {messages.map(msg => {
          const role = senderToRole(msg.sender)

          if (role === 'system') {
            return (
              <div key={msg.id} className="chat-message chat-message-system">
                <div className="chat-message-bubble">
                  <div className="chat-message-content" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(msg.content) }} />
                  <div className="chat-message-timestamp">
                    {new Date(msg.timestamp).toLocaleTimeString()}
                  </div>
                </div>
              </div>
            )
          }

          return (
            <div key={msg.id} className={`chat-message chat-message-${role}`}>
              <div className="chat-message-avatar">
                <i className={`fas ${role === 'user' ? 'fa-user' : 'fa-robot'}`} />
              </div>
              <div className="chat-message-bubble">
                <div className="chat-message-content">
                  {role === 'user' ? (
                    <div dangerouslySetInnerHTML={{ __html: msg.content.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\n/g, '<br>') }} />
                  ) : (
                    <div dangerouslySetInnerHTML={{ __html: renderMarkdown(msg.content) }} />
                  )}
                </div>
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
        })}
        {processing && (
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
  )
}
