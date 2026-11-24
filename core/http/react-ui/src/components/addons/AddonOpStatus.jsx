/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { Button } from '../ui'
import { cssVars } from '../../utils/modelLedger'
import { upgradeFailureWords } from './updateReview'
import { describeOutcome } from './errors'

// The phases of an install that /api/operations reports, worded as the
// operation cards of the Activity page word them.
const PHASES = new Set(['resolving', 'downloading', 'verifying', 'committing', 'persisting'])

// What an add-on operation says now, for a row, a banner and a dialog: waiting
// for a slot, running with its progress, canceling, or the result of one that
// failed. `op` is an entry of the upgrade store with what /api/operations says
// about its job laid over it; `note` is a finished operation that was canceled.
// The words are the ones the Updates and Activity pages use for the same state.
// Cancel is the existing cancel of the operation, offered while it waits and
// while it can still stop.
export default function AddonOpStatus({ op, note = null, name, version = '', onCancel, onDismiss, onDismissNote }) {
  const { t } = useTranslation(['addons', 'updates', 'operate'])
  if (!op && !note) return null
  if (!op) {
    return (
      <div className="ad-op" data-testid="addon-op-note" data-state="cancelled">
        <p className="ad-entry__note" role="status">{t('updates.cancelledNote', { name, version })}</p>
        {onDismissNote && <Button size="sm" variant="ghost" onClick={() => onDismissNote(note)} data-testid="addon-op-note-dismiss">{t('catalog.close')}</Button>}
      </div>
    )
  }
  const failed = op.state === 'failed'
  const cancelling = op.state === 'cancelling'
  const queued = !failed && !cancelling && op.phase === 'queued'
  const state = failed ? 'failed' : cancelling ? 'cancelling' : queued ? 'queued' : 'running'
  const progress = Math.max(0, Math.min(100, Math.round(op.progress || 0)))
  let text
  if (failed) {
    text = t('updates:row.failedWhy', { message: op.fileFailure ? describeOutcome(op.fileFailure.outcome, t, op.fileFailure.error) : upgradeFailureWords(op.errorKey, t) })
  } else if (cancelling) {
    text = t('updates.cancelling')
  } else if (queued) {
    text = t('updates:row.queued')
  } else {
    const phase = PHASES.has(op.stage) ? t(`operate:activity.phase.${op.stage}`) : ''
    text = phase ? t('updates:row.updatingPhase', { phase }) : t('updates:row.updatingPlain')
    if (progress > 0) text = `${text} · ${progress}%`
  }
  const canCancel = !failed && !cancelling && op.cancellable && Boolean(onCancel)
  return (
    <div className="ad-op" data-testid="addon-op" data-state={state} data-job={op.jobID}>
      <p className="ad-entry__note" data-tone={failed ? 'error' : undefined} role={failed ? 'alert' : 'status'} data-testid="addon-op-text">{text}</p>
      {!failed && (
        <span
          className="dk-progress ad-op__bar"
          role="progressbar"
          aria-label={t('updates:row.progress', { name })}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={queued ? 0 : progress}
          data-queued={queued ? '' : undefined}
          data-indeterminate={queued || progress > 0 ? undefined : ''}
        >
          <span className="dk-progress-bar" style={!queued && progress > 0 ? cssVars({ '--dk-value': `${progress}%` }) : undefined} />
        </span>
      )}
      {canCancel && (
        <Button size="sm" variant="ghost" onClick={() => onCancel(op)} aria-label={t('operate:activity.cancelLabel', { name })} data-testid="addon-op-cancel">
          {t('operate:activity.cancel')}
        </Button>
      )}
      {failed && onDismiss && <Button size="sm" variant="ghost" onClick={() => onDismiss(op)} data-testid="addon-op-dismiss">{t('catalog.close')}</Button>}
    </div>
  )
}
