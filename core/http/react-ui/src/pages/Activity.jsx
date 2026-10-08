/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useOutletContext } from 'react-router-dom'
import { useOperations } from '../hooks/useOperations'
import { useOperationActions, isRetryable } from '../hooks/useOperationActions'
import { CANCEL_UNDO_MS, useUndoableCancel } from '../hooks/useUndoableCancel'
import { backendsApi, modelsApi } from '../utils/api'
import PageHeader from '../components/PageHeader'
import OperationCard from '../components/OperationCard'
import HomeUndoToast from '../components/home/HomeUndoToast'
import Icon from '../components/Icon'
import './operate.css'

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
  completed: 'check-circle',
  failed: 'alert-circle',
  cancelled: 'ban',
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

export default function Activity() {
  const { t } = useTranslation('operate')
  const outlet = useOutletContext()
  const addToast = outlet?.addToast
  const { operations, history, fetchHistory, clearHistory, cancelOperation, pauseOperation } = useOperations()
  const { retry, dismiss } = useOperationActions(addToast)
  const [filter, setFilter] = useState('all')

  useEffect(() => { fetchHistory() }, [fetchHistory])

  const cancelling = useUndoableCancel({ operations, cancel: cancelOperation })

  // Starting a cancelled install again. A job that was paused kept its partial
  // download and continues; one that was cancelled starts over. The record does
  // not say which, so the button says what it does in both cases.
  const startAgain = useCallback(async (record) => {
    try {
      if (record.nodeID) throw new Error(t('activity.startAgainNode'))
      if (record.isBackend) await backendsApi.install(record.id || record.name)
      else await modelsApi.install(record.id || record.name)
      addToast?.(t('activity.startedAgain', { name: record.name }), 'info')
    } catch (err) {
      addToast?.(t('activity.retryFailed', { message: err.message }), 'error')
    }
  }, [addToast, t])

  const liveOps = useMemo(
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
  // count is zero rather than rendered as a literal 0.
  const runningTotal = operations.filter((op) => !op.error).length
  const failingTotal = operations.length - runningTotal
  const summaryClauses = []
  if (runningTotal > 0) summaryClauses.push(t('activity.summaryRunning', { count: runningTotal }))
  if (failingTotal > 0) summaryClauses.push(t('activity.summaryFailed', { count: failingTotal }))
  let supporting
  if (summaryClauses.length > 0) supporting = summaryClauses.join(' ')
  else if (history.length > 0) supporting = t('activity.summaryQuiet', { count: history.length })
  else supporting = t('activity.summaryIdle')

  return (
    <div className="page page--wide op-page activity-page">
      <PageHeader
        eyebrow={null}
        title={t('activity.title')}
        supporting={supporting}
        actions={history.length > 0 ? (
          <button type="button" className="dk-btn dk-btn--ghost" onClick={clearHistory}>
            {t('activity.clearHistory')}
          </button>
        ) : null}
      />

      <div className="activity-filters" role="group" aria-label={t('activity.filterLabel')}>
        {FILTERS.map((entry) => (
          <button
            key={entry.id}
            type="button"
            className="dk-chip activity-chip"
            aria-pressed={filter === entry.id}
            onClick={() => setFilter(entry.id)}
          >
            {t(entry.labelKey)}
          </button>
        ))}
      </div>

      {liveOps.length > 0 && (
        <section className="activity-section" aria-labelledby="activity-live">
          <h2 className="activity-section__title" id="activity-live">
            {t('activity.inProgress')} <span className="activity-section__count">{liveOps.length}</span>
          </h2>
          <div className="op-list">
            {liveOps.map((op) => (
              <OperationCard
                key={op.jobID || op.id}
                operation={op}
                cancelling={cancelling.waitingFor?.jobID === op.jobID}
                onCancel={cancelling.request}
                onPause={pauseOperation}
              />
            ))}
          </div>
        </section>
      )}

      {failing.length > 0 && (
        <section className="activity-section" aria-labelledby="activity-failing">
          <h2 className="activity-section__title" id="activity-failing">
            {t('activity.needsAttention')} <span className="activity-section__count">{failing.length}</span>
          </h2>
          <div className="op-list">
            {failing.map((op) => (
              <OperationCard
                key={op.jobID || op.id}
                operation={op}
                onDismiss={dismiss}
                onRetry={isRetryable(op) ? retry : undefined}
              />
            ))}
          </div>
        </section>
      )}

      {records.length > 0 && (
        <section className="activity-section" aria-labelledby="activity-record">
          <h2 className="activity-section__title" id="activity-record">
            {t('activity.record')} <span className="activity-section__count">{records.length}</span>
          </h2>
          <div className="op-list activity-rows">
            {records.map((record) => (
              <div key={record.jobID} className="activity-row">
                <Icon name={outcomeIcon[record.outcome] || 'check-circle'} className={`activity-row__icon activity-row__icon--${record.outcome}`} />
                <span className="activity-row__name">
                  {record.name}
                  <small>{recordSummary(record, t)}</small>
                </span>
                <span className="activity-row__when">{timeOfDay(record.finishedAt)}</span>
                <span className="activity-row__acts">
                  {record.outcome === 'cancelled' && record.taskType !== 'deletion' && record.taskType !== 'staging' && (
                    <button
                      type="button"
                      className="dk-btn dk-btn--ghost dk-btn--sm activity-row__resume"
                      title={t('activity.startAgainTitle')}
                      onClick={() => startAgain(record)}
                    >
                      {t('activity.startAgain')}
                    </button>
                  )}
                  <Link className="dk-link activity-row__action" to={record.isBackend ? '/app/backends' : '/app/models'}>
                    {record.isBackend ? t('activity.viewInBackends') : t('activity.viewInModels')}
                  </Link>
                </span>
              </div>
            ))}
          </div>
          <p className="activity-note">{t('activity.historyNote')}</p>
        </section>
      )}

      {/* A chip that matches nothing is not an empty system. Telling someone
          with three model installs on record that nothing has ever run, while
          the line above them counts those same three, is simply false. */}
      {liveOps.length === 0 && failing.length === 0 && records.length === 0 && (
        filter === 'all' ? (
          <div className="dk-empty activity-empty">
            <div className="dk-empty-icon"><Icon name="download" /></div>
            <h2 className="dk-empty-title activity-empty__title">{t('activity.emptyTitle')}</h2>
            <p className="dk-empty-text activity-empty__body">{t('activity.emptyBody')}</p>
            <Link className="dk-btn dk-btn--primary" to="/app/models">{t('activity.browseModels')}</Link>
          </div>
        ) : (
          <div className="dk-empty activity-empty activity-empty--filtered">
            <div className="dk-empty-icon"><Icon name="filter" /></div>
            <h2 className="dk-empty-title activity-empty__title">{t('activity.emptyFiltered')}</h2>
            <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setFilter('all')}>
              {t('activity.showAll')}
            </button>
          </div>
        )
      )}

      {cancelling.waitingFor && (
        <HomeUndoToast
          key={cancelling.waitingFor.jobID}
          message={t('activity.cancellingToast', { name: cancelling.waitingFor.name })}
          undoLabel={t('activity.undo')}
          dismissLabel={t('activity.cancelNow')}
          duration={CANCEL_UNDO_MS}
          testId="activity-undo-toast"
          onUndo={cancelling.undo}
          onExpire={cancelling.commit}
        />
      )}
    </div>
  )
}
