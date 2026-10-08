/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useTrafficWindow, useTraceSummary, useTracingEnabled, useUsage } from '../hooks/useTraffic'
import {
  bucketLabel, compactCount, durationText, groupUsage, overviewFigures, seriesByBucket, sortRows,
  totalsOf, traceColumns,
} from '../utils/traffic'
import TrafficChart from '../components/traffic/TrafficChart'
import WindowSwitch from '../components/traffic/WindowSwitch'
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import './traffic.css'

// The hour or the day a trace column starts at, in the viewer's locale.
function columnLabel(start, hours) {
  const d = new Date(start)
  if (Number.isNaN(d.getTime())) return ''
  return hours <= 24
    ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleDateString([], { month: 'short', day: 'numeric', hour: '2-digit' })
}

function Figure({ id, label, value, note, level, children }) {
  return (
    <div className="tf-figure" data-testid={`figure-${id}`} data-level={level}>
      <dt>{label}</dt>
      <dd>{value}</dd>
      {note && <small>{note}</small>}
      {children}
    </div>
  )
}

// Traffic opens here: what came in, what failed, how long the slow ones took,
// how many tokens. Five figures in a row and three charts, each saying which
// source its numbers come from, because the usage ledger and the trace buffer
// are two different records and only the buffer knows about failures.
export default function TrafficOverview() {
  const { t } = useTranslation('traffic')
  const { window: win } = useTrafficWindow()
  const usage = useUsage(win.period)
  const tracing = useTracingEnabled()
  const traces = useTraceSummary(win.hours)

  const ledger = useMemo(() => (usage.loading || usage.error ? null : totalsOf(usage.rows)), [usage.loading, usage.error, usage.rows])
  const figures = overviewFigures({ ledger, summary: traces.summary, tracing: tracing.enabled })
  const series = useMemo(() => seriesByBucket(usage.rows), [usage.rows])
  const models = useMemo(() => sortRows(groupUsage(usage.rows, 'model'), { key: 'total' }).slice(0, 5), [usage.rows])

  const requestColumns = useMemo(() => series.map(p => ({
    key: p.bucket, label: p.bucket, tick: bucketLabel(p.bucket, win.period),
    segments: [{ id: 'requests', value: p.requests }],
  })), [series, win.period])
  const tokenColumns = useMemo(() => series.map(p => ({
    key: p.bucket, label: p.bucket, tick: bucketLabel(p.bucket, win.period),
    segments: [{ id: 'prompt', value: p.prompt }, { id: 'completion', value: p.completion }],
  })), [series, win.period])
  const traceCols = useMemo(() => traceColumns(traces.summary).map(c => ({
    key: c.start, label: columnLabel(c.start, win.hours), segments: [{ id: 'ok', value: c.ok }, { id: 'failed', value: c.failed }],
  })), [traces.summary, win.hours])

  const loading = usage.loading && !usage.rows.length
  const firstRun = !loading && !usage.error && ledger && ledger.requests === 0
    && (tracing.enabled === false || (traces.summary && traces.summary.total === 0))
  const tracingOff = tracing.enabled === false
  const windowName = t(`window.long.${win.id}`)

  return (
    <div className="page page--wide tf-page" data-testid="traffic-overview">
      <header className="tf-head">
        <div className="tf-head__lead">
          <h1 className="tf-title">{t('overview.title', { window: windowName })}</h1>
        </div>
        <div className="tf-head__acts">
          <WindowSwitch />
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--icon" aria-label={t('refresh')} onClick={() => { usage.reload(); traces.refetch() }}>
            <Icon name="refresh" spin={usage.loading} />
          </button>
        </div>
      </header>
      <p className="tf-source">{t('overview.sources')}{win.capped && ` ${t('overview.capped')}`}</p>

      {usage.error && (
        <div className="tf-error" role="alert">
          <Icon name="alert-circle" />
          <span>{t('overview.usageError', { message: String(usage.error.message || usage.error) })}</span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={usage.reload}>{t('retry')}</button>
        </div>
      )}

      {loading ? (
        <div className="tf-loading" data-testid="traffic-loading"><LoadingSpinner size="lg" /></div>
      ) : firstRun ? (
        <div className="dk-empty tf-empty" data-testid="traffic-empty">
          <Icon name="chart-line" className="dk-empty-icon" />
          <h2 className="dk-empty-title">{t('overview.emptyTitle')}</h2>
          <p className="dk-empty-text">{t('overview.emptyText')}</p>
          <div className="tf-empty__acts">
            <Link className="dk-btn dk-btn--primary" to="/app/chat">{t('overview.emptyChat')}</Link>
            <Link className="dk-btn dk-btn--secondary" to="/app/traffic/prometheus">{t('overview.emptyApi')}</Link>
          </div>
        </div>
      ) : (
        <>
          <dl className="tf-figures" data-testid="traffic-figures">
            <Figure id="requests" label={t('figures.requests')} value={figures.requests == null ? '-' : compactCount(figures.requests)} note={t('figures.ledger')} />
            <Figure
              id="failed"
              label={t('figures.failed')}
              value={figures.failed == null ? '-' : compactCount(figures.failed)}
              level={figures.failed > 0 ? 'error' : undefined}
              note={figures.failed == null
                ? (tracingOff ? t('figures.tracingOff') : t('figures.unknown'))
                : (figures.failed > 0 ? t('figures.failedShare', { share: figures.failedShare }) : t('figures.noneFailed'))}
            >
              {figures.failed > 0 && (
                <Link className="dk-link tf-figure__link" to="/app/traces?state=failed">{t('figures.openFailed')}</Link>
              )}
              {figures.failed == null && tracingOff && (
                <Link className="dk-link tf-figure__link" to="/app/traces">{t('figures.turnOn')}</Link>
              )}
            </Figure>
            <Figure
              id="p95"
              label={t('figures.p95')}
              value={figures.p95 == null ? '-' : durationText(figures.p95)}
              note={figures.p95 == null ? (tracingOff ? t('figures.tracingOff') : t('figures.noTraces')) : t('figures.p95Note', { count: figures.traced })}
            />
            <Figure id="tokens-in" label={t('figures.tokensIn')} value={figures.tokensIn == null ? '-' : compactCount(figures.tokensIn)} note={t('figures.ledger')} />
            <Figure id="tokens-out" label={t('figures.tokensOut')} value={figures.tokensOut == null ? '-' : compactCount(figures.tokensOut)} note={t('figures.ledger')} />
          </dl>

          <section className="tf-section">
            <TrafficChart
              testId="chart-requests"
              title={t('charts.requests')}
              sub={t('charts.requestsSub', { window: windowName })}
              columns={requestColumns}
              groups={[{ id: 'requests', name: t('figures.requests'), series: 1 }]}
              unit={t('charts.requestsUnit')}
              emptyLabel={t('charts.noBuckets')}
            />
          </section>

          <section className="tf-section">
            {tracingOff ? (
              <div className="tf-note" data-testid="chart-failed-off">
                <h3 className="dk-chart-title">{t('charts.failed')}</h3>
                <p>{t('charts.failedOff')} <Link className="dk-link" to="/app/traces">{t('figures.turnOn')}</Link></p>
              </div>
            ) : traces.summary && traceCols.length > 0 ? (
              <TrafficChart
                testId="chart-failed"
                title={t('charts.failed')}
                sub={t('charts.failedSub', { count: traces.summary.total })}
                columns={traceCols}
                groups={[
                  { id: 'ok', name: t('charts.succeeded'), series: 1 },
                  { id: 'failed', name: t('charts.failedName'), tone: 'failed' },
                ]}
                unit={t('charts.requestsUnit')}
                emptyLabel={t('charts.noTraces')}
              />
            ) : (
              <div className="tf-note"><h3 className="dk-chart-title">{t('charts.failed')}</h3><p>{t('figures.unknown')}</p></div>
            )}
          </section>

          <section className="tf-section">
            <TrafficChart
              testId="chart-tokens"
              title={t('charts.tokens')}
              sub={t('charts.tokensSub', { window: windowName })}
              columns={tokenColumns}
              groups={[
                { id: 'prompt', name: t('charts.in'), series: 1 },
                { id: 'completion', name: t('charts.out'), series: 2 },
              ]}
              unit={t('charts.tokensUnit')}
              emptyLabel={t('charts.noBuckets')}
            />
          </section>

          {models.length > 0 && (
            <section className="tf-section" data-testid="busiest-models">
              <div className="tf-section__head">
                <h2 className="tf-h2">{t('busiest.title')} <span className="tf-sub">{windowName}</span></h2>
                <Link className="dk-link" to="/app/traffic/models">{t('busiest.all')}</Link>
              </div>
              <div className="dk-table-wrap dk-table-wrap--flat">
                <table className="dk-table">
                  <caption className="dk-sr-only">{t('busiest.title')}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t('table.model')}</th>
                      <th scope="col" className="dk-num">{t('table.requests')}</th>
                      <th scope="col" className="dk-num dk-hide-phone">{t('table.tokensIn')}</th>
                      <th scope="col" className="dk-num dk-hide-phone">{t('table.tokensOut')}</th>
                      <th scope="col" className="dk-num">{t('table.tokens')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {models.map(m => (
                      <tr key={m.id}>
                        <td><span className="dk-table-name dk-mono">{m.name}</span></td>
                        <td className="dk-num">{compactCount(m.requests)}</td>
                        <td className="dk-num dk-hide-phone">{compactCount(m.prompt)}</td>
                        <td className="dk-num dk-hide-phone">{compactCount(m.completion)}</td>
                        <td className="dk-num">{compactCount(m.total)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}

          <p className="tf-foot">
            <Icon name="chart-bar" /> {t('overview.scrape')} <Link className="dk-link" to="/app/traffic/prometheus">{t('overview.scrapeHow')}</Link>
          </p>
        </>
      )}
    </div>
  )
}
