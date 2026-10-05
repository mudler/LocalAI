import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendControlApi, nodesApi } from '../utils/api'
import { filterModels, filterNodes, groupModels, paginateModels, paginateNodes, runBounded, sortModels, sortNodes, summarizeFleet } from '../utils/nodeFleet'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import ConfirmDialog from '../components/ConfirmDialog'
import ClusterOverview from '../components/nodes/ClusterOverview'
import NodeFleetTable from '../components/nodes/NodeFleetTable'
import NodeInspector from '../components/nodes/NodeInspector'
import ModelFleetTable from '../components/nodes/ModelFleetTable'
import ModelInspector from '../components/nodes/ModelInspector'
import LocalMachineView from '../components/nodes/LocalMachineView'
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

// The route to more than one machine, shown from the single-node view on
// request rather than as the whole page.
function ScaleOutCard({ addToast }) {
  return (
    <div className="card p2p-enable pad-lg mb-xl" data-testid="scale-out">
      <h3 className="panel-title"><i className="fas fa-rocket text-accent" />Distributed mode is not enabled</h3>
      <p className="text-base text-secondary mb-md">Distributed mode spreads models across worker machines and routes inference across the fleet. Start LocalAI with it enabled, then register a worker.</p>
      <CommandBlock command={'local-ai run --distributed \\\n  --distributed-db "postgres://user:pass@host/db" \\\n  --distributed-nats "nats://host:4222"'} addToast={addToast} />
      <p className="text-note mt-md">See the <a href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer" className="text-primary">Distributed Mode documentation</a> for production setup.</p>
    </div>
  )
}

function FleetSelect({ label, value, onChange, children }) {
  return (
    <label className="fleet-select-wrap">
      <span className="sr-only">{label}</span>
      <select className="fleet-select" aria-label={label} value={value} onChange={onChange}>{children}</select>
      <i className="fas fa-chevron-down fleet-select__chevron" aria-hidden="true" />
    </label>
  )
}

export default function Nodes() {
  const navigate = useNavigate()
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
  const [confirmStopModel, setConfirmStopModel] = useState(null)
  const [stoppingModelName, setStoppingModelName] = useState(null)
  const [bulkRunning, setBulkRunning] = useState(false)
  const bulkRunningRef = useRef(false)
  const [showTips, setShowTips] = useState(false)
  const [emptyNodeType, setEmptyNodeType] = useState('backend')
  const [workbenchView, setWorkbenchView] = useState('nodes')
  const [modelRows, setModelRows] = useState([])
  const [modelLoadState, setModelLoadState] = useState('idle')
  const [modelError, setModelError] = useState('')
  const [modelQuery, setModelQuery] = useState('')
  const [modelSort, setModelSort] = useState({ key: 'model_name', direction: 'asc' })
  const [modelPage, setModelPage] = useState(1)
  const [inspectedModelName, setInspectedModelName] = useState(null)
  const [modelDrillNodeId, setModelDrillNodeId] = useState(null)
  const [returnFocusNodeId, setReturnFocusNodeId] = useState(null)
  const modelRequestStarted = useRef(false)
  const modelStopRunningRef = useRef(false)
  const modelStopInvokerRef = useRef(null)
  const nodesTabRef = useRef(null)
  const modelsTabRef = useRef(null)
  const nodeInvokerRef = useRef(null)
  const modelInvokerRef = useRef(null)

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
      // A single-node server never registers the cluster routes, so it
      // answers 404 here; 503 is what the routes say when mounted without a
      // registry. Treating only 503 as "not distributed" sent every
      // single-node install to the empty worker-registration card.
      if (error.status === 404 || error.status === 503 || error.message?.includes('503') || error.message?.includes('Service Unavailable')) setEnabled(false)
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
  const drilledNode = nodes.find(node => node.id === modelDrillNodeId) || null
  const groupedModels = useMemo(() => groupModels(modelRows), [modelRows])
  const filteredModels = useMemo(() => filterModels(groupedModels, modelQuery), [groupedModels, modelQuery])
  const orderedModels = useMemo(() => sortModels(filteredModels, modelSort), [filteredModels, modelSort])
  const modelPagination = useMemo(() => paginateModels(orderedModels, modelPage), [orderedModels, modelPage])
  const inspectedModel = groupedModels.find(model => model.model_name === inspectedModelName) || null

  useEffect(() => { if (pagination.page !== page) setPage(pagination.page) }, [page, pagination.page])
  useEffect(() => { setPage(1) }, [query, status, type, activeAttention, groupBy])
  useEffect(() => { if (modelPagination.page !== modelPage) setModelPage(modelPagination.page) }, [modelPage, modelPagination.page])
  useEffect(() => { setModelPage(1) }, [modelQuery])

  const refreshModels = useCallback(async () => {
    const data = await nodesApi.allModels()
    setModelRows(Array.isArray(data) ? data : [])
    setModelLoadState('loaded')
    setModelError('')
  }, [])

  const loadModels = useCallback(async () => {
    if (modelRequestStarted.current) return
    modelRequestStarted.current = true
    setModelLoadState('loading')
    setModelError('')
    try {
      await refreshModels()
    } catch (error) {
      setModelError(error.message || 'Unable to load running models')
      setModelLoadState('error')
    }
  }, [refreshModels])

  const stopModel = () => {
    const model = confirmStopModel
    if (!model || modelStopRunningRef.current) return
    modelStopRunningRef.current = true
    setStoppingModelName(model.model_name)

    void (async () => {
      let stopError = null
      try {
        await backendControlApi.shutdown({ model: model.model_name })
      } catch (error) {
        stopError = error
      }

      try {
        await refreshModels()
      } catch (error) {
        setModelError(error.message || 'Unable to refresh running models')
        setModelLoadState('error')
      }

      if (stopError) {
        addToast(`Could not stop ${model.model_name}: ${stopError.message || stopError}. Some replicas may already have stopped.`, 'warning')
      } else {
        addToast(`Stopped ${model.model_name}: ${model.replica_count} replica${model.replica_count === 1 ? '' : 's'} across ${model.node_count} node${model.node_count === 1 ? '' : 's'}.`, 'success')
      }
      setConfirmStopModel(null)
      setStoppingModelName(null)
      modelStopRunningRef.current = false
    })()
  }

  const promptStopModel = (model, invoker) => {
    modelStopInvokerRef.current = invoker
    setConfirmStopModel(model)
  }

  const cancelStopModel = () => {
    setConfirmStopModel(null)
    restoreFocus(modelStopInvokerRef)
  }

  const activateWorkbench = view => {
    setWorkbenchView(view)
    setInspectedId(null)
    setInspectedModelName(null)
    setModelDrillNodeId(null)
    if (view === 'models') void loadModels()
  }

  const handleTabKeyDown = event => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
    event.preventDefault()
    const nextView = workbenchView === 'nodes' ? 'models' : 'nodes'
    activateWorkbench(nextView)
    ;(nextView === 'nodes' ? nodesTabRef : modelsTabRef).current?.focus()
  }

  const restoreFocus = ref => requestAnimationFrame(() => ref.current?.focus())

  const openNodeInspector = (node, invoker) => {
    nodeInvokerRef.current = invoker
    setInspectedId(node.id)
  }

  const closeNodeInspector = () => {
    setInspectedId(null)
    restoreFocus(nodeInvokerRef)
  }

  const openModelInspector = (model, invoker) => {
    modelInvokerRef.current = invoker
    setInspectedModelName(model.model_name)
    setModelDrillNodeId(null)
    setReturnFocusNodeId(null)
  }

  const openReplicaLogs = (nodeId, processKey) => {
    navigate(`/app/node-backend-logs/${encodeURIComponent(nodeId)}/${encodeURIComponent(processKey)}`)
  }

  const openModelLogs = (model, invoker) => {
    const replicas = Array.isArray(model.replicas) ? model.replicas : []
    if (replicas.length === 1 && replicas[0].node_id) {
      openReplicaLogs(replicas[0].node_id, `${model.model_name}#${replicas[0].replica_index ?? 0}`)
      return
    }
    openModelInspector(model, invoker)
  }

  const closeModelDrilldown = () => {
    setInspectedModelName(null)
    setModelDrillNodeId(null)
    setReturnFocusNodeId(null)
    restoreFocus(modelInvokerRef)
  }

  const openModelNode = node => {
    setReturnFocusNodeId(null)
    setModelDrillNodeId(node.id)
  }

  const returnToModel = () => {
    setReturnFocusNodeId(modelDrillNodeId)
    setModelDrillNodeId(null)
  }

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
  if (!enabled) return <LocalMachineView addToast={addToast} scaleOut={<ScaleOutCard addToast={addToast} />} />
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
    <div className={`page page--wide nodes-fleet-page${inspectedNode || inspectedModel || drilledNode ? ' nodes-fleet-page--inspecting' : ''}`}>
      <PageHeader className="nodes-fleet-page__header" eyebrow={null} title={t('nodes.title')} supporting={t('nodes.subtitle')} actions={<button type="button" className="btn btn-secondary btn-sm" onClick={() => setShowTips(value => !value)}>{showTips ? 'Hide setup' : 'Register worker'}</button>} />
      {showTips && <WorkerHintCard addToast={addToast} hasWorkers />}
      <ClusterOverview summary={summary} activeAttention={activeAttention} onAttentionSelect={setActiveAttention} />

      <section className="fleet-workbench" aria-label="Fleet workbench">
        <div className="fleet-workbench__tabs" role="tablist" aria-label="Fleet views">
          <button ref={nodesTabRef} id="fleet-nodes-tab" type="button" role="tab" aria-selected={workbenchView === 'nodes'} aria-controls="fleet-nodes-panel"
            tabIndex={workbenchView === 'nodes' ? 0 : -1} className={workbenchView === 'nodes' ? 'is-active' : ''} onKeyDown={handleTabKeyDown} onClick={() => activateWorkbench('nodes')}>Nodes <span>{nodes.length}</span></button>
          <button ref={modelsTabRef} id="fleet-models-tab" type="button" role="tab" aria-selected={workbenchView === 'models'} aria-controls="fleet-models-panel"
            tabIndex={workbenchView === 'models' ? 0 : -1} className={workbenchView === 'models' ? 'is-active' : ''} onKeyDown={handleTabKeyDown} onClick={() => activateWorkbench('models')}>Running models <span>{modelLoadState === 'loaded' ? groupedModels.length : '—'}</span></button>
        </div>
        <div className="fleet-workbench__layout">
          <div id="fleet-nodes-panel" className="fleet-workbench__fleet" role="tabpanel" aria-labelledby="fleet-nodes-tab" hidden={workbenchView !== 'nodes'}>
          <div className="fleet-toolbar">
            <input className="input fleet-toolbar__search" type="search" aria-label="Search nodes" placeholder="Search name, address, label…" value={query} onChange={event => setQuery(event.target.value)} />
            <FleetSelect label="Filter status" value={status} onChange={event => setStatus(event.target.value)}><option value="">All statuses</option>{['healthy', 'draining', 'pending', 'unhealthy', 'offline'].map(value => <option key={value} value={value}>{value}</option>)}</FleetSelect>
            <FleetSelect label="Filter type" value={type} onChange={event => setType(event.target.value)}><option value="">All types</option><option value="backend">backend</option><option value="agent">agent</option></FleetSelect>
            <FleetSelect label="Group nodes" value={groupBy} onChange={event => setGroupBy(event.target.value)}><option value="none">No grouping</option><option value="node_type">Group by type</option>{labelKeys.map(key => <option key={key} value={`label:${key}`}>Label: {key}</option>)}</FleetSelect>
          </div>
          {selectedIds.size > 0 && <div className="fleet-bulkbar">
            <strong>{selectedIds.size} selected</strong>
            <button type="button" className="btn btn-secondary btn-sm" disabled={bulkRunning} onClick={() => runBulk('drain')}>Drain selected</button>
            <button type="button" className="btn btn-secondary btn-sm" disabled={bulkRunning} onClick={() => runBulk('resume')}>Resume selected</button>
            <button type="button" className="btn btn-danger btn-sm" disabled={bulkRunning} onClick={() => setConfirmRemove(true)}>Remove selected</button>
            <button type="button" className="fleet-bulkbar__clear" disabled={bulkRunning} onClick={() => setSelectedIds(new Set())}>Clear selection</button>
            <span className="fleet-bulkbar__count" aria-live="polite">{ordered.length} nodes in view</span>
          </div>}
          <NodeFleetTable nodes={pagination.items} selectedIds={selectedIds} onSelectionChange={setSelectedIds} onInspect={openNodeInspector} sort={sort} onSortChange={setSort} groupBy={groupBy}
            onApprove={id => actOnNode('approve', id, 'Node approved')} />
          <div className="fleet-pagination"><span>Page {pagination.page} of {pagination.totalPages}</span><button type="button" className="btn btn-secondary btn-sm" aria-label="Previous page" disabled={pagination.page === 1} onClick={() => setPage(value => value - 1)}>Previous</button><button type="button" className="btn btn-secondary btn-sm" aria-label="Next page" disabled={pagination.page === pagination.totalPages} onClick={() => setPage(value => value + 1)}>Next</button></div>
          </div>
          <div id="fleet-models-panel" className="fleet-workbench__fleet model-workbench" role="tabpanel" aria-labelledby="fleet-models-tab" hidden={workbenchView !== 'models'}>
            <div className="model-workbench__scope"><div><strong>Running models</strong><span>Current loaded replicas on healthy nodes</span></div>{modelLoadState === 'loaded' && <span aria-live="polite">{orderedModels.length} model{orderedModels.length === 1 ? '' : 's'} in view</span>}</div>
            {modelLoadState === 'loading' && <div className="model-workbench__state" role="status"><LoadingSpinner size="sm" /><strong>Loading running models…</strong><span>Reading the controller's current replica inventory.</span></div>}
            {modelLoadState === 'error' && <div className="model-workbench__state model-workbench__state--error" role="alert"><i className="fas fa-triangle-exclamation" aria-hidden="true" /><strong>Unable to load running models</strong><span>{modelError}</span><button type="button" className="btn btn-secondary btn-sm" aria-label="Retry loading running models" onClick={() => { modelRequestStarted.current = false; void loadModels() }}>Retry</button></div>}
            {modelLoadState === 'loaded' && groupedModels.length === 0 && <div className="model-workbench__state"><i className="fas fa-layer-group" aria-hidden="true" /><strong>No running models</strong><span>Loaded replicas on healthy nodes will appear here.</span></div>}
            {modelLoadState === 'loaded' && groupedModels.length > 0 && <>
              <div className="model-toolbar"><input className="input fleet-toolbar__search" type="search" aria-label="Search running models" placeholder="Search model or backend…" value={modelQuery} onChange={event => setModelQuery(event.target.value)} /></div>
              <ModelFleetTable models={modelPagination.items} selectedName={inspectedModelName} inspectorOpen={!!inspectedModel && !drilledNode} onInspect={openModelInspector}
                onViewLogs={openModelLogs} onStop={promptStopModel} stoppingName={stoppingModelName} sort={modelSort} onSortChange={setModelSort} />
              <div className="fleet-pagination"><span>Page {modelPagination.page} of {modelPagination.totalPages}</span><button type="button" className="btn btn-secondary btn-sm" aria-label="Previous model page" disabled={modelPagination.page === 1} onClick={() => setModelPage(value => value - 1)}>Previous</button><button type="button" className="btn btn-secondary btn-sm" aria-label="Next model page" disabled={modelPagination.page === modelPagination.totalPages} onClick={() => setModelPage(value => value + 1)}>Next</button></div>
            </>}
          </div>
        </div>
      </section>
      {workbenchView === 'nodes' && <NodeInspector node={inspectedNode} open={!!inspectedNode} onClose={closeNodeInspector}
        onApprove={id => actOnNode('approve', id, 'Node approved')}
        onDrain={id => actOnNode('drain', id, 'Node set to draining')} onResume={id => actOnNode('resume', id, 'Node resumed')} />
      }
      {workbenchView === 'models' && !drilledNode && <ModelInspector model={inspectedModel} nodes={nodes} open={!!inspectedModel} onClose={closeModelDrilldown} onOpenNode={openModelNode} onViewLogs={openReplicaLogs} focusNodeId={returnFocusNodeId} />}
      {workbenchView === 'models' && drilledNode && <NodeInspector node={drilledNode} open onClose={closeModelDrilldown}
        onBack={returnToModel} backLabel={`Back to ${inspectedModel?.model_name || 'model'}`}
        onApprove={id => actOnNode('approve', id, 'Node approved')}
        onDrain={id => actOnNode('drain', id, 'Node set to draining')} onResume={id => actOnNode('resume', id, 'Node resumed')} />}
      <ConfirmDialog open={confirmRemove} title="Remove selected nodes" message={`Remove ${selectedIds.size} selected nodes from the cluster?`} confirmLabel="Remove nodes" pendingLabel="Removing…" pending={bulkRunning} danger onConfirm={() => runBulk('delete')} onCancel={() => setConfirmRemove(false)} />
      <ConfirmDialog open={!!confirmStopModel} title={confirmStopModel ? `Stop ${confirmStopModel.model_name}?` : 'Stop model?'}
        message={confirmStopModel ? `${confirmStopModel.model_name} has ${confirmStopModel.replica_count} loaded replica${confirmStopModel.replica_count === 1 ? '' : 's'} across ${confirmStopModel.node_count} unique node${confirmStopModel.node_count === 1 ? '' : 's'}. This will stop all loaded placements on those nodes.` : ''}
        confirmLabel="Stop model" pendingLabel="Stopping…" pending={!!stoppingModelName} danger onConfirm={stopModel} onCancel={cancelStopModel} />
    </div>
  )
}
