/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Fragment, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useOutletContext } from 'react-router-dom'
import { useTrafficWindow, useUsage } from '../hooks/useTraffic'
import {
  bucketLabel, compactCount, filterRows, groupId, groupUsage, keyRows, projectTotals, quotaForecast,
  saveFile, seriesByBucket, sortRows, stackedByGroup, toCSV, totalsOf,
} from '../utils/traffic'
import TrafficChart from '../components/traffic/TrafficChart'
import WindowSwitch from '../components/traffic/WindowSwitch'
import { cssVars } from '../utils/modelLedger'
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import './traffic.css'

// Estimated cost is opt-in and entirely local. LocalAI has no price of its own;
// a multi-user install can type a price per million tokens to size a bill. The
// prices stay in this browser and apply to recorded token counts.
const PRICING_KEY = 'localai_token_pricing'

function loadPricing() {
  try {
    const p = JSON.parse(localStorage.getItem(PRICING_KEY) || '{}')
    return { prompt: Number(p.prompt) || 0, completion: Number(p.completion) || 0 }
  } catch { return { prompt: 0, completion: 0 } }
}

function savePricing(p) {
  try { localStorage.setItem(PRICING_KEY, JSON.stringify(p)) } catch { /* ignore */ }
}

const costOf = (row, p) => ((row.prompt || 0) / 1e6) * p.prompt + ((row.completion || 0) / 1e6) * p.completion
function costText(n) {
  if (!n) return '$0.00'
  return n < 0.01 ? '<$0.01' : `$${n.toFixed(2)}`
}

function SortHead({ col, label, sort, onSort, className }) {
  const active = sort.key === col
  return (
    <th scope="col" className={className} aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : undefined}>
      <button type="button" className="dk-table-sort" onClick={() => onSort({ key: col, direction: active && sort.direction === 'desc' ? 'asc' : 'desc' })}>
        {label}
        <Icon name="arrow-up" className="dk-icon" />
      </button>
    </th>
  )
}

function lastUsed(iso, t) {
  if (!iso) return '-'
  const ms = Date.parse(iso)
  if (Number.isNaN(ms)) return '-'
  const diff = Date.now() - ms
  if (diff < 60_000) return t('table.justNow')
  if (diff < 3_600_000) return t('table.minutesAgo', { count: Math.round(diff / 60_000) })
  if (diff < 86_400_000) return t('table.hoursAgo', { count: Math.round(diff / 3_600_000) })
  return t('table.daysAgo', { count: Math.round(diff / 86_400_000) })
}

// The time series of one row, opened in place under it.
function RowDetail({ id, by, buckets, period, t, cost, pricing }) {
  const mine = useMemo(() => buckets.filter(b => groupId(b, by) === id), [buckets, by, id])
  const series = useMemo(() => seriesByBucket(mine), [mine])
  const projected = projectTotals(series, period)
  const columns = series.map(p => ({
    key: p.bucket, label: p.bucket, tick: bucketLabel(p.bucket, period),
    segments: [{ id: 'prompt', value: p.prompt }, { id: 'completion', value: p.completion }],
  }))
  if (series.length === 0) return <p className="tf-note-line">{t('usage.noSeries')}</p>
  return (
    <div className="tf-detail">
      <TrafficChart
        testId="row-chart"
        title={t('usage.rowChart')}
        sub={t('charts.tokensUnit')}
        columns={columns}
        groups={[{ id: 'prompt', name: t('charts.in'), series: 1 }, { id: 'completion', name: t('charts.out'), series: 2 }]}
      />
      {projected && (
        <p className="tf-note-line" data-testid="row-projection">
          {t('usage.projected', { value: compactCount(projected.total) })}
          {cost ? ` ${t('usage.projectedCost', { value: costText(costOf(projected, pricing)) })}` : ''}
        </p>
      )}
    </div>
  )
}

export default function Usage() {
  const { addToast } = useOutletContext() || {}
  const { t } = useTranslation('traffic')
  const { window: win } = useTrafficWindow()
  const usage = useUsage(win.period, { sources: true })
  const { isAdmin, authEnabled } = usage

  const [by, setBy] = useState('model')
  const [model, setModel] = useState('')
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState({ key: 'total', direction: 'desc' })
  const [compact, setCompact] = useState(false)
  const [open, setOpen] = useState(() => new Set())
  const [pricing, setPricingState] = useState(loadPricing)
  const [showPricing, setShowPricing] = useState(false)
  const setPricing = p => { setPricingState(p); savePricing(p) }
  const costOn = pricing.prompt > 0 || pricing.completion > 0

  const groups = [
    { id: 'model', label: t('usage.byModel') },
    ...(isAdmin ? [{ id: 'user', label: t('usage.byUser') }] : []),
    ...(authEnabled ? [{ id: 'key', label: t('usage.byKey') }] : []),
  ]
  const grouping = groups.some(g => g.id === by) ? by : 'model'

  const keyBuckets = usage.keys?.buckets || []
  const keysTotals = usage.keys?.totals
  const ledgerRows = useMemo(
    () => (model ? usage.rows.filter(b => b.model === model) : usage.rows),
    [usage.rows, model],
  )
  const bucketsFor = grouping === 'key' ? keyBuckets : ledgerRows
  const rows = useMemo(() => {
    const base = grouping === 'key' ? keyRows(keysTotals, { showUser: isAdmin }) : groupUsage(ledgerRows, grouping)
    return sortRows(filterRows(base, query), sort)
  }, [grouping, keysTotals, ledgerRows, isAdmin, query, sort])

  const totals = useMemo(() => totalsOf(ledgerRows), [ledgerRows])
  const series = useMemo(() => seriesByBucket(ledgerRows), [ledgerRows])
  const models = useMemo(() => [...new Set(usage.rows.map(b => b.model).filter(Boolean))].sort(), [usage.rows])
  const stack = useMemo(() => stackedByGroup(bucketsFor, grouping, 4), [bucketsFor, grouping])
  const columns = useMemo(() => stack.points.map(p => ({
    key: p.bucket, label: p.bucket, tick: bucketLabel(p.bucket, win.period),
    segments: stack.groups.map(g => ({ id: g.id, value: p.values[g.id] || 0 })),
  })), [stack, win.period])
  const forecast = useMemo(() => quotaForecast(usage.quotas, series, win.period), [usage.quotas, series, win.period])
  const projected = useMemo(() => projectTotals(series, win.period), [series, win.period])
  const peak = rows.reduce((m, r) => Math.max(m, r.total), 0)
  const windowName = t(`window.long.${win.id}`)
  const groupName = groups.find(g => g.id === grouping)?.label || ''

  const toggleOpen = id => setOpen(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })

  // Export what the table holds, as it is sorted and filtered. Done here, in
  // the browser: there is no export endpoint.
  const exportColumns = [
    { label: groupName, value: r => r.name },
    ...(grouping === 'model' || grouping === 'user' ? [] : [{ label: t('table.owner'), value: r => r.sub }]),
    { label: t('table.requests'), value: r => r.requests },
    { label: t('table.tokensIn'), value: r => r.prompt },
    { label: t('table.tokensOut'), value: r => r.completion },
    { label: t('table.tokens'), value: r => r.total },
    ...(costOn ? [{ label: t('table.cost'), value: r => (r.prompt == null ? '' : costOf(r, pricing).toFixed(4)) }] : []),
  ]
  const stamp = new Date().toISOString().slice(0, 10)
  const exportCsv = () => {
    saveFile(`usage-${grouping}-${win.id}-${stamp}.csv`, toCSV(exportColumns, rows), 'text/csv')
    addToast?.(t('usage.exported', { count: rows.length }), 'success', 2000)
  }
  const exportJson = () => {
    const out = rows.map(r => Object.fromEntries(exportColumns.map(c => [c.label, c.value(r)])))
    saveFile(`usage-${grouping}-${win.id}-${stamp}.json`, JSON.stringify(out, null, 2), 'application/json')
    addToast?.(t('usage.exported', { count: rows.length }), 'success', 2000)
  }

  const cols = 7 + (costOn ? 1 : 0)

  return (
    <div className="page page--wide tf-page" data-testid="usage-page">
      <header className="tf-head">
        <div className="tf-head__lead">
          <h1 className="tf-title">{t('usage.title', { window: windowName })}</h1>
        </div>
        <div className="tf-head__acts">
          <WindowSwitch />
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--icon" aria-label={t('refresh')} onClick={usage.reload} disabled={usage.loading}>
            <Icon name="refresh" spin={usage.loading} />
          </button>
        </div>
      </header>
      <p className="tf-source">{isAdmin ? t('usage.sourceAdmin') : t('usage.sourceOwn')}</p>

      {usage.error && (
        <div className="tf-error" role="alert">
          <Icon name="alert-circle" />
          <span>{t('usage.error', { message: String(usage.error.message || usage.error) })}</span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={usage.reload}>{t('retry')}</button>
        </div>
      )}

      {usage.loading && usage.rows.length === 0 ? (
        <div className="tf-loading" data-testid="usage-loading"><LoadingSpinner size="lg" /></div>
      ) : (
        <>
          <div className="tf-controls">
            <div className="tf-controls__group">
              <span className="tf-controls__label" id="usage-group-label">{t('usage.groupBy')}</span>
              <div className="dk-segmented" role="radiogroup" aria-labelledby="usage-group-label">
                {groups.map(g => (
                  <button key={g.id} type="button" role="radio" aria-checked={grouping === g.id} className="dk-seg" onClick={() => { setBy(g.id); setOpen(new Set()) }}>
                    {g.label}
                  </button>
                ))}
              </div>
            </div>
            <div className="tf-controls__group tf-controls__group--end">
              {grouping !== 'key' && models.length > 1 && (
                <label className="tf-controls__field">
                  <span>{t('usage.modelFilter')}</span>
                  <select className="dk-select" value={model} onChange={e => setModel(e.target.value)}>
                    <option value="">{t('usage.allModels')}</option>
                    {models.map(m => <option key={m} value={m}>{m}</option>)}
                  </select>
                </label>
              )}
              <div className="dk-input-icon tf-search">
                <Icon name="search" className="dk-icon" />
                <input className="dk-input" type="search" aria-label={t('usage.search')} placeholder={t('usage.search')} value={query} onChange={e => setQuery(e.target.value)} />
              </div>
              <button type="button" className="dk-btn dk-btn--secondary" aria-pressed={showPricing || costOn} onClick={() => setShowPricing(v => !v)}>
                <Icon name="dollar" /> {costOn ? t('usage.pricing') : t('usage.setPricing')}
              </button>
            </div>
          </div>

          {showPricing && (
            <div className="dk-card tf-pricing" data-testid="pricing-panel">
              <label className="dk-field">
                <span className="dk-label">{t('usage.pricePrompt')}</span>
                <input className="dk-input dk-input--mono" type="number" min="0" step="0.01" placeholder="0.00" value={pricing.prompt || ''} onChange={e => setPricing({ ...pricing, prompt: Number(e.target.value) || 0 })} />
              </label>
              <label className="dk-field">
                <span className="dk-label">{t('usage.priceCompletion')}</span>
                <input className="dk-input dk-input--mono" type="number" min="0" step="0.01" placeholder="0.00" value={pricing.completion || ''} onChange={e => setPricing({ ...pricing, completion: Number(e.target.value) || 0 })} />
              </label>
              {costOn && <button type="button" className="dk-btn dk-btn--ghost" onClick={() => setPricing({ prompt: 0, completion: 0 })}><Icon name="close" /> {t('usage.clearPricing')}</button>}
              <p className="tf-note-line">{t('usage.pricingNote')}</p>
            </div>
          )}

          {rows.length === 0 && !usage.error ? (
            <div className="dk-empty tf-empty" data-testid="usage-empty">
              <Icon name="chart-bar" className="dk-empty-icon" />
              <h2 className="dk-empty-title">{query || model ? t('usage.noMatch') : t('usage.emptyTitle')}</h2>
              <p className="dk-empty-text">{query || model ? t('usage.noMatchText') : t('usage.emptyText')}</p>
            </div>
          ) : (
            <>
              <p className="tf-summary" data-testid="usage-summary">
                <strong>{compactCount(totals.requests)}</strong> {t('usage.summaryRequests')} · <strong>{compactCount(totals.prompt)}</strong> {t('usage.summaryIn')} · <strong>{compactCount(totals.completion)}</strong> {t('usage.summaryOut')}
                {costOn && <> · <strong>{costText(costOf(totals, pricing))}</strong> {t('usage.summaryCost')}</>}
                {projected && <> · {t('usage.projectedLine', { value: compactCount(projected.total) })}</>}
              </p>

              {forecast.length > 0 && (
                <section className="tf-section" data-testid="quota-forecast">
                  <h2 className="tf-h2">{t('usage.quotas')}</h2>
                  <ul className="tf-quotas">
                    {forecast.map((q, qi) => q.items.map(item => (
                      <li key={`${qi}-${item.label}`} className="tf-quota">
                        <span className="tf-quota__name">{q.model || t('usage.allModels')} <span className="tf-sub">{q.window}</span></span>
                        <span className="tf-quota__what">{t(`usage.quota.${item.label}`)}</span>
                        <span className="dk-meter tf-quota__meter" role="img" aria-label={`${compactCount(item.current)} / ${compactCount(item.max)}`}>
                          <span className="dk-meter-seg" style={cssVars({ '--dk-w': `${Math.min(100, (item.current / item.max) * 100).toFixed(1)}%` })} />
                        </span>
                        <span className="dk-mono tf-quota__fig">{compactCount(item.current)} / {compactCount(item.max)}</span>
                        <span className={`tf-quota__pace${item.within ? '' : ' tf-quota__pace--warn'}`}>
                          <Icon name={item.within ? 'check' : 'warning'} /> {item.within ? t('usage.quota.within') : t('usage.quota.runsOut', { time: hoursText(item.hoursLeft, t) })}
                        </span>
                      </li>
                    )))}
                  </ul>
                </section>
              )}

              <section className="tf-section">
                <TrafficChart
                  testId="usage-chart"
                  title={t('usage.chartTitle', { group: groupName.toLowerCase() })}
                  sub={t('usage.chartSub', { window: windowName })}
                  columns={columns}
                  groups={stack.groups.map(g => ({ ...g, name: g.id === '__other__' ? t('usage.other') : g.name }))}
                  unit={t('charts.tokensUnit')}
                  emptyLabel={t('charts.noBuckets')}
                />
              </section>

              <section className="tf-section">
                <div className="tf-section__head">
                  <h2 className="tf-h2">{groupName} <span className="tf-sub">{t('usage.rowCount', { count: rows.length })}</span></h2>
                  <div className="tf-section__acts">
                    <label className="tf-switch">
                      <button type="button" className="dk-switch" role="switch" aria-checked={compact} aria-label={t('usage.compact')} onClick={() => setCompact(v => !v)} />
                      <span>{t('usage.compact')}</span>
                    </label>
                    <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={exportCsv}><Icon name="download" /> {t('usage.exportCsv')}</button>
                    <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={exportJson}><Icon name="download" /> {t('usage.exportJson')}</button>
                  </div>
                </div>
                <div className="dk-table-wrap" data-testid="usage-table">
                  <table className={`dk-table tf-table${compact ? ' dk-table--compact' : ''}`}>
                    <caption className="dk-sr-only">{t('usage.caption', { group: groupName })}</caption>
                    <thead>
                      <tr>
                        <th scope="col" className="dk-table-toggle-cell"><span className="dk-sr-only">{t('table.open')}</span></th>
                        <SortHead col="name" label={groupName} sort={sort} onSort={setSort} />
                        <SortHead col="requests" label={t('table.requests')} sort={sort} onSort={setSort} className="dk-num" />
                        <SortHead col="prompt" label={t('table.tokensIn')} sort={sort} onSort={setSort} className="dk-num dk-hide-phone" />
                        <SortHead col="completion" label={t('table.tokensOut')} sort={sort} onSort={setSort} className="dk-num dk-hide-phone" />
                        <SortHead col="total" label={t('table.tokens')} sort={sort} onSort={setSort} className="dk-num" />
                        {costOn && <th scope="col" className="dk-num dk-hide-phone">{t('table.cost')}</th>}
                        <th scope="col" className="dk-hide-phone">{grouping === 'key' ? t('table.lastUsed') : t('table.share')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map(r => {
                        const isOpen = open.has(r.id)
                        return (
                          <Fragment key={r.id}>
                            <tr data-row data-clickable data-entity={r.name} onClick={e => { if (!e.target.closest('button, a')) toggleOpen(r.id) }}>
                              <td className="dk-table-toggle-cell">
                                <button type="button" className="dk-table-toggle" aria-expanded={isOpen} aria-label={t('table.openRow', { name: r.name })} onClick={() => toggleOpen(r.id)}>
                                  <Icon name="chevron-right" className="dk-icon" />
                                </button>
                              </td>
                              <td>
                                <span className="dk-table-name dk-mono">{r.name}</span>
                                {r.sub && <span className="dk-table-sub">{r.sub}</span>}
                              </td>
                              <td className="dk-num">{compactCount(r.requests)}</td>
                              <td className="dk-num dk-hide-phone">{r.prompt == null ? '-' : compactCount(r.prompt)}</td>
                              <td className="dk-num dk-hide-phone">{r.completion == null ? '-' : compactCount(r.completion)}</td>
                              <td className="dk-num">{compactCount(r.total)}</td>
                              {costOn && <td className="dk-num dk-hide-phone">{r.prompt == null ? '-' : costText(costOf(r, pricing))}</td>}
                              <td className="dk-hide-phone">
                                {grouping === 'key'
                                  ? <span className="tf-sub">{lastUsed(r.lastUsed, t)}</span>
                                  : <span className="tf-share" role="img" aria-label={`${peak > 0 ? Math.round((r.total / peak) * 100) : 0}%`}><span style={cssVars({ '--dk-w': `${peak > 0 ? Math.max(1, (r.total / peak) * 100).toFixed(1) : 0}%` })} /></span>}
                              </td>
                            </tr>
                            {isOpen && (
                              <tr className="dk-table-detail">
                                <td colSpan={cols}>
                                  <div className="dk-table-detail-body">
                                    <RowDetail id={r.id} by={grouping} buckets={bucketsFor} period={win.period} t={t} cost={costOn} pricing={pricing} />
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
                <p className="tf-note-line">{t('usage.noEndpoint')}</p>
              </section>
            </>
          )}
        </>
      )}
    </div>
  )
}

function hoursText(hours, t) {
  if (!Number.isFinite(hours) || hours < 0) return '-'
  if (hours < 1) return t('usage.quota.lessThanHour')
  if (hours < 48) return t('usage.quota.hours', { count: Math.round(hours) })
  return t('usage.quota.days', { count: Math.round(hours / 24) })
}
