import { useState, useEffect, useCallback, useRef, useMemo, Fragment } from 'react'
import { useOutletContext, Link, useNavigate, useLocation, useSearchParams } from 'react-router-dom'
import { apiUrl } from '../utils/basePath'
import { fromState } from '../utils/editorNav'
import { settingsApi, modelsApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import Toggle from '../components/Toggle'
import PageHeader from '../components/PageHeader'

// Middleware admin page. Three tabs:
//   - Filtering: per-model resolved PII state + per-model detector list
//     (detection policy lives on each detector model's pii_detection block).
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

function actionBadge(action) {
  const colors = {
    mask: 'var(--color-primary)',
    block: 'var(--color-error)',
    allow: 'var(--color-warning)',
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
  // The active tab lives in the URL (?tab=) so deep links and the model-editor
  // Back button (which captures location.search) return to the same tab; a
  // localStorage fallback restores it on a bare visit. Mirrors the Manage page.
  const [searchParams, setSearchParams] = useSearchParams()
  const initialTab = searchParams.get('tab') || localStorage.getItem('middleware-tab') || 'filtering'
  const [activeTab, setActiveTab] = useState(TABS.some(t => t.id === initialTab) ? initialTab : 'filtering')
  const selectTab = (id) => {
    setActiveTab(id)
    localStorage.setItem('middleware-tab', id)
    setSearchParams({ tab: id })
  }

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

  return (
    <div className="page page--wide">
      <PageHeader
        title="Middleware"
        supporting="Inspect and configure routing-module middleware: PII filtering and intelligent routing."
      />

      {/* Tab bar */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-md)', flexWrap: 'wrap' }}>
        {TABS.map(tab => (
          <button
            key={tab.id}
            className={`btn btn-sm ${activeTab === tab.id ? 'btn-primary' : 'btn-secondary'}`}
            onClick={() => selectTab(tab.id)}
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
        <FilteringTab status={status} addToast={addToast} onChanged={fetchAll} />
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

function FilteringTab({ status, addToast, onChanged }) {
  const location = useLocation()
  // Rows mid-save, so just that model's toggle disables while the PATCH
  // round-trips (and the 5s background poll re-syncs the resolved state).
  const [piiBusy, setPiiBusy] = useState(() => new Set())

  // Toggling the PII column writes an explicit pii.enabled to the model YAML
  // via PATCH /api/models/config-json/:name (a deep-merge that preserves
  // pii.detectors and every other field). This makes the resolved state
  // explicit: a cloud-proxy model shown ON by backend default becomes
  // pii.enabled:true; toggling it OFF writes pii.enabled:false.
  const togglePII = async (name, on) => {
    setPiiBusy(prev => new Set(prev).add(name))
    try {
      await modelsApi.patchConfig(name, { pii: { enabled: on } })
      addToast?.(on ? `PII filtering enabled for ${name}` : `PII filtering disabled for ${name}`, 'success')
      onChanged?.()
    } catch (err) {
      addToast?.(`Failed to update ${name}: ${err.message}`, 'error')
    } finally {
      setPiiBusy(prev => { const n = new Set(prev); n.delete(name); return n })
    }
  }

  if (!status?.pii) return null
  const pii = status.pii

  return (
    <>
      {/* Default rule banner */}
      <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-sm)' }}>
          <i className="fas fa-info-circle" style={{ color: 'var(--color-text-muted)', marginTop: 2 }} />
          <div>
            <div style={{ fontWeight: 600, marginBottom: 4 }}>NER-based PII redaction</div>
            <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
              Redaction is per-model and runs request-side. It is OFF by default; backends matching <code>{(pii.default_enabled_for_backends || []).join(', ')}</code> default to ON (cloud passthroughs). A model opts in with <code>pii: {'{'} enabled: true, detectors: [&hellip;] {'}'}</code>; each detector is a <code>token_classify</code> model whose <code>pii_detection</code> block defines the policy (which entities, what action, min score). Edit a detector model to change its policy.
            </div>
          </div>
        </div>
      </div>

      {/* Detector models + instance-wide default policy (per-row toggle) */}
      <DetectorModels pii={pii} addToast={addToast} onChanged={onChanged} />

      {/* Per-model resolved state */}
      <div className="card" style={{ padding: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
          <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>Per-model state</span>
          <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
            Toggle PII inline; edit a row for detectors and policy.
          </span>
        </div>
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th>Model</th>
                <th style={{ width: 120 }}>Backend</th>
                <th style={{ width: 120 }}>PII</th>
                <th style={{ width: 110 }}>Source</th>
                <th>Detectors</th>
                <th style={{ width: 80 }}>Edit</th>
              </tr>
            </thead>
            <tbody>
              {(pii.models || []).map(m => (
                <tr key={m.name}>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>{m.name}</td>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>{m.backend || '—'}</td>
                  <td>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                      <Toggle
                        checked={!!m.enabled}
                        disabled={piiBusy.has(m.name)}
                        onChange={(v) => togglePII(m.name, v)}
                      />
                      {m.enabled && (!m.detectors || m.detectors.length === 0) && (
                        <span
                          title="Enabled but no detector resolved — nothing is scanned. Toggle a detector's Default on above, or add pii.detectors to the model."
                          style={{ fontSize: '0.6875rem', fontWeight: 600, color: 'var(--color-warning)', whiteSpace: 'nowrap', cursor: 'help' }}
                        >
                          <i className="fas fa-triangle-exclamation" style={{ marginRight: 3 }} />no-op
                        </span>
                      )}
                    </span>
                  </td>
                  <td style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
                    {m.explicit ? 'YAML' : (m.default_for_backend ? 'backend default' : 'default off')}
                  </td>
                  <td style={{ fontSize: '0.75rem', fontFamily: 'var(--font-mono)' }}>
                    {m.detectors && m.detectors.length > 0
                      ? <>{m.detectors.join(', ')}{m.detectors_from_default && <span style={{ color: 'var(--color-text-muted)', fontFamily: 'var(--font-sans)' }}> (default)</span>}</>
                      : <span style={{ color: 'var(--color-text-muted)' }}>—</span>}
                  </td>
                  <td>
                    <Link
                      to={`/app/model-editor/${encodeURIComponent(m.name)}`}
                      state={fromState(location, 'Middleware')}
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

// detectorTypeBadge labels a detector model by how it matches: a neural NER
// token-classifier vs an in-process restricted-regex pattern matcher. `unknown`
// is a default that names a model no longer loaded.
function detectorTypeBadge(type) {
  const map = {
    ner: { label: 'NER', color: 'var(--color-primary)' },
    pattern: { label: 'pattern', color: 'var(--color-data-2, var(--color-warning))' },
    unknown: { label: 'not loaded', color: 'var(--color-text-muted)' },
  }
  const t = map[type] || map.unknown
  return (
    <span style={{
      display: 'inline-block',
      padding: '2px 8px',
      fontSize: '0.6875rem',
      fontWeight: 600,
      borderRadius: 'var(--radius-sm)',
      background: t.color,
      color: 'white',
      fontFamily: 'var(--font-mono)',
      textTransform: 'uppercase',
    }}>
      {t.label}
    </span>
  )
}

// DetectorModels lists the token_classify "filter" models (NER + in-process
// pattern matchers) and, via a per-row toggle, manages the instance-wide
// default detector set (RuntimeSettings.pii_default_detectors, saved via POST
// /api/settings). A detector toggled on is applied to any PII-enabled model
// that names none of its own — chiefly cloud-proxy / MITM models, which are
// PII-enabled by default but carry no detector list. Per-model `pii.detectors`
// always overrides. This replaces the old model-multiselect chooser: the table
// shows every available detector, so admins toggle defaults instead of retyping
// names, and link straight to each detector's config to edit its policy.
function DetectorModels({ pii, addToast, onChanged }) {
  const navigate = useNavigate()
  const location = useLocation()
  const rows = useMemo(() => pii.detector_models || [], [pii.detector_models])
  // Names currently in the default set; the toggle adds/removes against this.
  const defaults = useMemo(() => pii.default_detectors || [], [pii.default_detectors])
  // Track which rows are mid-save to disable just that toggle (optimistic).
  const [busy, setBusy] = useState(() => new Set())

  const toggleDefault = async (name, on) => {
    const next = on
      ? [...new Set([...defaults, name])]
      : defaults.filter(d => d !== name)
    setBusy(prev => new Set(prev).add(name))
    try {
      const body = await settingsApi.save({ pii_default_detectors: next })
      if (body && body.success === false) throw new Error(body.error || 'unknown error')
      addToast?.(on ? `${name} added to default detectors` : `${name} removed from default detectors`, 'success')
      onChanged?.()
    } catch (err) {
      addToast?.(`Failed to save: ${err.message}`, 'error')
    } finally {
      setBusy(prev => { const n = new Set(prev); n.delete(name); return n })
    }
  }

  return (
    <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
        <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>Detector models</span>
        <button
          className="btn btn-secondary btn-sm"
          onClick={() => navigate('/app/model-editor?template=secret-filter', { state: fromState(location, 'Middleware') })}
          title="Add a NER or pattern detector model"
        >
          <i className="fas fa-plus" /> Add detector model
        </button>
      </div>
      <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-sm)' }}>
        These token_classify models do the scanning. Toggle <strong>Default</strong> on to apply a
        detector to any PII-enabled model that names none of its own (chiefly cloud-proxy / MITM models).
        Per-model <code>pii.detectors</code> always overrides. Edit a detector to change which entities it
        flags and what action it takes.
      </div>

      <div className="table-container">
        <table className="table">
          <thead>
            <tr>
              <th>Detector model</th>
              <th style={{ width: 110 }}>Type</th>
              <th style={{ width: 120 }}>Backend</th>
              <th style={{ width: 110 }}>Default</th>
              <th style={{ width: 80 }}>Edit</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(d => (
              <tr key={d.name}>
                <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem', fontWeight: 600 }}>
                  {d.missing
                    ? <span title="This default detector names a model that is not loaded.">{d.name}</span>
                    : <Link to={`/app/model-editor/${encodeURIComponent(d.name)}`} state={fromState(location, 'Middleware')} title={`Edit ${d.name}.yaml`}>{d.name}</Link>}
                </td>
                <td>{detectorTypeBadge(d.type)}</td>
                <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>{d.backend || '—'}</td>
                <td>
                  <Toggle
                    checked={!!d.default}
                    disabled={busy.has(d.name)}
                    onChange={(v) => toggleDefault(d.name, v)}
                  />
                </td>
                <td>
                  {d.missing ? (
                    <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>—</span>
                  ) : (
                    <Link
                      to={`/app/model-editor/${encodeURIComponent(d.name)}`}
                      state={fromState(location, 'Middleware')}
                      className="btn btn-secondary btn-sm"
                      style={{ fontSize: '0.6875rem', padding: '2px 8px' }}
                      title={`Edit ${d.name}.yaml`}
                    >
                      <i className="fas fa-pen-to-square" /> Edit
                    </Link>
                  )}
                </td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={5} style={{ textAlign: 'center', color: 'var(--color-text-muted)', padding: 'var(--spacing-md)' }}>
                  No detector models loaded. Add one with the button above (a token_classify NER model
                  or a built-in secret pattern model).
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
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
          : d.nearest_similarity
            ? `Out-of-corpus fallback — the nearest labelled corpus entry was at similarity ${d.nearest_similarity.toFixed(2)}, below the router's gate. Seed exemplars near this kind of prompt to route it.`
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
  const location = useLocation()
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
          onClick={() => navigate('/app/model-editor?template=router', { state: fromState(location, 'Middleware') })}
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
              onClick={() => navigate('/app/model-editor?template=router', { state: fromState(location, 'Middleware') })}
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
                <th style={{ width: 200 }}>Cache / corpus</th>
                <th style={{ width: 140 }}>Fallback</th>
              </tr>
            </thead>
            <tbody>
              {router.models.map(m => (
                <tr key={m.name}>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem', fontWeight: 600 }}>
                    <Link to={`/app/model-editor/${encodeURIComponent(m.name)}`} state={fromState(location, 'Middleware')} title="Edit this router model's config">{m.name}</Link>
                  </td>
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
                    {m.knn ? <RouterKNNCell knn={m.knn} /> : <RouterCacheCell cache={m.embedding_cache} />}
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
  const location = useLocation()
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
                  <Link key={name} to={`/app/model-editor/${encodeURIComponent(name)}`} state={fromState(location, 'Middleware')} style={{ marginRight: 6, fontFamily: 'var(--font-mono)' }}>
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
                  {host} → <Link to={`/app/model-editor/${encodeURIComponent(name)}`} state={fromState(location, 'Middleware')}>{name}</Link>
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
            onClick={() => navigate('/app/model-editor?template=mitm', { state: fromState(location, 'Middleware') })}
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
                      state={fromState(location, 'Middleware')}
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
            Events appear here when a PII detector flags an entity, when the MITM proxy decides whether
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
// RouterKNNCell summarises a knn router's corpus for the Active
// routers table: embedding model, corpus size, per-label exemplar
// counts, and the epistemic-gate threshold. Counts only — corpus
// texts never reach the UI (the status endpoint doesn't send them,
// by design; seeding/curation is API-only).
function RouterKNNCell({ knn }) {
  if (!knn) {
    return <span style={{ color: 'var(--color-text-muted)' }}>—</span>
  }
  const corpus = knn.corpus || {}
  const total = corpus.total || 0
  const counts = corpus.label_counts || {}
  const k = knn.k || 3
  const sim = knn.similarity_threshold || 0.80
  return (
    <div style={{ fontFamily: 'var(--font-mono)', fontSize: '0.6875rem', lineHeight: 1.3 }}>
      <div style={{ fontWeight: 600 }}>{knn.embedding_model}</div>
      <div style={{ color: 'var(--color-text-muted)' }}>
        {total === 0 ? (
          <span title="Seed labelled example prompts via POST /api/router/{name}/corpus — every request falls back until the corpus has entries near it">
            empty corpus — seed via API
          </span>
        ) : (
          <span title={`${total} labelled exemplars; ${k} nearest vote; entries below similarity ${sim} cannot vote (out-of-corpus prompts use the fallback)`}>
            {total} exemplars · k={k} · sim ≥ {sim}
          </span>
        )}
      </div>
      {total > 0 && (
        <div style={{ color: 'var(--color-text-muted)', display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 2 }}>
          {Object.entries(counts).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([label, n]) => (
            <span key={label}>
              <span style={{ color: 'var(--color-primary)' }}>{label}</span>: {n}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}

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
