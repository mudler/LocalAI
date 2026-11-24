/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useMemo, useState } from 'react'
import { Link, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useOperateSummary } from '../contexts/OperateSummaryContext'
import { useOperationActions, isRetryable } from '../hooks/useOperationActions'
import { adminUsersApi } from '../utils/api'
import { capacityOf, isFirstRun, statusHeadline } from '../utils/operateStatus'
import { cssVars, gbLabel } from '../utils/modelLedger'
import AdminPage from '../components/admin/AdminPage'
import Icon from '../components/Icon'
import { Button, StatusChip } from '../components/ui'
import './admin-overview.css'

// The Administration landing. What needs a person comes first, each row with
// the button that deals with it; then a card for the cluster (or this machine),
// one for traffic and one for access. Every number is one the summary already
// holds (OperateSummaryContext); a card whose data does not exist, or whose
// calls this viewer may not make, is left out rather than drawn empty.

const NAMES_SHOWN = 3
const names = list => list.slice(0, NAMES_SHOWN).join(', ') + (list.length > NAMES_SHOWN ? ', …' : '')
const person = u => u.name || u.email
// What is down first, then what failed, then people, then what is merely stale.
const RANK = { 'node-unhealthy': 0, 'operation-failed': 1, 'signups-waiting': 2, 'backend-update': 3 }

export default function AdminOverview() {
  const { t } = useTranslation('operate')
  const { t: tu } = useTranslation('updates')
  const { addToast } = useOutletContext() || {}
  const summary = useOperateSummary()
  const actions = useOperationActions(addToast)
  const [pending, setPending] = useState(() => new Set())

  const ready = Boolean(summary?.ready)
  const attention = useMemo(
    () => [...(summary?.attention || [])].sort((a, b) => (RANK[a.kind] ?? 9) - (RANK[b.kind] ?? 9)),
    [summary?.attention],
  )
  const operations = useMemo(() => summary?.operations || [], [summary?.operations])
  const nodes = useMemo(() => summary?.nodes || [], [summary?.nodes])
  const distributed = summary?.distributed === true
  const traces = summary?.traces
  const loaded = summary?.loadedModels
  const refresh = summary?.refresh
  const userCounts = summary?.userCounts
  const memory = useMemo(
    () => capacityOf({ resources: summary?.resources, nodes, distributed }),
    [summary?.resources, nodes, distributed],
  )
  const firstRun = ready && isFirstRun({
    backends: summary?.installed?.backends,
    models: summary?.installed?.models,
    operations,
    loaded: loaded?.length || 0,
  })
  const headline = statusHeadline({ needs: attention.length, looking: 0, firstRun })

  const markPending = (key, on) => setPending(current => {
    const next = new Set(current)
    if (on) next.add(key)
    else next.delete(key)
    return next
  })
  const run = useCallback(async (key, work) => {
    markPending(key, true)
    try {
      await work()
      await refresh?.()
    } finally {
      markPending(key, false)
    }
  }, [refresh])

  const approve = (item) => run(item.id, async () => {
    try {
      await Promise.all(item.users.map(u => adminUsersApi.setStatus(u.id, 'active')))
      addToast?.(t('overview.approved', { count: item.users.length }), 'success')
    } catch (err) {
      addToast?.(t('overview.approveFailed', { message: err.message }), 'error')
    }
  })

  const scope = !ready ? null : distributed ? t('status.scopeCluster', { count: nodes.length }) : t('status.scopeSingle')
  const sentence = !ready
    ? t('status.headline.loading')
    : firstRun ? t('status.headline.firstRun') : t(`status.headline.${headline.kind === 'looking' ? 'ok' : headline.kind}`, { count: headline.count })

  return (
    <AdminPage
      width="full"
      className="ov-page"
      data-testid="operate-overview"
      aria-busy={!ready}
      title={t('overview.title')}
      supporting={(
        <span data-testid="operate-headline" data-kind={!ready ? 'loading' : firstRun ? 'firstRun' : headline.kind}>
          {scope ? `${scope}. ` : ''}{sentence}
        </span>
      )}
      action={firstRun ? (
        <div className="ov-first" data-testid="operate-first-run">
          <Button to="/app/models">{t('status.firstRunModels')}</Button>
          <Button variant="primary" icon="download" to="/app/backends?view=catalog">{t('status.firstRunBackend')}</Button>
        </div>
      ) : null}
    >
      {firstRun && <p className="ov-note">{t('status.firstRunBody')}</p>}

      {!firstRun && (
        <section className="ov-card ov-needs" aria-labelledby="ov-needs-h" data-testid="operate-needs">
          <header className="ov-card__head">
            <h2 className="ov-card__title" id="ov-needs-h">
              {t('status.rows.needs')}
              {attention.length > 0 && <span className="ov-count" data-testid="operate-needs-count">{attention.length}</span>}
            </h2>
          </header>
          {!ready ? (
            <p className="ov-empty"><span className="dk-skeleton dk-skeleton--line" /></p>
          ) : attention.length === 0 ? (
            <p className="ov-empty" data-testid="operate-attention-clear">{t('needs.clear')}</p>
          ) : (
            <ul className="ov-rows">
              {attention.map(item => {
                const op = item.kind === 'operation-failed' ? operations.find(o => `op:${o.id || o.uid || o.name}` === item.id) : null
                return (
                  <li key={item.id} className="ov-row" data-testid="operate-attention-item" data-kind={item.kind}>
                    <span className="ov-row__icon" data-tone={item.kind === 'backend-update' ? 'info' : item.kind === 'signups-waiting' ? 'warn' : 'error'}>
                      <Icon name={item.kind === 'backend-update' ? 'arrow-up' : item.kind === 'signups-waiting' ? 'users' : 'warning'} aria-hidden="true" />
                    </span>
                    <div className="ov-row__text">
                      <strong>
                        {item.kind === 'backend-update' && tu('strip.headline.updates', { count: item.count })}
                        {item.kind === 'operation-failed' && t('needs.operationFailed', { name: item.name })}
                        {item.kind === 'node-unhealthy' && t('needs.nodeUnhealthy', { name: item.name })}
                        {item.kind === 'signups-waiting' && t('overview.signups', { count: item.count })}
                      </strong>
                      <span>
                        {item.kind === 'backend-update' && names(item.names)}
                        {item.kind === 'signups-waiting' && names(item.users.map(person))}
                        {(item.kind === 'operation-failed' || item.kind === 'node-unhealthy') && item.detail}
                      </span>
                    </div>
                    <div className="ov-row__acts">
                      {item.kind === 'node-unhealthy' && (
                        <Button size="sm" to={`/app/nodes/${encodeURIComponent(item.name)}`}>{t('overview.openWorker')}</Button>
                      )}
                      {item.kind === 'operation-failed' && op && (
                        <>
                          {isRetryable(op) && (
                            <Button size="sm" busy={pending.has(item.id)} disabled={pending.has(item.id)} onClick={() => run(item.id, () => actions.retry(op))}>
                              {t('activity.retry')}
                            </Button>
                          )}
                          <Button size="sm" variant="ghost" onClick={() => actions.dismiss(op.jobID)}>{t('activity.dismiss')}</Button>
                        </>
                      )}
                      {item.kind === 'signups-waiting' && (
                        <>
                          <Button size="sm" busy={pending.has(item.id)} disabled={pending.has(item.id)} onClick={() => approve(item)} data-testid="overview-approve">
                            {item.count > 1 ? t('overview.approveAll') : t('overview.approve')}
                          </Button>
                          <Button size="sm" variant="ghost" to="/app/users">{tu('row.review')}</Button>
                        </>
                      )}
                      {item.kind === 'backend-update' && (
                        <Button size="sm" to={item.ready ? '/app/updates?filter=ready' : '/app/updates?filter=needs-you'} data-testid="operate-review-updates">{tu('row.review')}</Button>
                      )}
                    </div>
                  </li>
                )
              })}
            </ul>
          )}
        </section>
      )}

      {ready && !firstRun && (
        <div className="ov-cards">
          <section className="ov-card" aria-labelledby="ov-cluster-h" data-testid="overview-cluster">
            <header className="ov-card__head">
              <h2 className="ov-card__title" id="ov-cluster-h">{distributed ? t('overview.cluster') : t('overview.machine')}</h2>
              <Link className="dk-link" to="/app/nodes">{distributed ? t('overview.openWorkers') : t('overview.openMachine')}</Link>
            </header>
            <ClusterBody t={t} distributed={distributed} nodes={nodes} loaded={loaded} memory={memory} resources={summary?.resources} />
          </section>

          <section className="ov-card" aria-labelledby="ov-traffic-h" data-testid="overview-traffic">
            <header className="ov-card__head">
              <h2 className="ov-card__title" id="ov-traffic-h">{t('overview.traffic', { hours: traces?.window_hours || 24 })}</h2>
              <Link className="dk-link" to="/app/usage">{t('overview.openUsage')}</Link>
            </header>
            <TrafficBody t={t} traces={traces} />
          </section>

          {userCounts && (
            <section className="ov-card" aria-labelledby="ov-access-h" data-testid="overview-access">
              <header className="ov-card__head">
                <h2 className="ov-card__title" id="ov-access-h">{t('overview.access')}</h2>
                <Link className="dk-link" to="/app/users">{t('overview.openUsers')}</Link>
              </header>
              <dl className="ov-stats">
                <Stat label={t('overview.users')} value={userCounts.total} />
                <Stat label={t('overview.waiting')} value={summary.pendingUsers.length} tone={summary.pendingUsers.length > 0 ? 'warn' : undefined} />
                <Stat label={t('overview.admins')} value={userCounts.admins} />
              </dl>
            </section>
          )}
        </div>
      )}
    </AdminPage>
  )
}

function Stat({ label, value, unit, tone }) {
  return (
    <div className="ov-stat" data-tone={tone}>
      <dt>{label}</dt>
      <dd><strong>{value}</strong>{unit && <span>{unit}</span>}</dd>
    </div>
  )
}

const down = node => node?.healthy === false || ['unhealthy', 'down', 'offline', 'error'].includes(String(node?.status || '').toLowerCase())

function ClusterBody({ t, distributed, nodes, loaded, memory, resources }) {
  const disk = resources?.disk
  const diskLine = disk?.total > 0 ? (
    <p className="ov-foot">
      <StatusChip tone="ok">{t('overview.disk')}</StatusChip>
      <span>{t('overview.diskFree', { free: gbLabel(disk.available), total: gbLabel(disk.total) })}</span>
    </p>
  ) : null
  if (distributed) {
    const online = nodes.filter(n => !down(n)).length
    const models = nodes.reduce((sum, n) => sum + (Number(n.model_count) || 0), 0)
    const flight = nodes.reduce((sum, n) => sum + (Number(n.in_flight_count) || 0), 0)
    const metered = nodes.filter(n => n.total_vram > 0 || n.total_ram > 0)
    return (
      <>
        <dl className="ov-stats">
          <Stat label={t('overview.online')} value={online} unit={t('overview.ofTotal', { total: nodes.length })} tone={online < nodes.length ? 'warn' : undefined} />
          <Stat label={t('overview.loaded')} value={models} />
          <Stat label={t('overview.inFlight')} value={flight} />
        </dl>
        {metered.length > 0 && (
          <ul className="ov-meters" aria-label={t('capacity.nodes')}>
            {metered.slice(0, 5).map(node => {
              const total = node.total_vram > 0 ? node.total_vram : node.total_ram
              const free = node.total_vram > 0 ? node.available_vram : node.available_ram
              const used = Math.max(0, total - (free || 0))
              const pct = Math.min(100, (used / total) * 100)
              return (
                <li key={node.id || node.name} className="ov-meter">
                  <span className="ov-meter__name dk-mono">{node.name || node.id}</span>
                  <span className="dk-meter" role="img" aria-label={t('capacity.nodeMeter', { name: node.name || node.id, used: gbLabel(used), total: gbLabel(total) })}>
                    <span className="dk-meter-seg" style={cssVars({ '--dk-w': `${pct.toFixed(1)}%` })} />
                  </span>
                  <span className="ov-meter__fig">{Math.round(pct)}%</span>
                </li>
              )
            })}
          </ul>
        )}
        {diskLine}
      </>
    )
  }
  return (
    <>
      <dl className="ov-stats">
        <Stat label={t('overview.loaded')} value={loaded?.length ?? 0} />
        {memory && <Stat label={t('overview.memory')} value={`${Math.round(memory.pct)}%`} unit={`${gbLabel(memory.used)} / ${gbLabel(memory.total)}`} tone={memory.pct >= 90 ? 'warn' : undefined} />}
      </dl>
      {diskLine}
    </>
  )
}

function TrafficBody({ t, traces }) {
  if (!traces || !traces.total) return <p className="ov-empty">{t('failures.none')}</p>
  const buckets = Array.isArray(traces.buckets) ? traces.buckets : []
  const peak = buckets.reduce((m, b) => Math.max(m, b.count || 0), 0)
  const rate = traces.total > 0 ? (traces.errors / traces.total) * 100 : 0
  return (
    <>
      <dl className="ov-stats" data-testid="operate-traffic">
        <Stat label={t('overview.requests')} value={traces.total.toLocaleString()} />
        <Stat label={t('overview.failed')} value={`${rate.toFixed(rate >= 10 ? 0 : 1)}%`} tone={traces.errors > 0 ? 'warn' : undefined} />
        <Stat label={t('overview.p95')} value={(traces.p95_ms ?? 0).toLocaleString()} unit="ms" />
      </dl>
      {peak > 0 && (
        <div className="ov-bars" role="img" aria-label={t('overview.barsLabel', { hours: traces.window_hours || 24 })}>
          {buckets.map(b => (
            <span key={b.start} className="ov-bars__bar" data-error={b.errors > 0 ? 'true' : undefined} style={cssVars({ '--ov-h': `${Math.max(4, ((b.count || 0) / peak) * 100).toFixed(0)}%` })} />
          ))}
        </div>
      )}
    </>
  )
}
