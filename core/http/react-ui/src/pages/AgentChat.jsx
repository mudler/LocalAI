import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { agentsApi } from '../utils/api'

export default function AgentChat() {
  const { name } = useParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [messages, setMessages] = useState([])
  const [input, setInput] = useState('')
  const [processing, setProcessing] = useState(false)
  const messagesEndRef = useRef(null)
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

  return (
    <div className="page agent-chat-page">
      <style>{`
        .agent-chat-page {
          display: flex;
          flex-direction: column;
          height: calc(100vh - 80px);
          padding-bottom: 0 !important;
        }
        .agent-chat-messages {
          flex: 1;
          overflow-y: auto;
          padding: var(--spacing-md);
          display: flex;
          flex-direction: column;
          gap: var(--spacing-sm);
        }
        .agent-chat-message {
          display: flex;
          max-width: 75%;
          word-wrap: break-word;
        }
        .agent-chat-message-user {
          align-self: flex-end;
        }
        .agent-chat-message-agent {
          align-self: flex-start;
        }
        .agent-chat-bubble {
          padding: var(--spacing-sm) var(--spacing-md);
          border-radius: var(--radius-md);
          font-size: 0.9rem;
          line-height: 1.5;
          white-space: pre-wrap;
        }
        .agent-chat-message-user .agent-chat-bubble {
          background: var(--color-bg-tertiary, #e5e7eb);
          color: var(--color-text-primary);
          border-bottom-right-radius: var(--radius-xs, 4px);
        }
        .agent-chat-message-agent .agent-chat-bubble {
          background: var(--color-primary, #3b82f6);
          color: #fff;
          border-bottom-left-radius: var(--radius-xs, 4px);
        }
        .agent-chat-message-system {
          align-self: center;
          max-width: 90%;
        }
        .agent-chat-message-system .agent-chat-bubble {
          background: var(--color-bg-secondary);
          border: 1px solid var(--color-border);
          color: var(--color-text-secondary);
          font-size: 0.8rem;
          font-style: italic;
          padding: var(--spacing-xs) var(--spacing-sm);
        }
        .agent-chat-timestamp {
          font-size: 0.6875rem;
          color: var(--color-text-muted);
          margin-top: 2px;
          padding: 0 var(--spacing-xs);
        }
        .agent-chat-message-user .agent-chat-timestamp {
          text-align: right;
        }
        .agent-chat-input-area {
          display: flex;
          gap: var(--spacing-sm);
          padding: var(--spacing-md);
          border-top: 1px solid var(--color-border);
          background: var(--color-bg-secondary);
          align-items: flex-end;
        }
        .agent-chat-input-area textarea {
          flex: 1;
          min-height: 38px;
          max-height: 150px;
          resize: none;
          overflow-y: auto;
          line-height: 1.5;
          font-family: inherit;
          font-size: inherit;
        }
        .agent-chat-empty {
          flex: 1;
          display: flex;
          align-items: center;
          justify-content: center;
          color: var(--color-text-muted);
          font-size: 0.9rem;
        }
      `}</style>

      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h1 className="page-title">
          <i className="fas fa-robot" style={{ marginRight: 'var(--spacing-xs)' }} />
          {name}
        </h1>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
          <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/agents/${encodeURIComponent(name)}/status`)} title="View status & observables">
            <i className="fas fa-chart-bar" /> Status
          </button>
          <button className="btn btn-secondary btn-sm" onClick={() => setMessages([])} disabled={messages.length === 0} title="Clear chat history">
            <i className="fas fa-eraser" /> Clear
          </button>
        </div>
      </div>

      <div className="agent-chat-messages">
        {messages.length === 0 && !processing && (
          <div className="agent-chat-empty">
            Send a message to start chatting with {name}.
          </div>
        )}
        {messages.map(msg => (
          <div key={msg.id} className={`agent-chat-message agent-chat-message-${msg.sender}`}>
            <div>
              {msg.sender === 'system'
                ? <div className="agent-chat-bubble" dangerouslySetInnerHTML={{ __html: msg.content }} />
                : <div className="agent-chat-bubble">{msg.content}</div>
              }
              <div className="agent-chat-timestamp">
                {new Date(msg.timestamp).toLocaleTimeString()}
              </div>
            </div>
          </div>
        ))}
        {processing && (
          <div className="agent-chat-message agent-chat-message-agent">
            <div>
              <div className="agent-chat-bubble">
                <i className="fas fa-circle-notch fa-spin" /> Thinking...
              </div>
            </div>
          </div>
        )}
        <div ref={messagesEndRef} />
      </div>

      <div className="agent-chat-input-area">
        <textarea
          ref={textareaRef}
          className="input"
          value={input}
          onChange={(e) => {
            setInput(e.target.value)
            // Auto-resize
            const ta = e.target
            ta.style.height = 'auto'
            ta.style.height = Math.min(ta.scrollHeight, 150) + 'px'
          }}
          onKeyDown={handleKeyDown}
          placeholder="Type a message... (Shift+Enter for new line)"
          disabled={processing}
          rows={1}
        />
        <button
          className="btn btn-primary"
          onClick={handleSend}
          disabled={processing || !input.trim()}
        >
          <i className="fas fa-paper-plane" /> Send
        </button>
      </div>
    </div>
  )
}
