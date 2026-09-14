import { useEffect, useState } from 'react'
import StatusPill from './StatusPill'
import { formatBytes, formatCapacity, timeAgo } from './nodeStatus'
import { nodesApi } from '../../utils/api'

function InspectorMetric({ label, children }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>
}

function ResourceBar({ label, total, available, tone }) {
  if (!(total > 0) || !Number.isFinite(available)) return <InspectorMetric label={label}>No data</InspectorMetric>
  const free = Math.max(0, Math.min(total, available))
  const used = total - free
  const percent = Math.round(used / total * 100)
  return (
    <div className={`node-inspector__resource node-inspector__resource--${tone}`}>
      <div className="node-inspector__resource-label"><strong>{label} <span>{formatBytes(used)} / {formatBytes(total)}</span></strong><span>{formatBytes(free)} free</span></div>
      <progress className="node-inspector__resource-track" max="100" value={percent} aria-label={`${label} usage`} />
    </div>
  )
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
      <section className="node-inspector__section">
        <h3>Node</h3>
        <StatusPill status={node.status} />
        <dl className="node-inspector__metrics"><InspectorMetric label="Address"><span className="node-inspector__address">{node.address || 'No address reported'}</span></InspectorMetric></dl>
        <div className="node-inspector__labels" aria-label="Node labels">{Object.keys(node.labels || {}).length ? Object.entries(node.labels).map(([key, value]) => <span key={key}>{key}={value}</span>) : <span className="text-muted">No labels</span>}</div>
      </section>
      <section className="node-inspector__section">
        <h3>Resources</h3>
        <ResourceBar label="VRAM" total={node.total_vram} available={node.available_vram} tone="vram" />
        <ResourceBar label="RAM" total={node.total_ram} available={node.available_ram} tone="ram" />
        <dl className="node-inspector__metrics">
          <InspectorMetric label="CPU">{cpuKnown ? `${node.cpu_usage_percent.toFixed(1)}% of ${node.cpu_logical_cores} cores · ${node.cpu_load_1.toFixed(2)} load` : 'No data'}</InspectorMetric>
          <InspectorMetric label="Models disk">{formatCapacity(used(node.total_disk, node.available_disk), node.total_disk)}</InspectorMetric>
        </dl>
      </section>
      <section className="node-inspector__section">
        <h3>Workload</h3>
        <dl className="node-inspector__metrics">
          <InspectorMetric label="Loaded models">{node.model_count ?? 0}</InspectorMetric>
          <InspectorMetric label="In-flight work">{node.in_flight_count ?? 0}</InspectorMetric>
        </dl>
      </section>
      <section className="node-inspector__section">
        <h3>Runtime</h3>
        <dl className="node-inspector__metrics">
          <InspectorMetric label="Heartbeat">{timeAgo(node.last_heartbeat)}</InspectorMetric>
          <InspectorMetric label="Backends">{backendError ? <span className="text-error">{backendError}</span> : backends === null ? 'Loading…' : `${backends.length} backend${backends.length === 1 ? '' : 's'}`}</InspectorMetric>
        </dl>
      </section>
      <div className="node-inspector__actions">
        <a className="btn btn-primary btn-sm" href={`/app/nodes/${encodeURIComponent(node.id)}`} aria-label="Open full node details">Open full details</a>
        {node.status === 'draining'
          ? <button type="button" className="btn btn-secondary btn-sm" onClick={() => onResume(node.id)}>Resume</button>
          : <button type="button" className="btn btn-secondary btn-sm" onClick={() => onDrain(node.id)}>Drain</button>}
      </div>
    </aside>
  )
}
