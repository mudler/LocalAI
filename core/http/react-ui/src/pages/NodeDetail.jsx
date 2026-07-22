import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { nodesApi } from '../utils/api'
import PageHeader from '../components/PageHeader'
import LoadingSpinner from '../components/LoadingSpinner'
import ConfirmDialog from '../components/ConfirmDialog'
import StatusPill from '../components/nodes/StatusPill'
import CapacityEditor from '../components/nodes/CapacityEditor'
import KeyValueChips from '../components/nodes/KeyValueChips'
import { formatVRAM, modelStateConfig, timeAgo } from '../components/nodes/nodeStatus'

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
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [confirmUnload, setConfirmUnload] = useState(null)
  const [confirmDeleteBackend, setConfirmDeleteBackend] = useState(null)
  // Promise-based shrink confirmation: CapacityEditor awaits this hook so the
  // page owns the dialog (it can phrase the message with full node context).
  const [confirmShrinkState, setConfirmShrinkState] = useState(null)

  const refresh = useCallback(async () => {
    try {
      const n = await nodesApi.get(id)
      setNode(n)
      const [m, b] = await Promise.all([nodesApi.getModels(id), nodesApi.getBackends(id)])
      setModels(Array.isArray(m) ? m : [])
      setBackends(Array.isArray(b) ? b : [])
    } catch (err) {
      addToast(`Failed to load node: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [id, addToast])

  useEffect(() => { refresh() }, [refresh])

  const confirmShrink = useCallback((ctx) => new Promise((resolve) => {
    setConfirmShrinkState({ ...ctx, resolve })
  }), [])

  if (loading) return <div className="page page--wide" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>
  if (!node) return <div className="page page--wide"><PageHeader title="Node not found" /></div>

  const drain = async () => { try { await nodesApi.drain(id); addToast('Node set to draining', 'success'); refresh() } catch (e) { addToast(e.message, 'error') } }
  const resume = async () => { try { await nodesApi.resume(id); addToast('Node resumed', 'success'); refresh() } catch (e) { addToast(e.message, 'error') } }
  const remove = async () => { try { await nodesApi.delete(id); addToast('Node removed', 'success'); navigate('/app/nodes') } catch (e) { addToast(e.message, 'error') } }
  const unload = async (name) => { try { await nodesApi.unloadModel(id, name); addToast(`Model "${name}" unloaded`, 'success'); refresh() } catch (e) { addToast(e.message, 'error') } }
  // The upgrade runs async via the gallery job queue (202 + jobID); the
  // global Operations panel tracks progress, so the toast only reports the
  // dispatch, not completion.
  const upgradeBackend = async (name) => { try { await nodesApi.upgradeBackend(id, name); addToast(`Upgrading "${name}" on this node...`, 'info'); setTimeout(refresh, 1200) } catch (e) { addToast(e.message, 'error') } }
  const deleteBackend = async (name) => { try { await nodesApi.deleteBackend(id, name); addToast(`Backend "${name}" deleted`, 'success'); refresh() } catch (e) { addToast(e.message, 'error') } }
  const addLabel = async (k, v) => { try { await nodesApi.mergeLabels(id, { [k]: v }); refresh() } catch (e) { addToast(e.message, 'error') } }
  const delLabel = async (k) => { try { await nodesApi.deleteLabel(id, k); refresh() } catch (e) { addToast(e.message, 'error') } }

  const usedVRAM = node.total_vram && node.available_vram != null ? node.total_vram - node.available_vram : 0
  // {modelName: replicaCount} of loaded models so the shrink confirm can warn
  // if the new cap is below the actual count of any single model on this node.
  const loadedModelCounts = (() => {
    const counts = {}
    models.forEach(m => { if (m.state === 'loaded') counts[m.model_name] = (counts[m.model_name] || 0) + 1 })
    return counts
  })()

  return (
    <div className="page page--wide">
      <PageHeader
        eyebrow={<a onClick={() => navigate('/app/nodes')} style={{ cursor: 'pointer', color: 'var(--color-primary)' }}><i className="fas fa-arrow-left" style={{ marginRight: 6 }} aria-hidden="true" />Cluster</a>}
        title={<><StatusPill status={node.status} /> {node.name}</>}
        supporting={node.address}
        actions={
          <>
            {node.status === 'draining'
              ? <button className="btn btn-secondary btn-sm" onClick={resume}><i className="fas fa-play" /> Resume</button>
              : <button className="btn btn-secondary btn-sm" onClick={drain}><i className="fas fa-pause" /> Drain</button>}
            <button className="btn btn-danger btn-sm" onClick={() => setConfirmRemove(true)}><i className="fas fa-trash" /> Remove</button>
          </>
        }
      />

      {/* Inline metrics row: VRAM / in-flight - no boxes, just labelled values. */}
      <div className="node-detail__metrics">
        {node.total_vram > 0 && (
          <div>
            <div className="drawer-eyebrow">VRAM</div>
            <span className="cell-mono">{formatVRAM(usedVRAM) || '0'} / {formatVRAM(node.total_vram)}</span>
          </div>
        )}
        {node.total_disk > 0 && (
          <div>
            {/* Free space on the worker's MODELS filesystem. A node can look
                perfectly healthy on VRAM while having nowhere to put the
                weights, which is why this sits next to VRAM rather than
                buried in a diagnostics panel. */}
            <div className="drawer-eyebrow">Models disk free</div>
            <span className="cell-mono">{formatVRAM(node.available_disk || 0) || '0'} / {formatVRAM(node.total_disk)}</span>
          </div>
        )}
        <div>
          <div className="drawer-eyebrow">In-flight</div>
          <span className="cell-mono">{node.in_flight_count || 0}</span>
        </div>
        {node.node_type !== 'agent' && (
          <div style={{ minWidth: 0 }}>
            <div className="drawer-eyebrow">Capacity</div>
            <CapacityEditor
              node={node}
              loadedModelCounts={loadedModelCounts}
              confirmShrink={confirmShrink}
              addToast={addToast}
              onUpdate={() => refresh()}
            />
          </div>
        )}
      </div>

      {/* Running models */}
      <div style={{ marginTop: 'var(--spacing-lg)' }}>
        <div className="drawer-eyebrow">Running models</div>
        {models.length === 0 ? (
          <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', margin: '0 0 var(--spacing-md) 0' }}>
            <i className="fas fa-cube" style={{ marginRight: 6, opacity: 0.6 }} aria-hidden="true" />
            No models loaded yet - they'll appear here when scheduled to this node.
          </p>
        ) : (
          <table className="table" style={{ margin: 0 }}>
            <thead>
              <tr>
                <th>Model</th>
                <th>State</th>
                <th>In-Flight</th>
                <th style={{ width: 40 }}>Logs</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {(() => {
                // Pre-compute per-model replica counts so the disambiguation
                // pill only renders when this node actually hosts >1 replica
                // of the same model. Single-replica deployments stay clean.
                const replicaCounts = {}
                models.forEach(m => { replicaCounts[m.model_name] = (replicaCounts[m.model_name] || 0) + 1 })
                return models.map(m => {
                  const stCfg = modelStateConfig[m.state] || modelStateConfig.idle
                  const showReplica = (replicaCounts[m.model_name] || 0) > 1
                  // Per-replica process key - what the worker stores logs under and what the
                  // store's GetLines/Subscribe match on for replica-scoped filtering.
                  const processKey = `${m.model_name}#${m.replica_index ?? 0}`
                  return (
                    <tr key={m.id || `${m.model_name}#${m.replica_index ?? 0}`}>
                      <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>
                        {m.model_name}
                        {showReplica && (
                          <span
                            className="cell-mono"
                            aria-label={`replica ${m.replica_index ?? 0}`}
                            title={`Replica ${m.replica_index ?? 0} on this node`}
                            style={{
                              marginLeft: 8, padding: '1px 6px', borderRadius: 'var(--radius-sm)',
                              background: 'var(--color-bg-tertiary)',
                              border: '1px solid var(--color-border-subtle)',
                              fontSize: '0.6875rem', fontWeight: 500,
                              color: 'var(--color-text-secondary)',
                            }}
                          >
                            rep {m.replica_index ?? 0}
                          </span>
                        )}
                      </td>
                      <td>
                        <span style={{
                          display: 'inline-block', padding: '2px 8px', borderRadius: 'var(--radius-sm)',
                          fontSize: '0.75rem', fontWeight: 500,
                          background: stCfg.bg, color: stCfg.color, border: `1px solid ${stCfg.border}`,
                        }}>
                          {m.state}
                        </span>
                      </td>
                      <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>
                        {m.in_flight ?? 0}
                      </td>
                      <td>
                        <a
                          href="#"
                          onClick={(e) => {
                            e.preventDefault()
                            // Send the replica-scoped process key (modelName#replicaIndex).
                            navigate(`/app/node-backend-logs/${id}/${encodeURIComponent(processKey)}`)
                          }}
                          style={{ fontSize: '0.75rem', color: 'var(--color-primary)' }}
                          title={showReplica ? `View backend logs for replica ${m.replica_index ?? 0}` : 'View backend logs'}
                        >
                          <i className="fas fa-terminal" />
                        </a>
                      </td>
                      <td style={{ textAlign: 'right' }}>
                        <button
                          className="btn btn-danger btn-sm"
                          title={m.in_flight > 0 ? 'Unload model (has in-flight requests)' : 'Unload model'}
                          onClick={() => setConfirmUnload({ modelName: m.model_name, inFlight: m.in_flight ?? 0 })}
                        >
                          <i className="fas fa-stop" />
                        </button>
                      </td>
                    </tr>
                  )
                })
              })()}
            </tbody>
          </table>
        )}
      </div>

      {/* Installed backends */}
      <div style={{ marginTop: 'var(--spacing-lg)' }}>
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          marginBottom: 'var(--spacing-sm)',
        }}>
          <div className="drawer-eyebrow" style={{ margin: 0 }}>Installed backends</div>
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            onClick={() => navigate(`/app/backends?target=${encodeURIComponent(id)}`)}
            title={`Install a backend on ${node.name}`}
          >
            <i className="fas fa-plus" /> Add backend
          </button>
        </div>
        {backends.length === 0 ? (
          <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', margin: 0 }}>
            None installed. <a href="#" style={{ color: 'var(--color-primary)' }} onClick={(e) => { e.preventDefault(); navigate(`/app/backends?target=${encodeURIComponent(id)}`) }}>Install one from the gallery</a> to schedule models here.
          </p>
        ) : (
          <table className="table" style={{ margin: 0 }}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Installed At</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {backends.map(b => (
                <tr key={b.name}>
                  <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>
                    {b.name}
                  </td>
                  <td>
                    <span style={{
                      display: 'inline-block', padding: '2px 8px', borderRadius: 'var(--radius-sm)',
                      fontSize: '0.75rem', fontWeight: 500,
                      background: b.is_system ? 'var(--color-bg-tertiary)' : 'var(--color-primary-light)',
                      color: b.is_system ? 'var(--color-text-muted)' : 'var(--color-primary)',
                      border: `1px solid ${b.is_system ? 'var(--color-border-subtle)' : 'var(--color-primary-border)'}`,
                    }}>
                      {b.is_system ? 'system' : 'gallery'}
                    </span>
                  </td>
                  <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>
                    {b.installed_at ? timeAgo(b.installed_at) : '-'}
                  </td>
                  <td style={{ textAlign: 'right' }}>
                    {!b.is_system && (
                      <div style={{ display: 'inline-flex', gap: 'var(--spacing-xs)' }}>
                        <button
                          className="btn btn-secondary btn-sm"
                          onClick={() => upgradeBackend(b.name)}
                          title="Upgrade backend on this node"
                        >
                          <i className="fas fa-arrow-up" />
                        </button>
                        <button
                          className="btn btn-danger-ghost btn-sm"
                          onClick={() => setConfirmDeleteBackend({ backend: b.name })}
                          title="Delete backend from this node"
                        >
                          <i className="fas fa-trash" />
                        </button>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Labels - node.replica-slots is filtered out so the Capacity editor
          stays the single source of truth for that label. */}
      <div style={{ marginTop: 'var(--spacing-lg)' }}>
        <div className="drawer-eyebrow">Labels</div>
        <KeyValueChips
          pairs={Object.fromEntries(Object.entries(node.labels || {}).filter(([k]) => k !== 'node.replica-slots'))}
          onAdd={addLabel}
          onRemove={delLabel}
          placeholderKey="key"
          placeholderValue="value"
          ariaLabel="Node labels"
        />
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
