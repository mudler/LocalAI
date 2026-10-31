import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import Popover from '../Popover'
// eslint-disable-next-line no-unused-vars
import { SidePane } from '../ui'
import { usePhone } from '../../hooks/useSwarm'
import { CONTEXT_HOT_PCT, CONTEXT_WARN_PCT, contextReading, tokenCount } from '../../utils/chatSettings'
import { averageSpeed, liveRate, speedView, speedValue } from '../../utils/chatFoot'
import { formatDuration } from '../../utils/toolLabels'
import { goalProgress } from '../../utils/chatGoal'

// How often the line reads the pace of a stream: at most twice a second, so
// the speed does not jitter with every piece.
const PACE_MS = 500
const SHEET_QUERY = '(max-width: 639px)'

// The bar of the meter: the fill slides in with a transform, and the track
// marks where the meter warns and where it is hot.
const barOf = (pct) => ({
  '--cx-fill': Math.max(0, Math.min(100, pct || 0)) / 100,
  '--cx-warn': CONTEXT_WARN_PCT / 100,
  '--cx-hot': CONTEXT_HOT_PCT / 100,
})

// The status line under the chat composer. On the left one live state (an
// icon or a dot, a word and a short detail), and the keyboard hint while the
// composer waits for a first word. On the right the context meter, a button
// that opens what the last model request used.
//
//   state      chatFootState(): { key, tone, busy, icon, tool, arg, count }
//   status     the sentence of a failed or interrupted turn, shown as detail
//   model      the chat's model; backend the engine that serves it, if known
//   hint       show the keyboard hint
//   chat       useServerChat(): usage, usageHistory, firstTokenMs, readPace
//   showMeter  the chat exists, so a meter can show
//   quiet      a new, empty chat: the empty state under the composer already
//              says whether the model is ready, so only a busy state shows
//   onContinue set when the last turn paused at the step limit: the line
//              shows a Continue button that sends a message to go on
//   goal       the chat's goal: while it is active the line shows its
//              automatic turn ("Goal · turn 3 of 20")
export default function ChatFoot({ state, status, model, backend, hint, chat, showMeter, quiet = false, onContinue, goal = null }) {
  const { t, i18n } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')
  const phone = usePhone(SHEET_QUERY)
  const [open, setOpen] = useState(false)
  const anchor = useRef(null)
  const bodyRef = useRef(null)
  const running = state.busy && state.key !== 'sending'
  const progress = goalProgress(goal)
  const live = useLiveRate(running, chat.readPace)
  const meter = contextReading(chat.usage)
  const speed = meter ? speedView({ live, measured: chat.usage?.tokens_per_second, running }) : null

  // Escape closes the details and nothing else: the page stops a running turn
  // on Escape, so the key is marked as used before it gets there.
  useEffect(() => {
    if (!open) return undefined
    const onKey = (e) => { if (e.key === 'Escape') e.preventDefault() }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [open])

  // The details take focus when they open, so Tab and Escape work from there.
  useEffect(() => {
    if (!open || phone) return undefined
    const raf = requestAnimationFrame(() => bodyRef.current?.focus({ preventScroll: true }))
    return () => cancelAnimationFrame(raf)
  }, [open, phone])

  const word = state.key !== 'tool' ? t(`foot.state.${state.key}`)
    : state.verb && state.verb !== 'used' ? t(`foot.tool.${state.verb}`) : t('foot.state.tool', { tool: state.tool })
  const detail = state.key === 'tool' ? state.arg
    : state.key === 'loading' ? model
      : state.key === 'waiting' ? tc('pending.header', { count: state.count })
        : (state.key === 'failed' || state.key === 'interrupted' || state.key === 'paused') ? status : ''

  const n = (v) => new Intl.NumberFormat(i18n.language || undefined).format(v)
  // The line: used / limit, or the percentage where the line is narrow, and
  // what is left. With no known limit only the count used.
  const meterText = meter && (meter.limit
    ? t('meter.ofLimit', { used: tokenCount(meter.used), limit: tokenCount(meter.limit) })
    : t('meter.used', { used: tokenCount(meter.used) }))
  const meterShort = meter && (meter.pct !== null ? `${meter.pct}%` : tokenCount(meter.used))
  const meterLeft = meter && meter.left !== null ? t('meter.left', { left: tokenCount(meter.left) }) : ''
  const meterFull = meter && (meter.limit
    ? t('meter.usedOf', { used: tokenCount(meter.used), limit: tokenCount(meter.limit), pct: meter.pct })
    : meterText)
  const meterName = meter && (meter.pct !== null
    ? t('foot.meter.label', { pct: meter.pct })
    : t('foot.meter.labelNoLimit', { used: n(meter.used) }))

  // The name starts with what the button shows, so voice control finds it.
  const speedText = speed ? `${speed.approx ? '≈' : ''}${t('meter.speed', { speed: speed.value })}` : ''
  const meterAccessible = meter && [phone && meter.limit ? meterShort : meterFull, meterLeft, speedText, t('foot.details.title')].filter(Boolean).join(' · ')

  const details = meter && (
    <MeterDetails
      bodyRef={bodyRef}
      meter={meter}
      usage={chat.usage}
      model={chat.usage?.model || model}
      backend={backend}
      speed={speed}
      average={averageSpeed(chat.usageHistory)}
      firstTokenMs={chat.firstTokenMs}
      n={n}
      t={t}
      lng={i18n.language}
      phone={phone}
    />
  )

  return (
    <div className="cx-foot" data-testid="chat-foot">
      <span className="cx-foot__state" data-testid="chat-state" data-state={state.key} data-tone={state.tone} data-busy={state.busy || undefined} data-quiet={(quiet && !state.busy) || undefined}>
        <span className="cx-foot__mark" aria-hidden="true">
          {state.icon ? <Icon name={state.icon} spin={state.icon === 'spinner'} /> : <i className="cx-foot__dot" />}
        </span>
        <span className="cx-foot__live" role="status" aria-live="polite">
          {/* Keyed by the state, so a new state fades in. */}
          <span key={state.key} className="cx-foot__word">{word}</span>
          {detail && <span className="cx-foot__detail" title={detail}>{detail}</span>}
        </span>
        {onContinue && (
          <button type="button" className="cx-foot__continue" data-testid="chat-continue" title={t('foot.paused.continueTitle')} onClick={onContinue}>
            <Icon name="play" />
            <span>{t('foot.paused.continue')}</span>
          </button>
        )}
        {progress && (
          <span className="cx-foot__goal" data-testid="chat-goal-progress" title={goal.text}>
            <Icon name="target" />
            <span>{t(progress.key, { turn: progress.turn, max: progress.max })}</span>
          </span>
        )}
        {hint && <span className="cx-foot__hint" data-testid="chat-foot-hint">{t('foot.hint')}</span>}
      </span>
      {showMeter && meter && (
        <button
          ref={anchor}
          type="button"
          className="cx-meter"
          data-testid="chat-context-meter"
          data-tone={meter.tone}
          data-limit={meter.limit ? undefined : 'unknown'}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-label={meterAccessible}
          title={meterName}
          onClick={() => setOpen(v => !v)}
        >
          {meter.tone !== 'ok' && <Icon name="warning" className="cx-meter__warn" />}
          {meter.pct !== null && <MeterBar pct={meter.pct} />}
          <span className="cx-meter__text">{meterText}</span>
          <span className="cx-meter__short">{meterShort}</span>
          {meterLeft && <span className="cx-meter__left" data-testid="chat-meter-left">{meterLeft}</span>}
          {speed && (
            <span className="cx-meter__speed" data-testid="chat-speed" data-stale={speed.stale || undefined} data-approx={speed.approx || undefined}>
              {speedText}
            </span>
          )}
        </button>
      )}
      {showMeter && meter && !phone && (
        <Popover anchor={anchor} open={open} onClose={() => setOpen(false)} ariaLabel={t('foot.details.title')}>
          {details}
        </Popover>
      )}
      {showMeter && meter && phone && (
        <SidePane open={open} title={t('foot.details.title')} onClose={() => setOpen(false)} className="cx-meter-sheet" testId="chat-meter-sheet">
          {details}
        </SidePane>
      )}
    </div>
  )
}

// The bar of the meter. Decorative: the figures beside it say the same.
// eslint-disable-next-line no-unused-vars -- used as JSX
function MeterBar({ pct, wide = false }) {
  return (
    <span className={`cx-meter__bar${wide ? ' cx-meter__bar--wide' : ''}`} aria-hidden="true" style={barOf(pct)}>
      <i />
    </span>
  )
}

// The page's estimate of the speed of the request that streams now, read from
// the pace at most twice a second. Null when no turn runs.
function useLiveRate(running, readPace) {
  const [rate, setRate] = useState(null)
  useEffect(() => {
    if (!running || !readPace) { setRate(null); return undefined }
    const read = () => {
      const next = speedValue(liveRate(readPace()))
      setRate(prev => (prev === next ? prev : next))
    }
    read()
    const id = setInterval(read, PACE_MS)
    return () => clearInterval(id)
  }, [running, readPace])
  return rate
}

// What the last model request used, in rows. A row the server gave no data for
// is left out.
// eslint-disable-next-line no-unused-vars -- used as JSX
function MeterDetails({ bodyRef, meter, usage, model, backend, speed, average, firstTokenMs, n, t, lng, phone }) {
  const rows = [
    model && { key: 'model', label: t('foot.details.model'), value: model, mono: true },
    backend && { key: 'backend', label: t('foot.details.backend'), value: backend, mono: true },
    usage?.node && { key: 'node', label: t('foot.details.node'), value: usage.node, mono: true },
    { key: 'in', label: t('foot.details.tokensIn'), value: n(usage?.used || 0) },
    usage?.completion > 0 && { key: 'out', label: t('foot.details.tokensOut'), value: n(usage.completion) },
    firstTokenMs !== null && firstTokenMs !== undefined && { key: 'ttft', label: t('foot.details.firstToken'), value: formatDuration(firstTokenMs, t, lng) },
    usage?.prompt_ms > 0 && { key: 'prompt', label: t('foot.details.promptTime'), value: formatDuration(usage.prompt_ms, t, lng) },
    speed && { key: 'speed', label: speed.approx ? t('foot.details.speedLive') : t('foot.details.speed'), value: `${speed.approx ? '≈' : ''}${t('meter.speed', { speed: speed.value })}` },
    average && { key: 'avg', label: t('foot.details.average', { count: average.count }), value: t('meter.speed', { speed: average.value }) },
  ].filter(Boolean)
  return (
    <div ref={bodyRef} className="cx-meter-pop" data-sheet={phone || undefined} tabIndex={-1} data-testid="chat-meter-details">
      {!phone && <h2 className="cx-meter-pop__title">{t('foot.details.title')}</h2>}
      <section className="cx-meter-pop__ctx" data-tone={meter.tone}>
        <h3 className="cx-meter-pop__head cx-meter-pop__head--first">{t('foot.details.context')}</h3>
        <p className="cx-meter-pop__figure" data-testid="chat-meter-context">
          {meter.limit
            ? t('foot.details.contextOf', { used: n(meter.used), limit: n(meter.limit), pct: meter.pct })
            : t('foot.details.contextUsed', { used: n(meter.used) })}
        </p>
        {meter.pct !== null && <MeterBar pct={meter.pct} wide />}
        {meter.left !== null && (
          <p className="cx-meter-pop__left" data-testid="chat-meter-left-detail">{t('foot.details.contextLeft', { left: n(meter.left) })}</p>
        )}
        <p className="cx-meter-pop__help">{t('foot.details.contextHelp')}</p>
        {meter.tone !== 'ok' && (
          <p className="cx-meter-pop__help cx-meter-pop__help--warn" data-testid="chat-meter-near-full">
            <Icon name="warning" /> <span>{t('foot.details.nearFull')}</span>
          </p>
        )}
        {!meter.limit && <p className="cx-meter-pop__help">{t('foot.details.limitUnknown')}</p>}
      </section>
      <h3 className="cx-meter-pop__head">{t('foot.details.lastRequest')}</h3>
      <dl className="cx-meter-pop__list">
        {rows.map(r => (
          <div key={r.key} className="cx-meter-pop__row" data-testid={`chat-meter-${r.key}`}>
            <dt className="cx-meter-pop__label">{r.label}</dt>
            <dd className={`cx-meter-pop__value${r.mono ? ' cx-meter-pop__value--mono' : ''}`}>{r.value}</dd>
          </div>
        ))}
      </dl>
      <p className="cx-meter-pop__note">{t('foot.details.note')}</p>
    </div>
  )
}
