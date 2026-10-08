/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useParams, useSearchParams, useOutletContext, useNavigate, Link, Navigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendLogsApi, nodesApi } from '../utils/api'
import { formatTimestamp } from '../utils/format'
import { apiUrl } from '../utils/basePath'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import HomeUndoToast from '../components/home/HomeUndoToast'
import { useDistributedMode } from '../hooks/useDistributedMode'
import Icon from '../components/Icon'
import './operate.css'

function wsUrl(path) {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}${apiUrl(path)}`
}

const STREAM_LABEL = { stdout: 'out', stderr: 'err' }

// How long a Clear waits before the server's copy is wiped. The call erases
// every captured line and cannot be reversed, so the lines are hidden at once
// and this wait is the undo.
const CLEAR_UNDO_MS = 6000

// Detail view: log lines for a specific model
// `embedded` drops the page chrome (header, page width) so the same viewer sits
// inside another page, such as a tab of the model page.
function BackendLogsDetail({ modelId, embedded = false }) {
  const { t } = useTranslation('operate')
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const fromTimestamp = searchParams.get('from')

  const [lines, setLines] = useState([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState('all')
  const [text, setText] = useState('')
  const [autoScroll, setAutoScroll] = useState(true)
  const [showDetails, setShowDetails] = useState(true)
  const [wsConnected, setWsConnected] = useState(false)
  const [processes, setProcesses] = useState([])
  // How many of the first lines a pending Clear is hiding. Zero when none is.
  const [hidden, setHidden] = useState(0)
  const logContainerRef = useRef(null)
  const wsRef = useRef(null)
  const reconnectTimerRef = useRef(null)
  const loadingRef = useRef(true)
  const scrolledToTimestampRef = useRef(false)
  const pendingLinesRef = useRef([])
  const flushTimerRef = useRef(null)

  // Keep loadingRef in sync
  useEffect(() => { loadingRef.current = loading }, [loading])

  // The processes that have output, for the picker. Read once: a new process
  // appears the next time the page is opened.
  useEffect(() => {
    if (embedded) return undefined
    let cancelled = false
    backendLogsApi.listModels()
      .then(list => { if (!cancelled) setProcesses(Array.isArray(list) ? list : []) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [embedded])

  // Auto-scroll to bottom when new lines arrive
  useEffect(() => {
    if (autoScroll && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
    }
  }, [lines, autoScroll, hidden])

  // WebSocket connection with reconnect
  const connectWebSocket = useCallback(() => {
    if (wsRef.current && wsRef.current.readyState <= 1) return

    const url = wsUrl(`/ws/backend-logs/${encodeURIComponent(modelId)}`)
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
          // Batch incoming lines to reduce renders
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
      // Fall back to REST if WebSocket fails on first connect
      if (loadingRef.current) {
        backendLogsApi.getLines(modelId)
          .then(data => setLines(Array.isArray(data) ? data : []))
          .catch(() => {})
          .finally(() => setLoading(false))
      }
    }
  }, [modelId])

  useEffect(() => {
    connectWebSocket()
    return () => {
      if (wsRef.current) wsRef.current.close()
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current)
      if (flushTimerRef.current) cancelAnimationFrame(flushTimerRef.current)
    }
  }, [connectWebSocket])

  // Scroll to timestamp if `from` query param is set (once)
  useEffect(() => {
    if (!fromTimestamp || scrolledToTimestampRef.current || !logContainerRef.current || lines.length === 0) return
    const fromDate = new Date(fromTimestamp).getTime()
    const lineElements = logContainerRef.current.querySelectorAll('[data-log-line]')
    for (const el of lineElements) {
      const lineTime = new Date(el.dataset.timestamp).getTime()
      if (lineTime >= fromDate) {
        el.scrollIntoView({ behavior: 'smooth', block: 'start' })
        el.dataset.mark = 'true'
        setTimeout(() => { delete el.dataset.mark }, 3000)
        scrolledToTimestampRef.current = true
        break
      }
    }
  }, [fromTimestamp, lines])

  const filteredLines = useMemo(() => {
    const needle = text.trim().toLowerCase()
    return lines.slice(hidden).filter(l => (filter === 'all' || l.stream === filter)
      && (!needle || String(l.text || '').toLowerCase().includes(needle)))
  }, [lines, filter, text, hidden])

  // Hide now, wipe on the server when the window ends. Undo shows them again.
  const clear = () => setHidden(lines.length)
  const undoClear = () => setHidden(0)
  const commitClear = async () => {
    try {
      await backendLogsApi.clear(modelId)
      setLines([])
      addToast(t('logs.cleared'), 'success')
    } catch (err) {
      addToast(t('logs.clearFailed', { message: err.message }), 'error')
    } finally {
      setHidden(0)
    }
  }

  const handleExport = () => {
    const blob = new Blob([JSON.stringify(filteredLines, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `backend-logs-${modelId}-${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  const picker = [...new Set([modelId, ...processes])].sort((a, b) => a.localeCompare(b))
  const nothingToShow = filteredLines.length === 0

  return (
    <div className={embedded ? 'logs-embedded lg' : 'page page--wide op-page lg'}>
      {!embedded && (
        <>
          <Link className="lg-back dk-link" to="/app/backends?view=installed"><Icon name="arrow-left" /> {t('logs.back')}</Link>
          <PageHeader
            title={<span className="dk-mono">{modelId}</span>}
            supporting={t('logs.supporting')}
            actions={picker.length > 1 ? (
              <label className="lg-picker">
                <span className="dk-sr-only">{t('logs.process')}</span>
                <select
                  className="dk-select"
                  value={modelId}
                  onChange={e => navigate(`/app/backend-logs/${encodeURIComponent(e.target.value)}`)}
                >
                  {picker.map(id => <option key={id} value={id}>{id}</option>)}
                </select>
              </label>
            ) : null}
          />
        </>
      )}

      <div className="lg-bar">
        <div className="dk-segmented" role="group" aria-label={t('logs.stream')}>
          {['all', 'stdout', 'stderr'].map(f => (
            <button key={f} type="button" className="dk-seg" aria-pressed={filter === f} onClick={() => setFilter(f)}>
              {f === 'all' ? t('logs.all') : f}
            </button>
          ))}
        </div>
        <input
          className="dk-input lg-filter"
          type="text"
          aria-label={t('logs.filterLines')}
          placeholder={t('logs.filterLines')}
          value={text}
          onChange={e => setText(e.target.value)}
        />
        <div className="lg-bar__right">
          <span className="lg-live" role="status">
            <span className={`dk-dot${wsConnected ? ' dk-dot--ok' : ''}`} aria-hidden="true" />
            {wsConnected ? t('logs.live') : t('logs.reconnecting')}
          </span>
          <span className="lg-switch">
            <button type="button" className="dk-switch" role="switch" aria-checked={autoScroll} aria-label={t('logs.follow')} onClick={() => setAutoScroll(v => !v)} />
            <span aria-hidden="true">{t('logs.follow')}</span>
          </span>
          <span className="lg-switch">
            <button type="button" className="dk-switch" role="switch" aria-checked={showDetails} aria-label={t('logs.times')} onClick={() => setShowDetails(v => !v)} />
            <span aria-hidden="true">{t('logs.times')}</span>
          </span>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={handleExport} disabled={filteredLines.length === 0}>
            <Icon name="download" /> {t('logs.export')}
          </button>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={clear} disabled={lines.length === 0 || hidden > 0}>
            <Icon name="trash" /> {t('logs.clear')}
          </button>
        </div>
      </div>

      {/* Log output */}
      {loading ? (
        <div className="loading-center">
          <LoadingSpinner size="lg" />
        </div>
      ) : nothingToShow ? (
        <div className="dk-empty lg-empty">
          <div className="dk-empty-icon"><Icon name="terminal" /></div>
          <h2 className="dk-empty-title">{t('logs.emptyTitle')}</h2>
          <p className="dk-empty-text">
            {filter !== 'all'
              ? t('logs.emptyStream', { stream: filter })
              : text.trim() ? t('logs.emptyFilter') : t('logs.emptyBody')}
          </p>
        </div>
      ) : (
        <div
          ref={logContainerRef}
          className={`lg-log${embedded ? ' lg-log--embedded' : ''}`}
          role="log"
          aria-label={t('logs.output')}
          tabIndex={0}
        >
          {filteredLines.map((line, i) => (
            <div
              key={i}
              className="lg-line"
              data-log-line
              data-stream={line.stream === 'stderr' ? 'stderr' : 'stdout'}
              data-timestamp={line.timestamp}
            >
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

      {hidden > 0 && (
        <HomeUndoToast
          message={t('logs.clearing')}
          undoLabel={t('activity.undo')}
          dismissLabel={t('logs.clearNow')}
          duration={CLEAR_UNDO_MS}
          testId="logs-undo-toast"
          onUndo={undoClear}
          onExpire={commitClear}
        />
      )}
    </div>
  )
}

// DistributedBackendLogsResolver runs only in distributed mode. The local
// /api/backend-logs WebSocket has no backend behind it here (inference lives
// on workers), so we resolve modelId → hosting node(s) and forward to the
// per-node logs page. One hit redirects automatically; multiple hits render
// a picker so the operator can pick which worker's logs to inspect.
function DistributedBackendLogsResolver({ modelId, fromTimestamp }) {
  const [hits, setHits] = useState(null) // [{ node, model }] once resolved
  const [error, setError] = useState(null)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const nodes = await nodesApi.list()
        const nodeList = Array.isArray(nodes) ? nodes : []
        // Fan out to each node and collect entries that match this model.
        // Per-node failures are tolerated — a single offline worker shouldn't
        // hide logs available on its peers.
        const perNode = await Promise.all(nodeList.map(async (node) => {
          try {
            const models = await nodesApi.getModels(node.id)
            const matches = (Array.isArray(models) ? models : []).filter(m => m.model_name === modelId)
            return matches.map(m => ({ node, model: m }))
          } catch {
            return []
          }
        }))
        if (cancelled) return
        setHits(perNode.flat())
      } catch (err) {
        if (!cancelled) setError(err)
      }
    })()
    return () => { cancelled = true }
  }, [modelId])

  if (error) {
    return (
      <div className="page page--wide op-page">
        <div className="dk-empty">
          <div className="dk-empty-icon"><Icon name="warning" /></div>
          <h2 className="dk-empty-title">Failed to resolve hosting nodes</h2>
          <p className="dk-empty-text">{error.message}</p>
        </div>
      </div>
    )
  }

  if (hits === null) {
    return (
      <div className="loading-center">
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  if (hits.length === 0) {
    return (
      <div className="page page--wide op-page">
        <div className="dk-empty">
          <div className="dk-empty-icon"><Icon name="terminal" /></div>
          <h2 className="dk-empty-title">Model not loaded on any worker</h2>
          <p className="dk-empty-text">
            <span className="dk-mono">{modelId}</span> isn't currently loaded on any node in the cluster.
            Check the <Link to="/app/nodes" className="dk-link">Nodes page</Link> to see which models are running where.
          </p>
        </div>
      </div>
    )
  }

  // Bare model name aggregates this node's replicas via the worker's log
  // store; preserve ?from= so the deep-link from a trace still scrolls to
  // the right line on arrival.
  const buildHref = (nodeId) => {
    const base = `/app/node-backend-logs/${nodeId}/${encodeURIComponent(modelId)}`
    return fromTimestamp ? `${base}?from=${encodeURIComponent(fromTimestamp)}` : base
  }

  if (hits.length === 1) {
    return <Navigate to={buildHref(hits[0].node.id)} replace />
  }

  // Multiple workers host this model — let the operator pick.
  return (
    <div className="page page--wide op-page lg">
      <PageHeader
        title={<span className="dk-mono">{modelId}</span>}
        supporting={`Hosted on ${hits.length} workers — pick one to view its logs.`}
      />
      <ul className="dk-list lg-processes">
        {hits.map(({ node, model }) => (
          <li key={`${node.id}#${model.replica_index ?? 0}`}>
            <Link className="dk-row" to={buildHref(node.id)}>
              <span className="dk-row-lead"><Icon name="terminal" /></span>
              <span className="dk-row-main">
                <span className="dk-row-title">{node.name || node.id}</span>
                <span className="dk-row-meta dk-mono">
                  {node.id}{model.replica_index ? ` · replica ${model.replica_index}` : ''} · {model.state}
                </span>
              </span>
              <span className="dk-row-end"><Icon name="chevron-right" /></span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  )
}

// BackendLogsRouter picks between the local WebSocket view (standalone) and
// the distributed resolver. The probe runs once via useDistributedMode so a
// 503 from /api/nodes (the canonical "distributed disabled" signal) keeps the
// existing standalone path intact.
function BackendLogsRouter({ modelId }) {
  const [searchParams] = useSearchParams()
  const fromTimestamp = searchParams.get('from')
  const { enabled: distributedMode, loading } = useDistributedMode()

  if (loading) {
    return (
      <div className="loading-center">
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  if (distributedMode) {
    return <DistributedBackendLogsResolver modelId={modelId} fromTimestamp={fromTimestamp} />
  }

  return <BackendLogsDetail modelId={modelId} />
}

// With no model in the address the page lists the processes that have output,
// each a link to its log. Reached from a backend's "Logs" link.
function LogProcessList() {
  const { t } = useTranslation('operate')
  const [processes, setProcesses] = useState(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let cancelled = false
    backendLogsApi.listModels()
      .then(list => { if (!cancelled) setProcesses(Array.isArray(list) ? list : []) })
      .catch(err => { if (!cancelled) { setError(err.message); setProcesses([]) } })
    return () => { cancelled = true }
  }, [])

  return (
    <div className="page page--wide op-page lg" data-testid="logs-processes">
      <Link className="lg-back dk-link" to="/app/backends?view=installed"><Icon name="arrow-left" /> {t('logs.back')}</Link>
      <PageHeader title={t('logs.title')} supporting={t('logs.listSupporting')} />
      {processes === null ? (
        <div className="loading-center"><LoadingSpinner size="lg" /></div>
      ) : error ? (
        <div className="op-error" role="alert"><Icon name="alert-circle" /> <span>{t('logs.listFailed', { message: error })}</span></div>
      ) : processes.length === 0 ? (
        <div className="dk-empty">
          <div className="dk-empty-icon"><Icon name="terminal" /></div>
          <h2 className="dk-empty-title">{t('logs.noProcessesTitle')}</h2>
          <p className="dk-empty-text">
            {t('logs.noProcessesBody')} <Link to="/app/models?view=installed" className="dk-link">{t('logs.noProcessesLink')}</Link>.
          </p>
        </div>
      ) : (
        <ul className="dk-list lg-processes">
          {[...processes].sort((a, b) => a.localeCompare(b)).map(id => (
            <li key={id}>
              <Link className="dk-row" to={`/app/backend-logs/${encodeURIComponent(id)}`}>
                <span className="dk-row-lead"><Icon name="terminal" /></span>
                <span className="dk-row-main"><span className="dk-row-title dk-mono">{id}</span></span>
                <span className="dk-row-end"><Icon name="chevron-right" /></span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

export default function BackendLogs() {
  const { modelId } = useParams()

  if (modelId) {
    return <BackendLogsRouter modelId={modelId} />
  }
  return <LogProcessList />
}

// The log viewer without the page around it, for the model page's Logs tab.
// In distributed mode the local stream has nothing behind it, so the tab points
// at the full page, which resolves the node that hosts the model.
export function BackendLogsPanel({ modelId, fullPageLabel }) {
  const { enabled: distributedMode, loading } = useDistributedMode()
  if (loading) return <div className="loading-center"><LoadingSpinner size="lg" /></div>
  if (distributedMode) {
    return (
      <p className="logs-embedded__note">
        <Link to={`/app/backend-logs/${encodeURIComponent(modelId)}`} className="dk-link">{fullPageLabel}</Link>
      </p>
    )
  }
  return <BackendLogsDetail modelId={modelId} embedded />
}
