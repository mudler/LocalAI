import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useOperations } from '../hooks/useOperations'
import { modelsApi, backendsApi, nodesApi } from '../utils/api'
// eslint-plugin-react is not configured here, so eslint cannot see that an
// import used only inside JSX is used at all. Link, PageHeader and
// OperationCard are each referenced from JSX only.
// eslint-disable-next-line no-unused-vars
import { Link, useOutletContext } from 'react-router-dom'
// eslint-disable-next-line no-unused-vars
import PageHeader from '../components/PageHeader'
// eslint-disable-next-line no-unused-vars
import OperationCard from '../components/OperationCard'

const FILTERS = [
  { id: 'all', labelKey: 'activity.filter.all' },
  { id: 'models', labelKey: 'activity.filter.models' },
  { id: 'backends', labelKey: 'activity.filter.backends' },
  { id: 'cluster', labelKey: 'activity.filter.cluster' },
]

function matchesFilter(entry, filter) {
  if (filter === 'all') return true
  if (filter === 'models') return !entry.isBackend && entry.taskType !== 'staging'
  if (filter === 'backends') return Boolean(entry.isBackend)
  // Cluster covers anything scoped to a node: staged files and node-scoped
  // backend installs.
  return entry.taskType === 'staging' || Boolean(entry.nodeID) || (Array.isArray(entry.nodes) && entry.nodes.length > 0)
}

const outcomeIcon = {
  completed: 'fas fa-check',
  failed: 'fas fa-circle-exclamation',
  cancelled: 'fas fa-ban',
}

function timeOfDay(iso) {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

// Beyond this the elapsed time is not a duration, it is a broken start stamp.
// A zero-value Go time reaching the page renders as a span of millennia, which
// the row would state as fact; the page is the last place that can refuse to.
const MAX_PLAUSIBLE_DURATION_SECONDS = 24 * 60 * 60

// Returns '' when the elapsed time cannot be trusted, which the caller renders
// as a duration-less phrase rather than as "installed in " with nothing after
// it. recordTerminal seeds StartedAt = FinishedAt and only overwrites it with a
// real stamp, so a zero span is an ordinary arrival and gets a floor instead.
function durationLabel(record) {
  const started = new Date(record.startedAt).getTime()
  const finished = new Date(record.finishedAt).getTime()
  if (Number.isNaN(started) || Number.isNaN(finished) || finished < started) return ''
  const seconds = Math.round((finished - started) / 1000)
  if (seconds > MAX_PLAUSIBLE_DURATION_SECONDS) return ''
  if (seconds < 1) return '< 1s'
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}

// Cancellation is tested before the task type on purpose: a deletion cancelled
// mid-flight must report the cancellation, not "removed". recordTerminal
// produces exactly that pair, and the row's ban icon would otherwise sit beside
// text claiming work that never happened.
function recordSummary(record, t) {
  if (record.outcome === 'failed') return t('activity.rowFailed', { error: record.error })
  if (record.outcome === 'cancelled') return t('activity.rowCancelled')
  if (record.taskType === 'deletion') return t('activity.rowRemoved')
  const duration = durationLabel(record)
  return duration ? t('activity.rowInstalled', { duration }) : t('activity.rowInstalledPlain')
}

// Retry only ever means "install this again". A failed deletion would need the
// delete endpoint and a staging operation is driven by the router rather than
// by a user action, so neither is retryable from here.
function isRetryable(op) {
  return Boolean(op.error) && !op.isDeletion && op.taskType !== 'staging'
}

export default function Activity() {
  const { t } = useTranslation('admin')
  const outlet = useOutletContext()
  const addToast = outlet?.addToast
  const { operations, history, fetchHistory, clearHistory, cancelOperation, pauseOperation, dismissFailedOp } = useOperations()
  const [filter, setFilter] = useState('all')

  useEffect(() => { fetchHistory() }, [fetchHistory])

  const retryOperation = useCallback(async (op) => {
    // Dismiss before reinstalling, never after: the reinstall reuses the same
    // opcache key, and overwriting a failed entry in place skips recordTerminal
    // so the failure would never reach the record. Dismissing first is what
    // puts it there.
    //
    // By jobID, because the guarantee only holds while both calls address the
    // same job. Two ops can share an id (a local and a node-scoped install of
    // one backend), and dismissing by id could retire the other one instead,
    // leaving this failure to be overwritten in place by the reinstall below.
    await dismissFailedOp(op.jobID)
    // fullName is the gallery-qualified id the install endpoints expect;
    // `name` has the repo prefix stripped for display. Node-scoped ops already
    // had their prefix removed server side, so fullName is the bare slug there.
    const target = op.fullName || op.id
    try {
      if (op.nodeID) {
        await nodesApi.installBackend(op.nodeID, target)
      } else if (op.isBackend) {
        await backendsApi.install(target)
      } else {
        // The variant is not on the payload YET, so a pinned model retries as
        // an auto-select: someone who chose a specific quant, watched it fail
        // at 90% and pressed Retry gets a different build, with nothing on
        // screen saying so. Worth closing, and close to closed: ui_api.go
        // already reads ?variant= at enqueue and stores it on the ManagementOp,
        // so it only has to reach the /api/operations payload and this call.
        // Until then Retry stays, because nothing here distinguishes a pinned
        // install from an unpinned one and dropping it would cost every model
        // the button, including the common plain install that hit a network
        // error.
        await modelsApi.install(target)
      }
    } catch (err) {
      addToast?.(t('activity.retryFailed', { message: err.message }), 'error')
    }
  }, [dismissFailedOp, addToast, t])

  const live = useMemo(
    () => operations.filter((op) => !op.error && matchesFilter(op, filter)),
    [operations, filter],
  )
  const failing = useMemo(
    () => operations.filter((op) => op.error && matchesFilter(op, filter)),
    [operations, filter],
  )
  const records = useMemo(
    () => history.filter((entry) => matchesFilter(entry, filter)),
    [history, filter],
  )

  // The header describes the instance, not the current chip, which is what the
  // Clear-history button beside it already does. A filtered count here would
  // report "Nothing running" while two model installs were running just
  // offscreen; the filtered view explains itself through the sections and the
  // filtered empty state instead.
  //
  // "Nothing running" must also not be said while a failure is waiting for a
  // decision, so both counts get a clause. Each clause is dropped when its
  // count is zero rather than rendered as a literal 0: the ordinary happy path
  // would otherwise put "0 needs attention" under the page title on every
  // render, which reads as a report about failures rather than the absence of
  // one.
  const runningTotal = operations.filter((op) => !op.error).length
  const failingTotal = operations.length - runningTotal
  const summaryClauses = []
  if (runningTotal > 0) summaryClauses.push(t('activity.summaryRunning', { count: runningTotal }))
  if (failingTotal > 0) summaryClauses.push(t('activity.summaryFailed', { count: failingTotal }))
  let supporting
  if (summaryClauses.length > 0) supporting = summaryClauses.join(' ')
  else if (history.length > 0) supporting = t('activity.summaryQuiet', { count: history.length })
  // Saying "0 operations since startup" directly above "No operations since
  // startup" states the same nothing twice.
  else supporting = t('activity.summaryIdle')

  return (
    <div className="page page--wide activity-page">
      <PageHeader
        title={t('activity.title')}
        supporting={supporting}
        actions={history.length > 0 ? (
          <button type="button" className="btn btn-secondary" onClick={clearHistory}>
            {t('activity.clearHistory')}
          </button>
        ) : null}
      />

      <div className="activity-filters">
        {FILTERS.map((entry) => (
          <button
            key={entry.id}
            type="button"
            className="activity-chip"
            aria-pressed={filter === entry.id}
            onClick={() => setFilter(entry.id)}
          >
            {t(entry.labelKey)}
          </button>
        ))}
      </div>

      {live.length > 0 && (
        <section className="activity-section">
          <h2 className="activity-section__title">
            {t('activity.inProgress')} <span className="activity-section__count">{live.length}</span>
          </h2>
          {live.map((op) => (
            <OperationCard key={op.jobID || op.id} operation={op} onCancel={cancelOperation} onPause={pauseOperation} />
          ))}
        </section>
      )}

      {failing.length > 0 && (
        <section className="activity-section">
          <h2 className="activity-section__title">
            {t('activity.needsAttention')} <span className="activity-section__count">{failing.length}</span>
          </h2>
          {failing.map((op) => (
            <OperationCard
              key={op.jobID || op.id}
              operation={op}
              onDismiss={dismissFailedOp}
              onRetry={isRetryable(op) ? retryOperation : undefined}
            />
          ))}
        </section>
      )}

      {records.length > 0 && (
        <section className="activity-section">
          <h2 className="activity-section__title">
            {t('activity.record')} <span className="activity-section__count">{records.length}</span>
          </h2>
          <div className="activity-rows">
            {records.map((record) => (
              <div key={record.jobID} className="activity-row">
                <i
                  className={`${outcomeIcon[record.outcome] || 'fas fa-check'} activity-row__icon activity-row__icon--${record.outcome}`}
                  aria-hidden="true"
                />
                <span className="activity-row__name">
                  {record.name}
                  <small>{recordSummary(record, t)}</small>
                </span>
                <span className="activity-row__when">{timeOfDay(record.finishedAt)}</span>
                <Link className="activity-row__action" to={record.isBackend ? '/app/backends' : '/app/models'}>
                  {record.isBackend ? t('activity.viewInBackends') : t('activity.viewInModels')}
                </Link>
              </div>
            ))}
          </div>
          <p className="activity-note">{t('activity.historyNote')}</p>
        </section>
      )}

      {/* A chip that matches nothing is not an empty system. Telling someone
          with three model installs on record that nothing has ever run, while
          the line above them counts those same three, is simply false. */}
      {live.length === 0 && failing.length === 0 && records.length === 0 && (
        filter === 'all' ? (
          <div className="activity-empty">
            <i className="fas fa-download activity-empty__icon" aria-hidden="true" />
            <p className="activity-empty__title">{t('activity.emptyTitle')}</p>
            <p className="activity-empty__body">{t('activity.emptyBody')}</p>
            <Link className="btn btn-primary" to="/app/models">{t('activity.browseModels')}</Link>
          </div>
        ) : (
          <div className="activity-empty activity-empty--filtered">
            <i className="fas fa-filter activity-empty__icon" aria-hidden="true" />
            <p className="activity-empty__title">{t('activity.emptyFiltered')}</p>
            <button type="button" className="btn btn-secondary" onClick={() => setFilter('all')}>
              {t('activity.showAll')}
            </button>
          </div>
        )
      )}
    </div>
  )
}
