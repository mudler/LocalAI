/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useActivityFeed } from '../../hooks/useActivityFeed'
import { isRetryable, useOperationActions } from '../../hooks/useOperationActions'
import { CANCEL_UNDO_MS, useUndoableCancel } from '../../hooks/useUndoableCancel'
import { relativeDate } from '../../utils/fileBrowser'
import { cssVars } from '../../utils/modelLedger'
import {
  canCancel, detailOf, failedTitleOf, nameOf, reasonOf, recentRecords, recentTitle, splitOps, titleOf, verbOf,
} from '../../utils/opsActivity'
import HomeUndoToast from '../home/HomeUndoToast'
import Icon from '../Icon'
import { Button, EmptyState } from '../ui'
import OpsRing from './OpsRing'
import './activity.css'

const VERB_ICON = { installing: 'download', upgrading: 'arrow-up', removing: 'trash', staging: 'cloud-upload' }

// One operation that runs or waits: a ring, what it does, how far it is, and
// Cancel. The bar says the same as the percentage and carries the value for a
// screen reader without a live region: it is read on demand, never announced
// on every poll.
function RunningRow({ op, t, cancelling, onCancel }) {
  const queued = Boolean(op.isQueued)
  const pct = Math.round(op.progress || 0)
  const showBar = !queued && pct > 0
  const name = nameOf(op)
  const detail = cancelling ? t('activity.cancelling', { ns: 'operate' }) : detailOf(op, t)
  const title = titleOf(op, t)
  return (
    <li className={`nx-op${queued ? ' is-queued' : ''}`} data-testid="inbox-op" data-job={op.jobID}>
      <span className="nx-tile nx-tile--accent nx-op__tile" aria-hidden="true">
        {!queued && <OpsRing value={op.progress || 0} className="ops-ring--tile" />}
        <Icon name={queued ? 'clock' : VERB_ICON[verbOf(op)]} />
      </span>
      <span className="nx-op__text">
        <span className="nx-op__head">
          <span className="nx-op__title" title={title}>{title}</span>
          {showBar && <span className="nx-op__pct" aria-hidden="true">{pct}%</span>}
        </span>
        <span className="nx-op__detail" title={detail}>{detail}</span>
        {showBar && (
          <span
            className="nx-op__track"
            role="progressbar"
            aria-valuenow={pct}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={t('activity.progressLabel', { ns: 'operate', name })}
          >
            <span className="nx-op__fill" style={cssVars({ '--op-w': `${pct}%` })} />
          </span>
        )}
      </span>
      {canCancel(op, cancelling) && (
        <Button
          variant="ghost" size="sm" className="nx-op__act" data-testid="inbox-op-cancel"
          aria-label={t('activity.cancelLabel', { ns: 'operate', name })}
          onClick={() => onCancel(op.jobID)}
        >
          {t('activity.cancel', { ns: 'operate' })}
        </Button>
      )}
    </li>
  )
}

// One failed operation, pinned until it is dismissed: what failed, why, and
// the two things a person can do. Retry is drawn only where there is a call
// behind it (an install or an upgrade; not a removal or a staging job).
function FailedRow({ op, t, onRetry, onDismiss }) {
  const name = nameOf(op)
  const reason = reasonOf(op, t)
  return (
    <li className="nx-op nx-op--failed" data-testid="inbox-op-failed" data-job={op.jobID}>
      <span className="nx-tile nx-tile--error" aria-hidden="true"><Icon name="alert-circle" /></span>
      <span className="nx-op__text">
        <span className="nx-op__head"><span className="nx-op__title" title={failedTitleOf(op, t)}>{failedTitleOf(op, t)}</span></span>
        {reason && <span className="nx-op__reason" title={reason}>{reason}</span>}
      </span>
      <span className="nx-op__acts">
        {isRetryable(op) && (
          <Button
            size="sm" icon="refresh" data-testid="inbox-op-retry"
            aria-label={t('activity.retryLabel', { ns: 'operate', name })}
            onClick={() => onRetry(op)}
          >
            {t('activity.retry', { ns: 'operate' })}
          </Button>
        )}
        <Button
          variant="ghost" size="sm" icon="close" className="nx-act" data-testid="inbox-op-dismiss"
          title={t('activity.moveToHistory', { ns: 'operate' })}
          aria-label={t('activity.dismissLabel', { ns: 'operate', name })}
          onClick={() => onDismiss(op.jobID)}
        />
      </span>
    </li>
  )
}

function RecentRow({ rec, t, locale }) {
  const when = relativeDate(rec.finishedAt, { locale })
  return (
    <li className="nx-op nx-op--recent" data-testid="inbox-op-recent">
      <span className="nx-tile nx-tile--ok nx-tile--sm" aria-hidden="true"><Icon name="check-circle" /></span>
      <span className="nx-op__text">
        <span className="nx-op__title" title={recentTitle(rec, t)}>{recentTitle(rec, t)}</span>
      </span>
      <time className="nx-op__when" dateTime={rec.finishedAt} title={when.exact}>{when.label}</time>
    </li>
  )
}

// The Activity tab of the notification inbox: what needs a decision, what
// runs, and what just finished, for an administrator. It reads the same feed as
// the Activity page and calls the same actions, so the two cannot disagree.
// The undo window of a cancel is the page's too: the cancel waits, and leaving
// (closing the panel) makes it final, as leaving the page does.
export default function ActivityTab({ onClose }) {
  const { t, i18n } = useTranslation(['chats', 'operate'])
  const feed = useActivityFeed()
  const { fetchHistory } = feed
  const [note, setNote] = useState('')
  const addToast = useCallback((message, type) => setNote(type === 'error' ? message : ''), [])
  const { retry, dismiss } = useOperationActions(addToast)
  const cancelling = useUndoableCancel({ operations: feed.operations, cancel: feed.cancelOperation })

  useEffect(() => { fetchHistory() }, [fetchHistory])

  const { failed, live } = useMemo(() => splitOps(feed.operations), [feed.operations])
  const recent = useMemo(() => recentRecords(feed.history), [feed.history])
  const empty = failed.length + live.length + recent.length === 0

  const onRetry = (op) => { setNote(''); return retry(op) }

  return (
    <div className="nx-ops" data-testid="inbox-activity">
      {note && <p className="nx-note nx-note--error" role="alert"><Icon name="alert-circle" />{note}</p>}
      {failed.length > 0 && (
        <section className="nx-group" aria-labelledby="nx-ops-failed">
          <h3 id="nx-ops-failed" className="nx-day nx-day--error">{t('inbox.ops.needsAttention')}</h3>
          <ul className="nx-list">
            {failed.map((op) => <FailedRow key={op.jobID || op.id} op={op} t={t} onRetry={onRetry} onDismiss={dismiss} />)}
          </ul>
        </section>
      )}
      {live.length > 0 && (
        <section className="nx-group" aria-labelledby="nx-ops-live">
          <h3 id="nx-ops-live" className="nx-day">{t('inbox.ops.inProgress')} <span className="nx-day__n">{live.length}</span></h3>
          <ul className="nx-list">
            {live.map((op) => (
              <RunningRow
                key={op.jobID || op.id} op={op} t={t}
                cancelling={cancelling.waitingFor?.jobID === op.jobID}
                onCancel={cancelling.request}
              />
            ))}
          </ul>
        </section>
      )}
      {recent.length > 0 && (
        <section className="nx-group" aria-labelledby="nx-ops-recent">
          <h3 id="nx-ops-recent" className="nx-day">{t('inbox.ops.recent.title')}</h3>
          <ul className="nx-list">
            {recent.map((rec) => <RecentRow key={rec.jobID} rec={rec} t={t} locale={i18n.language} />)}
          </ul>
        </section>
      )}
      {empty && (
        <EmptyState variant="no-results" icon="check-circle" level={3} title={t('inbox.ops.emptyTitle')} body={t('inbox.ops.emptyBody')} testId="inbox-activity-empty" />
      )}
      <div className="nx-seeall">
        <Button variant="ghost" size="sm" iconEnd="arrow-right" to="/app/activity" data-testid="inbox-see-all" onClick={() => onClose(false)}>
          {t('inbox.ops.seeAll')}
        </Button>
      </div>
      {cancelling.waitingFor && (
        <HomeUndoToast
          key={cancelling.waitingFor.jobID}
          message={t('activity.cancellingToast', { ns: 'operate', name: cancelling.waitingFor.name })}
          undoLabel={t('activity.undo', { ns: 'operate' })}
          dismissLabel={t('activity.cancelNow', { ns: 'operate' })}
          duration={CANCEL_UNDO_MS}
          testId="activity-undo-toast"
          onUndo={cancelling.undo}
          onExpire={cancelling.commit}
        />
      )}
    </div>
  )
}
