/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Button from '../ui/Button'
import { chipLoops, loopEvery, pendingLoops, shortId } from '../../utils/chatLoops'

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

// LoopBar shows the loops of the open chat over the message box: a card for a
// loop that waits for a yes, and a chip for each other one.
export default function LoopBar({ loops, chatId, chat }) {
  const { t } = useTranslation('chats')
  const pending = pendingLoops(loops)
  const chips = chipLoops(loops)
  if (!chatId || (pending.length === 0 && chips.length === 0)) return null
  return (
    <section className="cx-loops" aria-label={t('loops.region')} data-testid="loop-bar">
      {pending.map(l => <LoopConfirm key={l.id} loop={l} chatId={chatId} chat={chat} />)}
      {chips.map(l => <LoopChip key={l.id} loop={l} chatId={chatId} chat={chat} />)}
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
                {[
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
