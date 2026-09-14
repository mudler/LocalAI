import { useEffect, useState } from 'react'
import StatusPill from './StatusPill'
import { formatCapacity, timeAgo } from './nodeStatus'
import { nodesApi } from '../../utils/api'

function InspectorMetric({ label, children }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>
}

export default function NodeInspector({ node, open, onClose, onDrain, onResume }) {
  const [backends, setBackends] = useState(null)
  const [backendError, setBackendError] = useState('')
  const nodeId = node?.id

  useEffect(() => {
    if (!open || !nodeId) return undefined
    let current = true
    setBackends(null)
    setBackendError('')
    nodesApi.getBackends(nodeId).then(data => {
      if (current) setBackends(Array.isArray(data) ? data : [])
    }).catch(error => {
      if (current) setBackendError(error.message || 'Unable to load backends')
    })
    return () => { current = false }
  }, [open, nodeId])

  if (!open || !node) return null
  const cpuKnown = node.cpu_logical_cores > 0 && Number.isFinite(node.cpu_usage_percent) && Number.isFinite(node.cpu_load_1)
  const used = (total, available) => total > 0 && Number.isFinite(available) ? Math.max(0, Math.min(total, total - available)) : Number.NaN

  return (
    <aside className="node-inspector" aria-label="Node inspector">
      <div className="node-inspector__header">
        <div><span className="fleet-kicker">Node inspector</span><h2>{node.name}</h2></div>
        <button type="button" className="btn btn-ghost btn-sm" aria-label="Close node inspector" onClick={onClose}><i className="fas fa-times" /></button>
      </div>
      <StatusPill status={node.status} />
      <p className="node-inspector__address">{node.address || 'No address reported'}</p>
      <dl className="node-inspector__metrics">
        <InspectorMetric label="VRAM">{formatCapacity(used(node.total_vram, node.available_vram), node.total_vram)}</InspectorMetric>
        <InspectorMetric label="RAM">{formatCapacity(used(node.total_ram, node.available_ram), node.total_ram)}</InspectorMetric>
        <InspectorMetric label="CPU">{cpuKnown ? `${node.cpu_usage_percent.toFixed(1)}% of ${node.cpu_logical_cores} cores · ${node.cpu_load_1.toFixed(2)} load` : 'No data'}</InspectorMetric>
        <InspectorMetric label="Models disk">{formatCapacity(used(node.total_disk, node.available_disk), node.total_disk)}</InspectorMetric>
        <InspectorMetric label="Models">{node.model_count ?? 0}</InspectorMetric>
        <InspectorMetric label="In-flight work">{node.in_flight_count ?? 0}</InspectorMetric>
        <InspectorMetric label="Heartbeat">{timeAgo(node.last_heartbeat)}</InspectorMetric>
        <InspectorMetric label="Backends">{backendError ? <span className="text-error">{backendError}</span> : backends === null ? 'Loading…' : `${backends.length} backend${backends.length === 1 ? '' : 's'}`}</InspectorMetric>
      </dl>
      <div className="node-inspector__labels">
        <span className="fleet-kicker">Labels</span>
        {Object.keys(node.labels || {}).length ? Object.entries(node.labels).map(([key, value]) => <span key={key}>{key}={value}</span>) : <span className="text-muted">None</span>}
      </div>
      <div className="node-inspector__actions">
        <a className="btn btn-primary btn-sm" href={`/app/nodes/${encodeURIComponent(node.id)}`} aria-label="Open full node details">Open full details</a>
        {node.status === 'draining'
          ? <button type="button" className="btn btn-secondary btn-sm" onClick={() => onResume(node.id)}>Resume</button>
          : <button type="button" className="btn btn-secondary btn-sm" onClick={() => onDrain(node.id)}>Drain</button>}
      </div>
    </aside>
  )
}
