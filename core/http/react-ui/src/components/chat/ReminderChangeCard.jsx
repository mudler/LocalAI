import { useTranslation } from 'react-i18next'
import { reminderOffsetWords, whenText } from '../../utils/watch'
import Icon from '../Icon'
import { Button } from '../ui'

// When a reminder fires, in the words of the watch page: "Thu 15:45, 15 min
// before", in the zone of the watch. A missing reminder (before a new one,
// after a cancel) is "none".
function reminderWhen(r, t, lang, zone) {
  if (!r) return t('watch.change.none')
  if (!r.due) return t('watch.started')
  return t('watch.dueLine', { when: whenText(r.due, zone || undefined, new Date(), lang), offset: reminderOffsetWords(t, r) })
}

// The body of the change card for a change of a reminder that the model
// proposed in this chat (view.reminder is set): the item, the watch and the
// time and task before and after, with Approve, Edit (the watch page, where
// the time is changed by hand) and Not now. Once answered it is a receipt;
// an applied change has Undo while it can be undone.
export default function ReminderChangeCard({ view, busy, apply, dismiss, undo, alert }) {
  const { t, i18n } = useTranslation('chats')
  const { op, reminder } = view
  const title = reminder.title || ''
  const watchLink = `/app/triggers/${encodeURIComponent(reminder.watch_id || '')}`
  const testid = 'reminder-change'

  if (view.kind === 'applied' || view.kind === 'dismissed' || view.kind === 'undone') {
    const key = view.kind === 'applied' ? `receipt.${op}` : `receipt.${view.kind}`
    return (
      <div className={`cx-receipt${view.kind === 'applied' ? '' : ' cx-receipt--quiet'}`} data-testid={testid} data-state={view.kind} data-op={op}>
        {view.kind === 'applied' && <Icon name="check-circle" className="cx-receipt__icon" />}
        <div className="cx-receipt__main">
          <p className="cx-receipt__line">
            <b>{t(`watch.change.${key}`)}</b>{' '}
            <span data-testid="change-title">{title}</span>
          </p>
          {view.kind === 'applied' && reminder.after && (
            <p className="cx-receipt__sub" data-testid="change-when">{reminderWhen(reminder.after, t, i18n.language, reminder.watch_zone)}</p>
          )}
          {view.kind === 'applied' && !view.canUndo && view.blocked && (
            <p className="cx-help" data-testid="change-blocked">{t(`watch.change.blocked.${view.blocked}`, { defaultValue: '' })}</p>
          )}
          {alert}
        </div>
        {view.kind === 'applied' && (
          <span className="cx-receipt__acts">
            <Button size="sm" variant="secondary" to={watchLink}>{t('watch.viewWatch')}</Button>
            {view.canUndo && (
              <Button size="sm" variant="ghost" icon="undo" busy={busy === 'undo'} disabled={!!busy} onClick={undo}>{t('proposal.receipt.undo')}</Button>
            )}
          </span>
        )}
      </div>
    )
  }

  const before = reminder.before || null
  const after = reminder.after || null
  const rows = [['watch', t('watch.change.watch'), reminder.watch_name || '', null]]
  rows.push(['when', t('watch.change.when'), reminderWhen(before, t, i18n.language, reminder.watch_zone), reminderWhen(after, t, i18n.language, reminder.watch_zone)])
  if ((before?.task || '') !== (after?.task || '') && op !== 'delete') {
    rows.push(['task', t('watch.change.task'), before?.task || t('watch.change.none'), after?.task || t('watch.change.none')])
  }
  return (
    <div className="cx-proposal cx-change-card" data-testid={testid} data-state="pending" data-op={op}>
      <div className="cx-proposal__head">
        <b>{t(`watch.change.title.${op}`, { title })}</b>
      </div>
      <p className="cx-help">{t(view.stale ? 'watch.change.stale' : 'watch.change.help')}</p>
      <dl className="cx-plan" data-testid="change-rows">
        {rows.map(([key, label, from, to]) => (
          <div className="cx-plan__row" key={key} data-row={key}>
            <dt>{label}</dt>
            {to === null ? <dd>{from}</dd> : (
              <dd className="cx-change-card__diff">
                <span className="cx-change-card__before" data-testid="change-before">{from}</span>
                <Icon name="arrow-right" className="cx-change-card__arrow" aria-hidden="true" />
                <span className="sr-only">{t('change.becomes')}</span>
                <span className="cx-change-card__after" data-testid="change-after">{to}</span>
              </dd>
            )}
          </div>
        ))}
      </dl>
      <span className="cx-pending__acts">
        {!view.stale && (
          <Button size="sm" variant={op === 'delete' ? 'danger' : 'primary'} disabled={!!busy} busy={busy === 'apply'} onClick={apply}>{t('proposal.approve')}</Button>
        )}
        <Button size="sm" variant="ghost" to={watchLink}>{t('proposal.edit')}</Button>
        <Button size="sm" variant="ghost" disabled={!!busy} busy={busy === 'dismiss'} onClick={dismiss}>{t('proposal.notNow')}</Button>
      </span>
      {alert}
    </div>
  )
}
