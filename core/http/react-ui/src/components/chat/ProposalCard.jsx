import { useCallback, useEffect, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { chatTriggersApi } from '../../utils/api'
import { browserZone, formatNext } from '../../utils/chatTrigger'
import { proposalEdit, proposalState } from '../../utils/chatProposal'
import { useWellKnown } from '../../hooks/useWellKnown'
import { widgetList } from '../../utils/wellKnown'
import { plainSummary } from '../../utils/widgetTrigger'

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
// proposal from the server, so it says the same thing after a reload, and once
// the proposal is gone it says what became of it.
export default function ProposalCard({ id, t, tick = 0 }) {
  const [view, setView] = useState({ kind: 'loading' })
  const [edit, setEdit] = useState(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const [props, list] = await Promise.all([chatTriggersApi.proposals(), chatTriggersApi.list().catch(() => ({ triggers: [] }))])
      setView(proposalState(id, props?.triggers, list?.triggers))
    } catch (e) {
      setView({ kind: 'error', message: e.message })
    }
  }, [id])
  useEffect(() => { load() }, [load, tick])

  const act = async (call) => {
    setBusy(true)
    setError('')
    try {
      await call()
      setEdit(null)
      await load()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  if (view.kind === 'loading') return <div className="cx-change" data-testid="trigger-proposal">{t('proposal.loading')}</div>
  if (view.kind === 'error') return <div className="cx-change" data-testid="trigger-proposal">{t('proposal.unread', { message: view.message })}</div>
  if (view.kind === 'created') {
    return (
      <div className="cx-change" data-testid="trigger-proposal" data-state="created">
        {t('proposal.created', { name: view.trigger.name })}{' '}
        <Link className="cx-link" to={`/app/triggers/${encodeURIComponent(view.trigger.id)}`}>{t('proposal.open')}</Link>
      </div>
    )
  }
  if (view.kind === 'gone') return <div className="cx-change" data-testid="trigger-proposal" data-state="gone">{t('proposal.gone')}</div>

  const p = view.proposal
  return (
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
          onSubmit={(e) => { e.preventDefault(); act(() => chatTriggersApi.editProposal(p.id, proposalEdit(p, edit))) }}
        >
          <label className="cx-field__label" htmlFor={`prop-name-${p.id}`}>{t('proposal.name')}</label>
          <input id={`prop-name-${p.id}`} className="cx-in" value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
          <label className="cx-field__label" htmlFor={`prop-when-${p.id}`}>{p.kind === 'file' ? t('proposal.folder') : t('proposal.schedule')}</label>
          <input id={`prop-when-${p.id}`} className="cx-in" value={edit.when} onChange={(e) => setEdit({ ...edit, when: e.target.value })} />
          <span className="cx-pending__acts">
            <button type="submit" className="cx-btn cx-btn--primary" disabled={busy || !edit.name.trim() || !edit.when.trim()}>{t('proposal.saveEdit')}</button>
            <button type="button" className="cx-btn" onClick={() => setEdit(null)}>{t('proposal.cancelEdit')}</button>
          </span>
        </form>
      ) : (
        <span className="cx-pending__acts">
          <button type="button" className="cx-btn cx-btn--primary" disabled={busy} onClick={() => act(() => chatTriggersApi.confirm(p.id, browserZone()))}>{t('proposal.create')}</button>
          <button type="button" className="cx-btn" disabled={busy} onClick={() => setEdit({ name: p.name, when: p.kind === 'file' ? p.folder || '' : p.schedule || '' })}>{t('proposal.edit')}</button>
          <button type="button" className="cx-btn" disabled={busy} onClick={() => act(() => chatTriggersApi.dismiss(p.id))}>{t('proposal.dismiss')}</button>
        </span>
      )}
      {error && <p className="cx-help cx-help--err" role="alert">{error}</p>}
    </div>
  )
}
