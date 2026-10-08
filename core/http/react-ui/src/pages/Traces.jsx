/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { tracesApi, settingsApi, DEFAULT_TRACE_PAGE_SIZE } from '../utils/api'
import { formatDateTime } from '../utils/format'
import { durationText, filterTraces, nsToMs, saveFile, traceCounts, traceState, traceTime } from '../utils/traffic'
import { useTracingEnabled } from '../hooks/useTraffic'
import { usePolling } from '../hooks/usePolling'
import { cssVars } from '../utils/modelLedger'
import BackendTraceDetail from '../components/traffic/BackendTraceDetail'
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import './traffic.css'

const PAGE_SIZE = DEFAULT_TRACE_PAGE_SIZE
// A request this slow is marked, not only long.
const SLOW_MS = 2000

function clock(value) {
  const ms = traceTime(value)
  if (ms == null) return '-'
  return new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

// Latency as a bar as well as a figure. The bar is scaled against the slowest
// request in view, not an absolute ceiling: scanning a page of traces is about
// which are the outliers here, and an absolute scale would flatten every row on
// a fast installation into nothing.
function LatencyCell({ ns, max }) {
  const ms = nsToMs(ns)
  if (ms == null) return <span className="tf-sub">-</span>
  const pct = max > 0 ? Math.max(2, Math.round((ns / max) * 100)) : 2
  return (
    <span className="lat">
      <span className={`lat__bar${ms >= SLOW_MS ? ' lat__bar--slow' : ''}`}>
        <i style={cssVars({ width: `${pct}%` })} />
      </span>
      <b>{durationText(ms)}</b>
    </span>
  )
}

function SortHead({ col, label, sort, onSort, className }) {
  const active = sort.key === col
  return (
    <th scope="col" className={className} aria-sort={active ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}>
      <button type="button" className="dk-table-sort" onClick={() => onSort(s => ({ key: col, dir: s.key === col && s.dir === 'asc' ? 'desc' : 'asc' }))}>
        {label}
        <Icon name="arrow-up" className="dk-icon" />
      </button>
    </th>
  )
}

const API_SORT = {
  method: (a, b) => (a.request?.method || '').localeCompare(b.request?.method || ''),
  path: (a, b) => (a.request?.path || '').localeCompare(b.request?.path || ''),
  user: (a, b) => (a.user_name || a.user_id || '').localeCompare(b.user_name || b.user_id || ''),
  status: (a, b) => (a.response?.status || 0) - (b.response?.status || 0),
  time: (a, b) => (traceTime(a.timestamp) || 0) - (traceTime(b.timestamp) || 0),
  duration: (a, b) => (a.duration || 0) - (b.duration || 0),
}
const BACKEND_SORT = {
  type: (a, b) => (a.type || '').localeCompare(b.type || ''),
  time: (a, b) => (traceTime(a.timestamp) || 0) - (traceTime(b.timestamp) || 0),
  model: (a, b) => (a.model_name || '').localeCompare(b.model_name || ''),
  duration: (a, b) => (a.duration || 0) - (b.duration || 0),
}

function ResultCell({ trace }) {
  const { t } = useTranslation('traffic')
  const state = traceState(trace)
  if (state === 'running') return <span className="tf-result" data-level="info"><Icon name="spinner" spin title={t('traces.inProgress')} /> {t('traces.state.running')}</span>
  if (state === 'failed') return <span className="tf-result" data-level="error"><Icon name="alert-circle" title={trace.error || undefined} /> {t('traces.state.failed')}</span>
  if (state === 'refused') return <span className="tf-result" data-level="warn"><Icon name="warning" /> {t('traces.state.refused')}</span>
  return <span className="tf-result" data-level="ok"><Icon name="check-circle" /> {t('traces.state.ok')}</span>
}

// Tracing is how failures and latency are known, and it is off until someone
// turns it on. This is the settings strip: one line that says where it stands,
// and the four settings behind it.
function TracingStrip({ tracing, expanded, setExpanded, addToast }) {
  const { t } = useTranslation('traffic')
  const [saving, setSaving] = useState(false)
  const { settings, setSettings } = tracing
  if (!settings) return null
  const on = !!tracing.enabled
  const logging = !!tracing.backend
  const all = on && logging

  const save = async (next) => {
    setSaving(true)
    try {
      await settingsApi.save(next)
      tracing.reload()
      addToast?.(t('traces.settings.saved'), 'success')
      if (next.enable_tracing) setExpanded(false)
    } catch (err) {
      addToast?.(t('traces.settings.failed', { message: err.message }), 'error')
    } finally {
      setSaving(false)
    }
  }

  const set = (key, value) => setSettings({ ...settings, [key]: value })

  return (
    <div className={`tf-strip${all ? ' tf-strip--ok' : ''}`} data-testid="tracing-strip">
      <button type="button" className="tf-strip__toggle" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
        <Icon name={all ? 'check-circle' : 'warning'} />
        <span>
          {t('traces.settings.tracingIs')} <strong>{on ? t('traces.settings.enabled') : t('traces.settings.disabled')}</strong>
          {' · '}{t('traces.settings.loggingIs')} <strong>{logging ? t('traces.settings.enabled') : t('traces.settings.disabled')}</strong>
          {!on && ` · ${t('traces.settings.notRecorded')}`}
        </span>
        <Icon name={expanded ? 'chevron-up' : 'chevron-down'} />
      </button>
      {expanded && (
        <div className="tf-strip__body">
          <div className="tf-setting">
            <div><strong>{t('traces.settings.enable')}</strong><span className="tf-hint">{t('traces.settings.enableHint')}</span></div>
            <button type="button" className="dk-switch" role="switch" aria-checked={!!settings.enable_tracing} aria-label={t('traces.settings.enable')} onClick={() => set('enable_tracing', !settings.enable_tracing)} />
          </div>
          <label className="tf-setting">
            <div><strong>{t('traces.settings.maxItems')}</strong><span className="tf-hint">{t('traces.settings.maxItemsHint')}</span></div>
            <input className="dk-input dk-input--mono tf-setting__num" type="number" value={settings.tracing_max_items ?? ''} placeholder="100" disabled={!settings.enable_tracing}
              onChange={e => set('tracing_max_items', parseInt(e.target.value) || 0)} />
          </label>
          <label className="tf-setting">
            <div><strong>{t('traces.settings.maxBody')}</strong><span className="tf-hint">{t('traces.settings.maxBodyHint')}</span></div>
            <input className="dk-input dk-input--mono tf-setting__num" type="number" value={settings.tracing_max_body_bytes ?? ''} placeholder="65536" disabled={!settings.enable_tracing}
              onChange={e => set('tracing_max_body_bytes', parseInt(e.target.value) || 0)} />
          </label>
          <div className="tf-setting">
            <div><strong>{t('traces.settings.logging')}</strong><span className="tf-hint">{t('traces.settings.loggingHint')}</span></div>
            <button type="button" className="dk-switch" role="switch" aria-checked={!!settings.enable_backend_logging} aria-label={t('traces.settings.logging')} onClick={() => set('enable_backend_logging', !settings.enable_backend_logging)} />
          </div>
          <div className="tf-strip__foot">
            <button type="button" className="dk-btn dk-btn--primary" disabled={saving} onClick={() => save(settings)}>
              {saving ? <><LoadingSpinner size="sm" /> {t('traces.settings.saving')}</> : <><Icon name="save" /> {t('traces.settings.save')}</>}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

export default function Traces() {
  const { addToast } = useOutletContext() || {}
  const { t } = useTranslation('traffic')
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const [tab, setTab] = useState(() => searchParams.get('tab') === 'backend' ? 'backend' : 'api')
  const [apiPage, setApiPage] = useState({ items: [], total: 0 })
  const [backendPage, setBackendPage] = useState({ items: [], total: 0 })
  const [loading, setLoading] = useState(true)
  const [sort, setSort] = useState({ key: null, dir: 'asc' })
  const [open, setOpen] = useState(null)
  const [detail, setDetail] = useState(null)
  const [expanded, setExpanded] = useState(null)
  const tracing = useTracingEnabled()
  const state = searchParams.get('state') === 'failed' ? 'failed' : searchParams.get('state') === 'slow' ? 'slow' : 'all'
  const [query, setQuery] = useState(searchParams.get('q') || '')

  // Open the settings when tracing turns out to be off, once.
  useEffect(() => {
    if (tracing.enabled === false && expanded === null) setExpanded(true)
  }, [tracing.enabled, expanded])

  // Only a bounded page is fetched, and the server strips the bodies from list
  // entries. The unbounded form was a multi-megabyte transfer on every poll.
  const fetchTraces = useCallback(async () => {
    try {
      const [api, backend] = await Promise.all([
        tracesApi.get({ limit: PAGE_SIZE }),
        tracesApi.getBackend({ limit: PAGE_SIZE }),
      ])
      setApiPage(api)
      setBackendPage(backend)
    } catch (err) {
      // Tracing off is the default, not an error: the strip above says so.
      const disabled = /disabled|not enabled|404|not found/i.test(err?.message || '')
      if (!disabled) addToast?.(t('traces.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast, t])

  const { refetch } = usePolling(fetchTraces, 5000)

  useEffect(() => { setSort({ key: null, dir: 'asc' }); setOpen(null); setDetail(null) }, [tab])

  const setFilterState = (next) => {
    const params = new URLSearchParams(searchParams)
    if (next === 'all') params.delete('state'); else params.set('state', next)
    setSearchParams(params, { replace: true })
  }

  const traces = tab === 'api' ? apiPage.items : backendPage.items
  const counts = useMemo(() => traceCounts(apiPage.items, SLOW_MS), [apiPage.items])
  const filtered = useMemo(() => {
    if (tab !== 'api') {
      const needle = query.trim().toLowerCase()
      if (!needle) return traces
      return traces.filter(tr => [tr.model_name, tr.summary, tr.type, tr.error].some(v => v && String(v).toLowerCase().includes(needle)))
    }
    return filterTraces(traces, { state, query, slowMs: SLOW_MS })
  }, [tab, traces, state, query])
  const sorters = tab === 'api' ? API_SORT : BACKEND_SORT
  const sorted = useMemo(() => {
    const cmp = sort.key && sorters[sort.key]
    if (!cmp) return filtered
    return [...filtered].sort((a, b) => (sort.dir === 'asc' ? cmp(a, b) : cmp(b, a)))
  }, [filtered, sort, sorters])
  const slowest = traces.reduce((m, tr) => Math.max(m, tr.duration || 0), 0)

  const toggleBackend = async (row, index) => {
    const key = row?.id ?? index
    if (open === key) { setOpen(null); setDetail(null); return }
    setOpen(key)
    setDetail(null)
    if (!row?.id) return
    try { setDetail(await tracesApi.getBackendOne(row.id)) } catch { /* the row still shows what it has */ }
  }

  const clear = async () => {
    try {
      if (tab === 'api') await tracesApi.clear(); else await tracesApi.clearBackend()
      if (tab === 'api') setApiPage({ items: [], total: 0 }); else setBackendPage({ items: [], total: 0 })
      setOpen(null)
      setDetail(null)
      addToast?.(t('traces.cleared'), 'success')
    } catch (err) {
      addToast?.(t('traces.clearFailed', { message: err.message }), 'error')
    }
  }

  // Export asks for the full payloads: the list holds summaries, and an export
  // without bodies would be useless. It is the page the server holds, not the
  // whole buffer.
  const exportJson = async () => {
    let rows = sorted
    try {
      const page = tab === 'api'
        ? await tracesApi.get({ limit: PAGE_SIZE, full: true })
        : await tracesApi.getBackend({ limit: PAGE_SIZE, full: true })
      rows = page.items
    } catch (err) {
      addToast?.(t('traces.exportSummaries', { message: err.message }), 'error')
    }
    saveFile(`traces-${tab}-${new Date().toISOString().slice(0, 10)}.json`, JSON.stringify(rows, null, 2), 'application/json')
  }

  const off = tracing.enabled === false
  const backendOff = tracing.backend === false

  return (
    <div className="page page--wide tf-page" data-testid="traces-page">
      <header className="tf-head">
        <div className="tf-head__lead">
          <h1 className="tf-title">{t('traces.title')}</h1>
        </div>
        <div className="tf-head__acts">
          <button type="button" className="dk-btn dk-btn--secondary" onClick={refetch}><Icon name="refresh" /> {t('traces.refresh')}</button>
          <button type="button" className="dk-btn dk-btn--secondary" onClick={exportJson} disabled={traces.length === 0}><Icon name="download" /> {t('traces.export')}</button>
          {/* Stays enabled while loading: a huge trace buffer is the case where
              the table cannot be seen yet and Clear is how to recover. */}
          <button type="button" className="dk-btn dk-btn--secondary tf-danger" onClick={clear} disabled={!loading && traces.length === 0}><Icon name="trash" /> {t('traces.clear')}</button>
        </div>
      </header>

      <TracingStrip tracing={tracing} expanded={!!expanded} setExpanded={setExpanded} addToast={addToast} />

      <div className="tf-controls">
        <div className="dk-segmented" role="group" aria-label={t('traces.kind')}>
          <button type="button" className="dk-seg" aria-pressed={tab === 'api'} onClick={() => setTab('api')}>
            {t('traces.apiTab')} <span className="tf-count">({apiPage.total})</span>
          </button>
          <button type="button" className="dk-seg" aria-pressed={tab === 'backend'} onClick={() => setTab('backend')}>
            {t('traces.backendTab')} <span className="tf-count">({backendPage.total})</span>
          </button>
        </div>
      </div>

      {traces.length > 0 && !(tab === 'api' && off) && (
        <div className="tf-controls">
          {tab === 'api' && <div className="tf-chips" role="group" aria-label={t('traces.filter')}>
            <button type="button" className="dk-chip" aria-pressed={state === 'all'} onClick={() => setFilterState('all')}>{t('traces.all')} <span className="tf-count">{counts.all}</span></button>
            <button type="button" className="dk-chip" aria-pressed={state === 'failed'} onClick={() => setFilterState('failed')}>{t('traces.failed')} <span className="tf-count">{counts.failed}</span></button>
            <button type="button" className="dk-chip" aria-pressed={state === 'slow'} onClick={() => setFilterState('slow')}>{t('traces.slow')} <span className="tf-count">{counts.slow}</span></button>
          </div>}
          <div className="dk-input-icon tf-search">
            <Icon name="search" className="dk-icon" />
            <input className="dk-input" type="search" aria-label={t('traces.search')} placeholder={t('traces.search')} value={query} onChange={e => setQuery(e.target.value)} />
          </div>
        </div>
      )}

      {loading ? (
        <div className="tf-loading"><LoadingSpinner size="lg" /></div>
      ) : traces.length === 0 ? (
        <div className="dk-empty tf-empty" data-testid="traces-empty">
          <Icon name="waveform" className="dk-empty-icon" />
          <h2 className="dk-empty-title">
            {tab === 'api'
              ? (off ? t('traces.offTitle') : t('traces.noneTitle'))
              : (backendOff ? t('traces.backendOffTitle') : t('traces.backendNoneTitle'))}
          </h2>
          <p className="dk-empty-text">
            {tab === 'api'
              ? (off ? t('traces.offText') : t('traces.noneText'))
              : (backendOff ? t('traces.backendOffText') : t('traces.backendNoneText'))}
          </p>
          {tab === 'api' && off && tracing.settings && (
            <div className="tf-empty__acts">
              <button type="button" className="dk-btn dk-btn--primary" data-testid="turn-on-tracing" onClick={async () => {
                try {
                  await settingsApi.save({ ...tracing.settings, enable_tracing: true })
                  tracing.reload()
                  addToast?.(t('traces.settings.saved'), 'success')
                } catch (err) {
                  addToast?.(t('traces.settings.failed', { message: err.message }), 'error')
                }
              }}>{t('traces.turnOn')}</button>
            </div>
          )}
          {tab === 'api' && off && <p className="tf-note-line">{t('traces.offEnv')}</p>}
        </div>
      ) : sorted.length === 0 ? (
        <div className="dk-empty tf-empty" data-testid="traces-nomatch">
          <h2 className="dk-empty-title">{t('traces.noMatch')}</h2>
          <p className="dk-empty-text">{t('traces.noMatchText')}</p>
        </div>
      ) : tab === 'api' ? (
        <div className="dk-table-wrap" data-testid="api-traces-table">
          <table className="dk-table tf-table">
            <caption className="dk-sr-only">{t('traces.apiTab')}</caption>
            <thead>
              <tr>
                <SortHead col="time" label={t('traces.col.time')} sort={sort} onSort={setSort} className="dk-hide-phone" />
                <SortHead col="method" label={t('traces.col.method')} sort={sort} onSort={setSort} />
                <SortHead col="path" label={t('traces.col.path')} sort={sort} onSort={setSort} />
                <SortHead col="user" label={t('traces.col.user')} sort={sort} onSort={setSort} className="dk-hide-phone" />
                <SortHead col="status" label={t('traces.col.status')} sort={sort} onSort={setSort} />
                <SortHead col="duration" label={t('traces.col.latency')} sort={sort} onSort={setSort} className="dk-hide-phone" />
                <th scope="col">{t('traces.col.result')}</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((trace, i) => {
                const status = trace.response?.status
                const href = trace.id ? `/app/traces/${encodeURIComponent(trace.id)}` : null
                return (
                  <tr key={trace.id ?? i} data-row data-clickable={href ? '' : undefined} data-entity={trace.request?.path}
                    onClick={e => { if (href && !e.target.closest('a, button')) navigate(href) }}
                    data-href={href || undefined}>
                    <td className="dk-hide-phone dk-mono tf-sub">{clock(trace.timestamp)}</td>
                    <td><span className="dk-chip dk-chip--sm tf-method">{trace.request?.method || '-'}</span></td>
                    <td>
                      {href
                        ? <Link className="dk-table-name dk-mono tf-name" to={href}>{trace.request?.path || '-'}</Link>
                        : <span className="dk-table-name dk-mono">{trace.request?.path || '-'}</span>}
                      {trace.error && <span className="dk-table-sub tf-error-text">{trace.error}</span>}
                    </td>
                    <td className="dk-hide-phone tf-sub" title={trace.user_name || trace.user_id || ''}>{trace.user_name || trace.user_id || '-'}</td>
                    <td className="dk-mono" data-level={traceState(trace) === 'failed' ? 'error' : undefined}>
                      {status === 0 ? t('traces.state.running') : status == null ? '-' : status}
                    </td>
                    <td className="dk-hide-phone"><LatencyCell ns={trace.duration} max={slowest} /></td>
                    <td><ResultCell trace={trace} /></td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="dk-table-wrap" data-testid="backend-traces-table">
          <table className="dk-table tf-table">
            <caption className="dk-sr-only">{t('traces.backendTab')}</caption>
            <thead>
              <tr>
                <th scope="col" className="dk-table-toggle-cell"><span className="dk-sr-only">{t('table.open')}</span></th>
                <SortHead col="type" label={t('traces.col.type')} sort={sort} onSort={setSort} />
                <SortHead col="time" label={t('traces.col.time')} sort={sort} onSort={setSort} className="dk-hide-phone" />
                <SortHead col="model" label={t('traces.col.model')} sort={sort} onSort={setSort} />
                <th scope="col" className="dk-hide-phone">{t('traces.col.summary')}</th>
                <SortHead col="duration" label={t('traces.col.duration')} sort={sort} onSort={setSort} className="dk-num" />
                <th scope="col">{t('traces.col.status')}</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((trace, i) => {
                const key = trace.id ?? i
                const isOpen = open === key
                const ms = nsToMs(trace.duration)
                return (
                  <Fragment key={key}>
                    <tr data-row data-clickable onClick={() => toggleBackend(trace, i)}>
                      <td className="dk-table-toggle-cell"><Icon name={isOpen ? 'chevron-down' : 'chevron-right'} className="dk-icon" /></td>
                      <td><span className="dk-chip dk-chip--sm">{trace.type || '-'}</span></td>
                      <td className="dk-hide-phone dk-mono tf-sub">{formatDateTime(trace.timestamp)}</td>
                      <td className="dk-mono">{trace.model_name || '-'}</td>
                      <td className="dk-hide-phone"><span className="dk-table-cut">{trace.summary || '-'}</span></td>
                      <td className="dk-num">{ms == null ? '-' : durationText(ms)}</td>
                      <td>
                        {trace.status === 'running'
                          ? <span className="tf-result" data-level="info"><Icon name="spinner" spin title={t('traces.running')} /> {t('traces.state.running')}</span>
                          : trace.error
                            ? <span className="tf-result" data-level="error"><Icon name="alert-circle" title={trace.error} /> {t('traces.state.failed')}</span>
                            : <span className="tf-result" data-level="ok"><Icon name="check-circle" /> {t('traces.state.ok')}</span>}
                      </td>
                    </tr>
                    {isOpen && (
                      <tr className="dk-table-detail">
                        <td colSpan={7}>
                          <div className="dk-table-detail-body">
                            <BackendTraceDetail trace={detail && detail.id === trace.id ? detail : trace} />
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {traces.length > 0 && (
        <p className="tf-note-line">{t('traces.bufferNote', { shown: traces.length, total: tab === 'api' ? apiPage.total : backendPage.total })}</p>
      )}
    </div>
  )
}
