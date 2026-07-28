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

function durationLabel(record) {
  const started = new Date(record.startedAt).getTime()
  const finished = new Date(record.finishedAt).getTime()
  if (Number.isNaN(started) || Number.isNaN(finished) || finished <= started) return ''
  const seconds = Math.round((finished - started) / 1000)
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
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
  const { operations, history, fetchHistory, clearHistory, cancelOperation, dismissFailedOp } = useOperations()
  const [filter, setFilter] = useState('all')

  useEffect(() => { fetchHistory() }, [fetchHistory])

  const retryOperation = useCallback(async (op) => {
    // Dismiss before reinstalling, never after: the reinstall reuses the same
    // opcache key, and overwriting a failed entry in place skips recordTerminal
    // so the failure would never reach the record. Dismissing first is what
    // puts it there.
    await dismissFailedOp(op.id)
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
        // Known limitation: /api/operations carries no variant, so a
        // variant-pinned model retries as an auto-select. Accepted rather than
        // dropping retry for every model, because auto-select reproduces the
        // gallery's own default and pinning is the rarer path; a user who
        // pinned a build can reinstall it from the Models picker.
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

  // "Nothing running" must not be said while a failure is waiting for a
  // decision, so the busy line covers both.
  const supporting = live.length > 0 || failing.length > 0
    ? t('activity.summaryBusy', { running: live.length, failed: failing.length })
    : t('activity.summaryQuiet', { count: history.length })

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
            <OperationCard key={op.jobID || op.id} operation={op} onCancel={cancelOperation} />
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
                  <small>
                    {record.outcome === 'failed'
                      ? t('activity.rowFailed', { error: record.error })
                      : record.taskType === 'deletion'
                        ? t('activity.rowRemoved')
                        : record.outcome === 'cancelled'
                          ? t('activity.rowCancelled')
                          : t('activity.rowInstalled', { duration: durationLabel(record) })}
                  </small>
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

      {live.length === 0 && failing.length === 0 && records.length === 0 && (
        <div className="activity-empty">
          <i className="fas fa-download activity-empty__icon" aria-hidden="true" />
          <p className="activity-empty__title">{t('activity.emptyTitle')}</p>
          <p className="activity-empty__body">{t('activity.emptyBody')}</p>
          <Link className="btn btn-primary" to="/app/models">{t('activity.browseModels')}</Link>
        </div>
      )}
    </div>
  )
}
