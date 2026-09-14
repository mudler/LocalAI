import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import { filterNodes, paginateNodes, runBounded, sortNodes, summarizeFleet } from '../utils/nodeFleet'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import ConfirmDialog from '../components/ConfirmDialog'
import ClusterOverview from '../components/nodes/ClusterOverview'
import NodeFleetTable from '../components/nodes/NodeFleetTable'
import NodeInspector from '../components/nodes/NodeInspector'
import ImageSelector, { dockerFlags, dockerImage, useImageSelector } from '../components/ImageSelector'

function CommandBlock({ command, addToast }) {
  const copy = () => {
    navigator.clipboard.writeText(command)
    addToast('Copied to clipboard', 'success', 2000)
  }
  return <div className="p2p-cmd"><pre>{command}</pre><button onClick={copy} className="btn btn-sm p2p-cmd__copy" title="Copy"><i className="fas fa-copy" /></button></div>
}

function WorkerHintCard({ addToast, nodeType = 'backend', hasWorkers }) {
  const frontendUrl = window.location.origin
  const { selected, setSelected, option, dev, setDev } = useImageSelector('cpu')
  const isAgent = nodeType === 'agent'
  const workerCmd = isAgent ? 'agent-worker' : 'worker'
  const flags = dockerFlags(option)
  const flagsString = flags ? `${flags} \
  ` : ''
  return (
    <div className="card pad-lg mb-xl">
      <h3 className="panel-title"><i className={`fas ${hasWorkers ? 'fa-plus-circle' : 'fa-info-circle'} text-primary`} />{hasWorkers ? 'Register another worker' : 'No workers registered yet'}</h3>
      <p className="text-base text-secondary mb-md">Start a worker to add compute capacity. It will register with this frontend and appear here automatically.</p>
      <p className="form-label">Select your hardware</p>
      <ImageSelector selected={selected} onSelect={setSelected} dev={dev} onDevChange={setDev} />
      <div className="stack">
        <div><p className="form-label">CLI</p><CommandBlock command={`local-ai ${workerCmd} \
  --register-to "${frontendUrl}" \
  --nats-url "nats://nats:4222" \
  --registration-token "$LOCALAI_REGISTRATION_TOKEN"`} addToast={addToast} /></div>
        <div><p className="form-label">Docker</p><CommandBlock command={`docker run --net host ${flagsString}\
  -e LOCALAI_REGISTER_TO="${frontendUrl}" \
  -e LOCALAI_NATS_URL="nats://nats:4222" \
  -e LOCALAI_REGISTRATION_TOKEN="$TOKEN" \
  ${dockerImage(option, dev)} ${workerCmd}`} addToast={addToast} /></div>
      </div>
    </div>
  )
}

function DisabledState({ addToast }) {
  return (
    <div className="page page--wide">
      <div className="p2p-hero"><i className="fas fa-network-wired" /><h1>Distributed Mode Not Enabled</h1><p>Enable distributed mode to manage backend nodes across multiple machines and route inference across the fleet.</p></div>
      <div className="card p2p-enable pad-lg">
        <h3 className="panel-title"><i className="fas fa-rocket text-accent" />How to Enable Distributed Mode</h3>
        <p className="form-label">Start LocalAI with distributed mode</p>
        <CommandBlock command={'local-ai run --distributed \\\n  --distributed-db "postgres://user:pass@host/db" \\\n  --distributed-nats "nats://host:4222"'} addToast={addToast} />
        <p className="text-note mt-md">Then register a worker and refresh this page. See the <a href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer" className="text-primary">Distributed Mode documentation</a> for production setup.</p>
      </div>
    </div>
  )
}

export default function Nodes() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const [nodes, setNodes] = useState([])
  const [loading, setLoading] = useState(true)
  const [enabled, setEnabled] = useState(true)
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState('')
  const [type, setType] = useState('')
  const [groupBy, setGroupBy] = useState('none')
  const [sort, setSort] = useState({ key: 'name', direction: 'asc' })
  const [page, setPage] = useState(1)
  const [selectedIds, setSelectedIds] = useState(new Set())
  const [activeAttention, setActiveAttention] = useState(null)
  const [inspectedId, setInspectedId] = useState(null)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [bulkRunning, setBulkRunning] = useState(false)
  const bulkRunningRef = useRef(false)
  const [showTips, setShowTips] = useState(false)
  const [emptyNodeType, setEmptyNodeType] = useState('backend')

  const fetchNodes = useCallback(async () => {
    try {
      const data = await nodesApi.list()
      const nextNodes = Array.isArray(data) ? data : []
      setNodes(nextNodes)
      setSelectedIds(current => {
        const existing = new Set(nextNodes.map(node => node.id))
        return new Set([...current].filter(id => existing.has(id)))
      })
      setEnabled(true)
    } catch (error) {
      if (error.message?.includes('503') || error.message?.includes('Service Unavailable')) setEnabled(false)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchNodes()
    const interval = setInterval(fetchNodes, 5000)
    return () => clearInterval(interval)
  }, [fetchNodes])

  const summary = useMemo(() => summarizeFleet(nodes), [nodes])
  const labelKeys = useMemo(() => [...new Set(nodes.flatMap(node => Object.keys(node.labels || {})))].sort(), [nodes])
  const attentionIds = useMemo(() => {
    if (!activeAttention) return null
    const values = activeAttention === 'all' ? Object.values(summary.attention).flat() : summary.attention[activeAttention]
    return new Set(values)
  }, [activeAttention, summary])
  const filtered = useMemo(() => {
    const result = filterNodes(nodes, { query, statuses: status ? [status] : [], types: type ? [type] : [] })
    return attentionIds ? result.filter(node => attentionIds.has(node.id)) : result
  }, [nodes, query, status, type, attentionIds])
  const ordered = useMemo(() => sortNodes(filtered, sort), [filtered, sort])
  const pagination = useMemo(() => paginateNodes(ordered, page), [ordered, page])
  const inspectedNode = nodes.find(node => node.id === inspectedId) || null

  useEffect(() => { if (pagination.page !== page) setPage(pagination.page) }, [page, pagination.page])
  useEffect(() => { setPage(1) }, [query, status, type, activeAttention, groupBy])

  const actOnNode = async (action, nodeId, successMessage) => {
    try {
      await nodesApi[action](nodeId)
      addToast(successMessage, 'success')
      await fetchNodes()
    } catch (error) {
      addToast(error.message, 'error')
    }
  }

  const runBulk = (action) => {
    if (bulkRunningRef.current) return
    bulkRunningRef.current = true
    setBulkRunning(true)

    const ids = [...selectedIds]
    const requiredStatus = action === 'drain' ? 'healthy' : action === 'resume' ? 'draining' : null
    const statusById = new Map(nodes.map(node => [node.id, node.status]))
    const eligibleIds = requiredStatus ? ids.filter(id => statusById.get(id) === requiredStatus) : ids
    const skipped = ids.length - eligibleIds.length

    void (async () => {
      try {
        const results = await runBounded(eligibleIds, 8, id => nodesApi[action](id))
        const succeeded = results.filter(result => result.status === 'fulfilled').length
        const failed = results.length - succeeded
        const label = action === 'delete' ? 'Remove' : action[0].toUpperCase() + action.slice(1)
        addToast(`${label} complete: ${succeeded} succeeded, ${failed} failed, ${skipped} skipped`, failed || skipped ? 'warning' : 'success')
        await fetchNodes()
      } finally {
        setConfirmRemove(false)
        bulkRunningRef.current = false
        setBulkRunning(false)
      }
    })()
  }

  if (loading) return <div className="page page--wide loading-center"><LoadingSpinner size="lg" /></div>
  if (!enabled) return <DisabledState addToast={addToast} />
  if (nodes.length === 0) return (
    <div className="page page--wide">
      <PageHeader title={t('nodes.title')} supporting={t('nodes.subtitle')} />
      <div role="radiogroup" aria-label="Worker type" className="segmented node-filter">
        {[['backend', 'Backend'], ['agent', 'Agent']].map(([value, label]) => <button key={value} type="button" role="radio" aria-checked={emptyNodeType === value} className={`segmented__item${emptyNodeType === value ? ' is-active' : ''}`} onClick={() => setEmptyNodeType(value)}>{label}</button>)}
      </div>
      <WorkerHintCard addToast={addToast} nodeType={emptyNodeType} />
    </div>
  )

  return (
    <div className={`page page--wide nodes-fleet-page${inspectedNode ? ' nodes-fleet-page--inspecting' : ''}`}>
      <div className="nodes-fleet-page__main">
        <PageHeader title={<><i className="fas fa-network-wired icon-before" />{t('nodes.title')}</>} supporting={t('nodes.subtitle')} actions={<button type="button" className="btn btn-secondary btn-sm" onClick={() => setShowTips(value => !value)}>{showTips ? 'Hide setup' : 'Register worker'}</button>} />
        {showTips && <WorkerHintCard addToast={addToast} hasWorkers />}
        <ClusterOverview summary={summary} activeAttention={activeAttention} onAttentionSelect={setActiveAttention} />

        <section className="fleet-roster" aria-label="Fleet roster">
          <div className="fleet-toolbar">
            <input className="input fleet-toolbar__search" type="search" aria-label="Search nodes" placeholder="Search name, address, label…" value={query} onChange={event => setQuery(event.target.value)} />
            <select className="select" aria-label="Filter status" value={status} onChange={event => setStatus(event.target.value)}><option value="">All statuses</option>{['healthy', 'draining', 'pending', 'unhealthy', 'offline'].map(value => <option key={value} value={value}>{value}</option>)}</select>
            <select className="select" aria-label="Filter type" value={type} onChange={event => setType(event.target.value)}><option value="">All types</option><option value="backend">backend</option><option value="agent">agent</option></select>
            <select className="select" aria-label="Group nodes" value={groupBy} onChange={event => setGroupBy(event.target.value)}><option value="none">None</option><option value="node_type">Type</option>{labelKeys.map(key => <option key={key} value={`label:${key}`}>Label: {key}</option>)}</select>
          </div>
          <div className="fleet-bulkbar">
            <strong>{selectedIds.size} selected</strong>
            <button type="button" className="btn btn-secondary btn-sm" disabled={!selectedIds.size || bulkRunning} onClick={() => runBulk('drain')}>Drain selected</button>
            <button type="button" className="btn btn-secondary btn-sm" disabled={!selectedIds.size || bulkRunning} onClick={() => runBulk('resume')}>Resume selected</button>
            <button type="button" className="btn btn-danger btn-sm" disabled={!selectedIds.size || bulkRunning} onClick={() => setConfirmRemove(true)}>Remove selected</button>
            {activeAttention && <button type="button" className="fleet-bulkbar__clear" onClick={() => setActiveAttention(null)}>Clear attention filter</button>}
            <span className="fleet-bulkbar__count" aria-live="polite">{ordered.length} nodes in view</span>
          </div>
          <NodeFleetTable nodes={pagination.items} selectedIds={selectedIds} onSelectionChange={setSelectedIds} onInspect={node => setInspectedId(node.id)} sort={sort} onSortChange={setSort} groupBy={groupBy}
            onApprove={id => actOnNode('approve', id, 'Node approved')} />
          <div className="fleet-pagination"><span>Page {pagination.page} of {pagination.totalPages}</span><button type="button" className="btn btn-secondary btn-sm" aria-label="Previous page" disabled={pagination.page === 1} onClick={() => setPage(value => value - 1)}>Previous</button><button type="button" className="btn btn-secondary btn-sm" aria-label="Next page" disabled={pagination.page === pagination.totalPages} onClick={() => setPage(value => value + 1)}>Next</button></div>
        </section>
      </div>
      <NodeInspector node={inspectedNode} open={!!inspectedNode} onClose={() => setInspectedId(null)}
        onDrain={id => actOnNode('drain', id, 'Node set to draining')} onResume={id => actOnNode('resume', id, 'Node resumed')} />
      <ConfirmDialog open={confirmRemove} title="Remove selected nodes" message={`Remove ${selectedIds.size} selected nodes from the cluster?`} confirmLabel="Remove nodes" pendingLabel="Removing…" pending={bulkRunning} danger onConfirm={() => runBulk('delete')} onCancel={() => setConfirmRemove(false)} />
    </div>
  )
}
