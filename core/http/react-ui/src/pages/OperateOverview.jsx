/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useMemo, useState } from 'react'
import { Link, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendsApi } from '../utils/api'
import { useOperateSummary } from '../contexts/OperateSummaryContext'
import { useOperationActions, isRetryable } from '../hooks/useOperationActions'
import {
  capacityOf, capacityProblems, isFirstRun, ledgerRows, statusHeadline,
} from '../utils/operateStatus'
import { cssVars, gbLabel } from '../utils/modelLedger'
import { localModelRows } from '../utils/localHost'
import LedgerRow from '../components/operate/LedgerRow'
import LocalRunningModels from '../components/nodes/LocalRunningModels'
import CapacityChart from '../components/operate/CapacityChart'
import { useUnloadModel } from '../components/operate/useUnloadModel'
import Icon from '../components/Icon'
import './operate.css'

// The front door to Operate: one sentence, then four rows that say what needs
// you, what the memory is doing, what is running, and what failed. A row with
// a problem arrives open, with the button that deals with it. A row with
// nothing to say stays one line, so a quiet installation is a quiet page.
//
// The numbers come from the Operate summary the hub already polls. Nothing is
// asked for twice, and nothing is drawn that the API did not say.

export default function OperateOverview() {
  const { t } = useTranslation('operate')
  const { addToast } = useOutletContext() || {}
  const summary = useOperateSummary()
  const actions = useOperationActions(addToast)
  const [overrides, setOverrides] = useState({})
  const [pending, setPending] = useState(() => new Set())

  const ready = Boolean(summary?.ready)
  const attention = useMemo(() => summary?.attention || [], [summary?.attention])
  const operations = useMemo(() => summary?.operations || [], [summary?.operations])
  const nodes = useMemo(() => summary?.nodes || [], [summary?.nodes])
  const distributed = summary?.distributed === true
  const traces = summary?.traces
  const loaded = summary?.loadedModels
  const refresh = summary?.refresh

  const memory = useMemo(
    () => capacityOf({ resources: summary?.resources, nodes, distributed }),
    [summary?.resources, nodes, distributed],
  )
  const problems = useMemo(
    () => capacityProblems({ resources: summary?.resources, nodes, distributed }),
    [summary?.resources, nodes, distributed],
  )
  const failedRequests = traces?.errors || 0
  const running = operations.filter(op => !op.error)
  const rows = ledgerRows({
    attention, memoryProblems: problems, requestErrors: failedRequests, running: running.length, loaded: loaded?.length || 0,
  })
  const byId = Object.fromEntries(rows.map(row => [row.id, row]))
  const looking = rows.filter(row => row.id !== 'needs' && row.id !== 'running' && row.open).length
  const firstRun = ready && isFirstRun({
    backends: summary?.installed?.backends,
    models: summary?.installed?.models,
    operations,
    loaded: loaded?.length || 0,
  })
  const headline = statusHeadline({ needs: attention.length, looking, firstRun })

  const isOpen = (id) => overrides[id] ?? byId[id].open
  const toggle = (id) => setOverrides(current => ({ ...current, [id]: !isOpen(id) }))

  const unload = useUnloadModel({ addToast, onDone: refresh })

  const markPending = (key, on) => setPending(current => {
    const next = new Set(current)
    if (on) next.add(key)
    else next.delete(key)
    return next
  })

  const updateBackend = useCallback(async (name) => {
    markPending(`update:${name}`, true)
    try {
      await backendsApi.upgrade(name)
      addToast?.(t('needs.updateStarted', { name }), 'info')
      await refresh?.()
    } catch (err) {
      addToast?.(t('needs.updateFailed', { name, message: err.message }), 'error')
    } finally {
      markPending(`update:${name}`, false)
    }
  }, [addToast, refresh, t])

  const retryOperation = useCallback(async (item, op) => {
    markPending(item.id, true)
    try {
      await actions.retry(op)
      await refresh?.()
    } finally {
      markPending(item.id, false)
    }
  }, [actions, refresh])

  const scope = !ready
    ? null
    : distributed
      ? t('status.scopeCluster', { count: nodes.length })
      : t('status.scopeSingle')

  return (
    <div className="page op-page op-status" data-testid="operate-overview" aria-busy={!ready}>
      <header className="op-status__head">
        {firstRun ? (
          <FirstRun t={t} />
        ) : (
          <>
            <h1 className="op-status__title" data-testid="operate-headline" data-kind={ready ? headline.kind : 'loading'}>
              {ready ? t(`status.headline.${headline.kind}`, { count: headline.count }) : t('status.headline.loading')}
            </h1>
            {ready && headline.kind === 'looking' && (
              <p className="op-status__sub">{t('status.lookingSub')}</p>
            )}
          </>
        )}
        {scope && <p className="op-status__scope">{scope}</p>}
      </header>

      <ol className="op-ledger" aria-label={t('status.ledger')}>
        <LedgerRow
          id="needs"
          loading={!ready}
          level={ready ? byId.needs.level : 'idle'}
          open={ready && isOpen('needs')}
          onToggle={() => toggle('needs')}
          title={t('status.rows.needs')}
          summary={!ready
            ? <span className="dk-skeleton dk-skeleton--line op-skeleton" />
            : attention.length === 0
              ? <span data-testid="operate-attention-clear">{t('needs.clear')}</span>
              : t('needs.summary', { count: attention.length })}
        >
          <ul className="op-items">
            {attention.map(item => {
              const op = item.kind === 'operation-failed' ? operations.find(o => `op:${o.id || o.uid || o.name}` === item.id) : null
              return (
                <li key={item.id} className="op-item" data-testid="operate-attention-item" data-kind={item.kind}>
                  <div className="op-item__text">
                    <strong className="op-item__title">
                      {item.kind === 'backend-update' && t('needs.backendUpdate', { name: item.name, from: item.from, to: item.to })}
                      {item.kind === 'operation-failed' && t('needs.operationFailed', { name: item.name })}
                      {item.kind === 'node-unhealthy' && t('needs.nodeUnhealthy', { name: item.name })}
                    </strong>
                    <span className="op-item__detail">
                      {item.kind === 'backend-update' && t('needs.backendUpdateDetail', { from: item.from, to: item.to })}
                      {item.kind !== 'backend-update' && item.detail}
                    </span>
                  </div>
                  <div className="op-item__acts">
                    {item.kind === 'backend-update' && (
                      <button
                        type="button"
                        className="dk-btn dk-btn--secondary dk-btn--sm"
                        disabled={pending.has(`update:${item.name}`)}
                        onClick={() => updateBackend(item.name)}
                      >
                        {t('needs.update')}
                      </button>
                    )}
                    {item.kind === 'operation-failed' && op && (
                      <>
                        {isRetryable(op) && (
                          <button
                            type="button"
                            className="dk-btn dk-btn--secondary dk-btn--sm"
                            disabled={pending.has(item.id)}
                            onClick={() => retryOperation(item, op)}
                          >
                            {t('activity.retry')}
                          </button>
                        )}
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => actions.dismiss(op.jobID)}>
                          {t('activity.dismiss')}
                        </button>
                      </>
                    )}
                    {item.kind === 'node-unhealthy' && (
                      <Link className="dk-btn dk-btn--secondary dk-btn--sm" to="/app/nodes">{t('needs.openNodes')}</Link>
                    )}
                  </div>
                </li>
              )
            })}
          </ul>
        </LedgerRow>

        <LedgerRow
          id="capacity"
          loading={!ready}
          level={ready ? byId.capacity.level : 'idle'}
          open={ready && isOpen('capacity')}
          onToggle={() => toggle('capacity')}
          title={t('status.rows.capacity')}
          summary={!ready
            ? <span className="dk-skeleton dk-skeleton--line op-skeleton" />
            : <CapacitySummary memory={memory} problems={problems} resources={summary?.resources} distributed={distributed} t={t} />}
        >
          <CapacityBody
            t={t}
            memory={memory}
            problems={problems}
            loaded={loaded}
            distributed={distributed}
            nodes={nodes}
            resources={summary?.resources}
            onUnload={unload.ask}
          />
        </LedgerRow>

        <LedgerRow
          id="running"
          loading={!ready}
          level={ready ? byId.running.level : 'idle'}
          open={ready && isOpen('running')}
          onToggle={() => toggle('running')}
          title={t('status.rows.running')}
          summary={!ready
            ? <span className="dk-skeleton dk-skeleton--line op-skeleton" />
            : runningSummary({ running, loaded, distributed, nodes, t })}
        >
          <RunningBody t={t} running={running} loaded={loaded} distributed={distributed} ready={ready} refresh={refresh} addToast={addToast} />
        </LedgerRow>

        <LedgerRow
          id="failures"
          loading={!ready}
          level={ready ? byId.failures.level : 'idle'}
          open={ready && isOpen('failures')}
          onToggle={() => toggle('failures')}
          title={t('status.rows.failures')}
          summary={!ready
            ? <span className="dk-skeleton dk-skeleton--line op-skeleton" />
            : failuresSummary(traces, t)}
        >
          <FailuresBody t={t} traces={traces} />
        </LedgerRow>
      </ol>

      {ready && !firstRun && (
        memory
          ? (
            <CapacityChart
              memory={memory}
              samples={summary?.samples || []}
              labelKey={memory.kind === 'gpu' ? 'chart.titleGpu' : memory.kind === 'ram' ? 'chart.titleRam' : memory.kind === 'cluster-gpu' ? 'chart.titleClusterGpu' : 'chart.titleClusterRam'}
            />
          )
          : <p className="op-capacity__wait" data-testid="operate-capacity-unavailable">{t('chart.unavailable')}</p>
      )}

      {unload.dialog}
    </div>
  )
}

function FirstRun({ t }) {
  return (
    <div className="op-first" data-testid="operate-first-run">
      <h1 className="op-status__title" data-testid="operate-headline" data-kind="firstRun">{t('status.headline.firstRun')}</h1>
      <p className="op-status__sub">{t('status.firstRunBody')}</p>
      <div className="op-first__acts">
        <Link className="dk-btn dk-btn--primary" to="/app/backends?view=catalog">
          <Icon name="download" /> {t('status.firstRunBackend')}
        </Link>
        <Link className="dk-btn dk-btn--secondary" to="/app/models">{t('status.firstRunModels')}</Link>
      </div>
    </div>
  )
}

function CapacitySummary({ memory, problems, resources, distributed, t }) {
  if (!memory) return <span>{t('capacity.unavailable')}</span>
  const subject = memory.kind === 'gpu' || memory.kind === 'cluster-gpu' ? 'gpu' : 'ram'
  const parts = [t(`capacity.summary.${subject}`, { used: gbLabel(memory.used), total: gbLabel(memory.total), percent: Math.round(memory.pct) })]
  if (distributed && memory.nodes) parts.push(t('capacity.acrossNodes', { count: memory.nodes }))
  const disk = problems.find(p => p.key === 'disk')
  if (disk) parts.push(t('capacity.diskLow', { free: gbLabel(disk.disk.free) }))
  else if (!distributed && resources?.disk?.total > 0) {
    parts.push(t('capacity.diskFree', { free: gbLabel(resources.disk.available), total: gbLabel(resources.disk.total) }))
  }
  return <span>{parts.join(' · ')}</span>
}

function CapacityBody({ t, memory, problems, loaded, distributed, nodes, resources, onUnload }) {
  const memoryProblem = problems.find(p => p.key === 'memory')
  const diskProblem = problems.find(p => p.key === 'disk')
  if (!memory && !diskProblem) {
    return <p className="op-note">{t('capacity.unavailableBody')}</p>
  }
  return (
    <div className="op-capbody">
      {memoryProblem && (
        <p className="op-note">
          {t('capacity.memoryFull', { percent: Math.round(memoryProblem.pct) })}{' '}
          {!distributed && (loaded?.length > 0 ? t('capacity.unloadHint') : t('capacity.nothingLoaded'))}
        </p>
      )}
      {!distributed && loaded?.length > 0 && (
        <ul className="op-items" aria-label={t('capacity.loadedModels')}>
          {[...loaded].sort((a, b) => a.id.localeCompare(b.id)).map(model => (
            <li key={model.id} className="op-item op-item--compact" data-testid="operate-loaded-model">
              <div className="op-item__text">
                <strong className="op-item__title dk-mono">{model.id}</strong>
                {model.backend && <span className="op-item__detail">{model.backend}</span>}
              </div>
              <div className="op-item__acts">
                <button
                  type="button"
                  className="dk-btn dk-btn--secondary dk-btn--sm"
                  aria-label={t('capacity.unloadLabel', { name: model.id })}
                  onClick={(event) => onUnload(model, event.currentTarget)}
                >
                  {t('capacity.unload')}
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {distributed && nodes.length > 0 && (
        <ul className="op-nodes" aria-label={t('capacity.nodes')}>
          {nodes.filter(n => n.total_vram > 0 || n.total_ram > 0).map(node => {
            const total = node.total_vram > 0 ? node.total_vram : node.total_ram
            const free = node.total_vram > 0 ? node.available_vram : node.available_ram
            const used = Math.max(0, total - (free || 0))
            return (
              <li key={node.id || node.name} className="op-nodes__row">
                <span className="op-nodes__name">{node.name || node.id}</span>
                <span
                  className="dk-meter op-nodes__meter"
                  role="img"
                  aria-label={t('capacity.nodeMeter', { name: node.name || node.id, used: gbLabel(used), total: gbLabel(total) })}
                >
                  <span className="dk-meter-seg" style={cssVars({ '--dk-w': `${Math.min(100, (used / total) * 100).toFixed(1)}%` })} />
                </span>
                <span className="op-nodes__fig dk-mono">{gbLabel(used)} / {gbLabel(total)}</span>
              </li>
            )
          })}
        </ul>
      )}
      {diskProblem && (
        <p className="op-note">
          {t('capacity.diskBody', { free: gbLabel(diskProblem.disk.free), total: gbLabel(diskProblem.disk.total) })}{' '}
          <Link className="dk-link" to="/app/models?view=installed">{t('capacity.reviewModels')}</Link>
        </p>
      )}
      <p className="op-note op-note--quiet">
        <Link className="dk-link" to="/app/nodes">{distributed ? t('capacity.openNodes') : t('capacity.openMachine')}</Link>
        {resources?.reclaimer_enabled && <> · {t('capacity.reclaimer')}</>}
      </p>
    </div>
  )
}

function runningSummary({ running, loaded, distributed, nodes, t }) {
  const parts = []
  if (running.length > 0) parts.push(t('running.operations', { count: running.length }))
  if (!distributed && loaded?.length > 0) parts.push(t('running.models', { count: loaded.length }))
  if (distributed && nodes.length > 0) {
    // A node that is not answering is already named in Needs you; here it only
    // changes the count.
    const down = nodes.filter(n => n?.healthy === false || ['unhealthy', 'down', 'offline', 'error'].includes(String(n?.status || '').toLowerCase())).length
    parts.push(t('running.nodes', { healthy: nodes.length - down, total: nodes.length }))
  }
  if (parts.length === 0) return t('running.none')
  return parts.join(' · ')
}

function RunningBody({ t, running, loaded, distributed, ready, refresh, addToast }) {
  // The heaviest models on this host, from the reading the summary already
  // took. The same table, menu and stop dialog as the This machine page.
  const machine = useMemo(() => ({
    rows: localModelRows({ loaded_models: loaded || [] }),
    state: loaded == null ? (ready ? 'error' : 'loading') : 'loaded',
    error: t('machine.loadFailedBody'),
    refresh,
  }), [loaded, ready, refresh, t])
  return (
    <div className="op-capbody">
      {running.length > 0 && (
        <ul className="op-items" aria-label={t('running.operationsLabel')}>
          {running.map(op => (
            <li key={op.jobID || op.id} className="op-item">
              <div className="op-item__text">
                <strong className="op-item__title dk-mono">{op.name || op.id}</strong>
                <span className="op-item__detail">
                  {op.isQueued ? t('running.queued') : op.isDeletion ? t('running.removing') : t('running.installing')}
                  {op.progress > 0 && ` · ${Math.round(op.progress)}%`}
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
      {!distributed && (
        <LocalRunningModels machine={machine} addToast={addToast} limit={5} moreHref="/app/nodes" />
      )}
      {distributed && <p className="op-note">{t('running.clusterNote')}</p>}
      <p className="op-note op-note--quiet">
        <Link className="dk-link" to="/app/activity">{t('running.openActivity')}</Link>
        {distributed && (
          <>
            {' · '}
            <Link className="dk-link" to="/app/nodes">{t('running.openCluster')}</Link>
          </>
        )}
      </p>
    </div>
  )
}

function failuresSummary(traces, t) {
  if (!traces || !traces.total) return t('failures.none')
  if (!traces.errors) return t('failures.clean', { total: traces.total.toLocaleString(), hours: traces.window_hours || 24, p95: traces.p95_ms?.toLocaleString() ?? '—' })
  return t('failures.some', { errors: traces.errors.toLocaleString(), total: traces.total.toLocaleString(), hours: traces.window_hours || 24 })
}

function FailuresBody({ t, traces }) {
  const hours = traces?.window_hours || 24
  return (
    <div className="op-capbody">
      {traces?.total > 0 ? (
        <dl className="dk-kv op-facts" data-testid="operate-traffic">
          <dt>{t('failures.requests', { hours })}</dt>
          <dd>{traces.total.toLocaleString()}</dd>
          <dt>{t('failures.failed')}</dt>
          <dd>{(traces.errors || 0).toLocaleString()}</dd>
          <dt>{t('failures.p95')}</dt>
          <dd>{`${(traces.p95_ms ?? 0).toLocaleString()} ms`}</dd>
        </dl>
      ) : (
        <p className="op-note">{t('failures.noneBody')}</p>
      )}
      <p className="op-note op-note--quiet">
        <Link className="dk-link" to="/app/traces">{t('failures.openTraces')}</Link>
        {' · '}
        <Link className="dk-link" to="/app/usage">{t('failures.openUsage')}</Link>
      </p>
    </div>
  )
}
