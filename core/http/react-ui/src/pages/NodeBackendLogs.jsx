import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useParams, useOutletContext, Link, useNavigate } from 'react-router-dom'
import { nodesApi } from '../utils/api'
import { formatTimestamp } from '../utils/format'
import { apiUrl } from '../utils/basePath'
import LoadingSpinner from '../components/LoadingSpinner'

function wsUrl(path) {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}${apiUrl(path)}`
}

const STREAM_BADGE = {
  stdout: { bg: 'var(--color-info-light)', color: 'var(--color-log-info)', label: 'stdout' },
  stderr: { bg: 'var(--color-error-light)', color: 'var(--color-log-stderr)', label: 'stderr' },
}

export default function NodeBackendLogs() {
  const { nodeId, modelId: rawModelId } = useParams()
  const modelId = decodeURIComponent(rawModelId || '')
  const { addToast } = useOutletContext()
  const navigate = useNavigate()

  // The route param can be a bare model name ("qwen3-0.6b") OR a per-replica
  // process key ("qwen3-0.6b#0"). The worker's BackendLogStore treats them
  // differently — bare = aggregate across replicas, suffixed = exact replica.
  // Surface that distinction so operators know what they're looking at.
  const replicaSepIdx = modelId.indexOf('#')
  const baseModelName = replicaSepIdx >= 0 ? modelId.slice(0, replicaSepIdx) : modelId
  const replicaIndex = replicaSepIdx >= 0 ? parseInt(modelId.slice(replicaSepIdx + 1), 10) : null
  const isMerged = replicaIndex === null

  const [lines, setLines] = useState([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState('all')
  const [autoScroll, setAutoScroll] = useState(true)
  const [showDetails, setShowDetails] = useState(true)
  const [wsConnected, setWsConnected] = useState(false)
  const [nodeName, setNodeName] = useState('')
  // Replicas of this base model on this node — drives whether the
  // merged-vs-replica toggle is rendered. Single-replica deployments
  // never see the toggle (no decision to make).
  const [replicas, setReplicas] = useState([])
  const logContainerRef = useRef(null)
  const wsRef = useRef(null)
  const reconnectTimerRef = useRef(null)
  const loadingRef = useRef(true)
  const pendingLinesRef = useRef([])
  const flushTimerRef = useRef(null)

  useEffect(() => { loadingRef.current = loading }, [loading])

  // Fetch node name for display
  useEffect(() => {
    if (nodeId) {
      nodesApi.get(nodeId).then(n => setNodeName(n.name || nodeId)).catch(() => {})
    }
  }, [nodeId])

  // Fetch the replica list for this base model on this node so we know
  // whether to render the merged-vs-replica toggle. Cheap query; runs once
  // per (nodeId, baseModelName) change.
  useEffect(() => {
    if (!nodeId || !baseModelName) return
    nodesApi.getModels(nodeId)
      .then(arr => {
        const reps = (Array.isArray(arr) ? arr : [])
          .filter(m => m.model_name === baseModelName)
          .map(m => m.replica_index ?? 0)
          .sort((a, b) => a - b)
        setReplicas(reps)
      })
      .catch(() => setReplicas([]))
  }, [nodeId, baseModelName])

  // Auto-scroll to bottom when new lines arrive
  useEffect(() => {
    if (autoScroll && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
    }
  }, [lines, autoScroll])

  // WebSocket connection with reconnect
  const connectWebSocket = useCallback(() => {
    if (wsRef.current && wsRef.current.readyState <= 1) return

    const url = wsUrl(`/ws/nodes/${nodeId}/backend-logs/${encodeURIComponent(modelId)}`)
    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => {
      setWsConnected(true)
      setLoading(false)
    }

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data)
        if (msg.type === 'initial') {
          setLines(Array.isArray(msg.lines) ? msg.lines : [])
          setLoading(false)
        } else if (msg.type === 'line' && msg.line) {
          pendingLinesRef.current.push(msg.line)
          if (!flushTimerRef.current) {
            flushTimerRef.current = requestAnimationFrame(() => {
              const batch = pendingLinesRef.current
              pendingLinesRef.current = []
              flushTimerRef.current = null
              setLines(prev => prev.concat(batch))
            })
          }
        }
      } catch {
        // ignore parse errors
      }
    }

    ws.onclose = () => {
      setWsConnected(false)
      reconnectTimerRef.current = setTimeout(connectWebSocket, 3000)
    }

    ws.onerror = () => {
      if (loadingRef.current) {
        nodesApi.getBackendLogLines(nodeId, modelId)
          .then(data => setLines(Array.isArray(data) ? data : []))
          .catch(() => {})
          .finally(() => setLoading(false))
      }
    }
  }, [nodeId, modelId])

  useEffect(() => {
    connectWebSocket()
    return () => {
      if (wsRef.current) wsRef.current.close()
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current)
      if (flushTimerRef.current) cancelAnimationFrame(flushTimerRef.current)
    }
  }, [connectWebSocket])

  const filteredLines = useMemo(
    () => filter === 'all' ? lines : lines.filter(l => l.stream === filter),
    [lines, filter]
  )

  const handleExport = () => {
    const blob = new Blob([JSON.stringify(filteredLines, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `node-backend-logs-${modelId}-${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  if (!nodeId || !modelId) {
    return (
      <div className="page page--wide">
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-terminal" /></div>
          <h2 className="empty-state-title">No node/model selected</h2>
          <p className="empty-state-text">
            View backend logs from the{' '}
            <Link to="/app/nodes" style={{ color: 'var(--color-primary)' }}>Nodes page</Link>.
          </p>
        </div>
      </div>
    )
  }

  // Show the merged/per-replica toggle only when this model has > 1 replica
  // on this node. Single-replica deployments don't see a control they can't
  // meaningfully use.
  const showReplicaToggle = replicas.length > 1

  return (
    <div className="page page--wide">
      <div className="page-header">
        <div>
          <h1 className="page-title" style={{ marginBottom: 0 }}>
            <i className="fas fa-terminal" style={{ fontSize: '0.8em', marginRight: 'var(--spacing-sm)' }} />
            {baseModelName}
            {!isMerged && (
              <span
                className="cell-mono"
                style={{
                  marginLeft: 'var(--spacing-sm)',
                  fontSize: '0.6875rem',
                  fontWeight: 500,
                  padding: '2px 8px',
                  borderRadius: 'var(--radius-sm)',
                  background: 'var(--color-bg-tertiary)',
                  border: '1px solid var(--color-border-subtle)',
                  color: 'var(--color-text-secondary)',
                  verticalAlign: 'middle',
                }}
              >
                replica {replicaIndex}
              </span>
            )}
            {isMerged && replicas.length > 1 && (
              <span
                className="cell-mono"
                style={{
                  marginLeft: 'var(--spacing-sm)',
                  fontSize: '0.6875rem',
                  fontWeight: 500,
                  padding: '2px 8px',
                  borderRadius: 'var(--radius-sm)',
                  background: 'var(--color-bg-tertiary)',
                  border: '1px solid var(--color-border-subtle)',
                  color: 'var(--color-text-secondary)',
                  verticalAlign: 'middle',
                }}
              >
                merged · {replicas.length} replicas
              </span>
            )}
          </h1>
          <p className="page-subtitle" style={{ marginTop: 'var(--spacing-xs)' }}>
            Backend logs from node <strong>{nodeName || nodeId}</strong>
            {' '}<Link to="/app/nodes" style={{ color: 'var(--color-primary)', fontSize: '0.8125rem' }}>(back to nodes)</Link>
          </p>
        </div>
      </div>

      {showReplicaToggle && (
        <div role="radiogroup" aria-label="Replica scope" className="segmented" style={{ marginBottom: 'var(--spacing-sm)' }}>
          {replicas.map(idx => (
            <button
              key={idx}
              type="button"
              role="radio"
              aria-checked={replicaIndex === idx}
              className={`segmented__item${replicaIndex === idx ? ' is-active' : ''}`}
              onClick={() => navigate(`/app/node-backend-logs/${nodeId}/${encodeURIComponent(baseModelName + '#' + idx)}`)}
            >
              Replica {idx}
            </button>
          ))}
          <button
            type="button"
            role="radio"
            aria-checked={isMerged}
            className={`segmented__item${isMerged ? ' is-active' : ''}`}
            onClick={() => navigate(`/app/node-backend-logs/${nodeId}/${encodeURIComponent(baseModelName)}`)}
            title="Show an interleaved timeline of all replicas — useful for comparing replica behavior side-by-side"
          >
            <i className="fas fa-layer-group" aria-hidden="true" /> All merged
          </button>
        </div>
      )}

      {/* Toolbar */}
      <div style={{ display: 'flex', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)', alignItems: 'center', flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', gap: 2 }}>
          {['all', 'stdout', 'stderr'].map(f => (
            <button
              key={f}
              className={`btn btn-sm ${filter === f ? 'btn-primary' : 'btn-secondary'}`}
              onClick={() => setFilter(f)}
            >
              {f === 'all' ? 'All' : f}
            </button>
          ))}
        </div>
        <button className="btn btn-secondary btn-sm" onClick={handleExport} disabled={filteredLines.length === 0}>
          <i className="fas fa-download" /> Export
        </button>
        <button
          className={`btn btn-sm ${showDetails ? 'btn-secondary' : 'btn-primary'}`}
          onClick={() => setShowDetails(prev => !prev)}
          title={showDetails ? 'Hide timestamps and stream labels for easier copying' : 'Show timestamps and stream labels'}
        >
          <i className={`fas ${showDetails ? 'fa-eye-slash' : 'fa-eye'}`} /> {showDetails ? 'Text only' : 'Show details'}
        </button>
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', fontSize: '0.8125rem' }}>
          <span style={{
            display: 'inline-block',
            width: 8, height: 8,
            borderRadius: '50%',
            background: wsConnected ? 'var(--color-success)' : 'var(--color-text-muted)',
          }} />
          <span style={{ color: 'var(--color-text-secondary)' }}>
            {wsConnected ? 'Live' : 'Reconnecting...'}
          </span>
          <label style={{ display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer', marginLeft: 'var(--spacing-sm)' }}>
            <input
              type="checkbox"
              checked={autoScroll}
              onChange={(e) => setAutoScroll(e.target.checked)}
            />
            <span style={{ color: 'var(--color-text-secondary)' }}>Auto-scroll</span>
          </label>
        </div>
      </div>

      {/* Log output */}
      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <LoadingSpinner size="lg" />
        </div>
      ) : filteredLines.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-terminal" /></div>
          <h2 className="empty-state-title">No log lines</h2>
          <p className="empty-state-text">
            {filter !== 'all'
              ? `No ${filter} output. Try switching to "All".`
              : 'Log output will appear here as the backend process runs.'}
          </p>
        </div>
      ) : (
        <div
          ref={logContainerRef}
          style={{
            background: 'var(--color-bg-primary)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-md)',
            overflow: 'auto',
            maxHeight: 'calc(100vh - 280px)',
            fontFamily: 'var(--font-mono)',
            fontSize: '0.75rem',
            lineHeight: '1.5',
          }}
        >
          {filteredLines.map((line, i) => {
            const badge = STREAM_BADGE[line.stream] || STREAM_BADGE.stdout
            return (
              <div
                key={i}
                data-log-line
                data-timestamp={line.timestamp}
                style={{
                  display: 'flex',
                  gap: showDetails ? 'var(--spacing-sm)' : undefined,
                  padding: '2px var(--spacing-sm)',
                  borderBottom: '1px solid var(--color-border-subtle, rgba(255,255,255,0.03))',
                  alignItems: 'flex-start',
                }}
              >
                {showDetails && (<>
                  <span style={{ color: 'var(--color-text-muted)', flexShrink: 0, minWidth: 90 }}>
                    {formatTimestamp(line.timestamp)}
                  </span>
                  <span style={{
                    background: badge.bg, color: badge.color,
                    padding: '0 4px', borderRadius: 'var(--radius-sm)',
                    fontSize: '0.625rem', fontWeight: 500, flexShrink: 0,
                    lineHeight: '1.5',
                  }}>
                    {badge.label}
                  </span>
                </>)}
                <span style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', flex: 1 }}>
                  {line.text}
                </span>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
