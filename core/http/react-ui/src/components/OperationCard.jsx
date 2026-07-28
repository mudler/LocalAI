import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { formatBytes } from '../utils/format'

const phaseKeys = {
  resolving: 'activity.phase.resolving',
  downloading: 'activity.phase.downloading',
  verifying: 'activity.phase.verifying',
  committing: 'activity.phase.committing',
  persisting: 'activity.phase.persisting',
}

const nodeStatusKeys = {
  success: 'activity.node.done',
  error: 'activity.node.failed',
  queued: 'activity.node.queued',
  running_on_worker: 'activity.node.workerBusy',
  downloading: 'activity.node.downloading',
}

// etaSeconds is derived by OperationsContext from the byte delta between
// polls. It is absent until two samples exist, and absent for every operation
// when it is absent for any byte-tracked one.
function formatEta(seconds) {
  if (!Number.isFinite(seconds) || seconds <= 0) return ''
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min`
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}

export default function OperationCard({ operation, onCancel, onDismiss }) {
  const { t } = useTranslation('admin')
  const nodes = Array.isArray(operation.nodes) ? operation.nodes : []
  const [nodesOpen, setNodesOpen] = useState(nodes.length > 0 && nodes.length <= 4)

  const failed = Boolean(operation.error)
  const cancelling = !failed && Boolean(operation.isCancelled)
  const byteLabel = Number.isFinite(operation.currentBytes) && Number.isFinite(operation.totalBytes) && operation.totalBytes > 0
    ? `${formatBytes(operation.currentBytes)} / ${formatBytes(operation.totalBytes)}`
    : ''
  const phaseKey = phaseKeys[operation.phase]
  const etaLabel = formatEta(operation.etaSeconds)
  // A cancelling operation keeps reporting progress it will never finish, so
  // the bar and the estimate are dropped rather than left creeping forward.
  // Same call the strip makes, for the same reason.
  const showProgress = !failed && !cancelling && !operation.isQueued && operation.progress > 0
  const canCancel = operation.cancellable && !operation.isCancelled && !failed

  return (
    <div className={`operation-card${failed ? ' operation-card--error' : ''}`}>
      <div className="operation-card__main">
        {failed
          ? <i className="fas fa-circle-exclamation operation-card__icon operation-card__icon--error" aria-hidden="true" />
          : cancelling
            ? <i className="fas fa-ban operation-card__icon operation-card__icon--cancelling" aria-hidden="true" />
            : operation.isQueued
              ? <i className="fas fa-clock operation-card__icon" aria-hidden="true" />
              : <span className="operation-card__spinner" aria-hidden="true" />}

        <div className="operation-card__body">
          <div className="operation-card__title">
            <span className="operation-card__name">{operation.name || operation.id}</span>
            <span className={`operation-card__tag operation-card__tag--${operation.isBackend ? 'backend' : 'model'}`}>
              {operation.isBackend ? t('activity.kind.backend') : t('activity.kind.model')}
            </span>
            {nodes.length > 1 && (
              <span className="operation-card__tag operation-card__tag--cluster">
                {t('activity.nodeCount', { count: nodes.length })}
              </span>
            )}
          </div>

          <div className="operation-card__sub">
            {failed && <span className="operation-card__error">{operation.error}</span>}
            {cancelling && <span className="operation-card__cancelling">{t('activity.verb.cancelling')}</span>}
            {!failed && !cancelling && phaseKey && <span>{t(phaseKey)}</span>}
            {!failed && !cancelling && operation.isQueued && <span>{t('activity.waitingForInstaller')}</span>}
            {!failed && byteLabel && <span className="operation-card__bytes">{byteLabel}</span>}
            {!failed && !cancelling && etaLabel && <span className="operation-card__bytes">{t('activity.timeLeft', { value: etaLabel })}</span>}
          </div>

          {showProgress && (
            <div
              className="operation-card__track"
              role="progressbar"
              aria-valuenow={Math.round(operation.progress)}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label={t('activity.progressLabel', { name: operation.name || operation.id })}
            >
              <span className="operation-card__fill" style={{ width: `${operation.progress}%` }} />
            </div>
          )}
        </div>

        <div className="operation-card__actions">
          {showProgress && <span className="operation-card__pct" aria-hidden="true">{Math.round(operation.progress)}%</span>}
          {canCancel && (
            <button
              type="button"
              className="btn btn-sm btn-danger operation-card__cancel"
              onClick={() => onCancel?.(operation.jobID)}
            >
              {t('activity.cancel')}
            </button>
          )}
          {failed && (
            <button
              type="button"
              className="operation-card__hide"
              onClick={() => onDismiss?.(operation.id)}
              title={t('activity.moveToHistory')}
              aria-label={t('activity.moveToHistory')}
            >
              <i className="fas fa-xmark" aria-hidden="true" />
            </button>
          )}
        </div>
      </div>

      {nodes.length > 0 && nodesOpen && (
        <ul className="operation-nodes-list">
          {nodes.map((node) => (
            <li key={node.node_id} className={`operation-node operation-node-${node.status}`}>
              <span className={`operation-node-status operation-node-status-${node.status}`}>
                {t(nodeStatusKeys[node.status] || 'activity.node.queued')}
              </span>
              <span className="operation-node-name">{node.node_name || node.node_id}</span>
              {(node.current || node.total) && (
                <span className="operation-node-bytes">{node.current || '?'} / {node.total || '?'}</span>
              )}
              {node.percentage > 0 && (
                <span className="operation-node-pct">{Math.round(node.percentage)}%</span>
              )}
              {node.error && (
                <span className="operation-node-error" title={node.error}>{node.error}</span>
              )}
            </li>
          ))}
        </ul>
      )}

      {nodes.length > 0 && (
        <button
          type="button"
          className="operation-card__nodes-toggle"
          aria-expanded={nodesOpen}
          onClick={() => setNodesOpen((open) => !open)}
        >
          <i className={`fas fa-chevron-${nodesOpen ? 'up' : 'down'}`} aria-hidden="true" />
          {nodesOpen ? t('activity.hideNodes') : t('activity.showNodes', { count: nodes.length })}
        </button>
      )}
    </div>
  )
}
