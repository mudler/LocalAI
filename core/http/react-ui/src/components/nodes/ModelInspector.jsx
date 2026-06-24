import { useEffect, useRef } from 'react'
import StatusPill from './StatusPill'
import { timeAgo } from './nodeStatus'
import useInspectorDrawer from './useInspectorDrawer'

function groupReplicasByNode(replicas, nodes) {
  const nodeById = new Map(nodes.map(node => [node.id, node]))
  const groups = new Map()
  replicas.forEach(replica => {
    const nodeId = String(replica?.node_id ?? '').trim()
    const key = nodeId || `unknown:${replica?.id ?? groups.size}`
    if (!groups.has(key)) groups.set(key, { nodeId, node: nodeById.get(nodeId) || null, replicas: [] })
    groups.get(key).replicas.push(replica)
  })
  return [...groups.values()]
}

function latestUse(replicas) {
  const values = replicas.map(replica => ({ value: replica.last_used, time: Date.parse(replica.last_used) })).filter(entry => Number.isFinite(entry.time))
  return values.length ? values.sort((left, right) => right.time - left.time)[0].value : null
}

export default function ModelInspector({ model, nodes, open, onClose, onOpenNode, onViewLogs, focusNodeId }) {
  const closeRef = useRef(null)
  const drawerRef = useRef(null)
  const nodeButtonRefs = useRef(new Map())
  const modal = useInspectorDrawer(open, onClose, drawerRef)

  useEffect(() => {
    if (open) closeRef.current?.focus()
  }, [open, model?.model_name])

  useEffect(() => {
    if (open && focusNodeId) nodeButtonRefs.current.get(focusNodeId)?.focus()
  }, [open, focusNodeId])

  if (!open || !model) return null
  const nodeGroups = groupReplicasByNode(model.replicas, nodes)

  return <>
    <div className="node-inspector__scrim" aria-hidden="true" onClick={onClose} />
    <aside ref={drawerRef} id="model-inspector" className="node-inspector model-inspector" aria-label="Model inspector" role={modal ? 'dialog' : undefined} aria-modal={modal ? 'true' : undefined} tabIndex={modal ? -1 : undefined}>
      <header className="node-inspector__header">
        <div className="node-inspector__topbar"><span className="fleet-kicker">Running model</span><button ref={closeRef} type="button" className="btn btn-ghost btn-sm" aria-label="Close model inspector" onClick={onClose}><i className="fas fa-times" /></button></div>
        <h2>{model.model_name}</h2>
        <p>Replica placement across the active fleet</p>
      </header>
      <div className="node-inspector__body">
        <section className="node-inspector__section model-inspector__summary">
        <h3>Model summary</h3>
        <dl className="node-inspector__metrics">
          <div><dt>Replicas</dt><dd>{model.replica_count}</dd></div>
          <div><dt>Nodes</dt><dd>{model.node_count}</dd></div>
          <div><dt>In-flight work</dt><dd>{model.in_flight}</dd></div>
          <div><dt>Last used</dt><dd>{model.last_used ? timeAgo(model.last_used) : 'Never'}</dd></div>
        </dl>
        <div className="model-backend-list model-inspector__backends" aria-label="Backend types">{model.backend_types.length ? model.backend_types.map(backend => <span key={backend}>{backend}</span>) : <span className="text-muted">Backend unknown</span>}</div>
        </section>
        <section className="node-inspector__section">
        <h3>Replica placement</h3>
        <div className="model-inspector__nodes">{nodeGroups.map(group => {
          const inFlight = group.replicas.reduce((total, replica) => total + (Number.isFinite(replica.in_flight) ? Math.max(0, Math.floor(replica.in_flight)) : 0), 0)
          const lastUsed = latestUse(group.replicas)
          const name = group.node?.name || group.nodeId || 'Unknown node'
          return (
            <article className="model-inspector__node" key={group.nodeId || group.replicas[0]?.id}>
              <div className="model-inspector__node-heading">
                {group.node
                  ? <button ref={element => { if (element) nodeButtonRefs.current.set(group.nodeId, element); else nodeButtonRefs.current.delete(group.nodeId) }}
                    type="button" onClick={event => onOpenNode(group.node, event.currentTarget)} aria-label={`Open node ${name}`}>{name}</button>
                  : <strong>{name}</strong>}
                <div className="model-inspector__node-actions">
                  {group.node ? <StatusPill status={group.node.status} /> : <span className="status-pill status-pill--neutral">Unknown</span>}
                  {group.nodeId && <button type="button" className="model-inspector__logs" aria-label={`View all ${model.model_name} logs on ${name}`}
                    onClick={() => onViewLogs(group.nodeId, model.model_name)}><i className="fas fa-terminal" aria-hidden="true" /> Logs</button>}
                </div>
              </div>
              <p>{group.replicas.length} replica{group.replicas.length === 1 ? '' : 's'} · {inFlight} in flight · {lastUsed ? timeAgo(lastUsed) : 'never used'}</p>
              <ul>{group.replicas.map(replica => {
                const replicaNumber = Number.isFinite(replica.replica_index) ? replica.replica_index + 1 : null
                const processKey = `${model.model_name}#${replica.replica_index ?? 0}`
                return <li key={replica.id || `${replica.replica_index}:${replica.address}`}>
                  <span>Replica {replicaNumber ?? '—'}</span>
                  <code>{replica.address || 'No address'}</code>
                  {group.nodeId && <button type="button" className="model-inspector__replica-logs"
                    aria-label={`View logs for ${model.model_name} replica ${replicaNumber ?? 'unknown'} on ${name}`}
                    onClick={() => onViewLogs(group.nodeId, processKey)}>View logs</button>}
                </li>
              })}</ul>
            </article>
          )
        })}</div>
        </section>
      </div>
      <footer className="node-inspector__actions node-inspector__actions--single"><button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>Close</button></footer>
    </aside>
  </>
}
