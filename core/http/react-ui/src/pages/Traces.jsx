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

// Latency as a bar as well as a figure. Scaled against the slowest request
// currently in view rather than an absolute ceiling: what matters when scanning
// a page of traces is which ones are the outliers here, and an absolute scale
// would flatten every row on a fast installation into nothing.
const SLOW_NS = 2_000_000_000

function LatencyCell({ ns, max }) {
  if (!ns && ns !== 0) return <span className="text-sub">-</span>
  const pct = max > 0 ? Math.max(2, Math.round((ns / max) * 100)) : 2
  const slow = ns >= SLOW_NS
  return (
    <span className="lat">
      <span className={`lat__bar${slow ? ' lat__bar--slow' : ''}`}>
        <i style={{ width: `${pct}%` }} />
      </span>
      <b>{formatDuration(ns)}</b>
    </span>
  )
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
    <div className="mb-md">
      <h4 className="hstack hstack--xs text-sm fw-semibold mb-xs">
        <i className="fas fa-headphones text-primary" /> Audio Snippet
      </h4>
      <div className="tr-well">
        {audioUrl
          ? <WaveformPlayer src={audioUrl} height={64} />
          : <div data-testid="audio-snippet-unavailable" className="text-xs text-secondary pad-xs">
              <i className="fas fa-triangle-exclamation" /> Audio clip not playable — it was truncated when recorded (raise Max Body Bytes in the tracing settings).
            </div>}
        <div className="tr-metrics mt-sm">
          {metrics.map(m => (
            <div key={m.label} className="tr-metric">
              <div className="text-secondary">{m.label}</div>
              <div className="text-mono">{m.value}</div>
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
      {!nested && <h4 className="text-sm fw-semibold mb-xs">Data Fields</h4>}
      <div className="tr-fields">
        {filtered.map(([key, value]) => {
          const objValue = isPlainObject(value)
          const large = !objValue && isLargeValue(value)
          const expandable = objValue || large
          const expanded = expandedFields[key]
          return (
            <div key={key} className="tr-field">
              <div
                onClick={expandable ? () => toggleField(key) : undefined}
                className={`tr-field__head${expandable ? ' tr-field__head--expandable' : ''}`}
              >
                {expandable ? (
                  <i className={`fas fa-chevron-${expanded ? 'down' : 'right'} tr-field__chevron`} />
                ) : (
                  <span className="tr-field__chevron" />
                )}
                <span className="tr-field__key">{key}</span>
                {objValue && !expanded && <span className="text-xs text-secondary">{fieldSummary(value)}</span>}
                {!objValue && !large && <span className="text-mono text-xs text-secondary">{formatValue(value)}</span>}
                {!objValue && large && !expanded && <span className="text-xs text-secondary cell-clip">{truncateValue(value, 120)}</span>}
              </div>
              {expanded && objValue && (
                <div className="tr-field__nested">
                  <DataFields data={value} nested />
                </div>
              )}
              {expanded && large && (
                <div className="tr-field__body">
                  <pre className="tr-code">
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
    <div className="tr-panel">
      {/* Summary cards */}
      <div className="tr-metrics tr-metrics--fixed mb-md">
        {infoItems.map(item => (
          <div key={item.label} className="tr-metric tr-metric--outlined">
            <div className="text-secondary">{item.label}</div>
            <div className="fw-medium">{item.label === 'Type' ? <span style={typeBadgeStyle(item.value)}>{item.value}</span> : item.value}</div>
          </div>
        ))}
      </div>

      {/* Error banner */}
      {trace.error && (
        <div className="tr-error mb-md">
          <i className="fas fa-exclamation-triangle text-error" />
          <span className="text-error text-sm">{trace.error}</span>
        </div>
      )}

      {/* Backend logs link — /app/backend-logs/:modelId is the unified entry
          point: in standalone mode it streams local logs, in distributed mode
          it resolves the model to the host worker(s) and either redirects to
          /app/node-backend-logs/<nodeId>/<modelId> or shows a node picker. */}
      {trace.model_name && (
        <div className="mb-md">
          <a
            href={`/app/backend-logs/${encodeURIComponent(trace.model_name)}${trace.timestamp ? `?from=${encodeURIComponent(trace.timestamp)}` : ''}`}
            className="hstack hstack--xs text-sm text-primary"
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
        <div className="mb-md">
          <h4 className="text-sm fw-semibold mb-xs">Request Body</h4>
          <pre className="tr-code">
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
    <div className="tr-panel">
      {meta.length > 0 && (
        <div className="tr-meta-grid mb-md">
          {meta.map(([label, value]) => (
            <React.Fragment key={label}>
              <span className="fw-semibold text-secondary">{label}</span>
              <span className="text-mono wrap-anywhere">{value}</span>
            </React.Fragment>
          ))}
        </div>
      )}
      {trace.error && (
        <div className="tr-error mb-md">
          <i className="fas fa-exclamation-triangle text-error" />
          <span className="text-error text-sm text-mono wrap-anywhere">{trace.error}</span>
        </div>
      )}
      <div className="tr-split">
        <div>
          <h4 className="text-sm fw-semibold mb-xs">Request Body</h4>
          <pre className="tr-code">
            {decodeTraceBody(trace.request?.body)}
          </pre>
        </div>
        <div>
          <h4 className="text-sm fw-semibold mb-xs">Response Body</h4>
          <pre className="tr-code">
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
      className="sortable-th" style={props.style}
    >
      {label}{sort.key === key && <i className={`fas fa-caret-${sort.dir === 'asc' ? 'up' : 'down'} ml-xs op-70`} aria-hidden="true" />}
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

  const slowestTrace = traces.reduce((m, t) => Math.max(m, t.duration || 0), 0)

  return (
    <div className="page page--wide">
      <PageHeader title={t('traces.title')} supporting={t('traces.subtitle')} />

      <div className="tabs">
        <button className={`tab ${activeTab === 'api' ? 'tab-active' : ''}`} onClick={() => setActiveTab('api')}>
          <i className="fas fa-exchange-alt icon-before text-xs" />
          API Traces
          <span className="ml-xs op-60 text-xs">({apiCount})</span>
        </button>
        <button className={`tab ${activeTab === 'backend' ? 'tab-active' : ''}`} onClick={() => setActiveTab('backend')}>
          <i className="fas fa-cogs icon-before text-xs" />
          Backend Traces
          <span className="ml-xs op-60 text-xs">({backendCount})</span>
        </button>
      </div>

      <div className="hstack mb-md">
        <button className="btn btn-secondary btn-sm" onClick={fetchTraces}><i className="fas fa-rotate" /> Refresh</button>
        <button className="btn btn-secondary btn-sm" onClick={handleExport} disabled={traces.length === 0}><i className="fas fa-download" /> Export</button>
        <div className="flex-1" />
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
        <div className={`tr-settings mb-md${allEnabled ? ' tr-settings--ok' : ''}`}>
          <button
            onClick={() => setSettingsExpanded(!settingsExpanded)}
            className="tr-settings__toggle"
          >
            <div className="hstack">
              <i className={`fas ${allEnabled ? 'fa-circle-check' : 'fa-exclamation-triangle'} shrink-0 ${allEnabled ? 'text-success' : 'text-warning'}`} />
              <span className="text-sm text-left">
                Tracing is <strong>{tracingEnabled ? 'enabled' : 'disabled'}</strong>
                {' · Backend logging is '}<strong>{backendLoggingEnabled ? 'enabled' : 'disabled'}</strong>
                {!tracingEnabled && ' — new requests will not be recorded'}
              </span>
            </div>
            <i className={`fas fa-chevron-${settingsExpanded ? 'up' : 'down'} text-meta shrink-0`} />
          </button>
          {settingsExpanded && (
            <div className="tr-settings__body">
              <SettingRow label="Enable Tracing" description="Record API requests, responses, and backend operations">
                <Toggle
                  checked={settings.enable_tracing}
                  onChange={(v) => setSettings(prev => ({ ...prev, enable_tracing: v }))}
                />
              </SettingRow>
              <SettingRow label="Max Items" description="Maximum trace items to retain (0 = unlimited)">
                <input
                  className="input col-w-120"
                  type="number"
                  value={settings.tracing_max_items ?? ''}
                  onChange={(e) => setSettings(prev => ({ ...prev, tracing_max_items: parseInt(e.target.value) || 0 }))}
                  placeholder="100"
                  disabled={!settings.enable_tracing}
                />
              </SettingRow>
              <SettingRow label="Max Body Bytes" description="Per-field cap for captured bodies and backend trace Data (0 = uncapped). Prevents oversized LLM histories or TTS snippets from locking this page in loading.">
                <input
                  className="input col-w-120"
                  type="number"
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
              <div className="form-group__actions hstack--end">
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
        <div className="loading-center"><LoadingSpinner size="lg" /></div>
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
                <th className="col-w-30"></th>
                {sortableTh('method', 'Method')}
                {sortableTh('path', 'Path')}
                {sortableTh('user', 'User')}
                {sortableTh('status', 'Status')}
                {sortableTh('duration', 'Latency')}
                <th className="col-w-40">Result</th>
              </tr>
            </thead>
            <tbody>
              {sortedTraces.map((trace, i) => (
                <React.Fragment key={i}>
                  <tr onClick={() => toggleRow(i, trace)} className="clickable">
                    <td><i className={`fas fa-chevron-${expandedRow === i ? 'down' : 'right'} text-xs`} /></td>
                    <td><span className="badge badge-info">{trace.request?.method || '-'}</span></td>
                    <td className="text-mono text-sm">{trace.request?.path || '-'}</td>
                    <td className="text-sub cell-clip" title={trace.user_name || trace.user_id || ''}>{trace.user_name || trace.user_id || '-'}</td>
                    <td><span className={`badge ${(trace.response?.status || 0) < 400 ? 'badge-success' : 'badge-error'}`}>{trace.response?.status || '-'}</span></td>
                    <td><LatencyCell ns={trace.duration} max={slowestTrace} /></td>
                    <td className="text-center">
                      {trace.error
                        ? <i className="fas fa-times-circle text-error" title={trace.error} />
                        : <i className="fas fa-check-circle text-success" />}
                    </td>
                  </tr>
                  {expandedRow === i && (
                    <tr>
                      <td colSpan="7" className="p-0">
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
                <th className="col-w-30"></th>
                {sortableTh('type', 'Type')}
                {sortableTh('time', 'Time')}
                {sortableTh('model', 'Model')}
                <th>Summary</th>
                {sortableTh('duration', 'Duration')}
                <th className="col-w-40">Status</th>
              </tr>
            </thead>
            <tbody>
              {sortedTraces.map((trace, i) => (
                <React.Fragment key={i}>
                  <tr onClick={() => toggleRow(i, trace)} className="clickable">
                    <td><i className={`fas fa-chevron-${expandedRow === i ? 'down' : 'right'} text-xs`} /></td>
                    <td><span style={typeBadgeStyle(trace.type)}>{trace.type || '-'}</span></td>
                    <td className="text-sub nowrap">{formatDateTime(trace.timestamp)}</td>
                    <td className="text-mono text-sm">{trace.model_name || '-'}</td>
                    <td className="cell-clip cell-clip--wide">
                      {trace.summary || '-'}
                    </td>
                    <td className="text-sub">{formatDuration(trace.duration)}</td>
                    <td className="text-center">
                      {trace.error
                        ? <i className="fas fa-times-circle text-error" title={trace.error} />
                        : <i className="fas fa-check-circle text-success" />}
                    </td>
                  </tr>
                  {expandedRow === i && (
                    <tr>
                      <td colSpan="7" className="p-0">
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
