/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import { formatTimestamp } from '../utils/format'
import { apiUrl } from '../utils/basePath'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import Icon from '../components/Icon'
import './operate.css'
import './swarm.css'

function wsUrl(path) {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}${apiUrl(path)}`
}

const STREAM_LABEL = { stdout: 'out', stderr: 'err' }

// The output of one backend process on one node, live over a WebSocket, with
// the replica scope when a model has several. The page mirrors the local logs
// page so the two read the same.
export default function NodeBackendLogs() {
  const { nodeId, modelId = '' } = useParams()
  const { t } = useTranslation('swarm')
  const { t: to } = useTranslation('operate')
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
  const [text, setText] = useState('')
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

  const filteredLines = useMemo(() => {
    const needle = text.trim().toLowerCase()
    return lines.filter(l => (filter === 'all' || l.stream === filter) && (!needle || String(l.text).toLowerCase().includes(needle)))
  }, [lines, filter, text])

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
      <div className="page page--wide sw-page">
        <div className="dk-empty">
          <div className="dk-empty-icon"><Icon name="terminal" /></div>
          <h2 className="dk-empty-title">{t('nodeLogs.noneTitle')}</h2>
          <p className="dk-empty-text">
            {t('nodeLogs.noneBefore')} <Link to="/app/nodes" className="dk-link">{t('nodeLogs.noneLink')}</Link>.
          </p>
        </div>
      </div>
    )
  }

  // Show the merged/per-replica switch only when this model has more than one
  // replica on this node. One replica has no choice to make.
  const showReplicaToggle = replicas.length > 1
  const go = suffix => navigate(`/app/node-backend-logs/${nodeId}/${encodeURIComponent(baseModelName + suffix)}`)

  return (
    <div className="page page--wide sw-page lg">
      <Link className="lg-back dk-link" to={`/app/nodes/${encodeURIComponent(nodeId)}`}><Icon name="arrow-left" /> {nodeName || t('nodeLogs.backNode')}</Link>
      <PageHeader
        title={(
          <>
            <span className="dk-mono">{baseModelName}</span>
            {!isMerged && <span className="dk-chip dk-chip--sm sw-rep">{t('nodeLogs.replica', { n: replicaIndex })}</span>}
            {isMerged && replicas.length > 1 && <span className="dk-chip dk-chip--sm sw-rep">{t('nodeLogs.merged', { count: replicas.length })}</span>}
          </>
        )}
        supporting={t('nodeLogs.supporting', { node: nodeName || nodeId })}
      />

      {showReplicaToggle && (
        <div className="dk-segmented" role="radiogroup" aria-label={t('nodeLogs.scope')}>
          {replicas.map(idx => (
            <button key={idx} type="button" role="radio" aria-checked={replicaIndex === idx} className="dk-seg" onClick={() => go(`#${idx}`)}>
              {t('nodeLogs.replicaN', { n: idx })}
            </button>
          ))}
          <button type="button" role="radio" aria-checked={isMerged} className="dk-seg" onClick={() => go('')} title={t('nodeLogs.mergedTitle')}>
            <Icon name="layers" /> {t('nodeLogs.allMerged')}
          </button>
        </div>
      )}

      <div className="lg-bar">
        <div className="dk-segmented" role="group" aria-label={to('logs.stream')}>
          {['all', 'stdout', 'stderr'].map(f => (
            <button key={f} type="button" className="dk-seg" aria-pressed={filter === f} onClick={() => setFilter(f)}>
              {f === 'all' ? to('logs.all') : f}
            </button>
          ))}
        </div>
        <input
          className="dk-input lg-filter"
          type="text"
          aria-label={to('logs.filterLines')}
          placeholder={to('logs.filterLines')}
          value={text}
          onChange={e => setText(e.target.value)}
        />
        <div className="lg-bar__right">
          <span className="lg-live" role="status">
            <span className={`dk-dot${wsConnected ? ' dk-dot--ok' : ''}`} aria-hidden="true" />
            {wsConnected ? to('logs.live') : to('logs.reconnecting')}
          </span>
          <span className="lg-switch">
            <button type="button" className="dk-switch" role="switch" aria-checked={autoScroll} aria-label={to('logs.follow')} onClick={() => setAutoScroll(v => !v)} />
            <span aria-hidden="true">{to('logs.follow')}</span>
          </span>
          <span className="lg-switch">
            <button type="button" className="dk-switch" role="switch" aria-checked={showDetails} aria-label={to('logs.times')} onClick={() => setShowDetails(v => !v)} />
            <span aria-hidden="true">{to('logs.times')}</span>
          </span>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={handleExport} disabled={filteredLines.length === 0}>
            <Icon name="download" /> {to('logs.export')}
          </button>
        </div>
      </div>

      {loading ? (
        <div className="loading-center">
          <LoadingSpinner size="lg" />
        </div>
      ) : filteredLines.length === 0 ? (
        <div className="dk-empty lg-empty">
          <div className="dk-empty-icon"><Icon name="terminal" /></div>
          <h2 className="dk-empty-title">{to('logs.emptyTitle')}</h2>
          <p className="dk-empty-text">
            {filter !== 'all'
              ? to('logs.emptyStream', { stream: filter })
              : text.trim() ? to('logs.emptyFilter') : to('logs.emptyBody')}
          </p>
        </div>
      ) : (
        <div ref={logContainerRef} className="lg-log" role="log" aria-label={to('logs.output')} tabIndex={0}>
          {filteredLines.map((line, i) => (
            <div key={i} className="lg-line" data-log-line data-stream={line.stream === 'stderr' ? 'stderr' : 'stdout'} data-timestamp={line.timestamp}>
              {showDetails && (
                <>
                  <span className="lg-line__time">{formatTimestamp(line.timestamp)}</span>
                  <span className="lg-line__stream">{STREAM_LABEL[line.stream] || 'out'}</span>
                </>
              )}
              <span className="lg-line__text">{line.text}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
