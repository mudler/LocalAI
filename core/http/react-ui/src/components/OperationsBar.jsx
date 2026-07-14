import { useState } from 'react'
import { useOperations } from '../hooks/useOperations'
import { formatBytes } from '../utils/format'

const artifactPhaseLabels = {
  resolving: 'Resolving model files',
  downloading: 'Downloading model files',
  verifying: 'Verifying model files',
  committing: 'Finalizing model installation',
  persisting: 'Saving model configuration',
}

const nodeStatusLabels = {
  success: 'Done',
  error: 'Failed',
  queued: 'Queued',
  running_on_worker: 'Worker busy',
  downloading: 'Downloading',
}

const runningOnWorkerTooltip = 'NATS round-trip timed out, but the worker is still installing in the background. The reconciler will confirm completion.'

export default function OperationsBar() {
  const { operations, cancelOperation, dismissFailedOp } = useOperations()
  const [expanded, setExpanded] = useState({})

  if (operations.length === 0) return null

  const toggle = (key) => setExpanded((m) => ({ ...m, [key]: !m[key] }))

  return (
    <div className="operations-bar">
      {operations.map(op => {
        const key = op.jobID || op.id
        const nodes = Array.isArray(op.nodes) ? op.nodes : []
        const canExpand = nodes.length > 1
        const isOpen = !!expanded[key]
        const phaseLabel = artifactPhaseLabels[op.phase]
        const byteLabel = Number.isFinite(op.currentBytes) && Number.isFinite(op.totalBytes) && op.totalBytes > 0
          ? `${formatBytes(op.currentBytes)} / ${formatBytes(op.totalBytes)}`
          : ''
        return (
        <div key={key} className="operation-item">
          <div className="operation-info">
            {op.error ? (
              <i className="fas fa-circle-exclamation" style={{ color: 'var(--color-error)', marginRight: 'var(--spacing-xs)' }} />
            ) : op.isCancelled ? (
              <i className="fas fa-ban" style={{ color: 'var(--color-warning)', marginRight: 'var(--spacing-xs)' }} />
            ) : op.isDeletion ? (
              <i className="fas fa-trash" style={{ color: 'var(--color-error)', marginRight: 'var(--spacing-xs)' }} />
            ) : (
              <div className="operation-spinner" />
            )}
            <span className="operation-text">
              {op.error ? (
                <>
                  Failed to install {op.isBackend ? 'backend' : 'model'}: {op.name || op.id}
                  <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginLeft: 'var(--spacing-xs)' }}>
                    ({op.error})
                  </span>
                </>
              ) : op.taskType === 'staging' ? (
                <>
                  <i className="fas fa-cloud-arrow-up" style={{ marginRight: 'var(--spacing-xs)' }} />
                  Staging model: {op.name}{op.nodeName ? ` → ${op.nodeName}` : ''}
                </>
              ) : (
                <>
                  {op.isDeletion ? 'Removing' : 'Installing'}{' '}
                  {op.isBackend ? 'backend' : 'model'}: {op.name || op.id}
                </>
              )}
            </span>
            {!op.error && op.isQueued && (
              <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginLeft: 'var(--spacing-xs)' }}>
                (Queued)
              </span>
            )}
            {!op.error && op.isCancelled && (
              <span style={{ fontSize: '0.75rem', color: 'var(--color-warning)', marginLeft: 'var(--spacing-xs)' }}>
                Cancelling...
              </span>
            )}
            {!op.error && phaseLabel && !op.isCancelled && (
              <span className="operation-phase" style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginLeft: 'var(--spacing-xs)' }}>
                {phaseLabel}
              </span>
            )}
            {!op.error && byteLabel && !op.isCancelled && (
              <span className="operation-bytes" style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginLeft: 'var(--spacing-xs)' }}>
                {byteLabel}
              </span>
            )}
            {!op.error && op.message && !phaseLabel && !op.isQueued && !op.isCancelled && (
              <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginLeft: 'var(--spacing-xs)' }}>
                {op.message}
              </span>
            )}
            {!op.error && op.progress !== undefined && op.progress > 0 && (
              <span className="operation-progress">{Math.round(op.progress)}%</span>
            )}
          </div>
          {!op.error && op.progress !== undefined && op.progress > 0 && (
            <div className="operation-bar-container">
              <div className="operation-bar" style={{ width: `${op.progress}%` }} />
            </div>
          )}
          {op.error ? (
            <button
              className="operation-cancel"
              onClick={() => dismissFailedOp(op.id)}
              title="Dismiss"
            >
              <i className="fas fa-xmark" />
            </button>
          ) : op.cancellable && !op.isCancelled ? (
            <button
              className="operation-cancel"
              onClick={() => cancelOperation(op.jobID)}
              title="Cancel"
            >
              <i className="fas fa-xmark" />
            </button>
          ) : null}
          {canExpand && (
            <button
              type="button"
              className="operation-expand"
              onClick={() => toggle(key)}
              aria-expanded={isOpen}
              title={isOpen ? 'Hide per-node detail' : `Show ${nodes.length} nodes`}
            >
              <i className={`fas fa-chevron-${isOpen ? 'up' : 'down'}`} />
              <span className="operation-expand-label">{nodes.length} nodes</span>
            </button>
          )}
          {canExpand && isOpen && (
            <ul className="operation-nodes-list">
              {nodes.map((n) => (
                <li key={n.node_id} className={`operation-node operation-node-${n.status}`}>
                  <span
                    className={`operation-node-status operation-node-status-${n.status}`}
                    title={n.status === 'running_on_worker' ? runningOnWorkerTooltip : undefined}
                  >
                    {nodeStatusLabels[n.status] || n.status}
                  </span>
                  <span className="operation-node-name">{n.node_name || n.node_id}</span>
                  {n.file_name && <span className="operation-node-file">{n.file_name}</span>}
                  {(n.current || n.total) && (
                    <span className="operation-node-bytes">
                      {n.current || '?'} / {n.total || '?'}
                    </span>
                  )}
                  {n.percentage > 0 && (
                    <span className="operation-node-pct">{Math.round(n.percentage)}%</span>
                  )}
                  {n.error && (
                    <span className="operation-node-error" title={n.error}>
                      {n.error.length > 80 ? n.error.slice(0, 80) + '...' : n.error}
                    </span>
                  )}
                  {n.percentage > 0 && n.percentage < 100 && (
                    <div className="operation-node-bar-container">
                      <div className="operation-node-bar" style={{ width: `${n.percentage}%` }} />
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
        )
      })}
    </div>
  )
}
