/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Button from '../ui/Button'
import { barItems, groupView, isWait, loopEvery, loopSummary, pendingLoops, shortId, waitView } from '../../utils/chatLoops'

function clock(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

// What a chip says after "Loop": the pace, the run, and what it does now.
function chipText(t, l) {
  if (l.state === 'ended') return t('loops.chip.ended')
  const parts = [l.interval_seconds > 0 ? t('loops.chip.every', { every: loopEvery(t, l.interval_seconds, true) }) : t('loops.chip.selfPaced')]
  if (l.runs > 0) parts.push(t('loops.chip.run', { count: l.runs }))
  if (l.state === 'paused') parts.push(t('loops.chip.paused'))
  else if (l.running) parts.push(t('loops.chip.running'))
  else if (l.next) parts.push(t('loops.chip.next', { time: clock(l.next) }))
  return parts.join(' · ')
}

// The card the first loop of a chat waits on: every run costs tokens, so the
// person says yes once per chat. Start makes it active, Cancel removes it. The
// server keeps the pending loop with the chat, so a reload shows the card again.
function LoopConfirm({ loop, chatId, chat }) {
  const { t } = useTranslation('chats')
  const [busy, setBusy] = useState('')
  const act = async (what, call) => {
    setBusy(what)
    try { await call() } finally { setBusy('') }
  }
  const question = loop.interval_seconds > 0
    ? t('loops.confirm.every', { every: loopEvery(t, loop.interval_seconds) })
    : t('loops.confirm.selfPaced')
  return (
    <div className="cx-loop-confirm" role="group" aria-label={question} data-testid="loop-confirm">
      <Icon name="repeat" className="cx-loop-confirm__icon" />
      <div className="cx-loop-confirm__text">
        <strong>{question}</strong>
        <span className="cx-loop-confirm__prompt" title={loop.prompt}>{loop.prompt}</span>
      </div>
      <div className="cx-loop-confirm__acts">
        <Button size="sm" variant="primary" disabled={!!busy} onClick={() => act('start', () => chat.setLoopState(chatId, loop.id, 'active'))} data-testid="loop-confirm-start">
          {t('loops.confirm.start')}
        </Button>
        <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => act('cancel', () => chat.stopLoop(chatId, loop.id))} data-testid="loop-confirm-cancel">
          {t('loops.confirm.cancel')}
        </Button>
      </div>
    </div>
  )
}

// One loop above the message box, so the person always sees what runs by
// itself: "Loop · every 5 min · run 4" with Pause (or Resume) and Stop. An
// ended loop says why and has Dismiss.
function LoopChip({ loop, chatId, chat }) {
  const { t } = useTranslation('chats')
  const ended = loop.state === 'ended'
  const label = chipText(t, loop)
  return (
    <div className={`cx-loop cx-loop--${loop.state}`} data-testid="loop-chip" data-state={loop.state} data-loop={loop.id}>
      <span className="cx-loop__main" title={loop.prompt}>
        <Icon name="repeat" className="cx-loop__icon" />
        <b className="cx-loop__name">{t('loops.chip.name')}</b>
        <span className="cx-loop__meta" data-testid="loop-chip-text">{label}</span>
      </span>
      {(loop.reason || loop.wait) && (
        <span className="cx-loop__why" data-testid="loop-chip-why">{loop.reason || t('loops.chip.wait', { wait: loop.wait })}</span>
      )}
      <span className="cx-loop__acts">
        {loop.state === 'active' && (
          <button type="button" className="cx-link" onClick={() => chat.setLoopState(chatId, loop.id, 'paused')} data-testid="loop-pause">
            {t('loops.actions.pause')}
          </button>
        )}
        {loop.state === 'paused' && (
          <button type="button" className="cx-link" onClick={() => chat.setLoopState(chatId, loop.id, 'active')} data-testid="loop-resume">
            {t('loops.actions.resume')}
          </button>
        )}
        <button type="button" className={`cx-link${ended ? '' : ' cx-link--danger'}`} onClick={() => chat.stopLoop(chatId, loop.id)} data-testid={ended ? 'loop-dismiss' : 'loop-stop'}>
          {ended ? t('loops.actions.dismiss') : t('loops.actions.stop')}
        </button>
      </span>
    </div>
  )
}

// The clock of a wait that counts down: it ticks once a second while `on`.
function useNow(on) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!on) return undefined
    setNow(Date.now())
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [on])
  return now
}

// One wait above the message box. It is not a loop with a different label: it
// says what the model waits for and why, what the server checks and how the
// checks went, when the next one is due and when the wait gives up. When it
// ends it says how, in words and with its own icon, so a timeout or a failing
// source never reads as success.
function WaitChip({ loop, chatId, chat }) {
  const { t } = useTranslation('chats')
  const now = useNow(loop.watch.phase === 'waiting')
  const v = waitView(t, loop, now)
  const ended = v.phase === 'ended'
  return (
    <div className={`cx-wait cx-wait--${v.tone}`} role="group" aria-label={`${v.title} ${v.reason}`.trim()}
      data-testid="wait-chip" data-state={loop.state} data-phase={v.phase} data-end={v.end} data-loop={loop.id}>
      <div className="cx-wait__head">
        <Icon name={v.icon} className="cx-wait__icon" />
        <span className="cx-wait__title" role="status" data-testid="wait-title">
          <b>{v.title}</b>
          {v.reason && <span className="cx-wait__reason" data-testid="wait-reason">{v.reason}</span>}
        </span>
        <span className="cx-wait__acts">
          {v.phase === 'paused' && (
            <button type="button" className="cx-link" onClick={() => chat.setLoopState(chatId, loop.id, 'active')} data-testid="wait-resume">
              {t('loops.actions.resume')}
            </button>
          )}
          <button type="button" className={`cx-link${ended ? '' : ' cx-link--danger'}`} onClick={() => chat.stopLoop(chatId, loop.id)} data-testid={ended ? 'wait-dismiss' : 'wait-stop'}>
            {ended ? t('loops.actions.dismiss') : t('loops.wait.stop')}
          </button>
        </span>
      </div>
      <ul className="cx-wait__facts" aria-label={t('loops.wait.details')}>
        {v.facts.map(f => (
          <li key={f.key} className={`cx-wait__fact cx-wait__fact--${f.key}`} data-testid={`wait-${f.key}`}>{f.text}</li>
        ))}
      </ul>
    </div>
  )
}

// Many waits at once: one chip says how many wait and when the next deadline
// is; it opens to the card of each wait, each with its own Stop. Stop all ends
// the waits of the group only. It starts closed, because the person did not
// ask for any of them to be this wide.
function WaitGroup({ waits, chatId, chat }) {
  const { t } = useTranslation('chats')
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const now = useNow(true)
  const v = groupView(t, waits, now)
  const bodyId = `wait-group-${chatId}`
  const stopAll = async () => {
    setBusy(true)
    try {
      for (const w of waits) await chat.stopLoop(chatId, w.id)
    } finally { setBusy(false) }
  }
  return (
    <div className="cx-wait-group" data-testid="wait-group" data-open={open || undefined} data-count={v.count}>
      <div className="cx-wait cx-wait--run cx-wait-group__head">
        <button type="button" className="cx-wait-group__toggle" aria-expanded={open} aria-controls={open ? bodyId : undefined}
          aria-label={t(open ? 'loops.wait.group.collapse' : 'loops.wait.group.expand')}
          onClick={() => setOpen(o => !o)} data-testid="wait-group-toggle">
          <Icon name="hourglass" className="cx-wait__icon" />
          <b data-testid="wait-group-title">{t('loops.wait.group.title', { count: v.count })}</b>
          <ul className="cx-wait__facts cx-wait-group__facts">
            {v.facts.map(f => <li key={f.key} className={`cx-wait__fact cx-wait__fact--${f.key}`} data-testid={`wait-group-${f.key}`}>{f.text}</li>)}
          </ul>
          <Icon name="chevron-right" className="cx-wait-group__chev" />
        </button>
        <button type="button" className="cx-link cx-link--danger" disabled={busy} onClick={stopAll} data-testid="wait-group-stop-all">
          {t('loops.wait.group.stopAll')}
        </button>
      </div>
      {open && (
        <div className="cx-wait-group__body" id={bodyId} data-testid="wait-group-body">
          {waits.map(l => <WaitChip key={l.id} loop={l} chatId={chatId} chat={chat} />)}
        </div>
      )}
    </div>
  )
}

// LoopBar shows the loops of the open chat over the message box: a card for a
// loop that waits for a yes, and a chip for each other one.
export default function LoopBar({ loops, chatId, chat }) {
  const { t } = useTranslation('chats')
  const pending = pendingLoops(loops)
  const items = barItems(loops)
  if (!chatId || (pending.length === 0 && items.length === 0)) return null
  return (
    <section className="cx-loops" aria-label={t('loops.region')} data-testid="loop-bar">
      {pending.map(l => <LoopConfirm key={l.id} loop={l} chatId={chatId} chat={chat} />)}
      {items.map(i => {
        if (i.kind === 'group') return <WaitGroup key="wait-group" waits={i.waits} chatId={chatId} chat={chat} />
        return i.kind === 'wait'
          ? <WaitChip key={i.loop.id} loop={i.loop} chatId={chatId} chat={chat} />
          : <LoopChip key={i.loop.id} loop={i.loop} chatId={chatId} chat={chat} />
      })}
    </section>
  )
}

// LoopList is the answer to /loop list: every loop of the chat with its ID (the
// start of it, which /loop stop takes), prompt, pace, next run, runs and state.
export function LoopList({ loops, onClose }) {
  const { t } = useTranslation('chats')
  const list = Array.isArray(loops) ? loops : []
  return (
    <section className="cx-loop-list" aria-label={t('loops.list.title')} data-testid="loop-list">
      <div className="cx-loop-list__head">
        <strong>{t('loops.list.title')}</strong>
        <button type="button" className="cx-link" onClick={onClose} data-testid="loop-list-close">{t('loops.list.close')}</button>
      </div>
      {list.length === 0 ? (
        <p className="cx-loop-list__empty">{t('loops.list.empty')}</p>
      ) : (
        <ul className="cx-loop-list__rows">
          {list.map(l => (
            <li key={l.id} className="cx-loop-list__row" data-testid="loop-list-row">
              <code className="cx-loop-list__id">{shortId(l.id)}</code>
              <span className="cx-loop-list__prompt" title={l.prompt}>{l.prompt}</span>
              <span className="cx-loop-list__meta">
                {isWait(l) ? [t('loops.wait.kind'), loopSummary(t, l)].join(' · ') : [
                  l.interval_seconds > 0 ? t('loops.chip.every', { every: loopEvery(t, l.interval_seconds, true) }) : t('loops.chip.selfPaced'),
                  t(`loops.state.${l.state}`),
                  t('loops.chip.run', { count: l.runs }),
                  l.next && l.state === 'active' && !l.running ? t('loops.chip.next', { time: clock(l.next) }) : '',
                ].filter(Boolean).join(' · ')}
              </span>
            </li>
          ))}
        </ul>
      )}
      <p className="cx-loop-list__hint">{t('loops.list.hint')}</p>
    </section>
  )
}
