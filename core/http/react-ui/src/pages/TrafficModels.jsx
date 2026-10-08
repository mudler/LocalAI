/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Fragment, useCallback, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useTrafficWindow, useUsage } from '../hooks/useTraffic'
import { useLocalMachine } from '../hooks/useLocalMachine'
import { usePolling } from '../hooks/usePolling'
import { tracesApi } from '../utils/api'
import { formatBytes } from '../utils/format'
import { compactCount, durationText, filterRows, groupUsage, modelStats, sortModelRows } from '../utils/traffic'
import WindowSwitch from '../components/traffic/WindowSwitch'
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import './traffic.css'

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

// What each model has done, from three records that do not share a clock:
//   the usage ledger (requests and tokens over the window),
//   the backend-operation buffer (loads, runs and their errors, as many as the
//   trace buffer still holds), and
//   the models this process holds in memory now.
// LocalAI keeps no per-model latency percentile and no memory history, so there
// are none here. Memory held is the backend process's resident host memory when
// the server reports it, and a dash when it does not.
export default function TrafficModels() {
  const { t } = useTranslation('traffic')
  const { window: win } = useTrafficWindow()
  const usage = useUsage(win.period)
  const machine = useLocalMachine({ withResources: false })
  const [ops, setOps] = useState({ items: [], total: 0, state: 'loading' })
  const readOps = useCallback(async () => {
    try {
      const page = await tracesApi.getBackend({ limit: 1000 })
      setOps({ items: page.items, total: page.total, state: 'ready' })
    } catch {
      setOps(prev => ({ ...prev, state: prev.items.length ? 'ready' : 'unavailable' }))
    }
  }, [])
  usePolling(readOps, 15_000)

  const [query, setQuery] = useState('')
  const [sort, setSort] = useState({ key: 'total', direction: 'desc' })
  const [open, setOpen] = useState(() => new Set())

  const ledger = useMemo(() => groupUsage(usage.rows, 'model'), [usage.rows])
  const all = useMemo(() => modelStats({ ledger, backend: ops.items, loaded: machine.rows }), [ledger, ops.items, machine.rows])
  const rows = useMemo(() => sortModelRows(filterRows(all.map(r => ({ ...r, sub: r.backend })), query), sort), [all, query, sort])
  const loadedCount = all.filter(r => r.loaded).length
  const windowName = t(`window.long.${win.id}`)
  const loading = usage.loading && machine.state === 'loading' && all.length === 0

  const toggle = id => setOpen(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })

  return (
    <div className="page page--wide tf-page" data-testid="traffic-models">
      <header className="tf-head">
        <div className="tf-head__lead">
          <h1 className="tf-title">{t('models.title')}</h1>
        </div>
        <div className="tf-head__acts">
          <WindowSwitch />
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--icon" aria-label={t('refresh')} onClick={() => { usage.reload(); machine.refresh(); readOps() }}>
            <Icon name="refresh" spin={usage.loading} />
          </button>
        </div>
      </header>
      <p className="tf-source">{t('models.sources', { window: windowName })}</p>

      {usage.error && (
        <div className="tf-error" role="alert">
          <Icon name="alert-circle" />
          <span>{t('usage.error', { message: String(usage.error.message || usage.error) })}</span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={usage.reload}>{t('retry')}</button>
        </div>
      )}

      {loading ? (
        <div className="tf-loading"><LoadingSpinner size="lg" /></div>
      ) : all.length === 0 ? (
        <div className="dk-empty tf-empty" data-testid="models-empty">
          <Icon name="cube" className="dk-empty-icon" />
          <h2 className="dk-empty-title">{t('models.emptyTitle')}</h2>
          <p className="dk-empty-text">{t('models.emptyText')}</p>
          <div className="tf-empty__acts"><Link className="dk-btn dk-btn--primary" to="/app/chat">{t('overview.emptyChat')}</Link></div>
        </div>
      ) : (
        <>
          <div className="tf-controls">
            <p className="tf-summary">{t('models.summary', { count: all.length, loaded: loadedCount })}</p>
            <div className="dk-input-icon tf-search">
              <Icon name="search" className="dk-icon" />
              <input className="dk-input" type="search" aria-label={t('models.search')} placeholder={t('models.search')} value={query} onChange={e => setQuery(e.target.value)} />
            </div>
          </div>

          <div className="dk-table-wrap" data-testid="models-table">
            <table className="dk-table tf-table">
              <caption className="dk-sr-only">{t('models.caption')}</caption>
              <thead>
                <tr>
                  <th scope="col" className="dk-table-toggle-cell"><span className="dk-sr-only">{t('table.open')}</span></th>
                  <SortHead col="name" label={t('table.model')} sort={sort} onSort={setSort} />
                  <SortHead col="requests" label={t('table.requests')} sort={sort} onSort={setSort} className="dk-num" />
                  <SortHead col="total" label={t('table.tokens')} sort={sort} onSort={setSort} className="dk-num dk-hide-phone" />
                  <SortHead col="failed" label={t('models.failedOps')} sort={sort} onSort={setSort} className="dk-num dk-hide-phone" />
                  <SortHead col="meanMs" label={t('models.meanOp')} sort={sort} onSort={setSort} className="dk-num dk-hide-phone" />
                  <SortHead col="rssBytes" label={t('models.memory')} sort={sort} onSort={setSort} className="dk-num" />
                </tr>
              </thead>
              <tbody>
                {rows.length === 0 && (
                  <tr className="dk-table-empty"><td colSpan={7}><div className="dk-empty"><p className="dk-empty-text">{t('usage.noMatchText')}</p></div></td></tr>
                )}
                {rows.map(r => {
                  const isOpen = open.has(r.id)
                  return (
                    <Fragment key={r.id}>
                      <tr data-row data-clickable data-entity={r.name} onClick={e => { if (!e.target.closest('button, a')) toggle(r.id) }}>
                        <td className="dk-table-toggle-cell">
                          <button type="button" className="dk-table-toggle" aria-expanded={isOpen} aria-label={t('table.openRow', { name: r.name })} onClick={() => toggle(r.id)}>
                            <Icon name="chevron-right" className="dk-icon" />
                          </button>
                        </td>
                        <td>
                          <span className="dk-table-name dk-mono">{r.name}</span>
                          <span className="dk-table-sub">
                            <span className={`tf-loaded${r.loaded ? ' tf-loaded--on' : ''}`}>{r.loaded ? t('models.inMemory') : t('models.notLoaded')}</span>
                            {r.backend && <> · {r.backend}</>}
                          </span>
                        </td>
                        <td className="dk-num">{compactCount(r.requests)}</td>
                        <td className="dk-num dk-hide-phone">{compactCount(r.total)}</td>
                        <td className="dk-num dk-hide-phone" data-level={r.failed > 0 ? 'error' : undefined}>{ops.state === 'unavailable' ? '-' : r.failed}</td>
                        <td className="dk-num dk-hide-phone">{r.meanMs == null ? '-' : durationText(r.meanMs)}</td>
                        <td className="dk-num">{r.rssBytes == null ? '-' : formatBytes(r.rssBytes)}</td>
                      </tr>
                      {isOpen && (
                        <tr className="dk-table-detail" data-testid="model-detail">
                          <td colSpan={7}>
                            <div className="dk-table-detail-body">
                              <dl className="dk-kv tf-kv">
                                <dt>{t('models.tokensSplit')}</dt>
                                <dd className="dk-mono">{t('models.tokensSplitValue', { prompt: compactCount(r.prompt), completion: compactCount(r.completion) })}</dd>
                                <dt>{t('models.operations')}</dt>
                                <dd className="dk-mono">{ops.state === 'unavailable' ? t('models.noBuffer') : t('models.operationsValue', { count: r.operations, failed: r.failed })}</dd>
                                {r.lastError && (<><dt>{t('models.lastError')}</dt><dd className="dk-mono tf-error-text">{r.lastError}</dd></>)}
                                {r.loaded && (<>
                                  <dt>{t('models.process')}</dt>
                                  <dd className="dk-mono">
                                    {r.rssBytes == null ? t('models.noReading') : `${formatBytes(r.rssBytes)}${r.cpuPercent != null ? ` · ${Math.round(r.cpuPercent)}% CPU` : ''}`}
                                  </dd>
                                </>)}
                              </dl>
                              <p className="tf-note-line">{t('models.memoryNote')}</p>
                              <div className="tf-links">
                                <Link className="dk-btn dk-btn--secondary dk-btn--sm" to={`/app/traces?tab=backend&q=${encodeURIComponent(r.name)}`}><Icon name="list" /> {t('models.traces')}</Link>
                                <Link className="dk-btn dk-btn--secondary dk-btn--sm" to={`/app/backend-logs/${encodeURIComponent(r.name)}`}><Icon name="terminal" /> {t('models.logs')}</Link>
                                <Link className="dk-btn dk-btn--secondary dk-btn--sm" to={`/app/models/${encodeURIComponent(r.name)}`}><Icon name="cube" /> {t('models.page')}</Link>
                              </div>
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
          <p className="tf-note-line">{t('models.bufferNote', { count: ops.items.length })}</p>
        </>
      )}
    </div>
  )
}
