/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useContext, useEffect, useId, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { agentCollectionsApi, chatChangesApi, filesApi } from '../../utils/api'
import {
  appliedReceiptKey, byteText, CHANGES_EVENT, changeProposalView, estimatePassages, KNOWLEDGE_MAX_BYTES, knowledgeEntryText, isVoiceProfile, KNOWN_KINDS, KNOWN_SCOPES, previewLines, scopeOfProposal, sourceOfProposal,
  targetPath, timeLeft, voiceProfileFields, writesElsewhere,
} from '../../utils/changeProposal'
import { diffLines, diffStats, withContext } from '../../utils/lineDiff'
import Icon from '../Icon'
import { Button } from '../ui'
import { useAuth } from '../../context/AuthContext'
import { ChatSendContext } from './chatSendContext'
import './changeproposal.css'


// Past this many shown lines a diff stops and says how many more there are.
const MAX_SHOWN = 120
// The most of the current file the card reads to draw the diff.
const MAX_READ = 1 << 20

const announceChanged = () => {
  if (typeof window !== 'undefined') window.dispatchEvent(new CustomEvent(CHANGES_EVENT))
}

function kindName(p, t) {
  return KNOWN_KINDS.includes(p.kind) ? t(`changes.kind.${p.kind}`) : String(p.kind || '')
}

function scopeName(p, t) {
  const { scope, agent } = scopeOfProposal(p)
  if (!KNOWN_SCOPES.includes(scope)) return String(scope)
  if (scope === 'agent' && !agent) return t('changes.scope.agentAny')
  return t(`changes.scope.${scope}`, { agent })
}

function sourceName(p, t) {
  const s = sourceOfProposal(p)
  if (s.kind === 'chat') return t('changes.source.chat')
  if (s.sender) return t('changes.source.connectorFrom', { name: s.id || s.kind, sender: s.sender })
  return t('changes.source.connector', { name: s.id || s.kind })
}

function expiryName(p, t) {
  const left = timeLeft(p)
  if (!left) return ''
  return left.hours > 0 ? t('changes.expiresHours', { hours: left.hours, minutes: left.minutes }) : t('changes.expiresMinutes', { minutes: left.minutes })
}

// The lines a proposal adds and removes, with a few unchanged lines around
// them. Every row says what it is in words for a screen reader; the sign in
// the margin is for the eye. A file the card could not read is shown as all
// new text.
function Diff({ before, after, target, t }) {
  const ops = useMemo(() => diffLines(before ?? '', after ?? ''), [before, after])
  const { added, removed } = diffStats(ops)
  const rows = useMemo(() => withContext(ops, 2), [ops])
  const shown = rows.slice(0, MAX_SHOWN)
  return (
    <div className="cp-diff" role="group" aria-label={t('changes.diff.label', { target })} data-testid="change-diff">
      <p className="cp-diff__stat" data-testid="change-diff-stat">
        {before === '' ? <span>{t('changes.diff.newFile')} · </span> : null}
        <span className="cp-diff__n cp-diff__n--add">{t('changes.diff.added', { count: added })}</span>
        {' '}
        <span className="cp-diff__n cp-diff__n--del">{t('changes.diff.removed', { count: removed })}</span>
      </p>
      {added + removed === 0 ? <p className="cx-help">{t('changes.diff.same')}</p> : (
        <ul className="cp-diff__lines">
          {shown.map((op, i) => (op.type === 'gap' ? (
            <li key={i} className="cp-diff__gap" data-op="gap">{t('changes.diff.gap', { count: op.count })}</li>
          ) : (
            <li key={i} className="cp-diff__line" data-op={op.type}>
              <span className="cp-diff__mark" aria-hidden="true">{op.type === 'add' ? '+' : op.type === 'del' ? '−' : ''}</span>
              {op.type !== 'same' && <span className="sr-only">{t(op.type === 'add' ? 'changes.diff.srAdded' : 'changes.diff.srRemoved')}</span>}
              <code className="cp-diff__text">{op.text || ' '}</code>
            </li>
          )))}
          {rows.length > shown.length && <li className="cp-diff__gap" data-op="gap">{t('changes.diff.more', { count: rows.length - shown.length })}</li>}
        </ul>
      )}
    </div>
  )
}

// The text a knowledge change would add to a collection: its first lines, how
// big it is against the cap, and what happens when it is confirmed. There is no
// file to compare it with, so no diff is drawn.
// The voice a proposal would clone, and the question the person answers by
// confirming: the speaker agreed. The recording is a file of the person's own
// folder, so the card names it and does not play it.
function VoiceProfilePreview({ p, t, admin }) {
  const f = voiceProfileFields(p)
  const rows = [['name', f.name], ['file', f.audio], ['clip', f.clip], ['language', f.language], ['transcript', f.transcript]].filter(([, v]) => v)
  return (
    <div className="cp-diff cp-knowledge" role="group" aria-label={t('changes.voice.label')} data-testid="change-voice-preview">
      <dl className="cx-plan">
        {rows.map(([key, value]) => (
          <div className="cx-plan__row" key={key} data-row={`voice-${key}`}><dt>{t(`changes.voice.${key}`)}</dt><dd>{key === 'file' || key === 'clip' ? <code>{value}</code> : value}</dd></div>
        ))}
      </dl>
      <p className="cx-help" data-testid="change-voice-consent"><b>{t('changes.voice.consentTitle')}</b> {t('changes.voice.consent')}{admin && <> {t('changes.voice.consentAdmin')}</>}</p>
    </div>
  )
}

function KnowledgePreview({ p, t, chunking }) {
  const text = p.content || ''
  const passages = estimatePassages(knowledgeEntryText(p), chunking.size, chunking.overlap)
  const { shown, more } = previewLines(text)
  const size = new TextEncoder().encode(text).length
  return (
    <div className="cp-diff cp-knowledge" role="group" aria-label={t('changes.knowledge.label', { collection: p.target })} data-testid="change-knowledge-preview">
      <p className="cp-diff__stat" data-testid="change-knowledge-size">{t('changes.knowledge.size', { size: byteText(size), max: byteText(KNOWLEDGE_MAX_BYTES) })}</p>
      <p className="cx-help cp-knowledge__embed" data-testid="change-knowledge-embed">
        {t('changes.knowledge.embed', { count: passages, model: chunking.model || t('changes.knowledge.serverModel') })}
      </p>
      <ul className="cp-diff__lines">
        {shown.map((line, i) => (
          <li key={i} className="cp-diff__line" data-op="same"><code className="cp-diff__text">{line || ' '}</code></li>
        ))}
        {more > 0 && <li className="cp-diff__gap" data-op="gap">{t('changes.diff.more', { count: more })}</li>}
      </ul>
      <p className="cx-help">{t('changes.knowledge.note')}</p>
    </div>
  )
}

// A change that a model proposed to something that lasts (a memory note, an
// agent profile, and the kinds that come later), drawn for any kind and any
// source: a tool line whose card is of kind change_proposal (card.id is the
// proposal's id), or a row of the Proposals page. It reads the proposal from
// the server, so it says the same after a reload and on every frontend:
// waiting (Confirm, Edit then confirm, Dismiss), then a receipt: applied (with
// Undo), undone, dismissed, expired or stale. Nothing is written until the
// person confirms.
export default function ChangeProposalCard({ id, initial, t, tick = 0 }) {
  const { isAdmin } = useAuth()
  const uid = useId()
  const sendToChat = useContext(ChatSendContext)
  const [view, setView] = useState(() => (initial ? changeProposalView(initial) : { kind: 'loading' }))
  // The current text of the target: '' for a file that is not there, null when
  // it could not be read.
  const [current, setCurrent] = useState(undefined)
  const [edit, setEdit] = useState(null)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [said, setSaid] = useState('')
  // How the server cuts a text into passages, and the model that embeds them.
  // The defaults are the server's; an older server or a failed read keeps them.
  const [chunking, setChunking] = useState({ size: 400, overlap: 0, model: '' })

  const load = useCallback(async () => {
    let v
    try {
      v = changeProposalView(await chatChangesApi.get(id))
    } catch (e) {
      v = changeProposalView(null, e)
    }
    setView(v)
    if (v.kind === 'pending' && !writesElsewhere(v.proposal)) {
      try {
        setCurrent(await filesApi.readText(targetPath(v.proposal), MAX_READ))
      } catch (e) {
        setCurrent(e?.status === 404 ? '' : null)
      }
    }
    return v
  }, [id])
  useEffect(() => { load() }, [load, tick])
  const waitingKnowledge = view.kind === 'pending' && view.proposal?.kind === 'knowledge'
  useEffect(() => {
    if (!waitingKnowledge) return undefined
    let live = true
    agentCollectionsApi.list().then((d) => {
      if (live) setChunking({ size: d?.max_chunk_size > 0 ? d.max_chunk_size : 400, overlap: d?.chunk_overlap > 0 ? d.chunk_overlap : 0, model: d?.embedding_model || '' })
    }).catch(() => {})
    return () => { live = false }
  }, [waitingKnowledge])

  const act = async (what, call, say) => {
    setBusy(what)
    setError('')
    let failure = null
    try {
      await call()
      setEdit(null)
      if (say) setSaid(say)
    } catch (e) {
      failure = e
      setSaid('')
    }
    const v = await load()
    // A 409 means the proposal moved on (stale, expired, answered elsewhere):
    // the card then says so in its own words. Any other failure leaves the
    // proposal waiting, with the reason; a write that failed is one of them.
    if (failure && !(failure.status === 409 && v.kind !== 'pending' && v.kind !== 'applied')) {
      const reason = failure.message.replace(/^studio: /, '').replace(/[.\s]+$/, '')
      setError(failure.status === 409 ? reason : t('changes.failed', { message: reason }))
    }
    setBusy('')
    announceChanged()
  }

  const status = <span className="sr-only" role="status" aria-live="polite">{said}</span>
  const wrap = (node) => <>{node}{status}</>
  const alert = error && <p className="cx-help cx-help--err" role="alert" data-testid="change-error">{error}</p>
  const tid = 'change-proposal'

  if (view.kind === 'loading') return wrap(<div className="cx-change" data-testid={tid} data-state="loading">{t('changes.loading')}</div>)
  if (view.kind === 'error') {
    return wrap(
      <div className="cx-change" data-testid={tid} data-state="error">
        {t('changes.unread', { message: view.message })}{' '}
        <Button size="sm" variant="ghost" onClick={load}>{t('proposal.retry')}</Button>
      </div>
    )
  }
  if (view.kind === 'removed') return wrap(<div className="cx-change" data-testid={tid} data-state="removed">{t('changes.removed')}</div>)

  const p = view.proposal
  const target = p.target || ''
  const voice = isVoiceProfile(p)
  const knowledge = writesElsewhere(p) && !voice
  // A knowledge change belongs to the person and names a collection, not a file.
  const meta = voice ? [
    ['kind', t('changes.row.kind'), kindName(p, t)],
    ['source', t('changes.row.source'), sourceName(p, t)],
  ] : knowledge ? [
    ['kind', t('changes.row.kind'), kindName(p, t)],
    ['target', t('changes.row.collection'), <code key="c">{target}</code>],
    ['source', t('changes.row.source'), sourceName(p, t)],
  ] : [
    ['kind', t('changes.row.kind'), `${kindName(p, t)} · ${scopeName(p, t)}`],
    ['target', t('changes.row.target'), <code key="c">{target}</code>],
    ['source', t('changes.row.source'), sourceName(p, t)],
  ]

  if (view.kind === 'applied') {
    return wrap(
      <div className="cx-receipt" data-testid={tid} data-state="applied" data-kind={p.kind} data-auto={p.auto ? 'true' : undefined}>
        <Icon name="check-circle" className="cx-receipt__icon" />
        <div className="cx-receipt__main">
          <p className="cx-receipt__line"><b>{t(appliedReceiptKey(p))}</b> <span data-testid="change-summary">{p.summary}</span></p>
          <p className="cx-receipt__sub"><span>{knowledge || voice ? kindName(p, t) : `${kindName(p, t)} · ${scopeName(p, t)}`}</span>{!voice && <code>{target}</code>}</p>
          {alert}
        </div>
        <span className="cx-receipt__acts">
          {view.canUndo && (
            <Button size="sm" variant="ghost" icon="undo" busy={busy === 'undo'} disabled={!!busy}
              onClick={() => act('undo', () => chatChangesApi.undo(p.id), t('changes.announce.undone'))}
            >{t('proposal.receipt.undo')}</Button>
          )}
        </span>
      </div>
    )
  }

  if (view.kind !== 'pending') {
    // Quiet receipts: reverted, dismissed, expired and stale. The last two
    // offer to ask the assistant again, where there is a chat to ask.
    const again = (view.kind === 'expired' || view.kind === 'stale') && sendToChat
    return wrap(
      <div className="cx-receipt cx-receipt--quiet" data-testid={tid} data-state={view.kind} data-kind={p.kind}>
        <div className="cx-receipt__main">
          <p className="cx-receipt__line"><b>{t(`changes.receipt.${view.kind}`)}</b> <span data-testid="change-summary">{p.summary}</span></p>
          {(view.kind === 'expired' || view.kind === 'stale') && <p className="cx-help" data-testid="change-why">{t(`changes.why.${view.kind}`)}</p>}
          {alert}
        </div>
        {again && (
          <span className="cx-receipt__acts">
            <Button size="sm" variant="secondary" busy={busy === 'again'} disabled={!!busy}
              onClick={() => act('again', async () => { await sendToChat(t('changes.askAgainText', { summary: p.summary, target })) }, t('changes.announce.asked'))}
            >{t('changes.askAgain')}</Button>
          </span>
        )}
      </div>
    )
  }

  // Waiting for an answer.
  const expiry = expiryName(p, t)
  return wrap(
    <div className="cx-proposal cp-card" data-testid={tid} data-state="pending" data-kind={p.kind} aria-labelledby={`${uid}-h`}>
      <div className="cx-proposal__head">
        <b id={`${uid}-h`} data-testid="change-summary">{p.summary}</b>
      </div>
      <p className="cx-help">{t('changes.help')}</p>
      <dl className="cx-plan" data-testid="change-rows">
        {meta.map(([key, label, value]) => (
          <div className="cx-plan__row" key={key} data-row={key}><dt>{label}</dt><dd>{value}</dd></div>
        ))}
        {expiry && <div className="cx-plan__row" data-row="expires"><dt>{t('changes.row.expires')}</dt><dd>{expiry}</dd></div>}
      </dl>
      {p.warning && (
        <p className="cx-help cx-help--err cp-warning" data-testid="change-warning" data-warning={p.warning}>
          <b>{t('changes.warning.title')}</b>{' '}
          {t(`changes.warning.${p.warning}`, { defaultValue: t('changes.warning.other') })}
        </p>
      )}
      {knowledge && edit === null && <KnowledgePreview p={p} t={t} chunking={chunking} />}
      {voice && <VoiceProfilePreview p={p} t={t} admin={isAdmin} />}
      {!knowledge && !voice && current !== undefined && edit === null && <Diff before={current ?? ''} after={p.content || ''} target={target} t={t} />}
      {!knowledge && !voice && current === null && <p className="cx-help">{t('changes.unreadFile')}</p>}
      {edit !== null ? (
        <form
          className="cx-proposal__edit cp-edit"
          onSubmit={(e) => { e.preventDefault(); act('confirm', () => chatChangesApi.confirm(p.id, edit), t('changes.announce.applied')) }}
          onKeyDown={(e) => { if (e.key === 'Escape') { e.stopPropagation(); setEdit(null) } }}
        >
          <label className="cx-field__label" htmlFor={`${uid}-text`}>{t(knowledge ? 'changes.knowledge.editLabel' : 'changes.editLabel', { target })}</label>
          <textarea
            id={`${uid}-text`} className="cx-in cp-edit__text" rows={Math.min(14, Math.max(5, edit.split('\n').length + 1))}
            value={edit} spellCheck={false} autoFocus data-testid="change-edit-text"
            aria-describedby={`${uid}-size`}
            onChange={(e) => setEdit(e.target.value)}
          />
          <p className="cx-help" id={`${uid}-size`}>{byteText(new TextEncoder().encode(edit).length)}</p>
          <span className="cx-pending__acts">
            <button type="submit" className="cx-btn cx-btn--primary" disabled={!!busy}>{t('changes.confirmEdited')}</button>
            <button type="button" className="cx-btn" onClick={() => setEdit(null)}>{t('proposal.cancelEdit')}</button>
          </span>
        </form>
      ) : (
        <span className="cx-pending__acts">
          <button type="button" className="cx-btn cx-btn--primary" disabled={!!busy}
            onClick={() => act('confirm', () => chatChangesApi.confirm(p.id), t('changes.announce.applied'))}
          >{t(voice ? 'changes.voice.confirm' : 'changes.confirm')}</button>
          {!voice && <button type="button" className="cx-btn" disabled={!!busy} onClick={() => setEdit(p.content || '')}>{t('changes.edit')}</button>}
          <button type="button" className="cx-btn" disabled={!!busy}
            onClick={() => act('dismiss', () => chatChangesApi.dismiss(p.id), t('changes.announce.dismissed'))}
          >{t('proposal.dismiss')}</button>
        </span>
      )}
      {alert}
    </div>
  )
}
