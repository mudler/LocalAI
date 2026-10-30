import { useCallback, useEffect, useState } from 'react'
import { chatTriggersApi } from '../../utils/api'
import { browserZone, formatNext } from '../../utils/chatTrigger'
import { proposalEdit, proposalView, receiptWhen } from '../../utils/chatProposal'
import { useWellKnown } from '../../hooks/useWellKnown'
import { widgetList } from '../../utils/wellKnown'
import { plainSummary } from '../../utils/widgetTrigger'
import Icon from '../Icon'
import { Button } from '../ui'

const fmt = (n) => {
  try { return new Intl.NumberFormat().format(n) } catch { return String(n) }
}

// Plan lists what the trigger will do before the person creates it: when it
// runs, as whom, what it reads and writes, what a run costs and how to stop it.
// A row the model left empty is left out, except the ones LocalAI knows itself.
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
function Plan({ p, t }) {
  const [first, setFirst] = useState('')
  const zone = p.time_zone || browserZone()
  useEffect(() => {
    if (p.kind !== 'schedule' || !p.schedule) return undefined
    let live = true
    chatTriggersApi.preview(p.schedule, zone)
      .then((res) => { if (live) setFirst(res?.times?.[0] ? formatNext(res.times[0], zone) : '') })
      .catch(() => {})
    return () => { live = false }
  }, [p.kind, p.schedule, zone])
  const home = useWellKnown('home', { enabled: !!p.plan?.widget })
  const widget = p.plan?.widget ? widgetList(home.doc).find((w) => w.id === p.plan.widget) : null
  const cost = p.cost || {}
  const runs = cost.max_runs_per_day || p.daily_cap || 0
  const tokens = cost.estimated_tokens || 0

  let when
  if (p.kind === 'file') when = t('proposal.plan.whenFile', { folder: p.folder })
  else if (first) when = t('proposal.plan.when', { schedule: plainSummary(p), zone, when: first })
  else when = t('proposal.plan.whenNoFirst', { schedule: plainSummary(p), zone })
  let costText = tokens > 0
    ? t(cost.remote ? 'proposal.plan.costRemote' : 'proposal.plan.costLocal', { tokens: fmt(tokens), model: p.model, count: runs })
    : t('proposal.plan.costUnknown', { count: runs })
  if (cost.remote && tokens === 0) costText = `${costText} ${t('proposal.plan.remoteNote', { model: p.model })}`
  const rows = [
    ['when', when],
    ['runsAs', p.model ? t('proposal.plan.runsAsModel', { model: p.model }) : t('proposal.plan.runsAs')],
    p.plan?.reads && ['reads', p.plan.reads],
    p.plan?.writes && ['writes', widget
      ? <><code>{p.plan.writes}</code>{t('proposal.plan.writesWidget', { widget: widget.title })}</>
      : <code>{p.plan.writes}</code>],
    ['cost', costText],
    ['stop', t(widget ? 'proposal.plan.stopWidget' : 'proposal.plan.stop')],
  ].filter(Boolean)
  return (
    <dl className="cx-plan" data-testid="trigger-plan">
      {rows.map(([key, value]) => (
        <div className="cx-plan__row" key={key} data-row={key}>
          <dt>{t(`proposal.plan.${key}Label`)}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  )
}

// A trigger the model proposed in this chat: a tool line whose card is of kind
// trigger_proposal (card.id is the proposal's id). A proposal runs nothing until
// the person creates it here or on the Triggers page. The card reads the
// proposal and its answer from the server, so it says the same after a reload
// and on every frontend: waiting, then a receipt once it was accepted (with
// Undo while the trigger has not run or been edited), declined or undone.
export default function ProposalCard({ id, t, tick = 0 }) {
  const [view, setView] = useState({ kind: 'loading' })
  const [edit, setEdit] = useState(null)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  // What a screen reader hears after the person acts; empty on first load.
  const [said, setSaid] = useState('')

  const load = useCallback(async () => {
    try {
      setView(proposalView(await chatTriggersApi.proposal(id)))
    } catch (e) {
      setView(proposalView(null, e))
    }
  }, [id])
  useEffect(() => { load() }, [load, tick])

  const act = async (what, call, say) => {
    setBusy(what)
    setError('')
    try {
      await call()
      setEdit(null)
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
  // The live region is outside the card that changes, so it stays mounted
  // when the pending card becomes a receipt and the change is announced.
  const wrap = (node) => <>{node}{status}</>
  const alert = error && <p className="cx-help cx-help--err" role="alert">{error}</p>

  if (view.kind === 'loading') return wrap(<div className="cx-change" data-testid="trigger-proposal" data-state="loading">{t('proposal.loading')}</div>)
  if (view.kind === 'error') {
    return wrap(
      <div className="cx-change" data-testid="trigger-proposal" data-state="error">
        {t('proposal.unread', { message: view.message })}{' '}
        <Button size="sm" variant="ghost" onClick={load}>{t('proposal.retry')}</Button>
      </div>
    )
  }
  if (view.kind === 'removed') return wrap(<div className="cx-change" data-testid="trigger-proposal" data-state="removed">{t('proposal.removed')}</div>)

  const p = view.trigger
  if (view.kind === 'accepted') {
    const when = receiptWhen(p)
    const next = p.next_run ? formatNext(p.next_run, p.time_zone) : ''
    return wrap(
      <div className="cx-receipt" data-testid="trigger-proposal" data-state="accepted">
        <Icon name="check-circle" className="cx-receipt__icon" />
        <div className="cx-receipt__main">
          <p className="cx-receipt__line">
            <b>{t(when.mode === 'file' ? 'proposal.receipt.watching' : 'proposal.receipt.scheduled')}</b>{' '}
            <span data-testid="receipt-when">{when.text}</span>
          </p>
          <p className="cx-receipt__sub">
            <span data-testid="receipt-title">{p.name}</span>
            {next && <span data-testid="receipt-next">{t('proposal.receipt.next', { when: next })}</span>}
          </p>
          {!view.canUndo && view.blocked && <p className="cx-help" data-testid="receipt-blocked">{t(`proposal.receipt.blocked.${view.blocked}`)}</p>}
          {!(!view.canUndo && view.blocked) && alert}
        </div>
        <span className="cx-receipt__acts">
          <Button size="sm" variant="secondary" to={`/app/triggers/${encodeURIComponent(p.id)}`}>{t('proposal.receipt.view')}</Button>
          {view.canUndo && (
            <Button
              size="sm"
              variant="ghost"
              icon="undo"
              busy={busy === 'undo'}
              disabled={!!busy}
              onClick={() => act('undo', () => chatTriggersApi.undoProposal(p.id), t('proposal.announce.undone', { name: p.name }))}
            >{t('proposal.receipt.undo')}</Button>
          )}
        </span>
      </div>
    )
  }
  if (view.kind === 'undone' || view.kind === 'declined') {
    const undone = view.kind === 'undone'
    return wrap(
      <div className="cx-receipt cx-receipt--quiet" data-testid="trigger-proposal" data-state={view.kind}>
        <div className="cx-receipt__main">
          <p className="cx-receipt__line">
            <b>{t(undone ? 'proposal.receipt.undone' : 'proposal.receipt.declined')}</b>{' '}
            <span data-testid="receipt-title">{p.name}</span>
          </p>
          {alert}
        </div>
        <span className="cx-receipt__acts">
          <Button
            size="sm"
            variant="ghost"
            busy={busy === 'reopen'}
            disabled={!!busy}
            onClick={() => act('reopen', () => chatTriggersApi.reopenProposal(p.id), t('proposal.announce.reopened', { name: p.name }))}
          >{t(undone ? 'proposal.receipt.again' : 'proposal.receipt.restore')}</Button>
        </span>
      </div>
    )
  }

  return wrap(
    <div className="cx-proposal" data-testid="trigger-proposal" data-state="proposed">
      <div className="cx-proposal__head">
        <b>{p.name}</b>
        <span className="cx-proposal__when">{p.summary || p.schedule || p.folder}</span>
      </div>
      <p className="cx-help">{t('proposal.inactive')}</p>
      {!edit && <Plan p={p} t={t} />}
      {edit ? (
        <form
          className="cx-proposal__edit"
          onSubmit={(e) => { e.preventDefault(); act('edit', () => chatTriggersApi.editProposal(p.id, proposalEdit(p, edit))) }}
        >
          <label className="cx-field__label" htmlFor={`prop-name-${p.id}`}>{t('proposal.name')}</label>
          <input id={`prop-name-${p.id}`} className="cx-in" value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
          <label className="cx-field__label" htmlFor={`prop-when-${p.id}`}>{p.kind === 'file' ? t('proposal.folder') : t('proposal.schedule')}</label>
          <input id={`prop-when-${p.id}`} className="cx-in" value={edit.when} onChange={(e) => setEdit({ ...edit, when: e.target.value })} />
          <span className="cx-pending__acts">
            <button type="submit" className="cx-btn cx-btn--primary" disabled={!!busy || !edit.name.trim() || !edit.when.trim()}>{t('proposal.saveEdit')}</button>
            <button type="button" className="cx-btn" onClick={() => setEdit(null)}>{t('proposal.cancelEdit')}</button>
          </span>
        </form>
      ) : (
        <span className="cx-pending__acts">
          <button type="button" className="cx-btn cx-btn--primary" disabled={!!busy} onClick={() => act('create', () => chatTriggersApi.confirm(p.id, browserZone()), t('proposal.announce.accepted', { name: p.name }))}>{t('proposal.create')}</button>
          <button type="button" className="cx-btn" disabled={!!busy} onClick={() => setEdit({ name: p.name, when: p.kind === 'file' ? p.folder || '' : p.schedule || '' })}>{t('proposal.edit')}</button>
          <button type="button" className="cx-btn" disabled={!!busy} onClick={() => act('dismiss', () => chatTriggersApi.dismiss(p.id), t('proposal.announce.declined', { name: p.name }))}>{t('proposal.dismiss')}</button>
        </span>
      )}
      {alert}
    </div>
  )
}
