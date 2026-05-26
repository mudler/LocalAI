import { useState, useEffect, useCallback, useRef, useMemo, Fragment } from 'react'
import { useOutletContext, Link, useNavigate } from 'react-router-dom'
import { apiUrl } from '../utils/basePath'
import { settingsApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'

// Middleware admin page. Three tabs:
//   - Filtering: PII pattern catalogue + per-model resolved state +
//     pattern-action editor (PUT /api/pii/patterns/:id, transient).
//   - Routing: placeholder until subsystem 2 lands. Renders the note
//     from /api/router/status so admins see "not yet implemented" rather
//     than an empty page.
//   - Events: recent PIIEvent rows from /api/pii/events. The page
//     intentionally NEVER displays the redacted content (the redactor
//     never stores it); only pattern_id, byte_offset, length, and an
//     8-char sha256 prefix admins can use to dedupe recurring leaks.
//
// Wiring is admin-only: RequireAdmin in router.jsx already redirects
// non-admin viewers; in single-user no-auth mode the local user has
// admin role so the page works without --auth.

const TABS = [
  { id: 'filtering', label: 'Filtering', icon: 'fa-shield-halved' },
  { id: 'routing', label: 'Routing', icon: 'fa-route' },
  { id: 'proxy', label: 'MITM Proxy', icon: 'fa-shield' },
  { id: 'events', label: 'Events', icon: 'fa-list-ul' },
]

const ACTIONS = ['mask', 'block', 'route_local']

function actionBadge(action) {
  const colors = {
    mask: 'var(--color-primary)',
    block: 'var(--color-error)',
    route_local: 'var(--color-warning)',
  }
  return (
    <span style={{
      display: 'inline-block',
      padding: '2px 8px',
      fontSize: '0.6875rem',
      fontWeight: 600,
      borderRadius: 'var(--radius-sm)',
      background: colors[action] || 'var(--color-bg-tertiary)',
      color: 'white',
      fontFamily: 'var(--font-mono)',
      textTransform: 'uppercase',
    }}>
      {action}
    </span>
  )
}

function enabledBadge(enabled) {
  return (
    <span style={{
      display: 'inline-block',
      padding: '2px 8px',
      fontSize: '0.6875rem',
      fontWeight: 600,
      borderRadius: 'var(--radius-sm)',
      background: enabled ? 'var(--color-success, #22c55e)' : 'var(--color-bg-tertiary)',
      color: enabled ? 'white' : 'var(--color-text-muted)',
      fontFamily: 'var(--font-mono)',
      textTransform: 'uppercase',
    }}>
      {enabled ? 'on' : 'off'}
    </span>
  )
}

export default function Middleware() {
  const { addToast } = useOutletContext()
  const [status, setStatus] = useState(null)
  const [events, setEvents] = useState([])
  const [decisions, setDecisions] = useState([])
  const [loading, setLoading] = useState(true)
  const [activeTab, setActiveTab] = useState('filtering')
  const [pendingPattern, setPendingPattern] = useState(null) // id while a PUT is in flight

  // silent=true on background polls: skips the loading spinner and
  // suppresses toast spam if the server is briefly unreachable.
  const fetchAll = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const [statusRes, eventsRes, decisionsRes] = await Promise.all([
        fetch(apiUrl('/api/middleware/status')),
        fetch(apiUrl('/api/pii/events?limit=100')),
        fetch(apiUrl('/api/router/decisions?limit=100')),
      ])
      if (!statusRes.ok) throw new Error(`status: HTTP ${statusRes.status}`)
      const statusData = await statusRes.json()
      setStatus(statusData)
      if (eventsRes.ok) {
        const data = await eventsRes.json()
        setEvents(data.events || [])
      }
      if (decisionsRes.ok) {
        const data = await decisionsRes.json()
        setDecisions(data.decisions || [])
      }
    } catch (err) {
      if (!silent) addToast(`Failed to load middleware status: ${err.message}`, 'error')
    } finally {
      if (!silent) setLoading(false)
    }
  }, [addToast])

  useEffect(() => { fetchAll() }, [fetchAll])

  // Auto-refresh every 5s so admins watching the Events / Routing tabs
  // see new rows without manual refresh. Matches the Traces page cadence.
  // ProxyTab guards against clobbering mid-typed config via its own
  // `dirty` check, so the poll is safe while the form is in use.
  const refreshRef = useRef(null)
  useEffect(() => {
    refreshRef.current = setInterval(() => fetchAll(true), 5000)
    return () => clearInterval(refreshRef.current)
  }, [fetchAll])

  const mutatePattern = async (patternID, body, successMsg) => {
    setPendingPattern(patternID)
    try {
      const res = await fetch(apiUrl(`/api/pii/patterns/${encodeURIComponent(patternID)}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || `HTTP ${res.status}`)
      }
      addToast(successMsg, 'success')
      await fetchAll()
    } catch (err) {
      addToast(`Failed to update pattern: ${err.message}`, 'error')
    } finally {
      setPendingPattern(null)
    }
  }

  const setPatternAction = (patternID, action) =>
    mutatePattern(patternID, { action }, `Pattern ${patternID}: action ${action} (transient — click "Save to disk" to persist)`)

  const setPatternDisabled = (patternID, disabled) =>
    mutatePattern(patternID, { disabled }, `Pattern ${patternID}: ${disabled ? 'disabled' : 'enabled'} (transient — click "Save to disk" to persist)`)

  const [persisting, setPersisting] = useState(false)
  const persistPatterns = async () => {
    setPersisting(true)
    try {
      const res = await fetch(apiUrl('/api/pii/patterns/persist'), { method: 'POST' })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || `HTTP ${res.status}`)
      }
      const data = await res.json().catch(() => ({}))
      addToast(`Saved ${data.override_count ?? 0} pattern override(s) to runtime_settings.json`, 'success')
    } catch (err) {
      addToast(`Failed to persist: ${err.message}`, 'error')
    } finally {
      setPersisting(false)
    }
  }

  return (
    <div className="page page--wide">
      <div className="page-header" style={{ marginBottom: 'var(--spacing-sm)' }}>
        <h1 className="page-title">Middleware</h1>
        <p className="page-subtitle">
          Inspect and configure routing-module middleware: PII filtering and intelligent routing.
        </p>
      </div>

      {/* Tab bar */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-md)', flexWrap: 'wrap' }}>
        {TABS.map(tab => (
          <button
            key={tab.id}
            className={`btn btn-sm ${activeTab === tab.id ? 'btn-primary' : 'btn-secondary'}`}
            onClick={() => setActiveTab(tab.id)}
          >
            <i className={`fas ${tab.icon}`} style={{ marginRight: 4 }} />
            {tab.label}
          </button>
        ))}
        <div style={{ flex: 1 }} />
        <button className="btn btn-secondary btn-sm" onClick={fetchAll} disabled={loading}>
          <i className={`fas fa-rotate${loading ? ' fa-spin' : ''}`} /> Refresh
        </button>
      </div>

      {loading && !status ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <LoadingSpinner size="lg" />
        </div>
      ) : activeTab === 'filtering' ? (
        <FilteringTab
          status={status}
          pendingPattern={pendingPattern}
          onSetAction={setPatternAction}
          onSetDisabled={setPatternDisabled}
          onPersist={persistPatterns}
          persisting={persisting}
        />
      ) : activeTab === 'routing' ? (
        <RoutingTab status={status} decisions={decisions} />
      ) : activeTab === 'proxy' ? (
        <ProxyTab status={status} addToast={addToast} onChanged={fetchAll} />
      ) : (
        <EventsTab events={events} />
      )}
    </div>
  )
}

function FilteringTab({ status, pendingPattern, onSetAction, onSetDisabled, onPersist, persisting }) {
  if (!status?.pii) return null
  const pii = status.pii

  if (!pii.enabled_globally) {
    return (
      <div className="empty-state">
        <div className="empty-state-icon"><i className="fas fa-shield-slash" /></div>
        <h2 className="empty-state-title">PII filtering disabled</h2>
        <p className="empty-state-text">
          The PII filter is disabled by <code>{pii.reason || '--disable-pii'}</code>.
          Restart without that flag to enable it.
        </p>
      </div>
    )
  }

  return (
    <>
      {/* Default rule banner */}
      <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-sm)' }}>
          <i className="fas fa-info-circle" style={{ color: 'var(--color-text-muted)', marginTop: 2 }} />
          <div>
            <div style={{ fontWeight: 600, marginBottom: 4 }}>Default policy</div>
            <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
              PII redaction is per-model and OFF by default. Backends matching <code>{(pii.default_enabled_for_backends || []).join(', ')}</code> default to ON (cloud passthroughs). Override per model with <code>pii: {'{'} enabled: true {'}'}</code> in the model YAML.
            </div>
          </div>
        </div>
      </div>

      {/* Patterns table */}
      <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
          <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>Active patterns</span>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
            <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
              Toggle / action edits are transient — click Save to disk to persist.
            </span>
            <button
              className="btn btn-secondary btn-sm"
              onClick={onPersist}
              disabled={persisting}
              style={{ fontSize: '0.75rem' }}
            >
              <i className={`fas ${persisting ? 'fa-spinner fa-spin' : 'fa-save'}`} /> Save to disk
            </button>
          </div>
        </div>
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 80 }}>Enabled</th>
                <th style={{ width: 140 }}>Pattern</th>
                <th>Description</th>
                <th style={{ width: 110 }}>Action</th>
                <th style={{ width: 250 }}>Change</th>
              </tr>
            </thead>
            <tbody>
              {pii.patterns.map(p => {
                const enabled = !p.disabled
                const muted = p.disabled
                return (
                <tr key={p.id} style={muted ? { opacity: 0.55 } : undefined}>
                  <td>
                    <input
                      type="checkbox"
                      checked={enabled}
                      disabled={pendingPattern === p.id}
                      onChange={e => onSetDisabled(p.id, !e.target.checked)}
                      style={{ cursor: 'pointer' }}
                      aria-label={`Enable ${p.id} pattern`}
                    />
                  </td>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem', fontWeight: 600 }}>{p.id}</td>
                  <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>{p.description}</td>
                  <td>{actionBadge(p.action)}</td>
                  <td>
                    <div style={{ display: 'flex', gap: 4 }}>
                      {ACTIONS.map(a => (
                        <button
                          key={a}
                          className={`btn btn-sm ${p.action === a ? 'btn-primary' : 'btn-secondary'}`}
                          onClick={() => onSetAction(p.id, a)}
                          disabled={pendingPattern === p.id || p.action === a || p.disabled}
                          style={{ fontSize: '0.6875rem', padding: '2px 8px' }}
                        >
                          {a}
                        </button>
                      ))}
                    </div>
                  </td>
                </tr>
              )})}
            </tbody>
          </table>
        </div>
      </div>

      {/* Per-model resolved state */}
      <div className="card" style={{ padding: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
          <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>Per-model state</span>
          <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
            Edit the model YAML to change these.
          </span>
        </div>
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th>Model</th>
                <th style={{ width: 120 }}>Backend</th>
                <th style={{ width: 80 }}>PII</th>
                <th style={{ width: 110 }}>Source</th>
                <th>Pattern overrides</th>
                <th style={{ width: 80 }}>Edit</th>
              </tr>
            </thead>
            <tbody>
              {(pii.models || []).map(m => (
                <tr key={m.name}>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>{m.name}</td>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>{m.backend || '—'}</td>
                  <td>{enabledBadge(m.enabled)}</td>
                  <td style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
                    {m.explicit ? 'YAML' : (m.default_for_backend ? 'backend default' : 'default off')}
                  </td>
                  <td style={{ fontSize: '0.75rem', fontFamily: 'var(--font-mono)' }}>
                    {m.overrides && Object.keys(m.overrides).length > 0
                      ? Object.entries(m.overrides).map(([k, v]) => `${k}=${v}`).join(', ')
                      : <span style={{ color: 'var(--color-text-muted)' }}>—</span>}
                  </td>
                  <td>
                    <Link
                      to={`/app/model-editor/${encodeURIComponent(m.name)}`}
                      className="btn btn-secondary btn-sm"
                      style={{ fontSize: '0.6875rem', padding: '2px 8px' }}
                      title={`Edit ${m.name}.yaml`}
                    >
                      <i className="fas fa-pen-to-square" /> Edit
                    </Link>
                  </td>
                </tr>
              ))}
              {(!pii.models || pii.models.length === 0) && (
                <tr>
                  <td colSpan={6} style={{ textAlign: 'center', color: 'var(--color-text-muted)', padding: 'var(--spacing-md)' }}>
                    No models loaded.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </>
  )
}

// decisionActiveSet rebuilds the Set of active labels from a
// DecisionRecord's comma-joined `label` column. Used by both the
// collapsed-row score suffix and the expanded-row bar rendering.
function decisionActiveSet(d) {
  return new Set((d?.label || '').split(',').filter(Boolean))
}

// formatDecisionScoreSuffix renders the top active label's score
// next to the label cell so operators can spot uncertain calls at a
// glance without expanding the row. Empty when the decision came from
// the cache or fallback — both cases lack per-label scores.
function formatDecisionScoreSuffix(d, activeSet) {
  if (!d?.label_scores?.length) return ''
  const top = d.label_scores
    .filter(ls => activeSet.has(ls.label))
    .sort((a, b) => b.score - a.score)[0]
  if (!top) return ''
  return ` ${(top.score * 100).toFixed(0)}%`
}

// LabelBar is one row in the expanded decision view — a horizontal
// score bar with a vertical marker at the activation threshold so
// operators can see how close inactive labels got to firing.
function LabelBar({ label, score, threshold, active }) {
  const scorePct = Math.max(0, Math.min(100, score * 100))
  const thresholdPct = Math.max(0, Math.min(100, (threshold || 0) * 100))
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', fontFamily: 'var(--font-mono)', fontSize: '0.75rem' }}>
      <div style={{
        width: 160,
        color: active ? 'var(--color-text)' : 'var(--color-text-muted)',
        fontWeight: active ? 600 : 400,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
      }} title={label}>
        {label}
      </div>
      <div style={{ flex: 1, position: 'relative', height: 14, background: 'var(--color-border, #e5e7eb)', borderRadius: 2 }}>
        <div style={{
          width: `${scorePct}%`,
          height: '100%',
          background: active ? 'var(--color-success, #2da44e)' : 'var(--color-text-muted)',
          opacity: active ? 1 : 0.45,
          borderRadius: 2,
        }} />
        {threshold > 0 && (
          <div
            title={`Activation threshold ${thresholdPct.toFixed(0)}%`}
            style={{
              position: 'absolute',
              top: -3,
              left: `${thresholdPct}%`,
              width: 2,
              height: 20,
              background: 'var(--color-warning, #d97706)',
              transform: 'translateX(-1px)',
              pointerEvents: 'none',
            }}
          />
        )}
      </div>
      <div style={{ width: 56, textAlign: 'right', color: 'var(--color-text-muted)' }}>
        {scorePct.toFixed(1)}%
      </div>
    </div>
  )
}

// DecisionDetail renders the per-label bar breakdown for one decision.
// Empty-state messaging covers cached and fallback rows where the
// classifier never produced per-label scores.
function DecisionDetail({ d }) {
  if (!d.label_scores?.length) {
    return (
      <div style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem', fontStyle: 'italic' }}>
        {d.cached
          ? 'Cached decision — per-label scores not recorded (the cache stores only the resulting label set).'
          : 'No per-label scores recorded for this decision (likely a fallback row).'}
      </div>
    )
  }
  const threshold = d.activation_threshold || 0
  const active = decisionActiveSet(d)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, maxWidth: 720 }}>
      <div style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
        Activation threshold:&nbsp;
        <span style={{ color: 'var(--color-warning, #d97706)', fontWeight: 600 }}>
          {(threshold * 100).toFixed(0)}%
        </span>
        &nbsp;(orange marker on each bar)
      </div>
      {d.label_scores.map(ls => (
        <LabelBar
          key={ls.label}
          label={ls.label}
          score={ls.score}
          threshold={threshold}
          active={active.has(ls.label)}
        />
      ))}
    </div>
  )
}

function RoutingTab({ status, decisions }) {
  const navigate = useNavigate()
  const router = status?.router || { configured: false }
  const [expanded, setExpanded] = useState(() => new Set())

  // Precompute per-row formatter strings once per decisions update.
  // The score suffix is shown in the collapsed row so operators can
  // scan top-label confidence without expanding everything.
  const decisionRows = useMemo(() => (decisions || []).map(d => {
    const active = decisionActiveSet(d)
    return {
      ...d,
      _scoreSuffix: formatDecisionScoreSuffix(d, active),
    }
  }), [decisions])

  const toggleExpanded = useCallback(id => {
    setExpanded(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  if (!router.configured || !router.models || router.models.length === 0) {
    return (
      <div className="empty-state">
        <div className="empty-state-icon"><i className="fas fa-route" /></div>
        <h2 className="empty-state-title">No routers configured</h2>
        <p className="empty-state-text">
          {router.note || 'Add a `router:` block to a model YAML to enable intelligent routing. The classifier picks one of the listed candidates per request and the standard model-resolution path runs against the chosen target.'}
        </p>
        <button
          className="btn btn-primary"
          style={{ marginTop: 'var(--spacing-md)' }}
          onClick={() => navigate('/app/model-editor?template=router')}
        >
          <i className="fas fa-plus" /> Create routing model
        </button>
      </div>
    )
  }

  return (
    <>
      {/* Configured router models */}
      <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
          <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>Active routers</span>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
            <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
              Edit the router model YAML to change candidates or rules.
            </span>
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => navigate('/app/model-editor?template=router')}
              title="Open the model editor with the Routing Model template pre-selected"
            >
              <i className="fas fa-plus" /> Add routing model
            </button>
          </div>
        </div>
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 160 }}>Model</th>
                <th style={{ width: 110 }}>Classifier</th>
                <th>Candidates</th>
                <th style={{ width: 200 }}>Embedding cache</th>
                <th style={{ width: 140 }}>Fallback</th>
              </tr>
            </thead>
            <tbody>
              {router.models.map(m => (
                <tr key={m.name}>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem', fontWeight: 600 }}>{m.name}</td>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem' }}>{m.classifier}</td>
                  <td style={{ fontSize: '0.75rem' }}>
                    {(m.candidates || []).map((c, i) => (
                      <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 6, fontFamily: 'var(--font-mono)' }}>
                        <span style={{ minWidth: 100, color: 'var(--color-primary)' }}>{(c.labels || []).join(', ') || '—'}</span>
                        <span style={{ color: 'var(--color-text-muted)' }}>→</span>
                        <span>{c.model}</span>
                      </div>
                    ))}
                  </td>
                  <td style={{ fontSize: '0.75rem' }}>
                    <RouterCacheCell cache={m.embedding_cache} />
                  </td>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
                    {m.fallback || '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Recent decisions */}
      <div className="card" style={{ padding: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
          <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>Recent decisions</span>
          <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
            Newest first, capped at 100.
          </span>
        </div>
        {(!decisions || decisions.length === 0) ? (
          <div style={{ padding: 'var(--spacing-md)', textAlign: 'center', color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}>
            No routing decisions yet. Send a request to a router model to populate this log.
          </div>
        ) : (
          <div className="table-container">
            <table className="table">
              <thead>
                <tr>
                  <th style={{ width: 170 }}>Time</th>
                  <th style={{ width: 130 }}>Router</th>
                  <th style={{ width: 80 }}>Label</th>
                  <th style={{ width: 130 }}>Served</th>
                  <th style={{ width: 90 }}>Latency</th>
                  <th>Correlation</th>
                </tr>
              </thead>
              <tbody>
                {decisionRows.map(d => {
                  const isExpanded = expanded.has(d.id)
                  return (
                    <Fragment key={d.id}>
                      <tr
                        onClick={() => toggleExpanded(d.id)}
                        style={{ cursor: 'pointer' }}
                        title={isExpanded ? 'Click to collapse' : 'Click to see per-label score breakdown'}
                      >
                        <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
                          <span style={{ display: 'inline-block', width: 12, color: 'var(--color-text-muted)' }}>
                            {isExpanded ? '▼' : '▶'}
                          </span>
                          {d.created_at}
                        </td>
                        <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem' }}>{d.router_model}</td>
                        <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', fontWeight: 600 }}>
                          {d.label}
                          {d._scoreSuffix}
                        </td>
                        <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem' }}>{d.served_model}</td>
                        <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>{d.latency_ms}ms</td>
                        <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
                          {d.correlation_id || '—'}
                        </td>
                      </tr>
                      {isExpanded && (
                        <tr>
                          <td colSpan={6} style={{ background: 'var(--color-bg-muted, #f6f8fa)', padding: 'var(--spacing-md)' }}>
                            <DecisionDetail d={d} />
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
      </div>
    </>
  )
}

function ProxyTab({ status, addToast, onChanged }) {
  const navigate = useNavigate()
  const mitm = status?.mitm
  const serverListen = mitm?.configured_addr || ''

  const [listen, setListen] = useState(serverListen)
  const [saving, setSaving] = useState(false)

  const dirty = listen !== serverListen

  // Refresh local state from the server only when the user has no
  // pending edits to clobber.
  useEffect(() => {
    if (dirty) return
    setListen(serverListen)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverListen])

  const save = async () => {
    setSaving(true)
    try {
      const body = await settingsApi.save({ mitm_listen: listen })
      if (body && body.success === false) {
        throw new Error(body.error || 'unknown error')
      }
      addToast('MITM proxy settings updated', 'success')
      onChanged?.()
    } catch (err) {
      addToast(`Failed to save: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  if (!mitm) {
    return (
      <div className="empty-state">
        <div className="empty-state-icon"><i className="fas fa-shield" /></div>
        <h2 className="empty-state-title">MITM proxy status unavailable</h2>
        <p className="empty-state-text">The status endpoint did not return a mitm section.</p>
      </div>
    )
  }

  const conflicts = mitm.host_conflicts || {}
  const owners = mitm.host_owners || {}
  const conflictHosts = Object.keys(conflicts)
  const ownerEntries = Object.entries(owners)
  const mitmModels = mitm.models || []

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
      {conflictHosts.length > 0 && (
        <div className="card" style={{ padding: 'var(--spacing-md)', borderLeft: '3px solid var(--color-error)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-xs)' }}>
            <i className="fas fa-triangle-exclamation" style={{ color: 'var(--color-error)' }} />
            <span style={{ fontWeight: 600 }}>MITM listener disabled — duplicate host claims</span>
          </div>
          <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', margin: 0 }}>
            Each MITM intercept host must be owned by exactly one model config. Resolve by editing the conflicting model YAMLs.
          </p>
          <ul style={{ margin: 'var(--spacing-xs) 0 0', paddingLeft: 20, fontSize: '0.8125rem' }}>
            {conflictHosts.map(h => (
              <li key={h}>
                <code style={{ fontFamily: 'var(--font-mono)' }}>{h}</code>
                {' claimed by: '}
                {(conflicts[h] || []).map(name => (
                  <Link key={name} to={`/app/model-editor/${encodeURIComponent(name)}`} style={{ marginRight: 6, fontFamily: 'var(--font-mono)' }}>
                    {name}
                  </Link>
                ))}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="card" style={{ padding: 'var(--spacing-lg)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
          <h2 style={{ fontSize: '1rem', fontWeight: 600, margin: 0 }}>State</h2>
          {enabledBadge(mitm.running)}
          {mitm.running && (
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
              listening on {mitm.listen_addr}
            </span>
          )}
        </div>
        <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-sm)' }}>
          The MITM proxy terminates TLS for allowlisted hosts so PII redaction
          can run on traffic from clients that authenticate via OAuth /
          subscription (Claude Code, Codex CLI). Non-allowlisted hosts get a
          plain CONNECT tunnel — no inspection, no CA-trust required.
        </p>
        {ownerEntries.length > 0 ? (
          <div style={{ marginBottom: 'var(--spacing-sm)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
            <div style={{ marginBottom: 4 }}>Hosts claimed by model configs (PII settings flow from the owning config):</div>
            <ul style={{ margin: 0, paddingLeft: 20, fontFamily: 'var(--font-mono)' }}>
              {ownerEntries.map(([host, name]) => (
                <li key={host}>
                  {host} → <Link to={`/app/model-editor/${encodeURIComponent(name)}`}>{name}</Link>
                </li>
              ))}
            </ul>
          </div>
        ) : (
          <div style={{ marginBottom: 'var(--spacing-sm)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
            No model config declares an MITM intercept host. Without one, every CONNECT tunnels through unmodified. Create one from the Add Model page using the MITM Intercept template.
          </div>
        )}
        {mitm.ca_available ? (
          <a
            className="btn btn-secondary btn-sm"
            href={apiUrl(mitm.ca_cert_url)}
            download="localai-mitm-ca.crt"
          >
            <i className="fas fa-download" /> Download CA cert
          </a>
        ) : (
          <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
            CA not generated yet — start the listener to generate it.
          </span>
        )}
      </div>

      <div className="card" style={{ padding: 'var(--spacing-lg)' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
          <h2 style={{ fontSize: '1rem', fontWeight: 600, margin: 0 }}>MITM Models</h2>
          <button
            className="btn btn-secondary btn-sm"
            onClick={() => navigate('/app/model-editor?template=mitm')}
            title="Open the model editor with the MITM Intercept template pre-selected"
          >
            <i className="fas fa-plus" /> Add MITM model
          </button>
        </div>
        {mitmModels.length === 0 ? (
          <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>
            No model config declares <code>mitm.hosts</code>. Use the Add MITM model button above — the template defaults to <code>api.anthropic.com</code> with PII filtering on.
          </div>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>Model</th>
                <th>Hosts</th>
                <th style={{ width: 80 }}>PII</th>
                <th style={{ width: 80 }}>Edit</th>
              </tr>
            </thead>
            <tbody>
              {mitmModels.map(m => (
                <tr key={m.name}>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem', fontWeight: 600 }}>{m.name}</td>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem' }}>
                    {(m.hosts || []).join(', ')}
                  </td>
                  <td>{enabledBadge(m.pii_enabled)}</td>
                  <td>
                    <Link
                      to={`/app/model-editor/${encodeURIComponent(m.name)}`}
                      className="btn btn-secondary btn-sm"
                      style={{ fontSize: '0.6875rem', padding: '2px 8px' }}
                    >
                      <i className="fas fa-pen-to-square" /> Edit
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card" style={{ padding: 'var(--spacing-lg)' }}>
        <h2 style={{ fontSize: '1rem', fontWeight: 600, marginTop: 0, marginBottom: 'var(--spacing-md)' }}>Configuration</h2>

        <label style={{ display: 'block', marginBottom: 'var(--spacing-md)' }}>
          <div style={{ fontSize: '0.875rem', fontWeight: 500, marginBottom: 'var(--spacing-xs)' }}>Listen address</div>
          <input
            type="text"
            value={listen}
            onChange={e => setListen(e.target.value)}
            placeholder=":8443  (leave empty to disable)"
            style={{ width: '100%', padding: '8px 12px', fontFamily: 'var(--font-mono)', fontSize: '0.875rem', background: 'var(--color-bg-tertiary)', border: '1px solid var(--color-border-default)', borderRadius: 'var(--radius-sm)', color: 'var(--color-text-primary)' }}
          />
          <div style={{ marginTop: 'var(--spacing-xs)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
            Bind address for the proxy listener. Empty disables it. Bind to <code>127.0.0.1:port</code> unless the listener is reachable only from clients you control — there is no auth on the CONNECT port. Clients connect to the proxy over plain HTTP (use <code>http://</code>, even for the <code>HTTPS_PROXY</code> env var); the proxy terminates TLS for allowlisted hosts inside the CONNECT tunnel.
          </div>
        </label>

        <div style={{ marginBottom: 'var(--spacing-md)', fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
          Intercept hosts are declared per-model in the model YAML's
          {' '}<code style={{ fontFamily: 'var(--font-mono)' }}>mitm.hosts:</code>{' '}
          block. Each host is owned by exactly one model config; PII filtering and
          pattern overrides flow from the owning config when the host is intercepted.
        </div>

        <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
          <button
            className="btn btn-primary btn-sm"
            onClick={save}
            disabled={!dirty || saving}
          >
            <i className={`fas ${saving ? 'fa-spinner fa-spin' : 'fa-save'}`} /> {saving ? 'Saving…' : 'Apply'}
          </button>
          {dirty && (
            <button
              className="btn btn-ghost btn-sm"
              onClick={() => setListen(mitm.configured_addr || '')}
              disabled={saving}
            >
              Discard changes
            </button>
          )}
        </div>
      </div>

      <div className="card" style={{ padding: 'var(--spacing-md)', background: 'var(--color-bg-secondary)' }}>
        <h2 style={{ fontSize: '0.875rem', fontWeight: 600, marginTop: 0, marginBottom: 'var(--spacing-sm)' }}>Client setup</h2>
        <ol style={{ margin: 0, paddingLeft: 20, fontSize: '0.8125rem', color: 'var(--color-text-secondary)', lineHeight: 1.7 }}>
          <li>Download the CA cert (button above).</li>
          <li>Trust it on the client. For Node-based CLIs (Claude Code, Codex): <code style={{ fontFamily: 'var(--font-mono)' }}>export NODE_EXTRA_CA_CERTS=$(pwd)/localai-mitm-ca.crt</code></li>
          <li>Point the client at the proxy: <code style={{ fontFamily: 'var(--font-mono)' }}>export HTTPS_PROXY=http://&lt;host&gt;:&lt;port&gt;</code> (yes, <code>http://</code> — clients speak plain HTTP to the proxy, which then terminates TLS for allowlisted hosts on the inner connection).</li>
        </ol>
      </div>
    </div>
  )
}

const EVENT_KINDS = [
  { id: '', label: 'All' },
  { id: 'pii', label: 'PII' },
  { id: 'proxy_connect', label: 'Proxy connect' },
  { id: 'proxy_traffic', label: 'Proxy traffic' },
  { id: 'admission', label: 'Admission' },
]

function eventKind(e) {
  return e.kind || 'pii'
}

function eventSubject(e) {
  switch (eventKind(e)) {
    case 'proxy_connect':
    case 'proxy_traffic':
    case 'admission':
      return e.host || '—'
    default:
      return e.pattern_id || '—'
  }
}

function eventDetails(e) {
  switch (eventKind(e)) {
    case 'proxy_connect':
      return e.intercepted ? 'intercepted (TLS terminated)' : 'tunneled (passthrough)'
    case 'proxy_traffic': {
      const status = e.status_code ? `HTTP ${e.status_code}` : 'no upstream'
      const sent = formatBytes(e.bytes_sent)
      const recv = formatBytes(e.bytes_received)
      const dur = e.duration_ms != null ? `${e.duration_ms}ms` : ''
      return `${status} · ↑${sent} ↓${recv} · ${dur}`
    }
    case 'admission': {
      const retry = e.duration_ms != null ? `retry-after ${Math.round(e.duration_ms / 1000)}s` : ''
      return `HTTP 503 rejected · ${retry}`
    }
    default: {
      const len = e.length != null ? `len ${e.length}` : ''
      const hash = e.hash_prefix ? `hash ${e.hash_prefix}` : ''
      return [len, hash].filter(Boolean).join(' · ') || '—'
    }
  }
}

function formatBytes(n) {
  if (!n) return '0B'
  if (n < 1024) return `${n}B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)}KB`
  return `${(n / (1024 * 1024)).toFixed(1)}MB`
}

function kindBadge(kind) {
  const colors = {
    pii: 'var(--color-warning)',
    proxy_connect: 'var(--color-primary)',
    proxy_traffic: 'var(--color-text-muted)',
    admission: 'var(--color-error)',
  }
  return (
    <span style={{
      display: 'inline-block',
      padding: '2px 8px',
      fontSize: '0.6875rem',
      fontWeight: 600,
      borderRadius: 'var(--radius-sm)',
      background: colors[kind] || 'var(--color-bg-tertiary)',
      color: 'white',
      fontFamily: 'var(--font-mono)',
      textTransform: 'uppercase',
      whiteSpace: 'nowrap',
    }}>
      {kind.replace(/_/g, ' ')}
    </span>
  )
}

function EventsTab({ events }) {
  const [kindFilter, setKindFilter] = useState('')
  const filtered = kindFilter ? events.filter(e => eventKind(e) === kindFilter) : events

  return (
    <div className="card" style={{ padding: 'var(--spacing-md)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
          <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>Recent events</span>
          <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
            shared by PII filter and MITM proxy · newest first · capped at 100
          </span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
          {EVENT_KINDS.map(k => (
            <button
              key={k.id || 'all'}
              className={`btn btn-sm ${kindFilter === k.id ? 'btn-primary' : 'btn-secondary'}`}
              onClick={() => setKindFilter(k.id)}
            >
              {k.label}
            </button>
          ))}
        </div>
      </div>
      {filtered.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-list-ul" /></div>
          <h2 className="empty-state-title">No events</h2>
          <p className="empty-state-text">
            Events appear here when the PII filter matches a pattern, when the MITM proxy decides whether
            to intercept a hostname, or when an intercepted request finishes. Request bodies are never
            stored — use the API and backend traces for that.
          </p>
        </div>
      ) : (
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 170 }}>Time</th>
                <th style={{ width: 130 }}>Kind</th>
                <th style={{ width: 200 }}>Subject</th>
                <th>Details</th>
                <th style={{ width: 110 }}>Action</th>
                <th>Correlation</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map(e => (
                <tr key={e.id}>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
                    {e.created_at}
                  </td>
                  <td>{kindBadge(eventKind(e))}</td>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem', fontWeight: 600 }}>
                    {eventSubject(e)}
                  </td>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
                    {eventDetails(e)}
                  </td>
                  <td>{e.action ? actionBadge(e.action) : '—'}</td>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
                    {e.correlation_id || '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// RouterCacheCell renders the L2 embedding-cache state for one router
// model. Shows nothing for routers without an embedding_cache: block;
// for configured caches, shows hit/miss/near-miss counters plus a
// similarity histogram with a marker at the configured threshold so
// admins can tell at a glance whether the threshold is well-placed.
function RouterCacheCell({ cache }) {
  if (!cache) {
    return <span style={{ color: 'var(--color-text-muted)' }}>—</span>
  }
  const stats = cache.stats || {}
  const hits = stats.hits || 0
  const misses = stats.misses || 0
  const nearMisses = stats.near_misses || 0
  const lowConf = stats.low_confidence || 0
  const totalLookups = hits + misses + nearMisses
  const hitRate = totalLookups > 0 ? Math.round((hits / totalLookups) * 100) : null
  const errors = (stats.embedder_errors || 0) + (stats.store_errors || 0)
  const buckets = stats.similarity_buckets || []
  const bucketMax = buckets.length ? Math.max(...buckets, 1) : 1
  const threshold = cache.similarity_threshold || 0.80
  const thresholdBucket = Math.max(0, Math.min(9, Math.floor(threshold * 10)))
  return (
    <div style={{ fontFamily: 'var(--font-mono)', fontSize: '0.6875rem', lineHeight: 1.3 }}>
      <div style={{ fontWeight: 600 }}>{cache.embedding_model}</div>
      <div style={{ color: 'var(--color-text-muted)' }}>
        {totalLookups === 0 ? (
          <span>no traffic yet</span>
        ) : (
          <>
            <span style={{ color: hitRate >= 50 ? 'var(--color-success, #2da44e)' : 'var(--color-text-muted)' }}>
              {hitRate}% hit
            </span>
            <span> · {hits}h/{nearMisses}n/{misses}m</span>
            {lowConf > 0 && <span> · {lowConf} skipped</span>}
            {errors > 0 && <span style={{ color: 'var(--color-warning, #d97706)' }}> · {errors} err</span>}
          </>
        )}
      </div>
      {buckets.length === 10 && buckets.some(v => v > 0) && (
        <div title={`Cosine similarity histogram, threshold=${threshold}`}
             style={{ display: 'flex', alignItems: 'flex-end', gap: 1, marginTop: 4, height: 18 }}>
          {buckets.map((count, i) => {
            const h = bucketMax > 0 ? Math.max(2, Math.round((count / bucketMax) * 18)) : 2
            const inHitZone = i >= thresholdBucket
            return (
              <div
                key={i}
                title={`[${(i/10).toFixed(1)}, ${((i+1)/10).toFixed(1)}): ${count}`}
                style={{
                  width: 6,
                  height: h,
                  background: count === 0
                    ? 'var(--color-border, #e5e7eb)'
                    : inHitZone
                      ? 'var(--color-success, #2da44e)'
                      : 'var(--color-warning, #d97706)',
                  opacity: count === 0 ? 0.3 : 1,
                }}
              />
            )
          })}
          <div style={{ marginLeft: 4, fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>
            sim ≥ {threshold}
          </div>
        </div>
      )}
    </div>
  )
}
