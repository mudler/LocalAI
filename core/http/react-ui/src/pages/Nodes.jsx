/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import { filterNodes, paginateNodes, runBounded, sortNodes } from '../utils/nodeFleet'
import { DISTRIBUTED_COMMAND, attentionOf, memoryOf, updateTargets } from '../utils/swarm'
import { useNodeList, usePhone, useReplicas } from '../hooks/useSwarm'
import { useOperateSummary } from '../contexts/OperateSummaryContext'
import LoadingSpinner from '../components/LoadingSpinner'
import ConfirmDialog from '../components/ConfirmDialog'
import LocalMachineView from '../components/nodes/LocalMachineView'
import AddNodeFlow from '../components/swarm/AddNodeFlow'
import CommandBlock from '../components/swarm/CommandBlock'
import NodeMap from '../components/swarm/NodeMap'
import NodesTable from '../components/swarm/NodesTable'
import RunningModels from '../components/swarm/RunningModels'
import Icon from '../components/Icon'
import './swarm.css'

const DENSITY_KEY = 'localai-swarm-density'
const STATUSES = ['healthy', 'draining', 'pending', 'unhealthy', 'offline']
const ATTENTION_FILTERS = ['pending', 'down', 'lowVram', 'lowRam', 'lowDisk']

function readDensity() {
  try { return localStorage.getItem(DENSITY_KEY) === 'compact' ? 'compact' : 'comfortable' } catch { return 'comfortable' }
}

// The distributed install, seen as a table of nodes, a map of them, or the
// models running across them. A single install never reaches this page's
// cluster view: it shows the machine instead, with the way to add more.
export default function Nodes() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('swarm')
  const summary = useOperateSummary()
  const { nodes, status: loadStatus, refetch } = useNodeList()
  const replicas = useReplicas()
  const phone = usePhone()

  const [view, setView] = useState('list')
  const [density, setDensity] = useState(readDensity)
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
  const [updating, setUpdating] = useState(false)

  // Phones do not draw the map: a tangle of lines is no use at 390 px.
  const shownView = phone && view === 'map' ? 'list' : view

  const loadReplicas = replicas.load
  useEffect(() => {
    if (shownView === 'map' || shownView === 'models') void loadReplicas()
  }, [shownView, loadReplicas])

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

  const setDensityKept = value => {
    setDensity(value)
    try { localStorage.setItem(DENSITY_KEY, value) } catch { /* the choice just does not persist */ }
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
  const updateBackends = async () => {
    if (updating || targets.pairs.length === 0) return
    setUpdating(true)
    try {
      const results = await runBounded(targets.pairs, 6, pair => nodesApi.upgradeBackend(pair.nodeId, pair.backend))
      const failed = results.filter(r => r.status === 'rejected').length
      addToast(
        failed
          ? t('update.partial', { failed, total: results.length })
          : t('update.started', { count: targets.nodeIds.length }),
        failed ? 'warning' : 'info',
      )
      await summary?.refresh?.()
    } finally {
      setUpdating(false)
    }
  }

  if (loadStatus === 'loading') return <div className="page page--wide loading-center"><LoadingSpinner size="lg" /></div>
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

  return (
    <div className="page page--wide sw-page" data-testid="swarm-nodes">
      <h1 className="dk-sr-only">{t('nodes.title')}</h1>

      {loadStatus === 'error' && (
        <div className="sw-error" role="alert"><Icon name="alert-circle" /><span>{t('nodes.stale')}</span></div>
      )}

      <div className="sw-bar">
        <div className="sw-chips" role="group" aria-label={t('nodes.filter')}>
          <button type="button" className="dk-chip" aria-pressed={!needsOnly} onClick={() => { setNeedsOnly(false); setReason(null) }}>
            {t('nodes.all')} <span className="sw-chip__n">{nodes.length}</span>
          </button>
          <button type="button" className="dk-chip" aria-pressed={needsOnly} onClick={() => setNeedsOnly(true)}>
            {t('nodes.needs')} <span className="sw-chip__n">{attentionCount}</span>
          </button>
        </div>
        <div className="sw-bar__acts">
          {targets.pairs.length > 0 && (
            <button type="button" className="dk-btn dk-btn--secondary" disabled={updating} onClick={updateBackends} data-testid="bulk-update">
              <Icon name={updating ? 'spinner' : 'arrow-up'} spin={updating} /> {updateLabel}
            </button>
          )}
          <div className="dk-segmented" role="radiogroup" aria-label={t('nodes.view')}>
            <button type="button" role="radio" aria-checked={shownView === 'list'} className="dk-seg" onClick={() => setView('list')}><Icon name="list" /> {t('nodes.list')}</button>
            {!phone && <button type="button" role="radio" aria-checked={shownView === 'map'} className="dk-seg" onClick={() => setView('map')}><Icon name="nodes" /> {t('nodes.map')}</button>}
            <button type="button" role="radio" aria-checked={shownView === 'models'} className="dk-seg" onClick={() => setView('models')}><Icon name="layers" /> {t('nodes.running')}</button>
          </div>
          <Link className="dk-btn dk-btn--primary" to="/app/nodes/add"><Icon name="plus" /> {t('nodes.add')}</Link>
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
            <div className="dk-segmented sw-filters__density" role="radiogroup" aria-label={t('nodes.density')}>
              <button type="button" role="radio" aria-checked={density === 'comfortable'} className="dk-seg" onClick={() => setDensityKept('comfortable')}>{t('nodes.comfortable')}</button>
              <button type="button" role="radio" aria-checked={density === 'compact'} className="dk-seg" onClick={() => setDensityKept('compact')}>{t('nodes.compact')}</button>
            </div>
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

          <NodesTable
            nodes={pagination.items}
            reasons={reasons}
            selectedIds={selectedIds}
            onSelectionChange={setSelectedIds}
            sort={sort}
            onSortChange={setSort}
            groupBy={groupBy}
            onApprove={approve}
            compact={density === 'compact'}
            busyId={busyId}
          />
          <div className="sw-pager">
            <span>{t('pager.page', { page: pagination.page, total: pagination.totalPages })}</span>
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={t('pager.previousAria')} disabled={pagination.page === 1} onClick={() => setPage(value => value - 1)}>{t('pager.previous')}</button>
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={t('pager.nextAria')} disabled={pagination.page === pagination.totalPages} onClick={() => setPage(value => value + 1)}>{t('pager.next')}</button>
          </div>
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
        <Link className="dk-btn dk-btn--secondary" to="/app/nodes/add">{t('scale.next')}</Link>
      </div>
    </div>
  )
}
