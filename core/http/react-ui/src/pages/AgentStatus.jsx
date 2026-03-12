import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { agentsApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'

function ObservableSummary({ observable }) {
  const creation = observable?.creation || {}
  const completion = observable?.completion || {}

  let creationMsg = ''
  if (creation?.chat_completion_message?.content) {
    creationMsg = creation.chat_completion_message.content
  } else {
    const messages = creation?.chat_completion_request?.messages
    if (Array.isArray(messages) && messages.length > 0) {
      creationMsg = messages[messages.length - 1]?.content || ''
    }
  }
  if (typeof creationMsg === 'object') creationMsg = 'Multimedia message'

  let funcDef = creation?.function_definition?.name ? `Function: ${creation.function_definition.name}` : ''
  let funcParams = creation?.function_params && Object.keys(creation.function_params).length > 0
    ? `Params: ${JSON.stringify(creation.function_params)}` : ''

  let completionMsg = ''
  let toolCallSummary = ''
  let chatCompletion = completion?.chat_completion_response
  if (!chatCompletion && Array.isArray(completion?.conversation) && completion.conversation.length > 0) {
    chatCompletion = { choices: completion.conversation.map(m => ({ message: m })) }
  }
  if (chatCompletion?.choices?.length > 0) {
    const last = chatCompletion.choices[chatCompletion.choices.length - 1]
    const toolCalls = last?.message?.tool_calls
    if (Array.isArray(toolCalls) && toolCalls.length > 0) {
      toolCallSummary = toolCalls.map(tc => {
        const args = tc.function?.arguments || ''
        return `${tc.function?.name || 'unknown'}(${typeof args === 'string' ? args : JSON.stringify(args)})`
      }).join(', ')
    }
    completionMsg = last?.message?.content || ''
  }

  let actionResult = completion?.action_result ? String(completion.action_result).slice(0, 100) : ''
  let errorMsg = completion?.error || ''
  let filterInfo = ''
  if (completion?.filter_result) {
    const fr = completion.filter_result
    if (fr.has_triggers && !fr.triggered_by) filterInfo = 'Failed to match triggers'
    else if (fr.triggered_by) filterInfo = `Triggered by ${fr.triggered_by}`
    if (fr.failed_by) filterInfo += `${filterInfo ? ', ' : ''}Failed by ${fr.failed_by}`
  }

  const items = []
  if (creationMsg) items.push({ icon: 'fa-comment-dots', text: creationMsg, cls: 'creation' })
  if (funcDef) items.push({ icon: 'fa-code', text: funcDef, cls: 'creation' })
  if (funcParams) items.push({ icon: 'fa-sliders-h', text: funcParams, cls: 'creation' })
  if (toolCallSummary) items.push({ icon: 'fa-wrench', text: toolCallSummary, cls: 'tool-call' })
  if (completionMsg) items.push({ icon: 'fa-robot', text: completionMsg, cls: 'completion' })
  if (actionResult) items.push({ icon: 'fa-bolt', text: actionResult, cls: 'tool-call' })
  if (errorMsg) items.push({ icon: 'fa-exclamation-triangle', text: errorMsg, cls: 'error' })
  if (filterInfo) items.push({ icon: 'fa-shield-alt', text: filterInfo, cls: 'completion' })

  if (items.length === 0) return null

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2, marginTop: 2 }}>
      {items.map((item, i) => (
        <div key={i} className={`as-summary-item as-summary-${item.cls}`} title={item.text}>
          <i className={`fas ${item.icon}`} />
          <span>{item.text}</span>
        </div>
      ))}
    </div>
  )
}

function ObservableCard({ observable, children: childNodes }) {
  const [expanded, setExpanded] = useState(false)
  const isComplete = !!observable.completion
  const hasProgress = observable.progress?.length > 0

  return (
    <div className="as-card">
      <div className="as-card-header" onClick={() => setExpanded(!expanded)}>
        <div className="as-card-title">
          <div className="as-obs-icon">
            <i className={`fas fa-${observable.icon || 'robot'}`} />
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
              <span style={{ fontWeight: 600, fontSize: '0.875rem' }}>{observable.name}</span>
              <span className="as-id">#{observable.id}</span>
              {!isComplete && <i className="fas fa-circle-notch fa-spin" style={{ fontSize: '0.7rem', color: 'var(--color-primary)' }} />}
            </div>
            <ObservableSummary observable={observable} />
          </div>
        </div>
        <i className={`fas fa-chevron-${expanded ? 'up' : 'down'}`} style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem' }} />
      </div>

      {expanded && (
        <div className="as-card-body">
          {/* Children (nested observables) */}
          {childNodes && childNodes.length > 0 && (
            <div style={{ marginBottom: 'var(--spacing-md)' }}>
              <div style={{ fontSize: '0.75rem', fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', marginBottom: 'var(--spacing-xs)' }}>
                Nested Observables
              </div>
              {childNodes}
            </div>
          )}

          {/* Progress entries */}
          {hasProgress && (
            <div style={{ marginBottom: 'var(--spacing-sm)' }}>
              <div className="as-section-label">Progress ({observable.progress.length})</div>
              {observable.progress.map((p, i) => (
                <div key={i} className="as-progress-entry">
                  {p.action_result && <div><span className="as-tag">Action Result</span> {p.action_result}</div>}
                  {p.error && <div className="as-error-text"><span className="as-tag as-tag-error">Error</span> {p.error}</div>}
                  {p.chat_completion_response?.choices?.length > 0 && (
                    <div>
                      <span className="as-tag">Response</span>{' '}
                      {p.chat_completion_response.choices.map((ch, ci) => (
                        <span key={ci}>{ch.message?.content || '(tool call)'}</span>
                      ))}
                    </div>
                  )}
                  {p.agent_state && (
                    <div><span className="as-tag">State</span> {JSON.stringify(p.agent_state)}</div>
                  )}
                </div>
              ))}
            </div>
          )}

          {/* Completion */}
          {observable.completion && (
            <div style={{ marginBottom: 'var(--spacing-sm)' }}>
              <div className="as-section-label">Completion</div>
              {observable.completion.action_result && (
                <div className="as-progress-entry"><span className="as-tag">Action Result</span> {observable.completion.action_result}</div>
              )}
              {observable.completion.error && (
                <div className="as-progress-entry as-error-text"><span className="as-tag as-tag-error">Error</span> {observable.completion.error}</div>
              )}
              {observable.completion.filter_result && (
                <div className="as-progress-entry"><span className="as-tag">Filter</span> {JSON.stringify(observable.completion.filter_result)}</div>
              )}
            </div>
          )}

          {/* Raw JSON */}
          <details className="as-raw">
            <summary>Raw JSON</summary>
            <pre className="as-json">{JSON.stringify(observable, null, 2)}</pre>
          </details>
        </div>
      )}
    </div>
  )
}

function buildTree(observables) {
  const byId = {}
  observables.forEach(obs => { byId[obs.id] = { ...obs, children: [] } })
  const roots = []
  observables.forEach(obs => {
    if (obs.parent_id && byId[obs.parent_id]) {
      byId[obs.parent_id].children.push(byId[obs.id])
    } else {
      roots.push(byId[obs.id])
    }
  })
  return roots
}

function renderTree(nodes) {
  return nodes.map(node => (
    <ObservableCard key={node.id} observable={node}>
      {node.children.length > 0 ? renderTree(node.children) : null}
    </ObservableCard>
  ))
}

export default function AgentStatus() {
  const { name } = useParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [observables, setObservables] = useState([])
  const [status, setStatus] = useState(null)
  const [loading, setLoading] = useState(true)

  const fetchData = useCallback(async () => {
    try {
      const obsData = await agentsApi.observables(name)
      const history = Array.isArray(obsData) ? obsData : (obsData?.History || [])
      setObservables(history)
    } catch (err) {
      addToast(`Failed to load observables: ${err.message}`, 'error')
    }
    try {
      const statusData = await agentsApi.status(name)
      setStatus(statusData)
    } catch (_) {
      // status endpoint may fail if no actions have run yet
    }
    setLoading(false)
  }, [name, addToast])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 5000)
    return () => clearInterval(interval)
  }, [fetchData])

  // SSE for real-time observable updates
  useEffect(() => {
    const url = apiUrl(`/api/agents/${encodeURIComponent(name)}/sse`)
    const es = new EventSource(url)

    es.addEventListener('observable_update', (e) => {
      try {
        const data = JSON.parse(e.data)
        setObservables(prev => {
          const idx = prev.findIndex(o => o.id === data.id)
          if (idx >= 0) {
            const updated = [...prev]
            const existing = updated[idx]
            updated[idx] = {
              ...existing,
              ...data,
              creation: data.creation || existing.creation,
              completion: data.completion || existing.completion,
              progress: (data.progress?.length ?? 0) > (existing.progress?.length ?? 0) ? data.progress : existing.progress,
            }
            return updated
          }
          return [...prev, data]
        })
      } catch (_) { /* ignore */ }
    })

    es.onerror = () => { /* reconnect handled by browser */ }
    return () => es.close()
  }, [name])

  const handleClear = async () => {
    try {
      await agentsApi.clearObservables(name)
      setObservables([])
      addToast('Observables cleared', 'success')
    } catch (err) {
      addToast(`Failed to clear: ${err.message}`, 'error')
    }
  }

  const tree = buildTree(observables)

  return (
    <div className="page">
      <style>{`
        .as-card {
          background: var(--color-bg-secondary);
          border: 1px solid var(--color-border);
          border-radius: var(--radius-md);
          margin-bottom: var(--spacing-sm);
          overflow: hidden;
        }
        .as-card .as-card {
          border-left: 3px solid var(--color-primary);
          margin-left: var(--spacing-md);
        }
        .as-card-header {
          display: flex;
          justify-content: space-between;
          align-items: center;
          padding: 10px var(--spacing-md);
          cursor: pointer;
          gap: var(--spacing-sm);
        }
        .as-card-header:hover { background: var(--color-bg-tertiary); }
        .as-card-title { display: flex; align-items: flex-start; gap: var(--spacing-sm); flex: 1; min-width: 0; }
        .as-obs-icon {
          width: 28px; height: 28px;
          border-radius: var(--radius-md);
          background: var(--color-primary-light);
          color: var(--color-primary);
          display: flex; align-items: center; justify-content: center;
          font-size: 0.75rem; flex-shrink: 0;
        }
        .as-id {
          font-size: 0.6875rem;
          color: var(--color-text-muted);
          font-family: 'JetBrains Mono', monospace;
        }
        .as-summary-item {
          display: flex; align-items: center; gap: 6px;
          font-size: 0.75rem; color: var(--color-text-secondary);
          overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
        }
        .as-summary-item i { font-size: 0.625rem; flex-shrink: 0; }
        .as-summary-creation i { color: var(--color-primary); }
        .as-summary-tool-call i { color: #f59e0b; }
        .as-summary-completion i { color: var(--color-success); }
        .as-summary-error i { color: var(--color-error); }
        .as-card-body {
          padding: var(--spacing-md);
          border-top: 1px solid var(--color-border);
        }
        .as-section-label {
          font-size: 0.6875rem; font-weight: 600; text-transform: uppercase;
          letter-spacing: 0.04em; color: var(--color-text-muted);
          margin-bottom: var(--spacing-xs);
        }
        .as-progress-entry {
          font-size: 0.8125rem; color: var(--color-text-primary);
          padding: 4px 0; border-bottom: 1px solid var(--color-border-subtle);
          word-break: break-word;
        }
        .as-progress-entry:last-child { border-bottom: none; }
        .as-tag {
          display: inline-block; padding: 1px 6px; border-radius: var(--radius-sm);
          font-size: 0.625rem; font-weight: 600; text-transform: uppercase;
          background: var(--color-bg-tertiary); color: var(--color-text-muted);
          margin-right: 4px; vertical-align: middle;
        }
        .as-tag-error { background: var(--color-error); color: #fff; }
        .as-error-text { color: var(--color-error); }
        .as-raw { margin-top: var(--spacing-sm); }
        .as-raw summary { font-size: 0.75rem; color: var(--color-text-muted); cursor: pointer; }
        .as-json {
          background: var(--color-bg-tertiary); border-radius: var(--radius-sm);
          padding: var(--spacing-sm); font-family: 'JetBrains Mono', monospace;
          font-size: 0.75rem; overflow-x: auto; white-space: pre-wrap;
          word-break: break-word; max-height: 300px; overflow-y: auto;
        }
        .as-status-grid {
          display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
          gap: var(--spacing-sm); margin-bottom: var(--spacing-lg);
        }
        .as-status-item {
          background: var(--color-bg-secondary); border: 1px solid var(--color-border);
          border-radius: var(--radius-md); padding: var(--spacing-md);
        }
        .as-status-label {
          font-size: 0.6875rem; text-transform: uppercase; letter-spacing: 0.05em;
          color: var(--color-text-muted); margin-bottom: 4px;
        }
        .as-status-value { font-size: 1rem; font-weight: 600; color: var(--color-text-primary); }
      `}</style>

      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <h1 className="page-title">
            <i className="fas fa-chart-bar" style={{ marginRight: 'var(--spacing-xs)' }} />
            {name} — Status
          </h1>
          <p className="page-subtitle">Agent observables and activity history</p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
          <button className="btn btn-secondary" onClick={() => navigate(`/app/agents/${encodeURIComponent(name)}/chat`)}>
            <i className="fas fa-comment" /> Chat
          </button>
          <button className="btn btn-secondary" onClick={() => navigate(`/app/agents/${encodeURIComponent(name)}/edit`)}>
            <i className="fas fa-edit" /> Edit
          </button>
          <button className="btn btn-secondary" onClick={fetchData}>
            <i className="fas fa-sync" /> Refresh
          </button>
          <button className="btn btn-danger" onClick={handleClear} disabled={observables.length === 0}>
            <i className="fas fa-trash" /> Clear
          </button>
        </div>
      </div>

      {/* Status summary */}
      {status && (
        <div className="as-status-grid">
          {status.state && (
            <div className="as-status-item">
              <div className="as-status-label">State</div>
              <div className="as-status-value">{status.state}</div>
            </div>
          )}
          {status.current_task && (
            <div className="as-status-item">
              <div className="as-status-label">Current Task</div>
              <div className="as-status-value" style={{ fontSize: '0.8125rem', fontWeight: 400 }}>{status.current_task}</div>
            </div>
          )}
          <div className="as-status-item">
            <div className="as-status-label">Observables</div>
            <div className="as-status-value">{observables.length}</div>
          </div>
        </div>
      )}

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <i className="fas fa-spinner fa-spin" style={{ fontSize: '2rem', color: 'var(--color-primary)' }} />
        </div>
      ) : tree.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-chart-bar" /></div>
          <h2 className="empty-state-title">No observables yet</h2>
          <p className="empty-state-text">Send a message to the agent to see its activity here.</p>
          <button className="btn btn-primary" onClick={() => navigate(`/app/agents/${encodeURIComponent(name)}/chat`)}>
            <i className="fas fa-comment" /> Chat with {name}
          </button>
        </div>
      ) : (
        <div>
          {renderTree(tree)}
        </div>
      )}
    </div>
  )
}
