import React, { useState, useEffect, useCallback, useRef } from 'react'
import { useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { tracesApi, settingsApi, DEFAULT_TRACE_PAGE_SIZE } from '../utils/api'
import { formatDateTime } from '../utils/format'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import ResponsiveTable from '../components/ResponsiveTable'
import Toggle from '../components/Toggle'
import SettingRow from '../components/SettingRow'
import WaveformPlayer from '../components/audio/WaveformPlayer'

// How many traces the page keeps on screen. The server buffer holds far more;
// the counters next to the tab labels report the true total.
const TRACE_PAGE_SIZE = DEFAULT_TRACE_PAGE_SIZE

const AUDIO_DATA_KEYS = new Set([
  'audio_wav_base64', 'audio_duration_s', 'audio_snippet_s',
  'audio_sample_rate', 'audio_samples', 'audio_rms_dbfs',
  'audio_peak_dbfs', 'audio_dc_offset',
])

function formatDuration(ns) {
  if (!ns && ns !== 0) return '-'
  if (ns < 1000) return `${ns}ns`
  if (ns < 1_000_000) return `${(ns / 1000).toFixed(1)}\u00b5s`
  if (ns < 1_000_000_000) return `${(ns / 1_000_000).toFixed(1)}ms`
  return `${(ns / 1_000_000_000).toFixed(2)}s`
}

function decodeTraceBody(body) {
  if (!body) return ''
  try {
    const bin = atob(body)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    const text = new TextDecoder().decode(bytes)
    try { return JSON.stringify(JSON.parse(text), null, 2) } catch { return text }
  } catch {
    return body
  }
}

function formatValue(value) {
  if (value === null || value === undefined) return 'null'
  if (typeof value === 'boolean') return value ? 'true' : 'false'
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

function formatLargeValue(value) {
  if (typeof value === 'string') {
    try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value }
  }
  if (typeof value === 'object') return JSON.stringify(value, null, 2)
  return String(value)
}

function isLargeValue(value) {
  if (typeof value === 'string') return value.length > 120
  if (typeof value === 'object') return JSON.stringify(value).length > 120
  return false
}

function truncateValue(value, maxLen) {
  const str = typeof value === 'object' ? JSON.stringify(value) : String(value)
  if (str.length <= maxLen) return str
  return str.substring(0, maxLen) + '...'
}

const TYPE_COLORS = {
  llm: { bg: 'var(--color-primary-light)', color: 'var(--color-data-1)' },
  embedding: { bg: 'var(--color-accent-light)', color: 'var(--color-data-3)' },
  transcription: { bg: 'var(--color-warning-light)', color: 'var(--color-data-4)' },
  image_generation: { bg: 'var(--color-success-light)', color: 'var(--color-data-5)' },
  video_generation: { bg: 'var(--color-accent-light)', color: 'var(--color-data-7)' },
  '3d_generation': { bg: 'var(--color-success-light)', color: 'var(--color-data-5)' },
  '3d_remesh': { bg: 'var(--color-accent-light)', color: 'var(--color-data-7)' },
  tts: { bg: 'var(--color-warning-light)', color: 'var(--color-data-6)' },
  sound_generation: { bg: 'var(--color-info-light)', color: 'var(--color-data-8)' },
  rerank: { bg: 'var(--color-primary-light)', color: 'var(--color-data-1)' },
  tokenize: { bg: 'var(--color-secondary-light)', color: 'var(--color-text-muted)' },
  detection: { bg: 'var(--color-info-light)', color: 'var(--color-data-8)' },
  model_load: { bg: 'var(--color-error-light)', color: 'var(--color-data-2)' },
  vector_store: { bg: 'var(--color-accent-light)', color: 'var(--color-data-7)' },
  token_classify: { bg: 'var(--color-info-light)', color: 'var(--color-data-3)' },
  pattern_pii: { bg: 'var(--color-error-light)', color: 'var(--color-data-2)' },
}

function typeBadgeStyle(type) {
  const c = TYPE_COLORS[type] || TYPE_COLORS.tokenize
  return { background: c.bg, color: c.color, padding: '2px 8px', borderRadius: 'var(--radius-sm)', fontSize: '0.75rem', fontWeight: 500 }
}

// useWavObjectURL — decode a base64 WAV payload into a blob: object URL for
// the waveform player. A data: URL would render in <audio> (media-src allows
// data:) but the peaks renderer fetch()es the src and the CSP's connect-src
// only allows blob:, so playback broke with a CSP violation. Decoding to a
// Blob also tolerates payloads that aren't valid base64 — e.g. the
// "<truncated: N bytes>" marker older servers stamped into oversized fields —
// by yielding null instead of a broken player.
function useWavObjectURL(b64) {
  const [url, setUrl] = useState(null)
  useEffect(() => {
    if (!b64) {
      setUrl(null)
      return undefined
    }
    let objectUrl = null
    try {
      const bin = atob(b64)
      const bytes = new Uint8Array(bin.length)
      for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
      objectUrl = URL.createObjectURL(new Blob([bytes], { type: 'audio/wav' }))
      setUrl(objectUrl)
    } catch {
      setUrl(null)
    }
    return () => {
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [b64])
  return url
}

// Audio player + metrics for transcription traces
function AudioSnippet({ data }) {
  const audioUrl = useWavObjectURL(data?.audio_wav_base64)
  if (!data?.audio_wav_base64) return null
  const metrics = [
    { label: 'Duration', value: data.audio_duration_s + 's' },
    { label: 'Sample Rate', value: data.audio_sample_rate + ' Hz' },
    { label: 'RMS Level', value: data.audio_rms_dbfs + ' dBFS' },
    { label: 'Peak Level', value: data.audio_peak_dbfs + ' dBFS' },
    { label: 'Samples', value: data.audio_samples },
    { label: 'Snippet', value: data.audio_snippet_s + 's' },
    { label: 'DC Offset', value: data.audio_dc_offset },
  ]
  return (
    <div style={{ marginBottom: 'var(--spacing-md)' }}>
      <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)', display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
        <i className="fas fa-headphones" style={{ color: 'var(--color-primary)' }} /> Audio Snippet
      </h4>
      <div style={{ background: 'var(--color-bg-primary)', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm)' }}>
        {audioUrl
          ? <WaveformPlayer src={audioUrl} height={64} />
          : <div data-testid="audio-snippet-unavailable" style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)', padding: 'var(--spacing-xs)' }}>
              <i className="fas fa-triangle-exclamation" /> Audio clip not playable — it was truncated when recorded (raise Max Body Bytes in the tracing settings).
            </div>}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(120px, 1fr))', gap: 'var(--spacing-xs)', fontSize: '0.75rem', marginTop: 'var(--spacing-sm)' }}>
          {metrics.map(m => (
            <div key={m.label} style={{ background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-sm)', padding: 'var(--spacing-xs)' }}>
              <div style={{ color: 'var(--color-text-secondary)' }}>{m.label}</div>
              <div style={{ fontFamily: 'var(--font-mono)' }}>{m.value}</div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function fieldSummary(value) {
  const count = Object.keys(value).length
  return `{${count} field${count !== 1 ? 's' : ''}}`
}

// Expandable data fields for backend traces (recursive for nested objects)
function DataFields({ data, nested }) {
  const [expandedFields, setExpandedFields] = useState({})
  const filtered = Object.entries(data).filter(([key]) => !AUDIO_DATA_KEYS.has(key))
  if (filtered.length === 0) return null

  const toggleField = (key) => {
    setExpandedFields(prev => ({ ...prev, [key]: !prev[key] }))
  }

  return (
    <div>
      {!nested && <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Data Fields</h4>}
      <div style={{ border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', overflow: 'hidden' }}>
        {filtered.map(([key, value]) => {
          const objValue = isPlainObject(value)
          const large = !objValue && isLargeValue(value)
          const expandable = objValue || large
          const expanded = expandedFields[key]
          return (
            <div key={key} style={{ borderBottom: '1px solid var(--color-border)' }}>
              <div
                onClick={expandable ? () => toggleField(key) : undefined}
                style={{
                  display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)',
                  padding: 'var(--spacing-xs) var(--spacing-sm)',
                  cursor: expandable ? 'pointer' : 'default',
                  fontSize: '0.8125rem',
                }}
              >
                {expandable ? (
                  <i className={`fas fa-chevron-${expanded ? 'down' : 'right'}`} style={{ fontSize: '0.6rem', color: 'var(--color-text-secondary)', width: 12, flexShrink: 0 }} />
                ) : (
                  <span style={{ width: 12, flexShrink: 0 }} />
                )}
                <span style={{ fontFamily: 'var(--font-mono)', color: 'var(--color-primary)', flexShrink: 0 }}>{key}</span>
                {objValue && !expanded && <span style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)' }}>{fieldSummary(value)}</span>}
                {!objValue && !large && <span style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--color-text-secondary)' }}>{formatValue(value)}</span>}
                {!objValue && large && !expanded && <span style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{truncateValue(value, 120)}</span>}
              </div>
              {expanded && objValue && (
                <div style={{ padding: '0 0 var(--spacing-xs) var(--spacing-md)' }}>
                  <DataFields data={value} nested />
                </div>
              )}
              {expanded && large && (
                <div style={{ padding: '0 var(--spacing-sm) var(--spacing-sm)' }}>
                  <pre style={{
                    background: 'var(--color-bg-primary)', border: '1px solid var(--color-border)',
                    borderRadius: 'var(--radius-sm)', padding: 'var(--spacing-sm)',
                    fontSize: '0.75rem', fontFamily: 'var(--font-mono)', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                    overflow: 'auto', maxHeight: '50vh', margin: 0,
                  }}>
                    {formatLargeValue(value)}
                  </pre>
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

// Expanded detail for a backend trace row
function BackendTraceDetail({ trace }) {
  const infoItems = [
    { label: 'Type', value: trace.type },
    { label: 'Model', value: trace.model_name || '-' },
    { label: 'Backend', value: trace.backend || '-' },
    { label: 'Duration', value: formatDuration(trace.duration) },
  ]

  return (
    <div style={{ padding: 'var(--spacing-md)', background: 'var(--color-bg-secondary)', borderBottom: '1px solid var(--color-border)' }}>
      {/* Summary cards */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-md)', fontSize: '0.75rem' }}>
        {infoItems.map(item => (
          <div key={item.label} style={{ background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-sm)', padding: 'var(--spacing-xs)', border: '1px solid var(--color-border)' }}>
            <div style={{ color: 'var(--color-text-secondary)' }}>{item.label}</div>
            <div style={{ fontWeight: 500 }}>{item.label === 'Type' ? <span style={typeBadgeStyle(item.value)}>{item.value}</span> : item.value}</div>
          </div>
        ))}
      </div>

      {/* Error banner */}
      {trace.error && (
        <div style={{
          background: 'var(--color-error-light)', border: '1px solid var(--color-error-border)',
          borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)',
          display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)',
        }}>
          <i className="fas fa-exclamation-triangle" style={{ color: 'var(--color-error)' }} />
          <span style={{ color: 'var(--color-error)', fontSize: '0.8125rem' }}>{trace.error}</span>
        </div>
      )}

      {/* Backend logs link — /app/backend-logs/:modelId is the unified entry
          point: in standalone mode it streams local logs, in distributed mode
          it resolves the model to the host worker(s) and either redirects to
          /app/node-backend-logs/<nodeId>/<modelId> or shows a node picker. */}
      {trace.model_name && (
        <div style={{ marginBottom: 'var(--spacing-md)' }}>
          <a
            href={`/app/backend-logs/${encodeURIComponent(trace.model_name)}${trace.timestamp ? `?from=${encodeURIComponent(trace.timestamp)}` : ''}`}
            style={{ fontSize: '0.8125rem', color: 'var(--color-primary)', textDecoration: 'none', display: 'inline-flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}
          >
            <i className="fas fa-terminal" /> View backend logs
          </a>
        </div>
      )}

      {/* Audio snippet */}
      {trace.data && <AudioSnippet data={trace.data} />}

      {/* Request body: cloud-proxy passthrough records the full
          payload here (capped to ~1MB upstream); pretty-print when
          it parses as JSON, otherwise show the raw text. */}
      {trace.body && (
        <div style={{ marginBottom: 'var(--spacing-md)' }}>
          <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Request Body</h4>
          <pre style={{
            background: 'var(--color-bg-primary)', border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-sm)', padding: 'var(--spacing-sm)',
            fontSize: '0.75rem', fontFamily: 'var(--font-mono)', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            overflow: 'auto', maxHeight: '50vh', margin: 0,
          }}>
            {formatLargeValue(trace.body)}
          </pre>
        </div>
      )}

      {/* Data fields */}
      {trace.data && Object.keys(trace.data).length > 0 && <DataFields data={trace.data} />}
    </div>
  )
}

// Expanded detail for an API trace row
function ApiTraceDetail({ trace }) {
  const user = trace.user_name || trace.user_id
  const meta = [
    ['User', user],
    ['Client IP', trace.client_ip],
    ['User Agent', trace.user_agent],
  ].filter(([, v]) => v)
  return (
    <div style={{ padding: 'var(--spacing-md)', background: 'var(--color-bg-secondary)', borderBottom: '1px solid var(--color-border)' }}>
      {meta.length > 0 && (
        <div style={{
          display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '0.25rem var(--spacing-md)',
          alignItems: 'baseline', marginBottom: 'var(--spacing-md)', fontSize: '0.8125rem',
        }}>
          {meta.map(([label, value]) => (
            <React.Fragment key={label}>
              <span style={{ fontWeight: 600, color: 'var(--color-text-secondary)' }}>{label}</span>
              <span style={{ fontFamily: 'var(--font-mono)', wordBreak: 'break-all' }}>{value}</span>
            </React.Fragment>
          ))}
        </div>
      )}
      {trace.error && (
        <div style={{
          background: 'var(--color-error-light)', border: '1px solid var(--color-error-border)',
          borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)',
          display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)',
        }}>
          <i className="fas fa-exclamation-triangle" style={{ color: 'var(--color-error)' }} />
          <span style={{ color: 'var(--color-error)', fontSize: '0.8125rem', fontFamily: 'var(--font-mono)', wordBreak: 'break-all' }}>{trace.error}</span>
        </div>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--spacing-md)' }}>
        <div>
          <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Request Body</h4>
          <pre style={{
            background: 'var(--color-bg-primary)', border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-sm)', padding: 'var(--spacing-sm)',
            fontSize: '0.75rem', fontFamily: 'var(--font-mono)', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            overflow: 'auto', maxHeight: '50vh', margin: 0,
          }}>
            {decodeTraceBody(trace.request?.body)}
          </pre>
        </div>
        <div>
          <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Response Body</h4>
          <pre style={{
            background: 'var(--color-bg-primary)', border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-sm)', padding: 'var(--spacing-sm)',
            fontSize: '0.75rem', fontFamily: 'var(--font-mono)', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            overflow: 'auto', maxHeight: '50vh', margin: 0,
          }}>
            {decodeTraceBody(trace.response?.body)}
          </pre>
        </div>
      </div>
    </div>
  )
}

export default function Traces() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const [searchParams] = useSearchParams()
  const [activeTab, setActiveTab] = useState(() => searchParams.get('tab') === 'backend' ? 'backend' : 'api')
  const [traces, setTraces] = useState([])
  const [apiCount, setApiCount] = useState(0)
  const [backendCount, setBackendCount] = useState(0)
  const [loading, setLoading] = useState(true)
  const [expandedRow, setExpandedRow] = useState(null)
  // detail holds the full record for the currently expanded row, fetched on
  // demand from /api/traces/:id (the list response omits the bodies).
  const [detail, setDetail] = useState(null)
  const [sort, setSort] = useState({ key: null, dir: 'asc' })
  const [tracingEnabled, setTracingEnabled] = useState(null)

  const TRACE_SORT = {
    method: (a, b) => (a.request?.method || '').localeCompare(b.request?.method || ''),
    path: (a, b) => (a.request?.path || '').localeCompare(b.request?.path || ''),
    user: (a, b) => (a.user_name || a.user_id || '').localeCompare(b.user_name || b.user_id || ''),
    status: (a, b) => (a.response?.status || 0) - (b.response?.status || 0),
    type: (a, b) => (a.type || '').localeCompare(b.type || ''),
    time: (a, b) => new Date(a.timestamp || 0) - new Date(b.timestamp || 0),
    model: (a, b) => (a.model_name || '').localeCompare(b.model_name || ''),
    duration: (a, b) => (a.duration || 0) - (b.duration || 0),
  }
  const toggleSort = (key) => {
    setExpandedRow(null)
    setSort(s => s.key === key ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'asc' })
  }
  const sortableTh = (key, label, props = {}) => (
    <th
      {...props}
      role="button"
      tabIndex={0}
      aria-sort={sort.key === key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}
      onClick={() => toggleSort(key)}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleSort(key) } }}
      style={{ cursor: 'pointer', userSelect: 'none', ...(props.style || {}) }}
    >
      {label}{sort.key === key && <i className={`fas fa-caret-${sort.dir === 'asc' ? 'up' : 'down'}`} style={{ marginLeft: 4, opacity: 0.7 }} aria-hidden="true" />}
    </th>
  )
  const [backendLoggingEnabled, setBackendLoggingEnabled] = useState(null)
  const [settings, setSettings] = useState(null)
  const [settingsExpanded, setSettingsExpanded] = useState(false)
  const [saving, setSaving] = useState(false)
  const refreshRef = useRef(null)

  useEffect(() => {
    settingsApi.get()
      .then(data => {
        setTracingEnabled(!!data.enable_tracing)
        setBackendLoggingEnabled(!!data.enable_backend_logging)
        setSettings(data)
        if (!data.enable_tracing) setSettingsExpanded(true)
      })
      .catch(() => {})
  }, [])

  const handleSaveSettings = async () => {
    setSaving(true)
    try {
      await settingsApi.save(settings)
      setTracingEnabled(!!settings.enable_tracing)
      setBackendLoggingEnabled(!!settings.enable_backend_logging)
      addToast('Tracing settings saved', 'success')
      if (settings.enable_tracing) setSettingsExpanded(false)
    } catch (err) {
      addToast(`Save failed: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  // Only a bounded page is fetched, and the server strips the request /
  // response bodies from list entries — the full record is pulled per row on
  // expand. The unbounded form was a multi-megabyte transfer on every poll.
  const fetchTraces = useCallback(async () => {
    try {
      const [apiPage, backendPage] = await Promise.all([
        tracesApi.get({ limit: TRACE_PAGE_SIZE }),
        tracesApi.getBackend({ limit: TRACE_PAGE_SIZE }),
      ])
      setApiCount(apiPage.total)
      setBackendCount(backendPage.total)
      setTraces(activeTab === 'api' ? apiPage.items : backendPage.items)
    } catch (err) {
      // Tracing disabled is the default state, not an error — the in-page banner covers it.
      const disabled = /disabled|not enabled|404|not found/i.test(err?.message || '')
      if (!disabled) {
        addToast(`Failed to load traces: ${err.message}`, 'error')
      }
    } finally {
      setLoading(false)
    }
  }, [activeTab, addToast])

  useEffect(() => {
    setLoading(true)
    setExpandedRow(null)
    setDetail(null)
    fetchTraces()
  }, [fetchTraces])

  // Expanding a row pulls the full record (bodies, data fields, audio
  // snippets) that the list response deliberately omits.
  const toggleRow = useCallback(async (index, row) => {
    if (expandedRow === index) {
      setExpandedRow(null)
      setDetail(null)
      return
    }
    setExpandedRow(index)
    setDetail(null)
    if (!row?.id) return
    try {
      const full = activeTab === 'api'
        ? await tracesApi.getOne(row.id)
        : await tracesApi.getBackendOne(row.id)
      setDetail(full)
    } catch {
      // Fall back to the summary view; the row still renders what it has.
    }
  }, [expandedRow, activeTab])

  // Auto-refresh every 5 seconds
  useEffect(() => {
    refreshRef.current = setInterval(fetchTraces, 5000)
    return () => clearInterval(refreshRef.current)
  }, [fetchTraces])

  const handleClear = async () => {
    try {
      if (activeTab === 'api') await tracesApi.clear()
      else await tracesApi.clearBackend()
      setTraces([])
      setExpandedRow(null)
      setDetail(null)
      addToast('Traces cleared', 'success')
    } catch (err) {
      addToast(`Failed to clear: ${err.message}`, 'error')
    }
  }

  // Export asks for the full payloads explicitly — the on-screen list only
  // holds summaries, and an export without bodies would be useless.
  const handleExport = async () => {
    let rows = traces
    try {
      const page = activeTab === 'api'
        ? await tracesApi.get({ limit: TRACE_PAGE_SIZE, full: true })
        : await tracesApi.getBackend({ limit: TRACE_PAGE_SIZE, full: true })
      rows = page.items
    } catch (err) {
      addToast(`Exporting summaries only: ${err.message}`, 'error')
    }
    const blob = new Blob([JSON.stringify(rows, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `traces-${activeTab}-${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  // Reset sort + expansion when switching trace tabs (columns differ).
  useEffect(() => { setSort({ key: null, dir: 'asc' }); setExpandedRow(null); setDetail(null) }, [activeTab])

  const sortedTraces = sort.key && TRACE_SORT[sort.key]
    ? [...traces].sort((a, b) => sort.dir === 'asc' ? TRACE_SORT[sort.key](a, b) : TRACE_SORT[sort.key](b, a))
    : traces

  return (
    <div className="page page--wide">
      <PageHeader title={t('traces.title')} supporting={t('traces.subtitle')} />

      <div className="tabs">
        <button className={`tab ${activeTab === 'api' ? 'tab-active' : ''}`} onClick={() => setActiveTab('api')}>
          <i className="fas fa-exchange-alt" style={{ marginRight: 'var(--spacing-xs)', fontSize: '0.75rem' }} />
          API Traces
          <span style={{ marginLeft: 'var(--spacing-xs)', opacity: 0.6, fontSize: '0.75rem' }}>({apiCount})</span>
        </button>
        <button className={`tab ${activeTab === 'backend' ? 'tab-active' : ''}`} onClick={() => setActiveTab('backend')}>
          <i className="fas fa-cogs" style={{ marginRight: 'var(--spacing-xs)', fontSize: '0.75rem' }} />
          Backend Traces
          <span style={{ marginLeft: 'var(--spacing-xs)', opacity: 0.6, fontSize: '0.75rem' }}>({backendCount})</span>
        </button>
      </div>

      <div style={{ display: 'flex', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)', alignItems: 'center' }}>
        <button className="btn btn-secondary btn-sm" onClick={fetchTraces}><i className="fas fa-rotate" /> Refresh</button>
        <button className="btn btn-secondary btn-sm" onClick={handleExport} disabled={traces.length === 0}><i className="fas fa-download" /> Export</button>
        <div style={{ flex: 1 }} />
        <button
          className="btn btn-danger btn-sm"
          onClick={handleClear}
          /* Stay enabled while loading: a massive in-memory trace buffer is
             precisely the case where the user can't see the table yet and
             needs Clear to recover. Clearing an already-empty server-side
             buffer is a harmless no-op. */
          disabled={!loading && traces.length === 0}
        ><i className="fas fa-trash" /> Clear</button>
      </div>

      {settings && (() => {
        const allEnabled = tracingEnabled && backendLoggingEnabled
        return (
        <div style={{
          border: `1px solid ${allEnabled ? 'var(--color-success-border)' : 'var(--color-warning-border)'}`,
          borderRadius: 'var(--radius-md)',
          marginBottom: 'var(--spacing-md)',
          overflow: 'hidden',
        }}>
          <button
            onClick={() => setSettingsExpanded(!settingsExpanded)}
            style={{
              width: '100%', display: 'flex', alignItems: 'center', justifyContent: 'space-between',
              padding: 'var(--spacing-sm) var(--spacing-md)',
              background: allEnabled ? 'var(--color-success-light)' : 'var(--color-warning-light)',
              border: 'none', cursor: 'pointer',
              color: 'var(--color-text-primary)',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
              <i className={`fas ${allEnabled ? 'fa-circle-check' : 'fa-exclamation-triangle'}`}
                style={{ color: allEnabled ? 'var(--color-success)' : 'var(--color-warning)', flexShrink: 0 }} />
              <span style={{ fontSize: '0.8125rem', textAlign: 'left' }}>
                Tracing is <strong>{tracingEnabled ? 'enabled' : 'disabled'}</strong>
                {' · Backend logging is '}<strong>{backendLoggingEnabled ? 'enabled' : 'disabled'}</strong>
                {!tracingEnabled && ' — new requests will not be recorded'}
              </span>
            </div>
            <i className={`fas fa-chevron-${settingsExpanded ? 'up' : 'down'}`}
              style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', flexShrink: 0 }} />
          </button>
          {settingsExpanded && (
            <div style={{ padding: '0 var(--spacing-md) var(--spacing-md)', background: 'var(--color-bg-secondary)', borderTop: '1px solid var(--color-border-subtle)' }}>
              <SettingRow label="Enable Tracing" description="Record API requests, responses, and backend operations">
                <Toggle
                  checked={settings.enable_tracing}
                  onChange={(v) => setSettings(prev => ({ ...prev, enable_tracing: v }))}
                />
              </SettingRow>
              <SettingRow label="Max Items" description="Maximum trace items to retain (0 = unlimited)">
                <input
                  className="input"
                  type="number"
                  style={{ width: 120 }}
                  value={settings.tracing_max_items ?? ''}
                  onChange={(e) => setSettings(prev => ({ ...prev, tracing_max_items: parseInt(e.target.value) || 0 }))}
                  placeholder="100"
                  disabled={!settings.enable_tracing}
                />
              </SettingRow>
              <SettingRow label="Max Body Bytes" description="Per-field cap for captured bodies and backend trace Data (0 = uncapped). Prevents oversized LLM histories or TTS snippets from locking this page in loading.">
                <input
                  className="input"
                  type="number"
                  style={{ width: 120 }}
                  value={settings.tracing_max_body_bytes ?? ''}
                  onChange={(e) => setSettings(prev => ({ ...prev, tracing_max_body_bytes: parseInt(e.target.value) || 0 }))}
                  placeholder="65536"
                  disabled={!settings.enable_tracing}
                />
              </SettingRow>
              <SettingRow label="Enable Backend Logging" description="Capture backend process output per model (without requiring debug mode)">
                <Toggle
                  checked={settings.enable_backend_logging}
                  onChange={(v) => setSettings(prev => ({ ...prev, enable_backend_logging: v }))}
                />
              </SettingRow>
              <div className="form-group__actions" style={{ justifyContent: 'flex-end' }}>
                <button className="btn btn-primary btn-sm" onClick={handleSaveSettings} disabled={saving}>
                  {saving ? <><LoadingSpinner size="sm" /> Saving...</> : <><i className="fas fa-save" /> Save</>}
                </button>
              </div>
            </div>
          )}
        </div>
        )
      })()}

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>
      ) : traces.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-wave-square" /></div>
          <h2 className="empty-state-title">
            {activeTab === 'api'
              ? (tracingEnabled ? 'No API traces yet' : 'API tracing is off')
              : (backendLoggingEnabled ? 'No backend traces yet' : 'Backend logging is off')}
          </h2>
          <p className="empty-state-text">
            {activeTab === 'api'
              ? (tracingEnabled
                  ? 'Traces will appear here as API requests are made.'
                  : 'Enable Tracing above to start recording API requests, responses, and backend operations.')
              : (backendLoggingEnabled
                  ? 'Backend operations will appear here as models run.'
                  : 'Enable Backend Logging above to capture per-model process output.')}
          </p>
        </div>
      ) : activeTab === 'api' ? (
        <ResponsiveTable>
            <thead>
              <tr>
                <th style={{ width: '30px' }}></th>
                {sortableTh('method', 'Method')}
                {sortableTh('path', 'Path')}
                {sortableTh('user', 'User')}
                {sortableTh('status', 'Status')}
                <th style={{ width: '40px' }}>Result</th>
              </tr>
            </thead>
            <tbody>
              {sortedTraces.map((trace, i) => (
                <React.Fragment key={i}>
                  <tr onClick={() => toggleRow(i, trace)} style={{ cursor: 'pointer' }}>
                    <td><i className={`fas fa-chevron-${expandedRow === i ? 'down' : 'right'}`} style={{ fontSize: '0.7rem' }} /></td>
                    <td><span className="badge badge-info">{trace.request?.method || '-'}</span></td>
                    <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>{trace.request?.path || '-'}</td>
                    <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', maxWidth: '160px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={trace.user_name || trace.user_id || ''}>{trace.user_name || trace.user_id || '-'}</td>
                    <td><span className={`badge ${(trace.response?.status || 0) < 400 ? 'badge-success' : 'badge-error'}`}>{trace.response?.status || '-'}</span></td>
                    <td style={{ textAlign: 'center' }}>
                      {trace.error
                        ? <i className="fas fa-times-circle" style={{ color: 'var(--color-error)' }} title={trace.error} />
                        : <i className="fas fa-check-circle" style={{ color: 'var(--color-success)' }} />}
                    </td>
                  </tr>
                  {expandedRow === i && (
                    <tr>
                      <td colSpan="6" style={{ padding: 0 }}>
                        <ApiTraceDetail trace={detail && detail.id === trace.id ? detail : trace} />
                      </td>
                    </tr>
                  )}
                </React.Fragment>
              ))}
            </tbody>
        </ResponsiveTable>
      ) : (
        <ResponsiveTable>
            <thead>
              <tr>
                <th style={{ width: '30px' }}></th>
                {sortableTh('type', 'Type')}
                {sortableTh('time', 'Time')}
                {sortableTh('model', 'Model')}
                <th>Summary</th>
                {sortableTh('duration', 'Duration')}
                <th style={{ width: '40px' }}>Status</th>
              </tr>
            </thead>
            <tbody>
              {sortedTraces.map((trace, i) => (
                <React.Fragment key={i}>
                  <tr onClick={() => toggleRow(i, trace)} style={{ cursor: 'pointer' }}>
                    <td><i className={`fas fa-chevron-${expandedRow === i ? 'down' : 'right'}`} style={{ fontSize: '0.7rem' }} /></td>
                    <td><span style={typeBadgeStyle(trace.type)}>{trace.type || '-'}</span></td>
                    <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', whiteSpace: 'nowrap' }}>{formatDateTime(trace.timestamp)}</td>
                    <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>{trace.model_name || '-'}</td>
                    <td style={{ maxWidth: '300px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {trace.summary || '-'}
                    </td>
                    <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>{formatDuration(trace.duration)}</td>
                    <td style={{ textAlign: 'center' }}>
                      {trace.error
                        ? <i className="fas fa-times-circle" style={{ color: 'var(--color-error)' }} title={trace.error} />
                        : <i className="fas fa-check-circle" style={{ color: 'var(--color-success)' }} />}
                    </td>
                  </tr>
                  {expandedRow === i && (
                    <tr>
                      <td colSpan="7" style={{ padding: 0 }}>
                        <BackendTraceDetail trace={detail && detail.id === trace.id ? detail : trace} />
                      </td>
                    </tr>
                  )}
                </React.Fragment>
              ))}
            </tbody>
        </ResponsiveTable>
      )}
    </div>
  )
}
