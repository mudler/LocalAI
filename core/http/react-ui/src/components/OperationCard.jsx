import { useId, useState } from 'react'
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

export default function OperationCard({ operation, onCancel, onPause, onDismiss, onRetry }) {
  const { t } = useTranslation('admin')
  const nodes = Array.isArray(operation.nodes) ? operation.nodes : []
  // Holds only what the user chose. The default has to stay a live
  // expression: an operation appears in /api/operations as soon as it is
  // admitted, but its nodes are filled in later, when the fan-out starts
  // reporting. State seeded at mount would latch on the empty list.
  const [nodesOpenOverride, setNodesOpenOverride] = useState(null)
  const listId = useId()

  const failed = Boolean(operation.error)
  const name = operation.name || operation.id
  const kind = operation.isBackend ? t('activity.kind.backend') : t('activity.kind.model')

  // Same chain as the one-line strip, so the two never describe one job
  // differently. Without it a removal and an install render identically: a
  // deletion has no phase, no bytes and no nodes to tell them apart.
  let icon
  let verb
  if (failed) {
    icon = <i className="fas fa-circle-exclamation operation-card__icon operation-card__icon--error" aria-hidden="true" />
    // The failure phrase has to name the work that actually failed. A removal
    // or a staging job reported as a failed install describes the opposite of
    // what happened, and would make the missing Retry button look like a bug.
    if (operation.isDeletion) verb = t('activity.verb.failedRemoval', { kind })
    else if (operation.taskType === 'staging') verb = t('activity.verb.failedStaging')
    else verb = t('activity.verb.failed', { kind })
  } else if (operation.isQueued) {
    icon = <i className="fas fa-clock operation-card__icon" aria-hidden="true" />
    verb = t('activity.verb.queued')
  } else if (operation.taskType === 'staging') {
    icon = <i className="fas fa-cloud-arrow-up operation-card__icon operation-card__icon--staging" aria-hidden="true" />
    verb = t('activity.verb.staging')
  } else if (operation.isDeletion) {
    icon = <i className="fas fa-trash operation-card__icon operation-card__icon--removing" aria-hidden="true" />
    verb = t('activity.verb.removing', { kind })
  } else {
    icon = <span className="operation-card__spinner" aria-hidden="true" />
    verb = t('activity.verb.installing', { kind })
  }

  const byteLabel = Number.isFinite(operation.currentBytes) && Number.isFinite(operation.totalBytes) && operation.totalBytes > 0
    ? `${formatBytes(operation.currentBytes)} / ${formatBytes(operation.totalBytes)}`
    : ''
  const phaseKey = phaseKeys[operation.phase]
  const etaLabel = formatEta(operation.etaSeconds)
  const rateLabel = Number.isFinite(operation.bytesPerSecond) && operation.bytesPerSecond > 0
    ? `${formatBytes(operation.bytesPerSecond)}/s`
    : ''
  // Same call the strip makes, for the same reason: a failed operation
  // stopped where it broke and a queued one has not moved, so neither has a
  // bar worth drawing.
  const showProgress = !failed && !operation.isQueued && operation.progress > 0
  const canCancel = operation.cancellable && !failed
  // Retrying means reconstructing an install call out of the operation, which
  // is page knowledge. The card offers the button only when the page handed it
  // a handler, so the control can never be present with nothing behind it.
  const canRetry = failed && typeof onRetry === 'function'

  // One node needs no disclosure: the single row is the whole story, and a
  // count-less string would render "Show 1 nodes".
  const showNodesToggle = nodes.length > 1
  const nodesOpen = nodesOpenOverride ?? (nodes.length > 0 && nodes.length <= 4)
  const showNodesList = nodes.length > 0 && (nodesOpen || !showNodesToggle)

  return (
    <div className={`operation-card${failed ? ' operation-card--error' : ''}`}>
      <div className="operation-card__main">
        {icon}

        <div className="operation-card__body">
          <div className="operation-card__title">
            <span className="operation-card__name">{name}</span>
            <span className={`operation-card__tag operation-card__tag--${operation.isBackend ? 'backend' : 'model'}`}>
              {kind}
            </span>
            {nodes.length > 1 && (
              <span className="operation-card__tag operation-card__tag--cluster">
                {t('activity.nodeCount', { count: nodes.length })}
              </span>
            )}
          </div>

          <div className="operation-card__sub">
            <span className="operation-card__verb">{verb}</span>
            {operation.nodeName && <span>{t('activity.toNode', { node: operation.nodeName })}</span>}
            {failed && <span className="operation-card__error" title={operation.error}>{operation.error}</span>}
            {!failed && phaseKey && <span>{t(phaseKey)}</span>}
            {/* Phases and byte counters exist only on the managed-artifact
                path, so a legacy files: gallery model and every backend
                install would otherwise say nothing beyond the verb. The
                server's own message is the only detail those jobs have. It is
                skipped while queued because there it is just "queued", which
                the line below already says in the user's language. */}
            {!failed && !phaseKey && !operation.isQueued && operation.message && (
              <span className="operation-card__message" title={operation.message}>{operation.message}</span>
            )}
            {!failed && operation.isQueued && <span>{t('activity.waitingForInstaller')}</span>}
            {!failed && byteLabel && (
              <span className="operation-card__bytes">
                {byteLabel}{rateLabel && ` · ${rateLabel}`}
              </span>
            )}
            {!failed && etaLabel && <span className="operation-card__bytes">{t('activity.timeLeft', { value: etaLabel })}</span>}
          </div>

          {showProgress && (
            <div
              className="operation-card__track"
              role="progressbar"
              aria-valuenow={Math.round(operation.progress)}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label={t('activity.progressLabel', { name })}
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
              className="btn btn-sm btn-secondary operation-card__pause"
              onClick={() => onPause?.(operation.jobID)}
              aria-label={t('activity.pauseLabel', { name })}
            >
              {t('activity.pause')}
            </button>
          )}
          {canCancel && (
            // A page of cards would otherwise hand a screen reader a list of
            // identical "Cancel" buttons with nothing to tell them apart.
            <button
              type="button"
              className="btn btn-sm btn-danger operation-card__cancel"
              onClick={() => onCancel?.(operation.jobID)}
              aria-label={t('activity.cancelLabel', { name })}
            >
              {t('activity.cancel')}
            </button>
          )}
          {canRetry && (
            <button
              type="button"
              className="btn btn-sm btn-secondary operation-card__retry"
              onClick={() => onRetry(operation)}
              aria-label={t('activity.retryLabel', { name })}
            >
              {t('activity.retry')}
            </button>
          )}
          {failed && (
            <button
              type="button"
              className="operation-card__hide"
              onClick={() => onDismiss?.(operation.jobID)}
              title={t('activity.moveToHistory')}
              aria-label={t('activity.moveToHistory')}
            >
              <i className="fas fa-xmark" aria-hidden="true" />
            </button>
          )}
        </div>
      </div>

      {/* The disclosure sits above what it discloses: a control that follows
          its own region reads backwards to anyone moving through the page. */}
      {showNodesToggle && (
        <button
          type="button"
          className="operation-card__nodes-toggle"
          aria-expanded={nodesOpen}
          aria-controls={listId}
          onClick={() => setNodesOpenOverride(!nodesOpen)}
        >
          <i className={`fas fa-chevron-${nodesOpen ? 'up' : 'down'}`} aria-hidden="true" />
          {nodesOpen ? t('activity.hideNodes') : t('activity.showNodes', { count: nodes.length })}
        </button>
      )}

      {/* Hidden rather than unmounted while collapsed, so the toggle's
          aria-controls always points at something that exists. */}
      {nodes.length > 0 && (
        <ul className="operation-nodes-list" id={listId} hidden={!showNodesList}>
          {nodes.map((node) => (
            <li key={node.node_id} className={`operation-node operation-node-${node.status}`}>
              <span className={`operation-node-status operation-node-status-${node.status}`}>
                {/* An unmapped status is shown as it arrived: inventing
                    "queued" for it would report a state the node is not in. */}
                {nodeStatusKeys[node.status] ? t(nodeStatusKeys[node.status]) : node.status}
              </span>
              <span className="operation-node-name">{node.node_name || node.node_id}</span>
              {node.file_name && (
                <span className="operation-node-file" title={node.file_name}>{node.file_name}</span>
              )}
              {(node.current || node.total) && (
                <span className="operation-node-bytes">{node.current || '?'} / {node.total || '?'}</span>
              )}
              {node.percentage > 0 && (
                <span className="operation-node-pct">{Math.round(node.percentage)}%</span>
              )}
              {node.error && (
                <span className="operation-node-error" title={node.error}>{node.error}</span>
              )}
              {node.percentage > 0 && node.percentage < 100 && (
                <div className="operation-node-bar-container">
                  <div className="operation-node-bar" style={{ width: `${node.percentage}%` }} />
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
