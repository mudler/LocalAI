import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useParams, useSearchParams, useOutletContext, Link } from 'react-router-dom'
import { backendLogsApi } from '../utils/api'
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

// Detail view: log lines for a specific model
function BackendLogsDetail({ modelId }) {
  const { addToast } = useOutletContext()
  const [searchParams] = useSearchParams()
  const fromTimestamp = searchParams.get('from')

  const [lines, setLines] = useState([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState('all')
  const [autoScroll, setAutoScroll] = useState(true)
  const [showDetails, setShowDetails] = useState(true)
  const [wsConnected, setWsConnected] = useState(false)
  const logContainerRef = useRef(null)
  const wsRef = useRef(null)
  const reconnectTimerRef = useRef(null)
  const loadingRef = useRef(true)
  const scrolledToTimestampRef = useRef(false)
  const pendingLinesRef = useRef([])
  const flushTimerRef = useRef(null)

  // Keep loadingRef in sync
  useEffect(() => { loadingRef.current = loading }, [loading])

  // Auto-scroll to bottom when new lines arrive
  useEffect(() => {
    if (autoScroll && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
    }
  }, [lines, autoScroll])

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
        el.style.background = 'rgba(59,130,246,0.1)'
        setTimeout(() => { el.style.background = '' }, 3000)
        scrolledToTimestampRef.current = true
        break
      }
    }
  }, [fromTimestamp, lines])

  const filteredLines = useMemo(
    () => filter === 'all' ? lines : lines.filter(l => l.stream === filter),
    [lines, filter]
  )

  const handleClear = async () => {
    try {
      await backendLogsApi.clear(modelId)
      setLines([])
      addToast('Logs cleared', 'success')
    } catch (err) {
      addToast(`Failed to clear: ${err.message}`, 'error')
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

  return (
    <div className="page page--wide">
      <div className="page-header">
        <div>
          <h1 className="page-title" style={{ marginBottom: 0 }}>
            <i className="fas fa-terminal" style={{ fontSize: '0.8em', marginRight: 'var(--spacing-sm)' }} />
            {modelId}
          </h1>
          <p className="page-subtitle" style={{ marginTop: 'var(--spacing-xs)' }}>Backend process output</p>
        </div>
      </div>

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
        <button className="btn btn-danger btn-sm" onClick={handleClear}><i className="fas fa-trash" /> Clear</button>
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

export default function BackendLogs() {
  const { modelId } = useParams()

  if (modelId) {
    return <BackendLogsDetail modelId={decodeURIComponent(modelId)} />
  }

  // No model specified — redirect to System page
  return (
    <div className="page page--wide">
      <div className="empty-state">
        <div className="empty-state-icon"><i className="fas fa-terminal" /></div>
        <h2 className="empty-state-title">No model selected</h2>
        <p className="empty-state-text">
          View backend logs for a specific model from the{' '}
          <Link to="/app/manage" style={{ color: 'var(--color-primary)' }}>System page</Link>.
        </p>
      </div>
    </div>
  )
}
