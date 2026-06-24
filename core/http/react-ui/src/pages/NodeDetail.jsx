import { useState, useEffect, useCallback } from 'react'
import { Link, useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { nodesApi } from '../utils/api'
import PageHeader from '../components/PageHeader'
import LoadingSpinner from '../components/LoadingSpinner'
import ConfirmDialog from '../components/ConfirmDialog'
import ActionMenu from '../components/ActionMenu'
import StatusPill from '../components/nodes/StatusPill'
import CapacityEditor from '../components/nodes/CapacityEditor'
import KeyValueChips from '../components/nodes/KeyValueChips'
import { formatVRAM, modelStateConfig, timeAgo } from '../components/nodes/nodeStatus'
import { capacityReading, nodeLifecycleAction } from '../utils/nodeFleet'

// Deep-linkable node management home. Reached by clicking a roster panel on
// /app/nodes. Surfaces what's running here plus the management affordances
// (capacity, backends, labels, drain/resume/remove) that previously lived in
// the expanded-row "Manage" drawer.
export default function NodeDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [node, setNode] = useState(null)
  const [models, setModels] = useState([])
  const [backends, setBackends] = useState([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [confirmUnload, setConfirmUnload] = useState(null)
  const [confirmDeleteBackend, setConfirmDeleteBackend] = useState(null)
  // Promise-based shrink confirmation: CapacityEditor awaits this hook so the
  // page owns the dialog (it can phrase the message with full node context).
  const [confirmShrinkState, setConfirmShrinkState] = useState(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    setLoadError('')
    try {
      const n = await nodesApi.get(id)
      const [m, b] = await Promise.all([nodesApi.getModels(id), nodesApi.getBackends(id)])
      setNode(n)
      setModels(Array.isArray(m) ? m : [])
      setBackends(Array.isArray(b) ? b : [])
    } catch (err) {
      setNode(null)
      setLoadError(err.message || 'Unable to load node')
      addToast(`Failed to load node: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [id, addToast])

  useEffect(() => { refresh() }, [refresh])

  const confirmShrink = useCallback((ctx) => new Promise((resolve) => {
    setConfirmShrinkState({ ...ctx, resolve })
  }), [])

  if (loading) return <div className="page page--wide loading-center"><LoadingSpinner size="lg" /></div>
  if (!node && loadError) {
    const notFound = loadError.includes('404') || loadError.toLowerCase().includes('not found')
    return <div className="page page--wide nodes-fleet-page node-detail-page">
      <PageHeader className="nodes-fleet-page__header" eyebrow={<Link to="/app/nodes" className="link-plain"><i className="fas fa-arrow-left icon-before" aria-hidden="true" />Nodes</Link>}
        title={notFound ? 'Node not found' : 'Could not load this node'} supporting={notFound ? 'It may have been removed from the cluster.' : loadError} />
      {!notFound && <div className="node-detail__load-error" role="alert"><i className="fas fa-triangle-exclamation" aria-hidden="true" /><div><strong>Could not load this node</strong><span>The cluster may be temporarily unavailable.</span></div><button type="button" className="btn btn-secondary btn-sm" onClick={() => void refresh()}>Retry</button></div>}
    </div>
  }
  if (!node) return <div className="page page--wide"><PageHeader title="Node not found" /></div>

  const drain = async () => { try { await nodesApi.drain(id); addToast('Node set to draining', 'success'); refresh() } catch (e) { addToast(e.message, 'error') } }
  const resume = async () => { try { await nodesApi.resume(id); addToast('Node resumed', 'success'); refresh() } catch (e) { addToast(e.message, 'error') } }
  const approve = async () => { try { await nodesApi.approve(id); addToast('Node approved', 'success'); refresh() } catch (e) { addToast(e.message, 'error') } }
  const remove = async () => { try { await nodesApi.delete(id); addToast('Node removed', 'success'); navigate('/app/nodes') } catch (e) { addToast(e.message, 'error') } }
  const unload = async (name) => { try { await nodesApi.unloadModel(id, name); addToast(`Model "${name}" unloaded`, 'success'); refresh() } catch (e) { addToast(e.message, 'error') } }
  // The upgrade runs async via the gallery job queue (202 + jobID); the
  // global Operations panel tracks progress, so the toast only reports the
  // dispatch, not completion.
  const upgradeBackend = async (name) => { try { await nodesApi.upgradeBackend(id, name); addToast(`Upgrading "${name}" on this node...`, 'info'); setTimeout(refresh, 1200) } catch (e) { addToast(e.message, 'error') } }
  const deleteBackend = async (name) => { try { await nodesApi.deleteBackend(id, name); addToast(`Backend "${name}" deleted`, 'success'); refresh() } catch (e) { addToast(e.message, 'error') } }
  const addLabel = async (k, v) => { try { await nodesApi.mergeLabels(id, { [k]: v }); refresh() } catch (e) { addToast(e.message, 'error') } }
  const delLabel = async (k) => { try { await nodesApi.deleteLabel(id, k); refresh() } catch (e) { addToast(e.message, 'error') } }

  const vram = capacityReading(node.total_vram, node.available_vram)
  const ram = capacityReading(node.total_ram, node.available_ram)
  const disk = capacityReading(node.total_disk, node.available_disk)
  const lifecycleAction = nodeLifecycleAction(node.status)
  // {modelName: replicaCount} of loaded models so the shrink confirm can warn
  // if the new cap is below the actual count of any single model on this node.
  const loadedModelCounts = (() => {
    const counts = {}
    models.forEach(m => { if (m.state === 'loaded') counts[m.model_name] = (counts[m.model_name] || 0) + 1 })
    return counts
  })()

  return (
    <div className="page page--wide nodes-fleet-page node-detail-page">
      <PageHeader
        className="nodes-fleet-page__header node-detail__header"
        eyebrow={<span className="node-detail__breadcrumb"><Link to="/app/nodes">Nodes</Link><i className="fas fa-chevron-right" aria-hidden="true" /><span>{node.name}</span></span>}
        title={node.name}
        supporting={<span className="node-detail__identity"><StatusPill status={node.status} /><span className="cell-mono">{node.address || node.id}</span><span>{node.node_type || 'backend'} node</span></span>}
        actions={
          <>
            {lifecycleAction === 'approve' && <button className="btn btn-primary btn-sm" onClick={approve}><i className="fas fa-check" /> Approve</button>}
            {lifecycleAction === 'resume' && <button className="btn btn-secondary btn-sm" onClick={resume}><i className="fas fa-play" /> Resume</button>}
            {lifecycleAction === 'drain' && <button className="btn btn-secondary btn-sm" onClick={drain}><i className="fas fa-pause" /> Drain</button>}
            <ActionMenu ariaLabel={`${node.name} actions`} triggerLabel={`Actions for ${node.name}`} items={[{
              key: 'remove', icon: 'fa-trash', label: 'Remove node…', danger: true, onClick: () => setConfirmRemove(true),
            }]} />
          </>
        }
      />

      <section className="node-detail__metrics" aria-label="Node resources">
        <div>
          <div className="drawer-eyebrow">VRAM</div>
          <span className="cell-mono">{vram ? `${formatVRAM(vram.used) || '0'} / ${formatVRAM(vram.total)}` : 'No data'}</span>
        </div>
        <div>
          <div className="drawer-eyebrow">RAM</div>
          <span className="cell-mono">{ram ? `${formatVRAM(ram.used) || '0'} / ${formatVRAM(ram.total)}` : 'No data'}</span>
        </div>
        <div>
            {/* Free space on the worker's MODELS filesystem. A node can look
                perfectly healthy on VRAM while having nowhere to put the
                weights, which is why this sits next to VRAM rather than
                buried in a diagnostics panel. */}
          <div className="drawer-eyebrow">Models disk free</div>
          <span className="cell-mono">{disk ? `${formatVRAM(disk.available) || '0'} / ${formatVRAM(disk.total)}` : 'No data'}</span>
        </div>
        {node.cpu_logical_cores > 0 && Number.isFinite(node.cpu_usage_percent) && (
          <div>
            <div className="drawer-eyebrow">CPU</div>
            <span className="cell-mono">{node.cpu_usage_percent.toFixed(1)}% of {node.cpu_logical_cores} cores</span>
            {Number.isFinite(node.cpu_load_1) && <span className="node-detail__metric-note">{node.cpu_load_1.toFixed(2)} load (1m)</span>}
          </div>
        )}
        <div>
          <div className="drawer-eyebrow">In-flight</div>
          <span className="cell-mono">{node.in_flight_count || 0}</span>
        </div>
        <div>
          <div className="drawer-eyebrow">Heartbeat</div>
          <span>{timeAgo(node.last_heartbeat)}</span>
        </div>
      </section>

      <div className="node-detail__layout">
        <div className="node-detail__workloads">
          <section className="fleet-workbench node-detail__workbench" aria-label="Running models">
            <div className="model-workbench__scope"><div><strong>Running models</strong><span>Replica processes scheduled to this node</span></div><span>{models.length} replica{models.length === 1 ? '' : 's'}</span></div>
            {models.length === 0 ? (
              <div className="node-detail__empty"><i className="fas fa-cube" aria-hidden="true" /><span>No models loaded yet. Replicas will appear here when scheduled to this node.</span></div>
            ) : (
              <div className="fleet-table-wrap">
                <table className="fleet-table node-detail__table node-detail__table--models">
                  <thead><tr><th>Model</th><th>State</th><th>In flight</th><th className="model-fleet-table__actions"><span className="sr-only">Actions</span></th></tr></thead>
                  <tbody>{(() => {
                    const replicaCounts = {}
                    models.forEach(model => { replicaCounts[model.model_name] = (replicaCounts[model.model_name] || 0) + 1 })
                    return models.map(model => {
                      const stCfg = modelStateConfig[model.state] || modelStateConfig.idle
                      const replicaNumber = (model.replica_index ?? 0) + 1
                      const showReplica = replicaCounts[model.model_name] > 1
                      const processKey = `${model.model_name}#${model.replica_index ?? 0}`
                      return <tr key={model.id || processKey}>
                        <td className="cell-mono">{model.model_name}{showReplica && <span aria-label={`replica ${replicaNumber}`} title={`Replica ${replicaNumber} on this node`} className="inline-tag">rep {replicaNumber}</span>}<span className="node-detail__mobile-meta">{model.state} · {model.in_flight ?? 0} in flight</span></td>
                        <td><span className="state-pill" style={{ background: stCfg.bg, color: stCfg.color, border: `1px solid ${stCfg.border}` }}>{model.state}</span></td>
                        <td className="cell-mono">{model.in_flight ?? 0}</td>
                        <td className="model-fleet-table__actions"><ActionMenu compact ariaLabel={`${model.model_name} replica ${replicaNumber} actions`} triggerLabel={`Actions for ${model.model_name} replica ${replicaNumber}`} items={[
                          { key: 'logs', icon: 'fa-terminal', label: 'View logs', onClick: () => navigate(`/app/node-backend-logs/${encodeURIComponent(id)}/${encodeURIComponent(processKey)}`) },
                          { divider: true },
                          { key: 'unload', icon: 'fa-stop', label: 'Unload model…', danger: true, onClick: () => setConfirmUnload({ modelName: model.model_name, inFlight: model.in_flight ?? 0 }) },
                        ]} /></td>
                      </tr>
                    })
                  })()}</tbody>
                </table>
              </div>
            )}
          </section>

          <section className="fleet-workbench node-detail__workbench" aria-label="Installed backends">
            <div className="model-workbench__scope"><div><strong>Installed backends</strong><span>Runtime engines available on this node</span></div><button type="button" className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/backends?target=${encodeURIComponent(id)}`)}><i className="fas fa-plus" aria-hidden="true" /> Add backend</button></div>
            {backends.length === 0 ? (
              <div className="node-detail__empty"><span>None installed.</span><button type="button" className="node-detail__text-action" onClick={() => navigate(`/app/backends?target=${encodeURIComponent(id)}`)}>Install one from the gallery</button></div>
            ) : (
              <div className="fleet-table-wrap">
                <table className="fleet-table node-detail__table node-detail__table--backends">
                  <thead><tr><th>Name</th><th>Source</th><th>Installed</th><th className="model-fleet-table__actions"><span className="sr-only">Actions</span></th></tr></thead>
                  <tbody>{backends.map(backend => <tr key={backend.name}>
                    <td className="cell-mono">{backend.name}</td>
                    <td><span className={`state-pill ${backend.is_system ? 'state-pill--system' : 'state-pill--gallery'}`}>{backend.is_system ? 'system' : 'gallery'}</span></td>
                    <td className="text-note">{backend.installed_at ? timeAgo(backend.installed_at) : '—'}</td>
                    <td className="model-fleet-table__actions">{!backend.is_system && <ActionMenu compact ariaLabel={`${backend.name} backend actions`} triggerLabel={`Actions for backend ${backend.name}`} items={[
                      { key: 'upgrade', icon: 'fa-arrow-up', label: 'Upgrade backend', onClick: () => upgradeBackend(backend.name) },
                      { divider: true },
                      { key: 'delete', icon: 'fa-trash', label: 'Delete backend…', danger: true, onClick: () => setConfirmDeleteBackend({ backend: backend.name }) },
                    ]} />}</td>
                  </tr>)}</tbody>
                </table>
              </div>
            )}
          </section>
        </div>

        <aside className="node-detail__configuration" aria-label="Node configuration">
          {node.node_type !== 'agent' && <section><div className="fleet-kicker">Replica capacity</div><p>Limit how many replicas of one model may run here.</p><CapacityEditor node={node} loadedModelCounts={loadedModelCounts} confirmShrink={confirmShrink} addToast={addToast} onUpdate={() => refresh()} /></section>}
          <section><div className="fleet-kicker">Labels</div><p>Labels control scheduling and describe this worker.</p><KeyValueChips pairs={Object.fromEntries(Object.entries(node.labels || {}).filter(([key]) => key !== 'node.replica-slots'))} onAdd={addLabel} onRemove={delLabel} placeholderKey="key" placeholderValue="value" ariaLabel="Node labels" /></section>
        </aside>
      </div>

      <ConfirmDialog
        open={confirmRemove}
        title="Remove node"
        message={`Remove "${node.name}" from the cluster? This will deregister it.`}
        confirmLabel="Remove"
        danger
        onConfirm={() => { remove(); setConfirmRemove(false) }}
        onCancel={() => setConfirmRemove(false)}
      />

      <ConfirmDialog
        open={!!confirmUnload}
        title="Unload Model"
        message={
          confirmUnload
            ? confirmUnload.inFlight > 0
              ? `"${confirmUnload.modelName}" currently has ${confirmUnload.inFlight} in-flight request(s). Unloading will interrupt them. Continue?`
              : `Unload "${confirmUnload.modelName}" from ${node.name}?`
            : ''
        }
        confirmLabel="Unload"
        danger={confirmUnload?.inFlight > 0}
        onConfirm={() => { if (confirmUnload) unload(confirmUnload.modelName); setConfirmUnload(null) }}
        onCancel={() => setConfirmUnload(null)}
      />

      <ConfirmDialog
        open={!!confirmDeleteBackend}
        title="Delete Backend"
        message={confirmDeleteBackend ? `Delete "${confirmDeleteBackend.backend}" from ${node.name}? This removes the backend files from this node only.` : ''}
        confirmLabel="Delete"
        danger
        onConfirm={() => { if (confirmDeleteBackend) deleteBackend(confirmDeleteBackend.backend); setConfirmDeleteBackend(null) }}
        onCancel={() => setConfirmDeleteBackend(null)}
      />

      <ConfirmDialog
        open={!!confirmShrinkState}
        title="Reduce replica capacity"
        message={
          confirmShrinkState
            ? `${node.name} currently has ${confirmShrinkState.currentLoaded} replica(s) of at least one model loaded. Reducing the cap to ${confirmShrinkState.newValue} won't evict anything immediately - running replicas keep going, but the reconciler will trim down on the next idle window. Continue?`
            : ''
        }
        confirmLabel="Reduce"
        onConfirm={() => { confirmShrinkState?.resolve(true); setConfirmShrinkState(null) }}
        onCancel={() => { confirmShrinkState?.resolve(false); setConfirmShrinkState(null) }}
      />
    </div>
  )
}
