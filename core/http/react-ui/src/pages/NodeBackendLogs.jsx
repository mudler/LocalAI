import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useParams, useOutletContext, Link, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import { formatTimestamp } from '../utils/format'
import { apiUrl } from '../utils/basePath'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'

function wsUrl(path) {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}${apiUrl(path)}`
}

const STREAM_BADGE = { stdout: 'stdout', stderr: 'stderr' }

export default function NodeBackendLogs() {
  const { nodeId, modelId = '' } = useParams()
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation('admin')
  const requestedReturnPath = location.state?.from
  const returnPath = typeof requestedReturnPath === 'string' && /^\/app\/nodes(?:\/[^/?#]+)?$/.test(requestedReturnPath) ? requestedReturnPath : '/app/nodes'

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

  const handleClear = () => {
    setLines([])
    pendingLinesRef.current = []
    addToast(t('backendLogs.cleared'), 'success')
  }

  if (!nodeId || !modelId) {
    return (
      <div className="page page--wide">
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-terminal" /></div>
          <h2 className="empty-state-title">{t('backendLogs.noSelectionTitle')}</h2>
          <p className="empty-state-text">
            {t('backendLogs.noSelectionPrefix')}{' '}
            <Link to="/app/nodes" className="text-primary">{t('backendLogs.nodesPage')}</Link>.
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
    <div className="page page--wide node-backend-logs">
      <PageHeader
        title={
          <>
            <i className="fas fa-terminal node-backend-logs__title-icon" aria-hidden="true" />
            {baseModelName}
            {!isMerged && (
              <span className="node-backend-logs__scope-tag">{t('backendLogs.replica', { number: replicaIndex + 1 })}</span>
            )}
            {isMerged && replicas.length > 1 && (
              <span className="node-backend-logs__scope-tag">
                {t('backendLogs.merged', { count: replicas.length })}
              </span>
            )}
          </>
        }
        supporting={
          <>
            {t('backendLogs.fromNode')} <strong>{nodeName || nodeId}</strong>
            {' '}<Link to={returnPath} className="node-backend-logs__back">{nodeName && returnPath !== '/app/nodes' ? t('backendLogs.backToNode', { name: nodeName }) : t('backendLogs.backToNodes')}</Link>
          </>
        }
      />

      {showReplicaToggle && (
        <div role="radiogroup" aria-label={t('backendLogs.replicaScope')} className="segmented node-backend-logs__replicas">
          {replicas.map(idx => (
            <button
              key={idx}
              type="button"
              role="radio"
              aria-checked={replicaIndex === idx}
              className={`segmented__item${replicaIndex === idx ? ' is-active' : ''}`}
              onClick={() => navigate(`/app/node-backend-logs/${nodeId}/${encodeURIComponent(baseModelName + '#' + idx)}`, { state: location.state })}
            >
              {t('backendLogs.replica', { number: idx + 1 })}
            </button>
          ))}
          <button
            type="button"
            role="radio"
            aria-checked={isMerged}
            className={`segmented__item${isMerged ? ' is-active' : ''}`}
            onClick={() => navigate(`/app/node-backend-logs/${nodeId}/${encodeURIComponent(baseModelName)}`, { state: location.state })}
            title={t('backendLogs.allMergedHelp')}
          >
            <i className="fas fa-layer-group" aria-hidden="true" /> {t('backendLogs.allMerged')}
          </button>
        </div>
      )}

      {/* Toolbar */}
      <div className="node-backend-logs__toolbar">
        <div className="node-backend-logs__filters">
          {['all', 'stdout', 'stderr'].map(f => (
            <button
              key={f}
              className={`btn btn-sm ${filter === f ? 'btn-primary' : 'btn-secondary'}`}
              onClick={() => setFilter(f)}
            >
              {f === 'all' ? t('backendLogs.filterAll') : f}
            </button>
          ))}
        </div>
        <button className="btn btn-secondary btn-sm" onClick={handleExport} disabled={filteredLines.length === 0}>
          <i className="fas fa-download" /> {t('backendLogs.export')}
        </button>
        <button className="btn btn-danger btn-sm" onClick={handleClear} disabled={lines.length === 0}>
          <i className="fas fa-trash" /> {t('backendLogs.clear')}
        </button>
        <button
          className={`btn btn-sm ${showDetails ? 'btn-secondary' : 'btn-primary'}`}
          onClick={() => setShowDetails(prev => !prev)}
          title={showDetails ? t('backendLogs.hideDetailsHelp') : t('backendLogs.showDetailsHelp')}
        >
          <i className={`fas ${showDetails ? 'fa-eye-slash' : 'fa-eye'}`} /> {showDetails ? t('backendLogs.textOnly') : t('backendLogs.showDetails')}
        </button>
        <div className="node-backend-logs__connection">
          <span className={`node-backend-logs__connection-dot${wsConnected ? ' is-live' : ''}`} />
          <span className="text-secondary">
            {wsConnected ? t('backendLogs.live') : t('backendLogs.reconnecting')}
          </span>
          <label className="node-backend-logs__autoscroll">
            <input
              type="checkbox"
              checked={autoScroll}
              onChange={(e) => setAutoScroll(e.target.checked)}
            />
            <span className="text-secondary">{t('backendLogs.autoScroll')}</span>
          </label>
        </div>
      </div>

      {/* Log output */}
      {loading ? (
        <div className="loading-center">
          <LoadingSpinner size="lg" />
        </div>
      ) : filteredLines.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-terminal" /></div>
          <h2 className="empty-state-title">{t('backendLogs.noLines')}</h2>
          <p className="empty-state-text">
            {filter !== 'all'
              ? t('backendLogs.noFilteredLines', { stream: filter })
              : t('backendLogs.waitingForLines')}
          </p>
        </div>
      ) : (
        <div ref={logContainerRef} className="node-backend-logs__output">
          {filteredLines.map((line, i) => {
            const badge = STREAM_BADGE[line.stream] || STREAM_BADGE.stdout
            return (
              <div
                key={i}
                data-log-line
                data-timestamp={line.timestamp}
                className={`node-backend-logs__line${showDetails ? ' has-details' : ''}`}
              >
                {showDetails && (<>
                  <span className="node-backend-logs__timestamp">
                    {formatTimestamp(line.timestamp)}
                  </span>
                  <span className={`node-backend-logs__stream node-backend-logs__stream--${badge}`}>
                    {badge}
                  </span>
                </>)}
                <span className="node-backend-logs__text">
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
