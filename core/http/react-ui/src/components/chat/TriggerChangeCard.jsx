import { useCallback, useEffect, useState } from 'react'
import { chatTriggersApi } from '../../utils/api'
import { changeValue, changeView } from '../../utils/chatProposal'
import Icon from '../Icon'
import { Button } from '../ui'

const fmtDate = (iso) => {
  try { return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(iso)) } catch { return iso }
}

// The value of one field in a before/after row. A long prompt is cut, so the
// row stays readable; the Triggers page shows it whole.
function shown(field, trigger, t) {
  const v = changeValue(field, trigger)
  if (field === 'enabled') return t(v === 'on' ? 'change.on' : 'change.off')
  if (!v) return <span className="cx-change-card__none">{t('change.none')}</span>
  if (field === 'prompt' && v.length > 160) return `${v.slice(0, 160)}...`
  if (field === 'writes') return <code>{v}</code>
  return v
}

// The trigger's last run, as the delete card says it.
function lastRunText(run, t) {
  if (!run) return t('change.neverRan')
  const when = run.ended || run.started || run.due
  return t('change.lastRunValue', { status: t(`change.runStatus.${run.status}`, { defaultValue: run.status }), when: when ? fmtDate(when) : '' })
}

// A change of a trigger that the model asked for in this chat: a tool line
// whose card is of kind trigger_change (card.id is the change's id). An edit
// shows the fields before and after with Apply and Dismiss, a delete shows the
// trigger with Delete and Keep; nothing changes until the person answers. A
// pause or a resume was applied when the model asked, so its card is a receipt
// with Undo. The card reads the change from the server, so it says the same
// after a reload and on every frontend.
export default function TriggerChangeCard({ id, t, tick = 0 }) {
  const [view, setView] = useState({ kind: 'loading' })
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [said, setSaid] = useState('')

  const load = useCallback(async () => {
    try {
      setView(changeView(await chatTriggersApi.change(id)))
    } catch (e) {
      setView(changeView(null, e))
    }
  }, [id])
  useEffect(() => { load() }, [load, tick])

  const act = async (what, call, say) => {
    setBusy(what)
    setError('')
    try {
      await call()
      if (say) setSaid(say)
    } catch (e) {
      setError(e.message)
      setSaid('')
    } finally {
      await load()
      setBusy('')
    }
  }

  const status = <span className="sr-only" role="status" aria-live="polite">{said}</span>
  const wrap = (node) => <>{node}{status}</>
  const alert = error && <p className="cx-help cx-help--err" role="alert">{error}</p>
  const testid = 'trigger-change'

  if (view.kind === 'loading') return wrap(<div className="cx-change" data-testid={testid} data-state="loading">{t('change.loading')}</div>)
  if (view.kind === 'error') {
    return wrap(
      <div className="cx-change" data-testid={testid} data-state="error">
        {t('change.unread', { message: view.message })}{' '}
        <Button size="sm" variant="ghost" onClick={load}>{t('proposal.retry')}</Button>
      </div>
    )
  }
  if (view.kind === 'removed') return wrap(<div className="cx-change" data-testid={testid} data-state="removed">{t('change.removed')}</div>)

  const { op, before, after } = view
  const name = before.name || after?.name || ''
  const isDelete = op === 'delete'

  if (view.kind === 'applied') {
    const now = after || before
    return wrap(
      <div className="cx-receipt" data-testid={testid} data-state="applied" data-op={op}>
        <Icon name={op === 'pause' ? 'pause' : 'check-circle'} className="cx-receipt__icon" />
        <div className="cx-receipt__main">
          <p className="cx-receipt__line">
            <b>{t(`change.receipt.${op}`)}</b>{' '}
            <span data-testid="change-title">{isDelete ? name : now.name}</span>
          </p>
          {op === 'update' && view.fields.length > 0 && (
            <p className="cx-receipt__sub" data-testid="change-fields">
              {t('change.receipt.fields', { fields: view.fields.map((f) => t(`change.field.${f}`)).join(', ') })}
            </p>
          )}
          {!view.canUndo && view.blocked && <p className="cx-help" data-testid="change-blocked">{t(`change.blocked.${view.blocked}`)}</p>}
          {alert}
        </div>
        <span className="cx-receipt__acts">
          {!isDelete && after && <Button size="sm" variant="secondary" to={`/app/triggers/${encodeURIComponent(view.after.id || '')}`}>{t('proposal.receipt.view')}</Button>}
          {view.canUndo && (
            <Button
              size="sm"
              variant="ghost"
              icon="undo"
              busy={busy === 'undo'}
              disabled={!!busy}
              onClick={() => act('undo', () => chatTriggersApi.undoChange(id), t(isDelete ? 'change.announce.restored' : 'change.announce.undone', { name }))}
            >{t('proposal.receipt.undo')}</Button>
          )}
        </span>
      </div>
    )
  }

  if (view.kind === 'dismissed' || view.kind === 'undone') {
    const key = view.kind === 'undone' ? (isDelete ? 'restored' : 'undone') : (isDelete ? 'kept' : 'dismissed')
    return wrap(
      <div className="cx-receipt cx-receipt--quiet" data-testid={testid} data-state={view.kind} data-op={op}>
        <div className="cx-receipt__main">
          <p className="cx-receipt__line">
            <b>{t(`change.receipt.${key}`)}</b>{' '}
            <span data-testid="change-title">{name}</span>
          </p>
        </div>
      </div>
    )
  }

  // Waiting for an answer.
  const rows = isDelete
    ? [['when', t('change.field.when'), before.summary || before.schedule || before.folder || ''],
        ['state', t('change.field.enabled'), t(before.enabled ? 'change.on' : 'change.off')],
        ['lastRun', t('change.lastRun'), lastRunText(view.lastRun, t)]]
    : view.fields.map((f) => [f, t(`change.field.${f}`), null])
  return wrap(
    <div className="cx-proposal cx-change-card" data-testid={testid} data-state="pending" data-op={op}>
      <div className="cx-proposal__head">
        <b>{t(isDelete ? 'change.deleteTitle' : 'change.updateTitle', { name })}</b>
      </div>
      <p className="cx-help">{t(view.stale ? 'change.stale' : (isDelete ? 'change.deleteHelp' : 'change.updateHelp'))}</p>
      <dl className="cx-plan" data-testid="change-rows">
        {rows.map(([key, label, value]) => (
          <div className="cx-plan__row" key={key} data-row={key}>
            <dt>{label}</dt>
            {value !== null ? <dd>{value}</dd> : (
              <dd className="cx-change-card__diff">
                <span className="cx-change-card__before" data-testid="change-before">{shown(key, before, t)}</span>
                <Icon name="arrow-right" className="cx-change-card__arrow" aria-hidden="true" />
                <span className="sr-only">{t('change.becomes')}</span>
                <span className="cx-change-card__after" data-testid="change-after">{shown(key, after, t)}</span>
              </dd>
            )}
          </div>
        ))}
      </dl>
      <span className="cx-pending__acts">
        {!view.stale && (isDelete ? (
          <button type="button" className="cx-btn cx-btn--danger" disabled={!!busy}
            onClick={() => act('apply', () => chatTriggersApi.applyChange(id), t('change.announce.deleted', { name }))}
          >{t('change.delete')}</button>
        ) : (
          <button type="button" className="cx-btn cx-btn--primary" disabled={!!busy}
            onClick={() => act('apply', () => chatTriggersApi.applyChange(id), t('change.announce.applied', { name }))}
          >{t('change.apply')}</button>
        ))}
        <button type="button" className="cx-btn" disabled={!!busy}
          onClick={() => act('dismiss', () => chatTriggersApi.dismissChange(id), t(isDelete ? 'change.announce.kept' : 'change.announce.dismissed', { name }))}
        >{t(isDelete ? 'change.keep' : 'proposal.dismiss')}</button>
      </span>
      {alert}
    </div>
  )
}
