import { useEffect, useRef, useState } from 'react'
import StatusPill from './StatusPill'
import { formatBytes, formatCapacity, timeAgo } from './nodeStatus'
import { nodesApi } from '../../utils/api'
import { capacityReading, nodeLifecycleAction } from '../../utils/nodeFleet'
import useInspectorDrawer from './useInspectorDrawer'

function InspectorMetric({ label, children }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>
}

function ResourceBar({ label, total, available, tone }) {
  const reading = capacityReading(total, available)
  if (!reading) return <InspectorMetric label={label}>No data</InspectorMetric>
  const percent = Math.round(reading.usagePercent)
  return (
    <div className={`node-inspector__resource node-inspector__resource--${tone}`}>
      <div className="node-inspector__resource-label"><strong>{label} <span>{formatBytes(reading.used)} / {formatBytes(reading.total)}</span></strong><span>{formatBytes(reading.available)} free</span></div>
      <progress className="node-inspector__resource-track" max="100" value={percent} aria-label={`${label} usage`} />
    </div>
  )
}

export default function NodeInspector({ node, open, onClose, onApprove, onDrain, onResume, onBack, backLabel }) {
  const [backends, setBackends] = useState(null)
  const [backendError, setBackendError] = useState('')
  const nodeId = node?.id
  const backRef = useRef(null)
  const closeRef = useRef(null)
  const drawerRef = useRef(null)
  const hasBack = Boolean(onBack)
  const modal = useInspectorDrawer(open, onClose, drawerRef)

  useEffect(() => {
    if (open) (hasBack ? backRef : closeRef).current?.focus()
  }, [open, nodeId, hasBack])

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
  const disk = capacityReading(node.total_disk, node.available_disk)
  const lifecycleAction = nodeLifecycleAction(node.status)

  return <>
    <div className="node-inspector__scrim" aria-hidden="true" onClick={onClose} />
    <aside ref={drawerRef} className="node-inspector" aria-label="Node inspector" role={modal ? 'dialog' : undefined} aria-modal={modal ? 'true' : undefined} tabIndex={modal ? -1 : undefined}>
      <header className="node-inspector__header">
        <div className="node-inspector__topbar">
          {onBack
            ? <button ref={backRef} type="button" className="node-inspector__back" onClick={onBack}><i className="fas fa-arrow-left" aria-hidden="true" /> {backLabel || 'Back'}</button>
            : <span className="fleet-kicker">Node inspector</span>}
          <button ref={closeRef} type="button" className="btn btn-ghost btn-sm" aria-label="Close node inspector" onClick={onClose}><i className="fas fa-times" /></button>
        </div>
        <div className="node-inspector__identity">
          <h2>{node.name}</h2>
          <p>{node.labels?.zone ? `${node.labels.zone} · ` : ''}{node.node_type || 'worker'} node</p>
          <StatusPill status={node.status} />
        </div>
      </header>
      <div className="node-inspector__body">
        <section className="node-inspector__section">
        <h3>Node</h3>
        <dl className="node-inspector__metrics">
          <InspectorMetric label="Address"><span className="node-inspector__address">{node.address || 'No address reported'}</span></InspectorMetric>
          <InspectorMetric label="Heartbeat">{timeAgo(node.last_heartbeat)}</InspectorMetric>
        </dl>
        <div className="node-inspector__labels" aria-label="Node labels">{Object.keys(node.labels || {}).length ? Object.entries(node.labels).map(([key, value]) => <span key={key}>{key}={value}</span>) : <span className="text-muted">No labels</span>}</div>
        </section>
        <section className="node-inspector__section">
        <h3>Resources</h3>
        <ResourceBar label="VRAM" total={node.total_vram} available={node.available_vram} tone="vram" />
        <ResourceBar label="RAM" total={node.total_ram} available={node.available_ram} tone="ram" />
        <dl className="node-inspector__metrics">
          <InspectorMetric label="CPU">{cpuKnown ? `${node.cpu_usage_percent.toFixed(1)}% of ${node.cpu_logical_cores} cores · ${node.cpu_load_1.toFixed(2)} load` : 'No data'}</InspectorMetric>
          <InspectorMetric label="Models disk">{disk ? <>{formatCapacity(disk.used, disk.total)} · {formatBytes(disk.available)} free</> : 'No data'}</InspectorMetric>
        </dl>
        </section>
        <section className="node-inspector__section">
        <h3>Workload</h3>
        <dl className="node-inspector__metrics">
          <InspectorMetric label="Loaded models">{node.model_count ?? 0}</InspectorMetric>
          <InspectorMetric label="Backends">{backendError ? <span className="text-error">{backendError}</span> : backends === null ? 'Loading…' : `${backends.length} backend${backends.length === 1 ? '' : 's'}`}</InspectorMetric>
          <InspectorMetric label="In-flight work">{node.in_flight_count ?? 0}</InspectorMetric>
        </dl>
        </section>
      </div>
      <footer className="node-inspector__actions">
        <a className="btn btn-primary btn-sm" href={`/app/nodes/${encodeURIComponent(node.id)}`} aria-label="Open full node details">Open full details</a>
        {lifecycleAction === 'approve' && <button type="button" className="btn btn-primary btn-sm" onClick={() => onApprove(node.id)}>Approve</button>}
        {lifecycleAction === 'resume' && <button type="button" className="btn btn-secondary btn-sm" onClick={() => onResume(node.id)}>Resume</button>}
        {lifecycleAction === 'drain' && <button type="button" className="btn btn-secondary btn-sm" onClick={() => onDrain(node.id)}>Drain</button>}
      </footer>
    </aside>
  </>
}
