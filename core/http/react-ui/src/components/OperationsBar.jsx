import { useEffect, useRef, useState } from 'react'
// eslint-plugin-react is not configured here, so eslint cannot see that a
// JSX-only import is used.
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useOperations } from '../hooks/useOperations'
import { formatBytes } from '../utils/format'

const artifactPhaseKeys = {
  resolving: 'activity.phase.resolving',
  downloading: 'activity.phase.downloading',
  verifying: 'activity.phase.verifying',
  committing: 'activity.phase.committing',
  persisting: 'activity.phase.persisting',
}

// How long a finished operation stays on screen. The API drops an operation
// the instant it succeeds, so without this a fast install is a flicker.
const SUCCESS_HOLD_MS = 4000

// An unacknowledged failure outranks any progress; otherwise the API's own
// sort (progress ascending) already puts the operation that gates the batch
// first, and it is the most stable choice across polls.
//
// The strip is the only surface that picks one operation out of many. The
// Activity page shows all of them, partitioned into failed and running, so it
// has no primary to agree with.
function primaryOperation(operations) {
  if (!operations || operations.length === 0) return null
  return operations.find((op) => op.error) || operations[0]
}

export default function OperationsBar() {
  const { t } = useTranslation('admin')
  const { operations, dismissFailedOp, wasCancelled } = useOperations()
  // Which operation the user hid. Keyed by job so a different operation
  // becoming primary brings the strip back: hiding must never be able to
  // silence a later failure.
  const [hiddenJobID, setHiddenJobID] = useState(null)
  const [finished, setFinished] = useState(null)
  const previousRef = useRef(null)

  const primary = primaryOperation(operations)

  useEffect(() => {
    const previous = previousRef.current
    previousRef.current = primary

    // The previous primary is gone from the live list and it was not failing:
    // it completed. Hold it on screen briefly, then drop it.
    //
    // Unless the user cancelled it. Cancelling deletes the operation server
    // side, so a cancel and a completion are the same event here: the
    // operation simply stops being listed. Nothing in the payload separates
    // them, which is why this asks the page whether it issued the cancel.
    // Without that, cancelling the last running install put a green
    // "Installed model X" on screen for four seconds.
    if (previous && !primary && !previous.error && !wasCancelled(previous.jobID)) {
      setFinished(previous)
      const timer = setTimeout(() => setFinished(null), SUCCESS_HOLD_MS)
      return () => clearTimeout(timer)
    }
    if (primary) setFinished(null)
    return undefined
  }, [primary, wasCancelled])

  const shown = primary || finished
  if (!shown) return null
  // A job keeps its jobID when it turns into a failure, so hiding the running
  // operation would otherwise swallow that same job's error. Hiding is a way
  // to get on with your work, never a way to opt out of bad news.
  if (hiddenJobID === shown.jobID && !shown.error) return null

  const extra = Math.max(0, operations.length - 1)
  const isFinished = !primary
  const phaseKey = artifactPhaseKeys[shown.phase]
  const byteLabel = Number.isFinite(shown.currentBytes) && Number.isFinite(shown.totalBytes) && shown.totalBytes > 0
    ? `${formatBytes(shown.currentBytes)} / ${formatBytes(shown.totalBytes)}`
    : ''
  const rateLabel = Number.isFinite(shown.bytesPerSecond) && shown.bytesPerSecond > 0
    ? `${formatBytes(shown.bytesPerSecond)}/s`
    : ''
  const kind = shown.isBackend ? t('activity.kind.backend') : t('activity.kind.model')

  let modifier = ''
  let icon = null
  let verb = ''
  if (isFinished) {
    modifier = 'operations-strip--done'
    icon = <i className="fas fa-check operations-strip__icon" aria-hidden="true" />
    // The completion phrase has to match the work that just ended: a removal
    // that reports "Installed" reads as the opposite of what happened.
    if (shown.isDeletion) verb = t('activity.verb.removed', { kind })
    else if (shown.taskType === 'staging') verb = t('activity.verb.staged')
    else verb = t('activity.verb.installed', { kind })
  } else if (shown.error) {
    modifier = 'operations-strip--error'
    icon = <i className="fas fa-circle-exclamation operations-strip__icon" aria-hidden="true" />
    // Same split as the card, so the two surfaces never describe one failed
    // job differently: a removal reported as a failed install is the opposite
    // of what happened.
    if (shown.isDeletion) verb = t('activity.verb.failedRemoval', { kind })
    else if (shown.taskType === 'staging') verb = t('activity.verb.failedStaging')
    else verb = t('activity.verb.failed', { kind })
  } else if (shown.isQueued) {
    modifier = 'operations-strip--queued'
    icon = <i className="fas fa-clock operations-strip__icon" aria-hidden="true" />
    verb = t('activity.verb.queued')
  } else if (shown.taskType === 'staging') {
    modifier = 'operations-strip--staging'
    icon = <i className="fas fa-cloud-arrow-up operations-strip__icon" aria-hidden="true" />
    verb = t('activity.verb.staging')
  } else if (shown.isDeletion) {
    modifier = 'operations-strip--removing'
    icon = <i className="fas fa-trash operations-strip__icon" aria-hidden="true" />
    verb = t('activity.verb.removing', { kind })
  } else {
    icon = <span className="operations-strip__spinner" aria-hidden="true" />
    verb = t('activity.verb.installing', { kind })
  }

  // A fanned-out backend install rolls its nodes up into one phrase. Without
  // this the strip would report one node's phase as if it were the whole job,
  // and the per-node list is what the Activity page is for.
  const nodes = Array.isArray(shown.nodes) ? shown.nodes : []
  const nodesDone = nodes.filter((node) => node.status === 'success').length
  const nodeRollup = nodes.length > 1
    ? t('activity.nodesDone', { done: nodesDone, total: nodes.length })
    : ''

  const detail = shown.error
    || nodeRollup
    || (shown.taskType === 'staging' && shown.nodeName ? t('activity.toNode', { node: shown.nodeName }) : '')
    || (phaseKey ? t(phaseKey) : '')
    || (shown.isQueued ? t('activity.waitingForInstaller') : '')

  // A finished, failed or not-yet-started operation has no progress worth a
  // bar: the first is over, the second stopped where it broke and the third
  // has not moved.
  const showProgress = !isFinished && !shown.error && !shown.isQueued && shown.progress > 0

  const onHide = () => {
    // A failure is dismissed server side, which moves it into the record.
    // Anything else is hidden locally: the work carries on and the sidebar
    // count still shows it.
    if (shown.error) {
      dismissFailedOp(shown.jobID)
      return
    }
    setHiddenJobID(shown.jobID)
    setFinished(null)
  }

  return (
    // role="status" already implies a polite live region. It deliberately does
    // not cover the percentage, which changes every poll and would have a
    // screen reader re-reading the whole strip once a second.
    <div className={`operations-strip ${modifier}`.trim()} role="status">
      {icon}
      <span className="operations-strip__verb">{verb}</span>
      <span className="operations-strip__name">{shown.name || shown.id}</span>
      {detail && <span className="operations-strip__sep" aria-hidden="true">·</span>}
      {detail && <span className="operations-strip__detail">{detail}</span>}
      {byteLabel && !shown.error && (
        <span className="operations-strip__bytes">
          {byteLabel}
          {rateLabel && <span aria-live="off"> · {rateLabel}</span>}
        </span>
      )}
      <span className="operations-strip__spacer" />
      {showProgress && (
        <>
          <span className="operations-strip__pct" aria-hidden="true">{Math.round(shown.progress)}%</span>
          {/* A progressbar carries the value without a live region's chatter:
              its updates are readable on demand rather than announced. */}
          <span
            className="operations-strip__track"
            role="progressbar"
            aria-valuenow={Math.round(shown.progress)}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={t('activity.progressLabel', { name: shown.name || shown.id })}
          >
            <span className="operations-strip__fill" style={{ width: `${shown.progress}%` }} />
          </span>
        </>
      )}
      {extra > 0 && (
        <Link
          className={`operations-strip__more${shown.error ? ' operations-strip__more--neutral' : ''}`}
          to="/app/activity"
        >
          {t('activity.moreCount', { count: extra })}
        </Link>
      )}
      <button
        type="button"
        className="operations-strip__hide"
        onClick={onHide}
        title={shown.error ? t('activity.moveToHistory') : t('activity.hide')}
        aria-label={shown.error ? t('activity.moveToHistory') : t('activity.hide')}
      >
        <i className="fas fa-xmark" aria-hidden="true" />
      </button>
    </div>
  )
}
