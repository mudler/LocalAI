/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import { DRAINABLE_STATUSES, paginateNodes, runBounded, sortNodes } from '../utils/nodeFleet'
import { DISTRIBUTED_COMMAND, attentionOf, memoryOf, updateTargets, updateTargetsForNode } from '../utils/swarm'
import { chipCounts, isSplit, matchesChip, matchesQuery, rosterSummary, splitAsRow } from '../utils/workerRoster'
import { useNodeList, usePhone, useReplicas } from '../hooks/useSwarm'
import { useClusterSettings } from '../hooks/useClusterSettings'
import { useSplitManagement } from '../hooks/useSplitManagement'
import { useOperateSummary } from '../contexts/OperateSummaryContext'
import { useUpdates } from '../contexts/UpdatesContext'
import { entriesFromPairs } from '../utils/nodeUpdates'
import NodeUpdateDialog from '../components/updates/NodeUpdateDialog'
import { Button, Callout, EmptyState, Segmented, SkeletonRows } from '../components/ui'
import ConfirmDialog from '../components/ConfirmDialog'
import SideSheet from '../components/SideSheet'
import AdminPage from '../components/admin/AdminPage'
import AdminFilterBar from '../components/admin/AdminFilterBar'
import LocalMachineView from '../components/nodes/LocalMachineView'
import ClusterSheet from '../components/cluster/ClusterSheet'
import CommandBlock from '../components/swarm/CommandBlock'
import ConnectivityPill from '../components/swarm/ConnectivityPill'
import FleetStrip from '../components/swarm/FleetStrip'
import NodeMap from '../components/swarm/NodeMap'
import RunningModels from '../components/swarm/RunningModels'
import WorkerInspector, { SplitInspector } from '../components/swarm/WorkerInspector'
import WorkerRoster from '../components/swarm/WorkerRoster'
import SplitWorkers from '../components/swarm/SplitWorkers'
import { SplitCallout, SplitDialogs } from '../components/swarm/SplitParts'
import Icon from '../components/Icon'
import './swarm.css'
import './swarm-nodes.css'

const CHIPS = ['all', 'healthy', 'attention', 'offline', 'pending']
const SORTS = [['name', 'table.node'], ['status', 'table.state'], ['_memory', 'table.memory'], ['model_count', 'table.models'], ['_seen', 'table.lastSeen'], ['version', 'table.version']]
const ATTENTION_FILTERS = ['pending', 'down', 'lowVram', 'lowRam', 'lowDisk']
const TYPES = ['backend', 'agent', 'model-split']

// The workers of the cluster as one roster: every machine, whatever way it
// connects, with the connections it holds and their state, and the inspector of
// the chosen one beside it. A header with the connectivity of the frontends and
// two buttons: the cluster settings (a side sheet) and Add worker. The roster
// is also drawn as a map of the workers, or as the models running across them.
// A single install never reaches the cluster view: it shows the machine
// instead, with the way to add more.
export default function Nodes() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('swarm')
  const { t: tc } = useTranslation('cluster')
  const summary = useOperateSummary()
  const { nodes, status: loadStatus, refetch } = useNodeList()
  const replicas = useReplicas()
  const cluster = useClusterSettings({ enabled: loadStatus === 'ready' })
  const m = useSplitManagement({ addToast, nodes: loadStatus === 'ready' ? nodes : undefined, enabled: loadStatus === 'ready' })
  const phone = usePhone()
  // Below this width the inspector is a sheet that a chosen row opens, not a column.
  const stacked = usePhone('(max-width: 1199px)')

  const [view, setView] = useState('list')
  const [query, setQuery] = useState('')
  const [chip, setChip] = useState('all')
  const [type, setType] = useState('')
  const [groupBy, setGroupBy] = useState('none')
  const [reason, setReason] = useState('')
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
  const [clusterOpen, setClusterOpen] = useState(false)

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
  const nodeNames = useMemo(() => new Map(nodes.map(node => [node.id, node.name])), [nodes])
  // Every machine of the cluster: the nodes and the model-split workers.
  const allRows = useMemo(() => [...nodes, ...m.split.workers.map(worker => splitAsRow(worker, nodeNames))], [nodes, m.split.workers, nodeNames])
  const modelsByNode = useMemo(() => {
    if (replicas.state !== 'loaded') return null
    const map = new Map()
    for (const row of replicas.rows) {
      if (row.state && row.state !== 'loaded') continue
      map.set(row.node_id, [...new Set([...(map.get(row.node_id) || []), row.model_name])])
    }
    return map
  }, [replicas.rows, replicas.state])
  const strip = useMemo(() => rosterSummary(allRows, reasons, modelsByNode), [allRows, reasons, modelsByNode])
  const runningCount = useMemo(() => {
    if (replicas.state !== 'loaded') return nodes.reduce((sum, node) => sum + (node.model_count || 0), 0)
    return new Set(replicas.rows.filter(row => !row.state || row.state === 'loaded').map(row => row.model_name)).size
  }, [nodes, replicas.rows, replicas.state])
  const labelKeys = useMemo(() => [...new Set(nodes.flatMap(node => Object.keys(node.labels || {})))].sort(), [nodes])
  const reasonCounts = useMemo(() => Object.fromEntries(ATTENTION_FILTERS.map(key => [key, [...reasons.values()].filter(list => list.includes(key)).length])), [reasons])
  const counts = useMemo(() => chipCounts(allRows, reasons), [allRows, reasons])

  const filtered = useMemo(() => allRows.filter(row => (
    matchesChip(row, chip, reasons)
    && matchesQuery(row, query, modelsByNode?.get(row.id))
    && (!type || (row.node_type || 'backend') === type)
    && (!reason || reasons.get(row.id)?.includes(reason))
  )), [allRows, chip, query, type, reason, reasons, modelsByNode])
  const ordered = useMemo(() => {
    const keyed = filtered.map(row => ({ ...row, _memory: isSplit(row) ? row.total_ram : memoryOf(row)?.total ?? null, _seen: Date.parse(row.last_heartbeat) || null }))
    return sortNodes(keyed, sort)
  }, [filtered, sort])
  const pagination = useMemo(() => paginateNodes(ordered, page), [ordered, page])

  useEffect(() => { if (pagination.page !== page) setPage(pagination.page) }, [page, pagination.page])
  useEffect(() => { setPage(1) }, [query, chip, type, reason, groupBy])

  // The wide layout always shows a worker: the one chosen, else the first.
  const inspected = ordered.find(row => row.id === activeId) || (stacked ? null : pagination.items[0] || null)
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
    const need = action === 'drain' ? DRAINABLE_STATUSES : action === 'resume' ? ['draining'] : null
    const status = new Map(nodes.map(node => [node.id, node.status]))
    const eligible = need ? ids.filter(id => need.includes(status.get(id))) : ids
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
  const planFor = pairs => setUpdatePlan(entriesFromPairs({
    pairs,
    items: updates && updates.snapshot ? updates.snapshot.items : [],
    nodes,
    upgrades: summary?.upgrades,
  }))
  const updateBackends = () => { if (targets.pairs.length > 0) planFor(targets.pairs) }
  const inspectedNodeTargets = useMemo(
    () => (inspected && !isSplit(inspected) ? updateTargetsForNode({ upgrades: summary?.upgrades, nodes, nodeId: inspected.id }) : null),
    [inspected, summary?.upgrades, nodes],
  )

  const cta = (
    <>
      <Button icon="settings" onClick={() => setClusterOpen(true)} data-testid="cluster-settings-open">{tc('sheet.title')}</Button>
      <Button variant="primary" icon="plus" to="/app/nodes/add" data-testid="add-worker">{tc('page.add')}</Button>
    </>
  )
  const clusterSheet = clusterOpen ? <ClusterSheet onClose={() => setClusterOpen(false)} /> : null

  if (loadStatus === 'loading') return <div className="page page--wide sw-page"><SkeletonRows /></div>
  if (loadStatus === 'single') {
    return <LocalMachineView addToast={addToast} scaleOut={<ScaleOut addToast={addToast} />} extra={<SplitWorkers addToast={addToast} />} />
  }
  if (loadStatus === 'ready' && allRows.length === 0) {
    return (
      <AdminPage
        title={t('nodes.title')} width="full" action={cta} sheet={clusterSheet} data-testid="swarm-first-run"
        supporting={<ConnectivitySupporting text={tc('page.supportingEmpty')} settings={cluster.data} />}
      >
        <EmptyState
          title={t('first.title')} body={t('first.sub')} icon="nodes"
          actions={<Button variant="primary" icon="plus" to="/app/nodes/add">{tc('page.add')}</Button>}
        />
      </AdminPage>
    )
  }

  const inspector = inspected && (isSplit(inspected)
    ? <SplitInspector worker={inspected._split} m={m} />
    : (
      <WorkerInspector
        node={inspected}
        nodes={nodes}
        onChanged={refetch}
        addToast={addToast}
        rows={replicas.rows.filter(row => row.node_id === inspected.id)}
        reasons={reasons.get(inspected.id)}
        weights={cluster.data}
        busy={oneBusy}
        updates={inspectedNodeTargets?.pairs.length || 0}
        onUpdate={() => planFor(inspectedNodeTargets.pairs)}
        onApprove={() => runOne(inspected, 'approve', 'toast.approvedOne')}
        onDrain={() => setConfirmNode({ node: inspected, action: 'drain' })}
        onResume={() => runOne(inspected, 'resume', 'toast.resumed')}
        onRemove={() => setConfirmNode({ node: inspected, action: 'delete' })}
      />
    ))

  const updateLabel = targets.backends.length === 1
    ? t('update.named', { backend: targets.backends[0], count: targets.nodeIds.length })
    : t('update.many', { count: targets.nodeIds.length })
  const filterCount = [type, groupBy !== 'none', reason].filter(Boolean).length
  const select = (label, value, onChange, children) => (
    <label className="wk-filter">
      <span>{label}</span>
      <span className="dk-select-wrap"><select className="dk-select" value={value} onChange={event => onChange(event.target.value)}>{children}</select></span>
    </label>
  )

  return (
    <AdminPage
      title={t('nodes.title')}
      width="full"
      className="wk-page"
      data-testid="swarm-nodes"
      action={cta}
      sheet={clusterSheet}
      supporting={<ConnectivitySupporting text={tc('page.supporting', { online: strip.online, total: strip.total })} settings={cluster.data} />}
      summary={<FleetStrip summary={strip} />}
    >
      {loadStatus === 'error' && (
        <Callout tone="error" role="alert" title={t('nodes.stale')} actions={<Button size="sm" icon="refresh" onClick={refetch}>{t('nodes.retry')}</Button>} />
      )}
      <SplitCallout m={m} />

      <AdminFilterBar
        search={{ value: query, onChange: setQuery, label: tc('page.search'), placeholder: tc('page.searchPlaceholder') }}
        chips={{
          label: t('nodes.filter'), value: chip, onChange: setChip,
          options: CHIPS.map(id => ({ id, label: tc(`page.chip.${id}`), count: counts[id] })),
        }}
        filters={{
          count: filterCount,
          children: (
            <>
              {select(t('nodes.filterType'), type, setType, (
                <>
                  <option value="">{t('nodes.anyType')}</option>
                  {TYPES.map(value => <option key={value} value={value}>{tc(`page.type.${value}`)}</option>)}
                </>
              ))}
              {select(t('nodes.group'), groupBy, setGroupBy, (
                <>
                  <option value="none">{t('nodes.noGroup')}</option>
                  <option value="node_type">{t('nodes.groupType')}</option>
                  {labelKeys.map(key => <option key={key} value={`label:${key}`}>{t('nodes.groupLabel', { key })}</option>)}
                </>
              ))}
              {select(tc('page.reason'), reason, setReason, (
                <>
                  <option value="">{tc('page.anyReason')}</option>
                  {ATTENTION_FILTERS.filter(key => reasonCounts[key] > 0 || reason === key).map(key => <option key={key} value={key}>{t(`attention.${key}Filter`)} ({reasonCounts[key]})</option>)}
                </>
              ))}
            </>
          ),
        }}
        sort={{
          value: sort.key, onChange: key => setSort({ key, direction: 'asc' }),
          options: SORTS.map(([key, label]) => ({ id: key, label: t(label) })),
        }}
        extra={(
          <>
            <Button
              size="sm" icon="arrow-up" aria-label={t('nodes.sortDirection')} aria-pressed={sort.direction === 'desc'}
              className="wk-sortdir" data-dir={sort.direction}
              onClick={() => setSort(current => ({ ...current, direction: current.direction === 'asc' ? 'desc' : 'asc' }))}
            />
            {targets.pairs.length > 0 && <Button size="sm" icon="arrow-up" onClick={updateBackends} data-testid="bulk-update">{updateLabel}</Button>}
            <Segmented
              mode="radio" label={t('nodes.view')} value={shownView} onChange={setView}
              options={[
                { id: 'list', label: t('nodes.list') },
                ...(phone ? [] : [{ id: 'map', label: t('nodes.map') }]),
                { id: 'models', label: t('nodes.running'), count: runningCount },
              ]}
            />
          </>
        )}
      />

      {shownView === 'list' && (
        <>
          {selectedIds.size > 0 && (
            <div className="sw-selection" role="group" aria-label={t('bulk.label')}>
              <strong aria-live="polite">{t('bulk.selected', { count: selectedIds.size })}</strong>
              <Button size="sm" disabled={bulkRunning} onClick={() => runBulk('drain')}>{t('bulk.drain')}</Button>
              <Button size="sm" disabled={bulkRunning} onClick={() => runBulk('resume')}>{t('bulk.resume')}</Button>
              <Button size="sm" variant="danger" disabled={bulkRunning} onClick={() => setConfirmRemove(true)}>{t('bulk.remove')}</Button>
              <Button size="sm" variant="ghost" disabled={bulkRunning} onClick={() => setSelectedIds(new Set())}>{t('bulk.clear')}</Button>
              <span className="sw-count" aria-live="polite">{t('bulk.inView', { count: ordered.length })}</span>
            </div>
          )}

          <div className="sw-split">
            <div className="dk-card sw-roster">
              <WorkerRoster
                rows={pagination.items}
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
                <Button size="sm" aria-label={t('pager.previousAria')} disabled={pagination.page === 1} onClick={() => setPage(value => value - 1)}>{t('pager.previous')}</Button>
                <Button size="sm" aria-label={t('pager.nextAria')} disabled={pagination.page === pagination.totalPages} onClick={() => setPage(value => value + 1)}>{t('pager.next')}</Button>
              </div>
            </div>
            {!stacked && (
              <aside className="dk-card sw-inspector" aria-label={t('inspector.label')}>
                {inspector || <p className="wk-insp__empty">{t('inspector.empty')}</p>}
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
          <NodeMap nodes={chip === 'attention' ? nodes.filter(node => reasons.has(node.id)) : nodes} rows={replicas.state === 'loaded' ? replicas.rows : null} />
          <p className="sw-note sw-note--quiet">{t('map.note')}</p>
        </>
      )}

      {shownView === 'models' && <RunningModels nodes={nodes} replicas={replicas} addToast={addToast} />}

      <SplitDialogs m={m} addToast={addToast} />
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
    </AdminPage>
  )
}

// The sentence under the title, with the connectivity of the frontends beside it.
function ConnectivitySupporting({ text, settings }) {
  return (
    <span className="wk-supporting">
      <span>{text}</span>
      <ConnectivityPill settings={settings} />
    </span>
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
