/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Fragment, useId, useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendControlApi } from '../../utils/api'
import { filterModels, groupModels, paginateModels, sortModels } from '../../utils/nodeFleet'
import { timeAgo } from '../nodes/nodeStatus'
import ConfirmDialog from '../ConfirmDialog'
import ActionMenu from '../ActionMenu'
import LoadingSpinner from '../LoadingSpinner'
import Icon from '../Icon'

function SortHead({ col, label, sort, onSort, className }) {
  const active = sort.key === col
  return (
    <th scope="col" className={className} aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : undefined}>
      <button type="button" className="dk-table-sort" onClick={() => onSort({ key: col, direction: active && sort.direction === 'asc' ? 'desc' : 'asc' })}>
        {label}
        <Icon name="arrow-up" className="dk-icon" />
      </button>
    </th>
  )
}

function byNode(replicas, nodes) {
  const names = new Map(nodes.map(node => [node.id, node]))
  const groups = new Map()
  replicas.forEach(replica => {
    const id = String(replica?.node_id ?? '').trim()
    const key = id || `unknown:${replica?.id ?? groups.size}`
    if (!groups.has(key)) groups.set(key, { id, node: names.get(id) || null, replicas: [] })
    groups.get(key).replicas.push(replica)
  })
  return [...groups.values()]
}

// What is running across the cluster, one row per model. A row opens to show
// which node holds each replica and the logs of each. Reading it is one
// controller query, so the page asks only when this view is shown.
export default function RunningModels({ nodes, replicas, addToast }) {
  const { t } = useTranslation('swarm')
  const navigate = useNavigate()
  const baseId = useId()
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState({ key: 'model_name', direction: 'asc' })
  const [page, setPage] = useState(1)
  const [open, setOpen] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [stopping, setStopping] = useState(null)
  const stopRunning = useRef(false)
  const invoker = useRef(null)

  const grouped = useMemo(() => groupModels(replicas.rows), [replicas.rows])
  const filtered = useMemo(() => filterModels(grouped, query), [grouped, query])
  const ordered = useMemo(() => sortModels(filtered, sort), [filtered, sort])
  const pagination = useMemo(() => paginateModels(ordered, page), [ordered, page])
  const current = pagination.page

  const logs = (nodeId, key) => navigate(`/app/node-backend-logs/${encodeURIComponent(nodeId)}/${encodeURIComponent(key)}`)
  const openLogs = (model) => {
    const list = model.replicas || []
    if (list.length === 1 && list[0].node_id) logs(list[0].node_id, `${model.model_name}#${list[0].replica_index ?? 0}`)
    else setOpen(model.model_name)
  }

  const stop = () => {
    const model = confirm
    if (!model || stopRunning.current) return
    stopRunning.current = true
    setStopping(model.model_name)
    void (async () => {
      let failure = null
      try { await backendControlApi.shutdown({ model: model.model_name }) } catch (error) { failure = error }
      try { await replicas.load(true) } catch { /* the table keeps what it had */ }
      if (failure) addToast?.(t('models.stopFailed', { name: model.model_name, message: failure.message || String(failure) }), 'warning')
      else addToast?.(t('models.stopped', { name: model.model_name, replicas: t('models.nReplicas', { count: model.replica_count }), nodes: t('models.nNodes', { count: model.node_count }) }), 'success')
      setConfirm(null)
      setStopping(null)
      stopRunning.current = false
    })()
  }

  if (replicas.state === 'idle' || replicas.state === 'loading') {
    return <div className="sw-state-box" role="status"><LoadingSpinner size="sm" /><strong>{t('models.loading')}</strong><span>{t('models.loadingSub')}</span></div>
  }
  if (replicas.state === 'error') {
    return (
      <div className="sw-state-box" role="alert" data-level="error">
        <Icon name="alert-circle" />
        <strong>{t('models.error')}</strong>
        <span>{replicas.error}</span>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={t('models.retryAria')} onClick={() => replicas.retry()}>{t('models.retry')}</button>
      </div>
    )
  }
  if (grouped.length === 0) {
    return <div className="sw-state-box"><Icon name="layers" /><strong>{t('models.none')}</strong><span>{t('models.noneSub')}</span></div>
  }

  return (
    <div className="sw-models">
      <div className="sw-models__head">
        <div className="dk-input-icon sw-search">
          <Icon name="search" className="dk-icon" />
          <input
            className="dk-input"
            type="search"
            aria-label={t('models.search')}
            placeholder={t('models.search')}
            value={query}
            onChange={event => { setQuery(event.target.value); setPage(1) }}
          />
        </div>
        <span className="sw-count" aria-live="polite">{t('models.inView', { count: ordered.length })}</span>
      </div>
      <div className="dk-table-wrap sw-wrap">
        <table className="dk-table sw-table" data-testid="running-models">
          <caption className="dk-sr-only">{t('models.caption')}</caption>
          <thead>
            <tr>
              <SortHead col="model_name" label={t('models.model')} sort={sort} onSort={setSort} />
              <SortHead col="replica_count" label={t('models.replicas')} sort={sort} onSort={setSort} className="dk-num" />
              <SortHead col="node_count" label={t('models.nodes')} sort={sort} onSort={setSort} className="dk-hide-phone dk-num" />
              <SortHead col="in_flight" label={t('models.inFlight')} sort={sort} onSort={setSort} className="dk-hide-phone dk-num" />
              <th scope="col" className="dk-hide-phone">{t('models.backends')}</th>
              <SortHead col="last_used" label={t('models.lastUsed')} sort={sort} onSort={setSort} className="dk-hide-phone" />
              <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('models.actions')}</span></th>
            </tr>
          </thead>
          <tbody>
            {pagination.items.map((model, index) => {
              const isOpen = open === model.model_name
              const detailId = `${baseId}-m${index}`
              return (
                <Fragment key={model.model_name}>
                  <tr data-row data-clickable data-selected={isOpen ? 'true' : undefined} data-entity={model.model_name} onClick={event => { if (!event.target.closest('button, a, [role="menu"]')) setOpen(isOpen ? null : model.model_name) }}>
                    <td><span className="dk-table-name dk-mono">{model.model_name}</span></td>
                    <td className="dk-num dk-mono">{model.replica_count}</td>
                    <td className="dk-hide-phone dk-num dk-mono">{model.node_count}</td>
                    <td className="dk-hide-phone dk-num dk-mono">{model.in_flight}</td>
                    <td className="dk-hide-phone">{model.backend_types.length ? model.backend_types.join(', ') : <span className="sw-unknown">{t('models.unknown')}</span>}</td>
                    <td className="dk-hide-phone dk-mono">{model.last_used ? timeAgo(model.last_used) : <span className="sw-unknown">{t('models.never')}</span>}</td>
                    <td className="dk-table-actions">
                      <span className="sw-acts">
                        <ActionMenu
                          compact
                          ariaLabel={t('models.menu', { name: model.model_name })}
                          triggerLabel={t('models.menuTrigger', { name: model.model_name })}
                          items={[
                            { key: 'logs', icon: 'terminal', label: t('models.viewLogs'), onClick: () => openLogs(model) },
                            { divider: true },
                            {
                              key: 'stop',
                              icon: 'stop',
                              label: stopping === model.model_name ? t('models.stopping') : t('models.stop'),
                              danger: true,
                              disabled: !!stopping,
                              onClick: node => { invoker.current = node; setConfirm(model) },
                            },
                          ]}
                        />
                        <button
                          type="button"
                          className="dk-table-toggle"
                          aria-expanded={isOpen}
                          aria-controls={detailId}
                          aria-label={t(isOpen ? 'models.hide' : 'models.show', { name: model.model_name })}
                          onClick={() => setOpen(isOpen ? null : model.model_name)}
                        >
                          <Icon name="chevron-right" />
                        </button>
                      </span>
                    </td>
                  </tr>
                  <tr className="dk-table-detail" id={detailId}>
                    <td colSpan={7}>
                      <div className="dk-collapse" data-open={isOpen ? 'true' : 'false'}>
                        <div>
                          <div className="dk-table-detail-body">
                            {isOpen && byNode(model.replicas, nodes).map(group => {
                              const name = group.node?.name || group.id || t('models.unknownNode')
                              return (
                                <div key={group.id || group.replicas[0]?.id} className="sw-replicas">
                                  <div className="sw-replicas__head">
                                    {group.node
                                      ? <Link className="dk-link dk-mono" to={`/app/nodes/${encodeURIComponent(group.id)}`}>{name}</Link>
                                      : <strong className="dk-mono">{name}</strong>}
                                    <span className="sw-count">{t('models.replicaCount', { count: group.replicas.length })}</span>
                                  </div>
                                  <ul className="sw-replicas__list">
                                    {group.replicas.map(replica => {
                                      const number = Number.isFinite(replica.replica_index) ? replica.replica_index + 1 : null
                                      const key = `${model.model_name}#${replica.replica_index ?? 0}`
                                      return (
                                        <li key={replica.id || `${replica.replica_index}:${replica.address}`}>
                                          <span>{t('models.replica', { n: number ?? '—' })}</span>
                                          <code className="dk-mono">{replica.address || t('models.noAddress')}</code>
                                          {group.id && (
                                            <button
                                              type="button"
                                              className="dk-btn dk-btn--ghost dk-btn--sm"
                                              aria-label={t('models.logsFor', { name: model.model_name, n: number ?? '?', node: name })}
                                              onClick={() => logs(group.id, key)}
                                            >
                                              <Icon name="terminal" /> {t('models.logs')}
                                            </button>
                                          )}
                                        </li>
                                      )
                                    })}
                                  </ul>
                                </div>
                              )
                            })}
                          </div>
                        </div>
                      </div>
                    </td>
                  </tr>
                </Fragment>
              )
            })}
          </tbody>
        </table>
      </div>
      <div className="sw-pager">
        <span>{t('pager.page', { page: current, total: pagination.totalPages })}</span>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={t('models.prevPage')} disabled={current === 1} onClick={() => setPage(current - 1)}>{t('pager.previous')}</button>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={t('models.nextPage')} disabled={current === pagination.totalPages} onClick={() => setPage(current + 1)}>{t('pager.next')}</button>
      </div>
      <ConfirmDialog
        open={!!confirm}
        title={confirm ? t('models.stopTitle', { name: confirm.model_name }) : t('models.stopTitleFallback')}
        message={confirm ? t('models.stopMessage', { name: confirm.model_name, replicas: t('models.nReplicas', { count: confirm.replica_count }), nodes: t('models.nNodes', { count: confirm.node_count }) }) : ''}
        confirmLabel={t('models.stopConfirm')}
        pendingLabel={t('models.stopping')}
        pending={!!stopping}
        danger
        onConfirm={stop}
        onCancel={() => { setConfirm(null); requestAnimationFrame(() => invoker.current?.focus?.()) }}
      />
    </div>
  )
}
