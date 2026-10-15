/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback, useRef, useMemo, Fragment } from 'react'
import { useOutletContext, Link, useNavigate, useLocation, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../utils/basePath'
import { fromState } from '../utils/editorNav'
import { settingsApi, modelsApi } from '../utils/api'
import { cssVars } from '../utils/modelLedger'
import LoadingSpinner from '../components/LoadingSpinner'
import Icon from '../components/Icon'
import './traffic.css'

// Middleware: what a request passes through, in the order the server runs it.
//   Proxy      an optional TLS proxy for clients that cannot be pointed at the API
//   Admission  rate limits and quotas, which refuse a request before a model sees it
//   Filtering  PII detection, per model, on the request
//   Routing    a classifier that picks the model
//   Model      the backend runs it
// The order is fixed by the server. Selecting a step shows only that step's
// rules; the events below are shared by the proxy, the filters and admission.
// The page never shows redacted content: the redactor does not store it, only
// a pattern id, an offset, a length and a short hash to dedupe a recurring leak.
//
// Wiring is admin-only: RequireAdmin in router.jsx redirects other viewers. In
// single-user no-auth mode the local user is an admin, so it works without
// --auth.

const STEPS = ['proxy', 'admission', 'filtering', 'routing']

function Switch({ checked, onChange, disabled, label }) {
  return (
    <button
      type="button"
      className="dk-switch"
      role="switch"
      aria-checked={!!checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    />
  )
}

function StateWord({ on }) {
  const { t } = useTranslation('traffic')
  return <span className="tf-onoff" data-on={on ? '' : undefined}>{on ? t('middleware.on') : t('middleware.off')}</span>
}

function eventKind(e) {
  return e.kind || 'pii'
}

export default function Middleware() {
  const { addToast } = useOutletContext() || {}
  const { t } = useTranslation('traffic')
  const [status, setStatus] = useState(null)
  const [events, setEvents] = useState([])
  const [decisions, setDecisions] = useState([])
  const [loading, setLoading] = useState(true)
  // The step lives in the URL (?tab=) so deep links and the model editor's Back
  // button, which captures location.search, return to the same step; a stored
  // value restores it on a bare visit. "events" is the old name of the events
  // tab, which now sits under every step.
  const [searchParams, setSearchParams] = useSearchParams()
  const stored = (() => { try { return localStorage.getItem('middleware-tab') } catch { return null } })()
  const wanted = searchParams.get('tab') || stored || 'filtering'
  const step = STEPS.includes(wanted) ? wanted : 'filtering'
  const selectStep = (id) => {
    try { localStorage.setItem('middleware-tab', id) } catch { /* ignore */ }
    setSearchParams({ tab: id })
  }

  // silent=true on background polls: no spinner, and no toast spam if the
  // server is briefly unreachable.
  const fetchAll = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const [statusRes, eventsRes, decisionsRes] = await Promise.all([
        fetch(apiUrl('/api/middleware/status')),
        fetch(apiUrl('/api/pii/events?limit=100')),
        fetch(apiUrl('/api/router/decisions?limit=100')),
      ])
      if (!statusRes.ok) throw new Error(`status: HTTP ${statusRes.status}`)
      setStatus(await statusRes.json())
      if (eventsRes.ok) setEvents((await eventsRes.json()).events || [])
      if (decisionsRes.ok) setDecisions((await decisionsRes.json()).decisions || [])
    } catch (err) {
      if (!silent) addToast?.(t('middleware.loadFailed', { message: err.message }), 'error')
    } finally {
      if (!silent) setLoading(false)
    }
  }, [addToast, t])

  useEffect(() => { fetchAll() }, [fetchAll])

  // Every 5 s so an admin watching sees new rows without a refresh. The proxy
  // form guards against clobbering a half-typed address with its own `dirty`
  // check, so the poll is safe while it is in use.
  const refreshRef = useRef(null)
  useEffect(() => {
    refreshRef.current = setInterval(() => fetchAll(true), 5000)
    return () => clearInterval(refreshRef.current)
  }, [fetchAll])

  const summaries = stepSummaries(status, events, t)

  return (
    <div className="page page--wide tf-page" data-testid="middleware-page">
      <header className="tf-head">
        <div className="tf-head__lead">
          <h1 className="tf-title">{t('middleware.title')}</h1>
          <p className="tf-lede">{t('middleware.lede')}</p>
        </div>
        <div className="tf-head__acts">
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--icon" aria-label={t('refresh')} onClick={() => fetchAll()} disabled={loading}>
            <Icon name="refresh" spin={Boolean(loading)} />
          </button>
        </div>
      </header>

      <div className="tf-pipeline" role="group" aria-label={t('middleware.pipeline')} data-testid="pipeline">
        {STEPS.map((id, i) => (
          <Fragment key={id}>
            {i > 0 && <Icon name="arrow-right" className="tf-pipeline__arrow" />}
            <button type="button" className="tf-step" aria-pressed={step === id} data-step={id} onClick={() => selectStep(id)}>
              <span className="tf-step__name"><span className="tf-step__n">{i + 1}</span> {t(`middleware.steps.${id}`)}</span>
              <span className="tf-step__sum">{summaries[id]}</span>
            </button>
          </Fragment>
        ))}
        <Icon name="arrow-right" className="tf-pipeline__arrow" />
        <div className="tf-step tf-step--end" data-step="model">
          <span className="tf-step__name"><span className="tf-step__n">5</span> {t('middleware.steps.model')}</span>
          <span className="tf-step__sum">{t('middleware.sum.model')}</span>
        </div>
      </div>
      <p className="tf-note-line">{t('middleware.orderNote')}</p>

      {loading && !status ? (
        <div className="tf-loading"><LoadingSpinner size="lg" /></div>
      ) : (
        <>
          {step === 'filtering' && <FilteringStep status={status} addToast={addToast} onChanged={fetchAll} />}
          {step === 'routing' && <RoutingStep status={status} decisions={decisions} />}
          {step === 'proxy' && <ProxyStep status={status} addToast={addToast} onChanged={fetchAll} />}
          {step === 'admission' && <AdmissionStep events={events} />}
          {step !== 'routing' && <EventsSection events={events} />}
        </>
      )}
    </div>
  )
}

// One line under each step's name: how it stands now, from the status endpoint.
function stepSummaries(status, events, t) {
  const mitm = status?.mitm
  const models = status?.pii?.models || []
  const routers = status?.router?.models || []
  const refused = events.filter(e => eventKind(e) === 'admission').length
  return {
    proxy: !status ? '' : mitm?.running ? t('middleware.sum.proxyOn', { addr: mitm.listen_addr }) : t('middleware.sum.proxyOff'),
    admission: refused > 0 ? t('middleware.sum.refused', { count: refused }) : t('middleware.sum.limits'),
    filtering: !status ? '' : t('middleware.sum.filtering', { on: models.filter(m => m.enabled).length, total: models.length }),
    routing: !status ? '' : routers.length ? t('middleware.sum.routers', { count: routers.length }) : t('middleware.sum.noRouters'),
  }
}

function FilteringStep({ status, addToast, onChanged }) {
  const { t } = useTranslation('traffic')
  const location = useLocation()
  // Rows mid-save, so only that model's switch is disabled while the PATCH
  // round-trips (and the 5 s poll re-syncs the resolved state).
  const [busy, setBusy] = useState(() => new Set())

  // The switch writes an explicit pii.enabled to the model YAML through
  // PATCH /api/models/config-json/:name, a deep merge that keeps pii.detectors
  // and every other field. That makes the resolved state explicit: a cloud-proxy
  // model shown ON by backend default becomes pii.enabled:true, and turning it
  // OFF writes pii.enabled:false.
  const toggle = async (name, on) => {
    setBusy(prev => new Set(prev).add(name))
    try {
      await modelsApi.patchConfig(name, { pii: { enabled: on } })
      addToast?.(on ? t('middleware.piiOn', { name }) : t('middleware.piiOff', { name }), 'success')
      onChanged?.()
    } catch (err) {
      addToast?.(t('middleware.updateFailed', { name, message: err.message }), 'error')
    } finally {
      setBusy(prev => { const n = new Set(prev); n.delete(name); return n })
    }
  }

  if (!status?.pii) return null
  const pii = status.pii
  const models = pii.models || []

  return (
    <div className="tf-stack" data-testid="step-filtering">
      <section className="tf-section">
        <h2 className="tf-h2">{t('middleware.rules')}</h2>
        <p className="tf-note-line">
          {t('middleware.piiIntro')} <code>{(pii.default_enabled_for_backends || []).join(', ')}</code>. {t('middleware.piiIntro2')}
        </p>
        <div className="dk-table-wrap">
          <table className="dk-table tf-table">
            <caption className="dk-sr-only">{t('middleware.perModel')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('table.model')}</th>
                <th scope="col" className="dk-hide-phone">{t('middleware.backend')}</th>
                <th scope="col">PII</th>
                <th scope="col" className="dk-hide-phone">{t('middleware.source')}</th>
                <th scope="col" className="dk-hide-phone">{t('middleware.detectors')}</th>
                <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('middleware.edit')}</span></th>
              </tr>
            </thead>
            <tbody>
              {models.map(m => (
                <tr key={m.name} data-row data-entity={m.name}>
                  <td>
                    <span className="dk-table-name dk-mono">{m.name}</span>
                    {m.enabled && (!m.detectors || m.detectors.length === 0) && (
                      <span className="tf-noop" title={t('middleware.noopTitle')}>
                        <Icon name="warning" /> {t('middleware.noop')}
                      </span>
                    )}
                  </td>
                  <td className="dk-hide-phone dk-mono tf-sub">{m.backend || '-'}</td>
                  <td>
                    <span className="tf-switchcell">
                      <Switch checked={!!m.enabled} disabled={busy.has(m.name)} onChange={v => toggle(m.name, v)} label={t('middleware.piiFor', { name: m.name })} />
                      <StateWord on={m.enabled} />
                    </span>
                  </td>
                  <td className="dk-hide-phone tf-sub">{m.explicit ? t('middleware.srcYaml') : (m.default_for_backend ? t('middleware.srcBackend') : t('middleware.srcOff'))}</td>
                  <td className="dk-hide-phone dk-mono tf-sub">
                    {m.detectors && m.detectors.length > 0
                      ? <>{m.detectors.join(', ')}{m.detectors_from_default && <span> ({t('middleware.default')})</span>}</>
                      : '-'}
                  </td>
                  <td className="dk-table-actions">
                    <Link
                      to={`/app/model-editor/${encodeURIComponent(m.name)}`}
                      state={fromState(location, 'Middleware')}
                      className="dk-btn dk-btn--ghost dk-btn--sm"
                      title={t('middleware.editFile', { name: m.name })}
                    >
                      <Icon name="edit" /> {t('middleware.edit')}
                    </Link>
                  </td>
                </tr>
              ))}
              {models.length === 0 && (
                <tr className="dk-table-empty"><td colSpan={6}><p className="tf-note-line">{t('middleware.noModels')}</p></td></tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <DetectorModels pii={pii} addToast={addToast} onChanged={onChanged} />
    </div>
  )
}

function DetectorModels({ pii, addToast, onChanged }) {
  const { t } = useTranslation('traffic')
  const navigate = useNavigate()
  const location = useLocation()
  const rows = useMemo(() => pii.detector_models || [], [pii.detector_models])
  // Names in the instance-wide default set (pii_default_detectors, saved with
  // POST /api/settings). A detector switched on applies to any PII-enabled model
  // that names none of its own, chiefly cloud-proxy and TLS-proxy models.
  // A model's own pii.detectors always overrides.
  const defaults = useMemo(() => pii.default_detectors || [], [pii.default_detectors])
  const [busy, setBusy] = useState(() => new Set())
  const [open, setOpen] = useState(true)

  const toggleDefault = async (name, on) => {
    const next = on ? [...new Set([...defaults, name])] : defaults.filter(d => d !== name)
    setBusy(prev => new Set(prev).add(name))
    try {
      const body = await settingsApi.save({ pii_default_detectors: next })
      if (body && body.success === false) throw new Error(body.error || 'unknown error')
      addToast?.(on ? t('middleware.defaultAdded', { name }) : t('middleware.defaultRemoved', { name }), 'success')
      onChanged?.()
    } catch (err) {
      addToast?.(t('middleware.saveFailed', { message: err.message }), 'error')
    } finally {
      setBusy(prev => { const n = new Set(prev); n.delete(name); return n })
    }
  }

  return (
    <section className="tf-section">
      <div className="tf-section__head">
        <h2 className="tf-h2">
          <button type="button" className="tf-disclose" aria-expanded={open} onClick={() => setOpen(v => !v)}>
            <Icon name={open ? 'chevron-down' : 'chevron-right'} /> {t('middleware.detectorsTitle')}
          </button>
        </h2>
        <button
          type="button"
          className="dk-btn dk-btn--secondary dk-btn--sm"
          onClick={() => navigate('/app/model-editor?template=secret-filter', { state: fromState(location, 'Middleware') })}
          title={t('middleware.addDetectorTitle')}
        >
          <Icon name="plus" /> {t('middleware.addDetector')}
        </button>
      </div>
      {open && (
        <>
          <p className="tf-note-line">{t('middleware.detectorsIntro')}</p>
          <div className="dk-table-wrap">
            <table className="dk-table tf-table">
              <caption className="dk-sr-only">{t('middleware.detectorsTitle')}</caption>
              <thead>
                <tr>
                  <th scope="col">{t('middleware.detectorCol')}</th>
                  <th scope="col">{t('middleware.type')}</th>
                  <th scope="col" className="dk-hide-phone">{t('middleware.backend')}</th>
                  <th scope="col">{t('middleware.default')}</th>
                  <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('middleware.edit')}</span></th>
                </tr>
              </thead>
              <tbody>
                {rows.map(d => (
                  <tr key={d.name} data-row data-entity={d.name}>
                    <td className="dk-mono">
                      {d.missing
                        ? <span className="dk-table-name" title={t('middleware.missingDetector')}>{d.name}</span>
                        : <Link className="dk-table-name tf-name" to={`/app/model-editor/${encodeURIComponent(d.name)}`} state={fromState(location, 'Middleware')} title={t('middleware.editFile', { name: d.name })}>{d.name}</Link>}
                    </td>
                    <td><span className="dk-chip dk-chip--sm" data-type={d.type}>{d.type === 'ner' ? 'NER' : d.type === 'pattern' ? 'pattern' : t('middleware.notLoaded')}</span></td>
                    <td className="dk-hide-phone dk-mono tf-sub">{d.backend || '-'}</td>
                    <td><Switch checked={!!d.default} disabled={busy.has(d.name)} onChange={v => toggleDefault(d.name, v)} label={t('middleware.defaultFor', { name: d.name })} /></td>
                    <td className="dk-table-actions">
                      {!d.missing && (
                        <Link to={`/app/model-editor/${encodeURIComponent(d.name)}`} state={fromState(location, 'Middleware')} className="dk-btn dk-btn--ghost dk-btn--sm" title={t('middleware.editFile', { name: d.name })}>
                          <Icon name="edit" /> {t('middleware.edit')}
                        </Link>
                      )}
                    </td>
                  </tr>
                ))}
                {rows.length === 0 && (
                  <tr className="dk-table-empty"><td colSpan={5}><p className="tf-note-line">{t('middleware.noDetectors')}</p></td></tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  )
}

// The labels that fired for one routing decision, from its comma-joined
// `label` column.
function decisionActiveSet(d) {
  return new Set((d?.label || '').split(',').filter(Boolean))
}

// The top active label's score, shown in the collapsed row so uncertain calls
// can be spotted without opening it. Empty for a cached or fallback decision,
// which has no per-label scores.
function scoreSuffix(d, active) {
  if (!d?.label_scores?.length) return ''
  const top = d.label_scores.filter(ls => active.has(ls.label)).sort((a, b) => b.score - a.score)[0]
  return top ? ` ${(top.score * 100).toFixed(0)}%` : ''
}

// A score bar with a marker at the activation threshold, so a label that
// stayed just under it can be seen.
function LabelBar({ label, score, threshold, active, t }) {
  const scorePct = Math.max(0, Math.min(100, score * 100))
  const thresholdPct = Math.max(0, Math.min(100, (threshold || 0) * 100))
  return (
    <div className="tf-score">
      <div className={`tf-score__label${active ? ' tf-score__label--active' : ''}`} title={label}>{label}</div>
      <div className="tf-score__track">
        <div className={`tf-score__fill${active ? ' tf-score__fill--active' : ''}`} style={cssVars({ width: `${scorePct}%` })} />
        {threshold > 0 && (
          <div title={t('middleware.threshold', { value: thresholdPct.toFixed(0) })} className="tf-score__mark" style={cssVars({ left: `${thresholdPct}%` })} />
        )}
      </div>
      <div className="tf-score__value dk-mono">{scorePct.toFixed(1)}%</div>
    </div>
  )
}

function DecisionDetail({ d }) {
  const { t } = useTranslation('traffic')
  if (!d.label_scores?.length) {
    return (
      <p className="tf-note-line">
        {d.cached
          ? t('middleware.cachedDecision')
          : d.nearest_similarity
            ? t('middleware.fallbackDecision', { value: d.nearest_similarity.toFixed(2) })
            : t('middleware.noScores')}
      </p>
    )
  }
  const threshold = d.activation_threshold || 0
  const active = decisionActiveSet(d)
  return (
    <div className="tf-scores">
      <p className="tf-note-line">{t('middleware.activation', { value: (threshold * 100).toFixed(0) })}</p>
      {d.label_scores.map(ls => (
        <LabelBar key={ls.label} label={ls.label} score={ls.score} threshold={threshold} active={active.has(ls.label)} t={t} />
      ))}
    </div>
  )
}

function RoutingStep({ status, decisions }) {
  const { t } = useTranslation('traffic')
  const navigate = useNavigate()
  const location = useLocation()
  const router = status?.router || { configured: false }
  const [expanded, setExpanded] = useState(() => new Set())

  const rows = useMemo(() => (decisions || []).map(d => ({ ...d, _suffix: scoreSuffix(d, decisionActiveSet(d)) })), [decisions])
  const toggle = useCallback(id => {
    setExpanded(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }, [])

  if (!router.configured || !router.models || router.models.length === 0) {
    return (
      <div className="dk-empty tf-empty" data-testid="step-routing">
        <Icon name="route" className="dk-empty-icon" />
        <h2 className="dk-empty-title">{t('middleware.noRoutersTitle')}</h2>
        <p className="dk-empty-text">{router.note || t('middleware.noRoutersText')}</p>
        <div className="tf-empty__acts">
          <button type="button" className="dk-btn dk-btn--primary" onClick={() => navigate('/app/model-editor?template=router', { state: fromState(location, 'Middleware') })}>
            <Icon name="plus" /> {t('middleware.createRouter')}
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="tf-stack" data-testid="step-routing">
      <section className="tf-section">
        <div className="tf-section__head">
          <h2 className="tf-h2">{t('middleware.activeRouters')}</h2>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => navigate('/app/model-editor?template=router', { state: fromState(location, 'Middleware') })} title={t('middleware.addRouterTitle')}>
            <Icon name="plus" /> {t('middleware.addRouter')}
          </button>
        </div>
        <p className="tf-note-line">{t('middleware.routersNote')}</p>
        <div className="dk-table-wrap">
          <table className="dk-table tf-table">
            <caption className="dk-sr-only">{t('middleware.activeRouters')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('table.model')}</th>
                <th scope="col" className="dk-hide-phone">{t('middleware.classifier')}</th>
                <th scope="col">{t('middleware.candidates')}</th>
                <th scope="col" className="dk-hide-phone">{t('middleware.cache')}</th>
                <th scope="col" className="dk-hide-phone">{t('middleware.fallback')}</th>
              </tr>
            </thead>
            <tbody>
              {router.models.map(m => (
                <tr key={m.name} data-row data-entity={m.name}>
                  <td className="dk-mono"><Link className="dk-table-name tf-name" to={`/app/model-editor/${encodeURIComponent(m.name)}`} state={fromState(location, 'Middleware')} title={t('middleware.editRouter')}>{m.name}</Link></td>
                  <td className="dk-hide-phone dk-mono tf-sub">{m.classifier}</td>
                  <td>
                    {(m.candidates || []).map((c, i) => (
                      <div key={i} className="tf-candidate dk-mono">
                        <span>{(c.labels || []).join(', ') || '-'}</span>
                        <Icon name="arrow-right" />
                        <span>{c.model}</span>
                      </div>
                    ))}
                  </td>
                  <td className="dk-hide-phone">{m.knn ? <RouterKNNCell knn={m.knn} /> : <RouterCacheCell cache={m.embedding_cache} />}</td>
                  <td className="dk-hide-phone dk-mono tf-sub">{m.fallback || '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="tf-section">
        <h2 className="tf-h2">{t('middleware.decisions')} <span className="tf-sub">{t('middleware.decisionsSub')}</span></h2>
        {rows.length === 0 ? (
          <p className="tf-note-line">{t('middleware.noDecisions')}</p>
        ) : (
          <div className="dk-table-wrap">
            <table className="dk-table dk-table--compact tf-table">
              <caption className="dk-sr-only">{t('middleware.decisions')}</caption>
              <thead>
                <tr>
                  <th scope="col" className="dk-table-toggle-cell"><span className="dk-sr-only">{t('table.open')}</span></th>
                  <th scope="col">{t('middleware.time')}</th>
                  <th scope="col" className="dk-hide-phone">{t('middleware.router')}</th>
                  <th scope="col">{t('middleware.label')}</th>
                  <th scope="col" className="dk-hide-phone">{t('middleware.served')}</th>
                  <th scope="col" className="dk-num dk-hide-phone">{t('traces.col.latency')}</th>
                  <th scope="col" className="dk-hide-phone">{t('middleware.correlation')}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map(d => {
                  const isOpen = expanded.has(d.id)
                  return (
                    <Fragment key={d.id}>
                      <tr data-row data-clickable onClick={() => toggle(d.id)} title={isOpen ? t('middleware.collapse') : t('middleware.expand')}>
                        <td className="dk-table-toggle-cell"><Icon name={isOpen ? 'chevron-down' : 'chevron-right'} className="dk-icon" /></td>
                        <td className="dk-mono tf-sub">{d.created_at}</td>
                        <td className="dk-hide-phone dk-mono">{d.router_model}</td>
                        <td className="dk-mono"><strong>{d.label}{d._suffix}</strong></td>
                        <td className="dk-hide-phone dk-mono">{d.served_model}</td>
                        <td className="dk-num dk-hide-phone">{d.latency_ms}ms</td>
                        <td className="dk-hide-phone dk-mono tf-sub">{d.correlation_id || '-'}</td>
                      </tr>
                      {isOpen && (
                        <tr className="dk-table-detail">
                          <td colSpan={7}><div className="dk-table-detail-body"><DecisionDetail d={d} /></div></td>
                        </tr>
                      )}
                    </Fragment>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}

function ProxyStep({ status, addToast, onChanged }) {
  const { t } = useTranslation('traffic')
  const navigate = useNavigate()
  const location = useLocation()
  const mitm = status?.mitm
  const serverListen = mitm?.configured_addr || ''
  const [listen, setListen] = useState(serverListen)
  const [saving, setSaving] = useState(false)
  const [setup, setSetup] = useState(false)
  const dirty = listen !== serverListen

  // Take the server's value only when there is no pending edit to clobber.
  useEffect(() => {
    if (dirty) return
    setListen(serverListen)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverListen])

  const save = async () => {
    setSaving(true)
    try {
      const body = await settingsApi.save({ mitm_listen: listen })
      if (body && body.success === false) throw new Error(body.error || 'unknown error')
      addToast?.(t('middleware.proxyUpdated'), 'success')
      onChanged?.()
    } catch (err) {
      addToast?.(t('middleware.saveFailed', { message: err.message }), 'error')
    } finally {
      setSaving(false)
    }
  }

  if (!mitm) {
    return (
      <div className="dk-empty tf-empty" data-testid="step-proxy">
        <Icon name="shield" className="dk-empty-icon" />
        <h2 className="dk-empty-title">{t('middleware.proxyUnavailable')}</h2>
        <p className="dk-empty-text">{t('middleware.proxyUnavailableText')}</p>
      </div>
    )
  }

  const conflicts = mitm.host_conflicts || {}
  const conflictHosts = Object.keys(conflicts)
  const ownerEntries = Object.entries(mitm.host_owners || {})
  const mitmModels = mitm.models || []

  return (
    <div className="tf-stack" data-testid="step-proxy">
      {conflictHosts.length > 0 && (
        <div className="tf-error" role="alert">
          <Icon name="alert-circle" />
          <div>
            <strong>{t('middleware.conflictTitle')}</strong>
            <p className="tf-note-line">{t('middleware.conflictText')}</p>
            <ul className="tf-list">
              {conflictHosts.map(h => (
                <li key={h}>
                  <code>{h}</code> {t('middleware.claimedBy')}{' '}
                  {(conflicts[h] || []).map(name => (
                    <Link key={name} className="dk-link dk-mono" to={`/app/model-editor/${encodeURIComponent(name)}`} state={fromState(location, 'Middleware')}>{name}</Link>
                  ))}
                </li>
              ))}
            </ul>
          </div>
        </div>
      )}

      <section className="tf-section">
        <h2 className="tf-h2">{t('middleware.state')} <StateWord on={mitm.running} />{mitm.running && <span className="dk-mono tf-sub">{t('middleware.listeningOn', { addr: mitm.listen_addr })}</span>}</h2>
        <p className="tf-note-line">{t('middleware.proxyIntro')}</p>
        {ownerEntries.length > 0 ? (
          <>
            <p className="tf-note-line">{t('middleware.hostsClaimed')}</p>
            <ul className="tf-list dk-mono">
              {ownerEntries.map(([host, name]) => (
                <li key={host}>{host} <Icon name="arrow-right" /> <Link className="dk-link" to={`/app/model-editor/${encodeURIComponent(name)}`} state={fromState(location, 'Middleware')}>{name}</Link></li>
              ))}
            </ul>
          </>
        ) : (
          <p className="tf-note-line">{t('middleware.noHosts')}</p>
        )}
        {mitm.ca_available ? (
          <a className="dk-btn dk-btn--secondary dk-btn--sm" href={apiUrl(mitm.ca_cert_url)} download="localai-mitm-ca.crt">
            <Icon name="download" /> {t('middleware.downloadCa')}
          </a>
        ) : (
          <p className="tf-note-line">{t('middleware.noCa')}</p>
        )}
      </section>

      <section className="tf-section">
        <div className="tf-section__head">
          <h2 className="tf-h2">{t('middleware.proxyModels')}</h2>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => navigate('/app/model-editor?template=mitm', { state: fromState(location, 'Middleware') })} title={t('middleware.addProxyModelTitle')}>
            <Icon name="plus" /> {t('middleware.addProxyModel')}
          </button>
        </div>
        {mitmModels.length === 0 ? (
          <p className="tf-note-line">{t('middleware.noProxyModels')}</p>
        ) : (
          <div className="dk-table-wrap">
            <table className="dk-table tf-table">
              <caption className="dk-sr-only">{t('middleware.proxyModels')}</caption>
              <thead>
                <tr>
                  <th scope="col">{t('table.model')}</th>
                  <th scope="col">{t('middleware.hosts')}</th>
                  <th scope="col">PII</th>
                  <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('middleware.edit')}</span></th>
                </tr>
              </thead>
              <tbody>
                {mitmModels.map(m => (
                  <tr key={m.name} data-row data-entity={m.name}>
                    <td className="dk-table-name dk-mono">{m.name}</td>
                    <td className="dk-mono tf-sub">{(m.hosts || []).join(', ')}</td>
                    <td><StateWord on={m.pii_enabled} /></td>
                    <td className="dk-table-actions">
                      <Link to={`/app/model-editor/${encodeURIComponent(m.name)}`} state={fromState(location, 'Middleware')} className="dk-btn dk-btn--ghost dk-btn--sm">
                        <Icon name="edit" /> {t('middleware.edit')}
                      </Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="tf-section">
        <h2 className="tf-h2">{t('middleware.config')}</h2>
        <label className="dk-field tf-field">
          <span className="dk-label">{t('middleware.listenAddress')}</span>
          <input type="text" className="dk-input dk-input--mono" value={listen} onChange={e => setListen(e.target.value)} placeholder={t('middleware.listenPlaceholder')} />
          <span className="dk-hint">{t('middleware.listenHint')}</span>
        </label>
        <p className="tf-note-line">{t('middleware.interceptNote')}</p>
        <div className="tf-actions">
          <button type="button" className="dk-btn dk-btn--primary" onClick={save} disabled={!dirty || saving}>
            <Icon name={saving ? 'spinner' : 'save'} spin={Boolean(saving)} /> {saving ? t('traces.settings.saving') : t('middleware.apply')}
          </button>
          {dirty && (
            <button type="button" className="dk-btn dk-btn--ghost" onClick={() => setListen(mitm.configured_addr || '')} disabled={saving}>{t('middleware.discard')}</button>
          )}
        </div>
      </section>

      <section className="tf-section">
        <h2 className="tf-h2">
          <button type="button" className="tf-disclose" aria-expanded={setup} onClick={() => setSetup(v => !v)}>
            <Icon name={setup ? 'chevron-down' : 'chevron-right'} /> {t('middleware.clientSetup')}
          </button>
        </h2>
        {setup && (
          <ol className="tf-list tf-list--num">
            <li>{t('middleware.setup1')}</li>
            <li>{t('middleware.setup2')} <code>export NODE_EXTRA_CA_CERTS=$(pwd)/localai-mitm-ca.crt</code></li>
            <li>{t('middleware.setup3')} <code>export HTTPS_PROXY=http://&lt;host&gt;:&lt;port&gt;</code> {t('middleware.setup3b')}</li>
          </ol>
        )}
      </section>
    </div>
  )
}

function AdmissionStep({ events }) {
  const { t } = useTranslation('traffic')
  const refused = events.filter(e => eventKind(e) === 'admission')
  return (
    <div className="tf-stack" data-testid="step-admission">
      <section className="tf-section">
        <h2 className="tf-h2">{t('middleware.admissionTitle')}</h2>
        <p className="tf-note-line">{t('middleware.admissionText')}</p>
        <div className="tf-actions">
          <Link className="dk-btn dk-btn--secondary dk-btn--sm" to="/app/users"><Icon name="users" /> {t('middleware.admissionUsers')}</Link>
        </div>
        <p className="tf-note-line" data-testid="admission-count">{refused.length > 0 ? t('middleware.admissionRecent', { count: refused.length }) : t('middleware.admissionNone')}</p>
      </section>
    </div>
  )
}

const EVENT_KINDS = ['', 'pii', 'proxy_connect', 'proxy_traffic', 'admission']

function eventSubject(e) {
  switch (eventKind(e)) {
    case 'proxy_connect':
    case 'proxy_traffic':
    case 'admission':
      return e.host || '-'
    default:
      return e.pattern_id || '-'
  }
}

function bytesShort(n) {
  if (!n) return '0B'
  if (n < 1024) return `${n}B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)}KB`
  return `${(n / (1024 * 1024)).toFixed(1)}MB`
}

function eventDetails(e, t) {
  switch (eventKind(e)) {
    case 'proxy_connect':
      return e.intercepted ? t('middleware.intercepted') : t('middleware.tunneled')
    case 'proxy_traffic': {
      const status = e.status_code ? `HTTP ${e.status_code}` : t('middleware.noUpstream')
      const dur = e.duration_ms != null ? `${e.duration_ms}ms` : ''
      return `${status} · ↑${bytesShort(e.bytes_sent)} ↓${bytesShort(e.bytes_received)} · ${dur}`
    }
    case 'admission': {
      const retry = e.duration_ms != null ? `retry-after ${Math.round(e.duration_ms / 1000)}s` : ''
      // Older audit rows were recorded as 503; newer ones as 429.
      return `HTTP ${e.status_code || 429} rejected · ${retry}`
    }
    default: {
      const len = e.length != null ? `len ${e.length}` : ''
      const hash = e.hash_prefix ? `hash ${e.hash_prefix}` : ''
      return [len, hash].filter(Boolean).join(' · ') || '-'
    }
  }
}

function EventsSection({ events }) {
  const { t } = useTranslation('traffic')
  const [kind, setKind] = useState('')
  const filtered = kind ? events.filter(e => eventKind(e) === kind) : events
  return (
    <section className="tf-section" data-testid="events-section">
      <div className="tf-section__head">
        <h2 className="tf-h2">{t('middleware.events')} <span className="tf-sub">{t('middleware.eventsSub')}</span></h2>
        <div className="tf-chips" role="group" aria-label={t('middleware.eventKinds')}>
          {EVENT_KINDS.map(k => (
            <button key={k || 'all'} type="button" className="dk-chip dk-chip--sm" aria-pressed={kind === k} onClick={() => setKind(k)}>
              {k === '' ? t('middleware.kindAll') : k === 'pii' ? 'PII' : t(`middleware.kind.${k}`)}
            </button>
          ))}
        </div>
      </div>
      {filtered.length === 0 ? (
        <div className="dk-empty tf-empty">
          <Icon name="list" className="dk-empty-icon" />
          <h3 className="dk-empty-title">{t('middleware.noEvents')}</h3>
          <p className="dk-empty-text">{t('middleware.noEventsText')}</p>
        </div>
      ) : (
        <div className="dk-table-wrap">
          <table className="dk-table dk-table--compact tf-table">
            <caption className="dk-sr-only">{t('middleware.events')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('middleware.time')}</th>
                <th scope="col">Kind</th>
                <th scope="col">{t('middleware.subject')}</th>
                <th scope="col" className="dk-hide-phone">{t('middleware.details')}</th>
                <th scope="col">{t('middleware.action')}</th>
                <th scope="col" className="dk-hide-phone">{t('middleware.correlation')}</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map(e => (
                <tr key={e.id} data-row>
                  <td className="dk-mono tf-sub">{e.created_at}</td>
                  <td><span className="dk-chip dk-chip--sm tf-kind">{eventKind(e).replace(/_/g, ' ')}</span></td>
                  <td className="dk-mono"><strong>{eventSubject(e)}</strong></td>
                  <td className="dk-hide-phone dk-mono tf-sub">{eventDetails(e, t)}</td>
                  <td>{e.action ? <span className="tf-action" data-action={e.action}>{e.action}</span> : '-'}</td>
                  <td className="dk-hide-phone dk-mono tf-sub">{e.correlation_id || '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

// The L2 embedding-cache state of one router: hit, near-miss and miss counts
// and a similarity histogram with the hit zone marked, so an admin can tell
// whether the threshold sits in the right place. Nothing for a router without
// an embedding_cache block.
function RouterKNNCell({ knn }) {
  const { t } = useTranslation('traffic')
  if (!knn) return <span className="tf-sub">-</span>
  const corpus = knn.corpus || {}
  const total = corpus.total || 0
  const counts = corpus.label_counts || {}
  const k = knn.k || 3
  const sim = knn.similarity_threshold || 0.80
  return (
    <div className="tf-knn dk-mono">
      <div><strong>{knn.embedding_model}</strong></div>
      <div className="tf-sub">
        {total === 0 ? (
          <span title={t('middleware.knnEmptyTitle')}>{t('middleware.knnEmpty')}</span>
        ) : (
          <span title={t('middleware.knnTitle', { total, k, sim })}>{total} exemplars · k={k} · sim ≥ {sim}</span>
        )}
      </div>
      {total > 0 && (
        <div className="tf-sub tf-knn__labels">
          {Object.entries(counts).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([label, n]) => (
            <span key={label}>{label}: {n}</span>
          ))}
        </div>
      )}
    </div>
  )
}

function RouterCacheCell({ cache }) {
  const { t } = useTranslation('traffic')
  if (!cache) return <span className="tf-sub">-</span>
  const stats = cache.stats || {}
  const hits = stats.hits || 0
  const misses = stats.misses || 0
  const nearMisses = stats.near_misses || 0
  const lowConf = stats.low_confidence || 0
  const lookups = hits + misses + nearMisses
  const hitRate = lookups > 0 ? Math.round((hits / lookups) * 100) : null
  const errors = (stats.embedder_errors || 0) + (stats.store_errors || 0)
  const buckets = stats.similarity_buckets || []
  const bucketMax = buckets.length ? Math.max(...buckets, 1) : 1
  const threshold = cache.similarity_threshold || 0.80
  const thresholdBucket = Math.max(0, Math.min(9, Math.floor(threshold * 10)))
  return (
    <div className="tf-knn dk-mono">
      <div><strong>{cache.embedding_model}</strong></div>
      <div className="tf-sub">
        {lookups === 0 ? (
          <span>{t('middleware.noTraffic')}</span>
        ) : (
          <>
            <span>{hitRate}% hit</span>
            <span> · {hits}h/{nearMisses}n/{misses}m</span>
            {lowConf > 0 && <span> · {lowConf} skipped</span>}
            {errors > 0 && <span> · {errors} err</span>}
          </>
        )}
      </div>
      {buckets.length === 10 && buckets.some(v => v > 0) && (
        <div title={t('middleware.histTitle', { value: threshold })} className="tf-hist">
          {buckets.map((count, i) => {
            const h = bucketMax > 0 ? Math.max(2, Math.round((count / bucketMax) * 18)) : 2
            return (
              <div
                key={i}
                title={`[${(i / 10).toFixed(1)}, ${((i + 1) / 10).toFixed(1)}): ${count}`}
                className={`tf-hist__bar${count === 0 ? '' : i >= thresholdBucket ? ' tf-hist__bar--hit' : ' tf-hist__bar--miss'}`}
                style={cssVars({ height: h })}
              />
            )
          })}
          <div className="tf-sub tf-hist__note">sim ≥ {threshold}</div>
        </div>
      )}
    </div>
  )
}
