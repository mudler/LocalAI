/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { tracesApi } from '../utils/api'
import {
  bytesText, decodeBody, durationText, failureWords, nsToMs, prettyBody, relatedOperations,
  requestModel, traceState, traceTime,
} from '../utils/traffic'
import { cssVars } from '../utils/modelLedger'
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import './traffic.css'

// A body stays closed until someone opens it: it can hold prompts and personal
// data. Opening it is a choice made on this page and is not remembered.
function Body({ id, title, body, truncated, bytes, t }) {
  const [shown, setShown] = useState(false)
  const text = useMemo(() => (shown ? prettyBody(body) : ''), [shown, body])
  const size = decodeBody(body).length
  return (
    <section className="tf-block" data-testid={`body-${id}`}>
      <div className="tf-block__head">
        <h3 className="tf-h3">{title} <span className="tf-sub dk-mono">{size > 0 ? bytesText(bytes || size) : t('trace.noBody')}</span></h3>
        {size > 0 && (
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-expanded={shown} onClick={() => setShown(v => !v)}>
            <Icon name={shown ? 'eye-off' : 'eye'} /> {shown ? t('trace.hide') : t('trace.reveal')}
          </button>
        )}
      </div>
      {size === 0
        ? <p className="tf-note-line">{t('trace.noBodyText')}</p>
        : shown
          ? <pre className="tf-code" tabIndex={0}>{text}</pre>
          : <p className="tf-note-line">{t('trace.hidden')}</p>}
      {shown && truncated && (
        <p className="tf-note-line">{t('trace.truncated', { shown: bytesText(size), total: bytesText(bytes) })}</p>
      )}
    </section>
  )
}

// One request, as a page. What it shows is what the trace buffer holds for it:
// the method, path, status, time and who, the error it recorded, and the two
// bodies behind a reveal. The timeline is the request as one bar, and under it
// the backend operations that ran while it was open, matched on time and model
// because the two buffers share no request id. Request headers are never shown:
// the server redacts the sensitive ones and the page does not list any.
export default function TracePage() {
  const { id } = useParams()
  const { t } = useTranslation('traffic')
  const [state, setState] = useState({ loading: true, trace: null, missing: false, error: '' })
  const [operations, setOperations] = useState([])

  useEffect(() => {
    let cancelled = false
    setState({ loading: true, trace: null, missing: false, error: '' })
    tracesApi.getOne(id)
      .then(trace => { if (!cancelled) setState({ loading: false, trace, missing: false, error: '' }) })
      .catch(err => {
        if (cancelled) return
        const missing = /404|not found/i.test(err?.message || '')
        setState({ loading: false, trace: null, missing, error: missing ? '' : String(err?.message || err) })
      })
    return () => { cancelled = true }
  }, [id])

  const { trace } = state
  useEffect(() => {
    if (!trace) return undefined
    let cancelled = false
    tracesApi.getBackend({ limit: 200 })
      .then(page => { if (!cancelled) setOperations(relatedOperations(trace, page.items)) })
      .catch(() => { if (!cancelled) setOperations([]) })
    return () => { cancelled = true }
  }, [trace])

  const back = (
    <Link className="tf-back" to="/app/traces"><Icon name="arrow-left" /> {t('trace.back')}</Link>
  )

  if (state.loading) {
    return <div className="page page--wide tf-page" data-testid="trace-page">{back}<div className="tf-loading"><LoadingSpinner size="lg" /></div></div>
  }
  if (!trace) {
    return (
      <div className="page page--wide tf-page" data-testid="trace-page">
        {back}
        <div className="dk-empty tf-empty" data-testid="trace-missing">
          <Icon name="waveform" className="dk-empty-icon" />
          <h2 className="dk-empty-title">{state.missing ? t('trace.missingTitle') : t('trace.errorTitle')}</h2>
          <p className="dk-empty-text">{state.missing ? t('trace.missingText') : state.error}</p>
          <div className="tf-empty__acts"><Link className="dk-btn dk-btn--primary" to="/app/traces">{t('trace.back')}</Link></div>
        </div>
      </div>
    )
  }

  const st = traceState(trace)
  const status = trace.response?.status
  const ms = nsToMs(trace.duration)
  const at = traceTime(trace.timestamp)
  const model = requestModel(trace)
  const why = failureWords(trace)
  const who = trace.user_name || trace.user_id
  const total = Math.max(ms || 0, ...operations.map(o => o.offsetMs + o.ms), 1)
  const pct = v => `${Math.max(0, Math.min(100, (v / total) * 100)).toFixed(2)}%`
  const details = [
    [t('trace.id'), trace.id],
    [t('trace.user'), who],
    [t('trace.clientIp'), trace.client_ip],
    [t('trace.userAgent'), trace.user_agent],
    [t('trace.model'), model],
  ].filter(([, v]) => v)

  return (
    <div className="page page--wide tf-page tf-page--narrow" data-testid="trace-page">
      {back}
      <header className="tf-head tf-head--col">
        <h1 className="tf-title tf-title--mono">{trace.request?.method} {trace.request?.path}</h1>
        <p className="tf-trace-line">
          <span className="tf-result" data-level={st === 'failed' ? 'error' : st === 'ok' ? 'ok' : st === 'running' ? 'info' : 'warn'} data-testid="trace-state">
            <Icon name={st === 'failed' ? 'alert-circle' : st === 'ok' ? 'check-circle' : st === 'running' ? 'spinner' : 'warning'} spin={st === 'running'} />
            {st === 'running' ? t('traces.state.running') : `${status ?? '-'} ${t(`traces.state.${st}`)}`}
          </span>
          {at != null && <span className="dk-mono">{new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}</span>}
          {ms != null && <span className="dk-mono">{durationText(ms)}</span>}
          {who && <span>{who}</span>}
        </p>
      </header>

      {why && (
        <section className="tf-why" data-testid="trace-why" data-level={st === 'failed' ? 'error' : 'warn'}>
          <h2 className="tf-h3">{t('trace.why')}</h2>
          {why.kind === 'running' && <p>{t('trace.whyRunning')}</p>}
          {why.kind === 'error' && <p>{t('trace.whyError', { status: why.status || '-' })}</p>}
          {why.kind === 'status' && <p>{t('trace.whyStatus', { status: why.status })}</p>}
          {why.kind === 'refused' && <p>{t('trace.whyRefused', { status: why.status })}</p>}
          {why.error && <pre className="tf-code tf-code--error">{why.error}</pre>}
          <p className="tf-note-line">{t('trace.whyNote')}</p>
        </section>
      )}

      <section className="tf-block" data-testid="trace-timeline">
        <h2 className="tf-h3">{t('trace.timeline')}</h2>
        <ol className="tf-timeline">
          <li className="tf-timeline__row">
            <span className="tf-timeline__name">{t('trace.request')}</span>
            <span className="tf-timeline__track" role="img" aria-label={t('trace.requestBar', { time: durationText(ms) })}>
              <span className={`tf-timeline__bar${st === 'failed' ? ' tf-timeline__bar--failed' : ''}`} style={cssVars({ '--tf-left': '0%', '--tf-width': pct(ms || 0) })} />
            </span>
            <span className="dk-mono tf-timeline__time">{ms == null ? '-' : durationText(ms)}</span>
          </li>
          {operations.map(op => (
            <li key={op.id} className="tf-timeline__row" data-testid="trace-operation">
              <span className="tf-timeline__name" title={op.summary || op.type}>{op.type}{op.model ? ` · ${op.model}` : ''}</span>
              <span className="tf-timeline__track" role="img" aria-label={t('trace.opBar', { type: op.type, offset: durationText(op.offsetMs), time: durationText(op.ms) })}>
                <span className={`tf-timeline__bar tf-timeline__bar--op${op.failed ? ' tf-timeline__bar--failed' : ''}`} style={cssVars({ '--tf-left': pct(op.offsetMs), '--tf-width': pct(Math.max(op.ms, total * 0.004)) })} />
              </span>
              <span className="dk-mono tf-timeline__time">{durationText(op.ms)}</span>
            </li>
          ))}
        </ol>
        <p className="tf-note-line">{operations.length > 0 ? t('trace.timelineMatched') : t('trace.timelineNone')}</p>
        {operations.some(o => o.error) && (
          <ul className="tf-op-errors">
            {operations.filter(o => o.error).map(o => (
              <li key={o.id}><span className="tf-sub">{o.type}</span> <span className="dk-mono tf-error-text">{o.error}</span></li>
            ))}
          </ul>
        )}
      </section>

      <Body id="request" title={t('trace.requestBody')} body={trace.request?.body} truncated={trace.request?.body_truncated} bytes={trace.request?.body_bytes} t={t} />
      <Body id="response" title={t('trace.responseBody')} body={trace.response?.body} truncated={trace.response?.body_truncated} bytes={trace.response?.body_bytes} t={t} />

      {details.length > 0 && (
        <section className="tf-block">
          <h2 className="tf-h3">{t('trace.details')}</h2>
          <dl className="dk-kv tf-kv">
            {details.map(([label, value]) => (
              <div key={label} className="tf-kv__pair"><dt>{label}</dt><dd className="dk-mono">{value}</dd></div>
            ))}
          </dl>
        </section>
      )}

      {model && (
        <div className="tf-links">
          <a className="dk-btn dk-btn--secondary" href={`/app/backend-logs/${encodeURIComponent(model)}${trace.timestamp ? `?from=${encodeURIComponent(trace.timestamp)}` : ''}`}>
            <Icon name="terminal" /> {t('trace.logs')}
          </a>
          <Link className="dk-btn dk-btn--secondary" to="/app/traffic/models"><Icon name="cube" /> {t('trace.moreModel', { model })}</Link>
        </div>
      )}
    </div>
  )
}
