/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import { filterNodes, paginateNodes, runBounded, sortNodes, summarizeFleet } from '../utils/nodeFleet'
import { DISTRIBUTED_COMMAND, attentionOf, memoryOf, updateTargets } from '../utils/swarm'
import { useNodeList, usePhone, useReplicas } from '../hooks/useSwarm'
import { useOperateSummary } from '../contexts/OperateSummaryContext'
import { useUpdates } from '../contexts/UpdatesContext'
import { entriesFromPairs } from '../utils/nodeUpdates'
import NodeUpdateDialog from '../components/updates/NodeUpdateDialog'
import { Button, Callout, SkeletonRows } from '../components/ui'
import ConfirmDialog from '../components/ConfirmDialog'
import SideSheet from '../components/SideSheet'
import LocalMachineView from '../components/nodes/LocalMachineView'
import AddNodeFlow from '../components/swarm/AddNodeFlow'
import CommandBlock from '../components/swarm/CommandBlock'
import NodeMap from '../components/swarm/NodeMap'
import FleetHero from '../components/swarm/FleetHero'
import NodeInspector from '../components/swarm/NodeInspector'
import NodeList from '../components/swarm/NodeList'
import RunningModels from '../components/swarm/RunningModels'
import Icon from '../components/Icon'
import './swarm.css'
import './swarm-nodes.css'

const STATUSES = ['healthy', 'draining', 'pending', 'unhealthy', 'offline']
const SORTS = [['name', 'table.node'], ['status', 'table.state'], ['_memory', 'table.memory'], ['model_count', 'table.models'], ['_seen', 'table.lastSeen'], ['version', 'table.version']]
const ATTENTION_FILTERS = ['pending', 'down', 'lowVram', 'lowRam', 'lowDisk']

// The distributed install, seen as a roster of nodes, a map of them, or the
// models running across them. A single install never reaches this page's
// cluster view: it shows the machine instead, with the way to add more.
export default function Nodes() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('swarm')
  const summary = useOperateSummary()
  const { nodes, status: loadStatus, refetch } = useNodeList()
  const replicas = useReplicas()
  const phone = usePhone()
  // Below this width the inspector is a sheet that a chosen row opens, not a column.
  const stacked = usePhone('(max-width: 1199px)')

  const [view, setView] = useState('list')
  const [query, setQuery] = useState('')
  const [state, setState] = useState('')
  const [type, setType] = useState('')
  const [groupBy, setGroupBy] = useState('none')
  const [needsOnly, setNeedsOnly] = useState(false)
  const [reason, setReason] = useState(null)
  const [sort, setSort] = useState({ key: 'name', direction: 'asc' })
  const [page, setPage] = useState(1)
  const [selectedIds, setSelectedIds] = useState(() => new Set())
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [bulkRunning, setBulkRunning] = useState(false)
  const bulkRef = useRef(false)
  const [busyId, setBusyId] = useState(null)
  const [activeId, setActiveId] = useState(null)
  const [sheetOpen, setSheetOpen] = useState(false)
  const [confirmNode, setConfirmNode] = useState(null)
  const [oneBusy, setOneBusy] = useState(false)

  // Phones do not draw the map: a tangle of lines is no use at 390 px.
  const shownView = phone && view === 'map' ? 'list' : view

  const loadReplicas = replicas.load
  // The roster names the models each node runs, so it reads the replicas too.
  useEffect(() => { void loadReplicas() }, [loadReplicas])

  // A selection never outlives its node.
  useEffect(() => {
    if (loadStatus !== 'ready') return
    setSelectedIds(current => {
      const live = new Set(nodes.map(node => node.id))
      const kept = [...current].filter(id => live.has(id))
      return kept.length === current.size ? current : new Set(kept)
    })
  }, [nodes, loadStatus])

  const reasons = useMemo(() => attentionOf(nodes), [nodes])
  const fleet = useMemo(() => summarizeFleet(nodes), [nodes])
  const modelsByNode = useMemo(() => {
    if (replicas.state !== 'loaded') return null
    const map = new Map()
    for (const row of replicas.rows) {
      if (row.state && row.state !== 'loaded') continue
      map.set(row.node_id, [...new Set([...(map.get(row.node_id) || []), row.model_name])])
    }
    return map
  }, [replicas.rows, replicas.state])
  const runningCount = useMemo(() => {
    if (replicas.state !== 'loaded') return nodes.reduce((sum, node) => sum + (node.model_count || 0), 0)
    return new Set(replicas.rows.filter(row => !row.state || row.state === 'loaded').map(row => row.model_name)).size
  }, [nodes, replicas.rows, replicas.state])
  const labelKeys = useMemo(() => [...new Set(nodes.flatMap(node => Object.keys(node.labels || {})))].sort(), [nodes])
  const reasonCounts = useMemo(() => Object.fromEntries(ATTENTION_FILTERS.map(key => [key, [...reasons.values()].filter(list => list.includes(key)).length])), [reasons])

  const filtered = useMemo(() => {
    const base = filterNodes(nodes, { query, statuses: state ? [state] : [], types: type ? [type] : [] })
    if (!needsOnly) return base
    return base.filter(node => {
      const why = reasons.get(node.id)
      return why && (!reason || why.includes(reason))
    })
  }, [nodes, query, state, type, needsOnly, reason, reasons])
  const ordered = useMemo(() => {
    const keyed = filtered.map(node => ({ ...node, _memory: memoryOf(node)?.total ?? null, _seen: Date.parse(node.last_heartbeat) || null }))
    return sortNodes(keyed, sort)
  }, [filtered, sort])
  const pagination = useMemo(() => paginateNodes(ordered, page), [ordered, page])

  useEffect(() => { if (pagination.page !== page) setPage(pagination.page) }, [page, pagination.page])
  useEffect(() => { setPage(1) }, [query, state, type, needsOnly, reason, groupBy])

  // The wide layout always shows a node: the one chosen, else the first.
  const inspected = ordered.find(node => node.id === activeId) || (stacked ? null : pagination.items[0] || null)
  const inspect = id => { setActiveId(id); if (stacked) setSheetOpen(true) }

  const runOne = async (node, action, toastKey) => {
    setOneBusy(true)
    try {
      await nodesApi[action](node.id)
      addToast(t(toastKey), 'success')
      setConfirmNode(null)
      setSheetOpen(false)
      await refetch()
    } catch (error) {
      addToast(error.message, 'error')
    } finally {
      setOneBusy(false)
    }
  }

  const approve = async id => {
    setBusyId(id)
    try {
      await nodesApi.approve(id)
      addToast(t('toast.approvedOne'), 'success')
      await refetch()
    } catch (error) {
      addToast(error.message, 'error')
    } finally {
      setBusyId(null)
    }
  }

  const runBulk = action => {
    if (bulkRef.current) return
    bulkRef.current = true
    setBulkRunning(true)
    const ids = [...selectedIds]
    const need = action === 'drain' ? 'healthy' : action === 'resume' ? 'draining' : null
    const status = new Map(nodes.map(node => [node.id, node.status]))
    const eligible = need ? ids.filter(id => status.get(id) === need) : ids
    const skipped = ids.length - eligible.length
    void (async () => {
      try {
        const results = await runBounded(eligible, 8, id => nodesApi[action](id))
        const succeeded = results.filter(result => result.status === 'fulfilled').length
        const failed = results.length - succeeded
        const label = t(`bulk.done.${action}`)
        addToast(t('bulk.result', { label, succeeded, failed, skipped }), failed || skipped ? 'warning' : 'success')
        await refetch()
      } finally {
        setConfirmRemove(false)
        bulkRef.current = false
        setBulkRunning(false)
      }
    })()
  }

  const targets = useMemo(
    () => updateTargets({ upgrades: summary?.upgrades, nodes, selectedIds }),
    [summary?.upgrades, nodes, selectedIds],
  )
  // Every update asks first: a worker stops the running models of a backend
  // before it installs the new one. The dialog names them, then runs the
  // nodes one after another and shows each result.
  const updates = useUpdates()
  const [updatePlan, setUpdatePlan] = useState(null)
  const updateBackends = () => {
    if (targets.pairs.length === 0) return
    setUpdatePlan(entriesFromPairs({
      pairs: targets.pairs,
      items: updates && updates.snapshot ? updates.snapshot.items : [],
      nodes,
      upgrades: summary?.upgrades,
    }))
  }
  if (loadStatus === 'loading') return <div className="page page--wide sw-page"><SkeletonRows /></div>
  if (loadStatus === 'single') {
    return <LocalMachineView addToast={addToast} scaleOut={<ScaleOut addToast={addToast} />} />
  }
  if (loadStatus === 'ready' && nodes.length === 0) {
    return (
      <div className="page page--wide sw-page" data-testid="swarm-first-run">
        <header className="sw-head">
          <h1 className="sw-title">{t('first.title')}</h1>
          <p className="sw-note">{t('first.sub')}</p>
        </header>
        <AddNodeFlow nodes={nodes} single={false} addToast={addToast} onApproved={refetch} />
      </div>
    )
  }

  const attentionCount = reasons.size
  const updateLabel = targets.backends.length === 1
    ? t('update.named', { backend: targets.backends[0], count: targets.nodeIds.length })
    : t('update.many', { count: targets.nodeIds.length })
  const inspector = inspected && (
    <NodeInspector
      node={inspected}
      rows={replicas.rows.filter(row => row.node_id === inspected.id)}
      reasons={reasons.get(inspected.id)}
      busy={oneBusy}
      onApprove={() => runOne(inspected, 'approve', 'toast.approvedOne')}
      onDrain={() => setConfirmNode({ node: inspected, action: 'drain' })}
      onResume={() => runOne(inspected, 'resume', 'toast.resumed')}
      onRemove={() => setConfirmNode({ node: inspected, action: 'delete' })}
    />
  )

  return (
    <div className="page page--wide sw-page" data-testid="swarm-nodes">
      <h1 className="dk-sr-only">{t('nodes.title')}</h1>

      {loadStatus === 'error' && (
        <Callout tone="error" role="alert" title={t('nodes.stale')} actions={<Button size="sm" icon="refresh" onClick={refetch}>{t('nodes.retry')}</Button>} />
      )}

      <FleetHero summary={fleet} running={runningCount} attention={attentionCount} />

      <div className="sw-bar">
        <div className="dk-segmented" role="radiogroup" aria-label={t('nodes.view')}>
          <button type="button" role="radio" aria-checked={shownView === 'list'} className="dk-seg" onClick={() => setView('list')}><Icon name="list" /> {t('nodes.list')} <span className="sw-chip__n">{nodes.length}</span></button>
          {!phone && <button type="button" role="radio" aria-checked={shownView === 'map'} className="dk-seg" onClick={() => setView('map')}><Icon name="nodes" /> {t('nodes.map')}</button>}
          <button type="button" role="radio" aria-checked={shownView === 'models'} className="dk-seg" onClick={() => setView('models')}><Icon name="layers" /> {t('nodes.running')} <span className="sw-chip__n">{runningCount}</span></button>
        </div>
        <div className="sw-bar__acts">
          {targets.pairs.length > 0 && (
            <Button icon="arrow-up" onClick={updateBackends} data-testid="bulk-update">{updateLabel}</Button>
          )}
          <div className="sw-chips" role="group" aria-label={t('nodes.filter')}>
            <button type="button" className="dk-chip" aria-pressed={!needsOnly} onClick={() => { setNeedsOnly(false); setReason(null) }}>
              {t('nodes.all')} <span className="sw-chip__n">{nodes.length}</span>
            </button>
            <button type="button" className="dk-chip" aria-pressed={needsOnly} onClick={() => setNeedsOnly(true)}>
              {t('nodes.needs')} <span className="sw-chip__n">{attentionCount}</span>
            </button>
          </div>
          <Button variant="primary" icon="plus" to="/app/nodes/add">{t('nodes.add')}</Button>
        </div>
      </div>

      {needsOnly && shownView === 'list' && (
        <div className="sw-chips" role="group" aria-label={t('nodes.reasons')}>
          {ATTENTION_FILTERS.filter(key => reasonCounts[key] > 0 || reason === key).map(key => (
            <button key={key} type="button" className="dk-chip dk-chip--sm" aria-pressed={reason === key} onClick={() => setReason(reason === key ? null : key)}>
              {t(`attention.${key}Filter`)} <span className="sw-chip__n">{reasonCounts[key]}</span>
            </button>
          ))}
        </div>
      )}

      {shownView === 'list' && (
        <>
          <div className="sw-filters">
            <div className="dk-input-icon sw-search">
              <Icon name="search" className="dk-icon" />
              <input className="dk-input" type="search" aria-label={t('nodes.search')} placeholder={t('nodes.searchPlaceholder')} value={query} onChange={event => setQuery(event.target.value)} />
            </div>
            <select className="dk-select" aria-label={t('nodes.filterState')} value={state} onChange={event => setState(event.target.value)}>
              <option value="">{t('nodes.anyState')}</option>
              {STATUSES.map(value => <option key={value} value={value}>{t(`state.${value === 'unhealthy' ? 'down' : value}`)}</option>)}
            </select>
            <select className="dk-select" aria-label={t('nodes.filterType')} value={type} onChange={event => setType(event.target.value)}>
              <option value="">{t('nodes.anyType')}</option>
              <option value="backend">backend</option>
              <option value="agent">agent</option>
            </select>
            <select className="dk-select" aria-label={t('nodes.group')} value={groupBy} onChange={event => setGroupBy(event.target.value)}>
              <option value="none">{t('nodes.noGroup')}</option>
              <option value="node_type">{t('nodes.groupType')}</option>
              {labelKeys.map(key => <option key={key} value={`label:${key}`}>{t('nodes.groupLabel', { key })}</option>)}
            </select>
            <select className="dk-select" aria-label={t('nodes.sort')} value={sort.key} onChange={event => setSort({ key: event.target.value, direction: 'asc' })}>
              {SORTS.map(([key, label]) => <option key={key} value={key}>{t('nodes.sortBy', { by: t(label) })}</option>)}
            </select>
            <button
              type="button"
              className="dk-btn dk-btn--secondary dk-btn--icon sw-sortdir"
              aria-label={t('nodes.sortDirection')}
              aria-pressed={sort.direction === 'desc'}
              onClick={() => setSort(current => ({ ...current, direction: current.direction === 'asc' ? 'desc' : 'asc' }))}
            >
              <Icon name="arrow-up" className="sw-sortdir__icon" data-dir={sort.direction} />
            </button>
          </div>

          {selectedIds.size > 0 && (
            <div className="sw-selection" role="group" aria-label={t('bulk.label')}>
              <strong aria-live="polite">{t('bulk.selected', { count: selectedIds.size })}</strong>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={bulkRunning} onClick={() => runBulk('drain')}>{t('bulk.drain')}</button>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={bulkRunning} onClick={() => runBulk('resume')}>{t('bulk.resume')}</button>
              <button type="button" className="dk-btn dk-btn--danger dk-btn--sm" disabled={bulkRunning} onClick={() => setConfirmRemove(true)}>{t('bulk.remove')}</button>
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={bulkRunning} onClick={() => setSelectedIds(new Set())}>{t('bulk.clear')}</button>
              <span className="sw-count" aria-live="polite">{t('bulk.inView', { count: ordered.length })}</span>
            </div>
          )}

          <div className="sw-split">
            <div className="dk-card sw-roster">
              <NodeList
                nodes={pagination.items}
                reasons={reasons}
                selectedIds={selectedIds}
                onSelectionChange={setSelectedIds}
                groupBy={groupBy}
                onApprove={approve}
                busyId={busyId}
                activeId={inspected?.id}
                onInspect={inspect}
                modelsByNode={modelsByNode}
              />
              <div className="sw-pager">
                <span>{t('pager.page', { page: pagination.page, total: pagination.totalPages })}</span>
                <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={t('pager.previousAria')} disabled={pagination.page === 1} onClick={() => setPage(value => value - 1)}>{t('pager.previous')}</button>
                <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={t('pager.nextAria')} disabled={pagination.page === pagination.totalPages} onClick={() => setPage(value => value + 1)}>{t('pager.next')}</button>
              </div>
            </div>
            {!stacked && (
              <aside className="dk-card sw-inspector" aria-label={t('inspector.label')}>
                {inspector || <p className="sw-insp__empty">{t('inspector.empty')}</p>}
              </aside>
            )}
          </div>
          {stacked && sheetOpen && inspector && (
            <SideSheet title={t('inspector.label')} onClose={() => setSheetOpen(false)} closeLabel={t('inspector.close')} testId="node-sheet" labelId="node-sheet-title">
              {inspector}
            </SideSheet>
          )}
        </>
      )}

      {shownView === 'map' && (
        <>
          <NodeMap nodes={needsOnly ? nodes.filter(node => reasons.has(node.id)) : nodes} rows={replicas.state === 'loaded' ? replicas.rows : null} />
          <p className="sw-note sw-note--quiet">{t('map.note')}</p>
        </>
      )}

      {shownView === 'models' && <RunningModels nodes={nodes} replicas={replicas} addToast={addToast} />}

      <ConfirmDialog
        open={confirmRemove}
        title={t('bulk.removeTitle')}
        message={t('bulk.removeMessage', { count: selectedIds.size })}
        confirmLabel={t('bulk.removeConfirm')}
        pendingLabel={t('bulk.removing')}
        pending={bulkRunning}
        danger
        onConfirm={() => runBulk('delete')}
        onCancel={() => setConfirmRemove(false)}
      />
      <ConfirmDialog
        open={Boolean(confirmNode)}
        title={confirmNode?.action === 'drain' ? t('drain.title', { name: confirmNode.node.name }) : t('remove.title', { name: confirmNode?.node.name })}
        message={confirmNode?.action === 'drain' ? `${t('drain.desc')} ${t('drain.undo')}` : t('remove.desc')}
        confirmLabel={confirmNode?.action === 'drain' ? t('drain.confirm') : t('remove.confirm')}
        pendingLabel={confirmNode?.action === 'drain' ? t('drain.pending') : t('remove.pending')}
        pending={oneBusy}
        danger={confirmNode?.action === 'delete'}
        onConfirm={() => runOne(confirmNode.node, confirmNode.action, confirmNode.action === 'drain' ? 'toast.draining' : 'toast.removed')}
        onCancel={() => setConfirmNode(null)}
      />
      {updatePlan && (
        <NodeUpdateDialog
          entries={updatePlan}
          onClose={() => setUpdatePlan(null)}
          onSettled={() => { refetch(); summary?.refresh?.() }}
        />
      )}
    </div>
  )
}

// The way to more than one machine, shown from the single-node view on request:
// distributed mode first, then the worker steps on their own page.
function ScaleOut({ addToast }) {
  const { t } = useTranslation('swarm')
  return (
    <div className="dk-card sw-scale" data-testid="scale-out">
      <h2 className="sw-scale__title">{t('scale.title')}</h2>
      <p className="sw-note">{t('scale.sub')}</p>
      <CommandBlock command={DISTRIBUTED_COMMAND} label={t('scale.title')} addToast={addToast} />
      <p className="sw-note sw-note--quiet">
        {t('add.distributed.docs')}{' '}
        <a className="dk-link" href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer">{t('add.distributed.docsLink')}</a>
      </p>
      <div>
        <Button to="/app/nodes/add">{t('scale.next')}</Button>
      </div>
    </div>
  )
}
