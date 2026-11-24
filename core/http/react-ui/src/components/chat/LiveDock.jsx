/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import StatusChip from '../ui/StatusChip'
import './live.css'

// The dock of Live: it takes the place of the message box while Live is on.
// One orb and one state line, the mode, and the few things a person can do.
// There is no primary button: the conversation goes on by voice.
//
//   live           the result of useLive
//   waiting        the chat is working on a turn the voice did not start
//   onTypeInstead  ends Live and moves the person to the message box
//   onEnd          ends Live (the page then gives focus back to the Live button)
// The machine's own English fallbacks. They say nothing the localised line
// does not, so they are not repeated as a detail.
const FALLBACK_TEXT = /^(microphone access was denied|the microphone is not available|transcription (failed|timed out)|the message could not be sent|the reply could not be read|the voice could not be played)\.?$/i

export default function LiveDock({ live, waiting = false, onTypeInstead, onEnd }) {
  const { t } = useTranslation('chat')
  const { state } = live
  const name = state.name
  const [muted, setMuted] = useState(false)
  // Focus moves into the dock when Live starts: the button that was pressed is
  // replaced, and a keyboard user keeps their place.
  const root = useRef(null)
  useEffect(() => { root.current?.focus() }, [])
  const [tabHidden, setTabHidden] = useState(() => typeof document !== 'undefined' && document.hidden)
  useEffect(() => {
    const onVisibility = () => setTabHidden(document.hidden)
    document.addEventListener('visibilitychange', onVisibility)
    return () => document.removeEventListener('visibilitychange', onVisibility)
  }, [])

  const toggleMute = () => {
    if (muted) live.unmute()
    else live.mute()
    setMuted(!muted)
  }

  const isError = name === 'error'
  const code = state.error?.code
  const errorText = t(`live.error.${code}`, { defaultValue: t('live.error.generic') })
  const rawDetail = code === 'denied' ? '' : state.error?.message || ''
  const detail = FALLBACK_TEXT.test(rawDetail.trim()) ? '' : rawDetail
  const retry = () => {
    // The Retry button goes away with the error; keep focus in the dock.
    root.current?.focus()
    live.retry()
  }
  const waitingForReply = waiting && (name === 'listening' || name === 'hearing')

  return (
    <div className="live-dock" data-testid="live-dock" data-state={name} ref={root} tabIndex={-1}>
      <div className="live-dock__head">
        <span className="live-orb" data-state={name} aria-hidden="true" />
        <div className="live-dock__status">
          <p className="live-dock__state" data-testid="live-state" data-state={name} aria-live="polite">
            {t(`live.state.${name}`)}
            {waitingForReply && <span className="live-dock__sub">{t('live.waiting')}</span>}
          </p>
        </div>
        <StatusChip tone="idle" icon="waveform" testId="live-mode">{t('live.mode')}</StatusChip>
      </div>

      {isError && (
        <Callout
          tone="error"
          role="alert"
          testId="live-error"
          actions={(
            <>
              <Button size="sm" data-testid="live-retry" onClick={retry}>{t('live.retry')}</Button>
              <Button size="sm" variant="ghost" data-testid="live-type" onClick={onTypeInstead}>{t('live.typeInstead')}</Button>
              <Button size="sm" data-testid="live-end" onClick={onEnd || live.end}>{t('live.end')}</Button>
            </>
          )}
        >
          {errorText}
          {detail && <small className="live-dock__detail" data-testid="live-error-detail">{detail}</small>}
        </Callout>
      )}
      {name === 'blocked' && (
        <Callout
          tone="warn"
          testId="live-blocked"
          actions={<Button size="sm" icon="play" data-testid="live-play" onClick={live.resume}>{t('live.play')}</Button>}
        >
          {t('live.blocked')}
        </Callout>
      )}
      {name === 'paused' && tabHidden && (
        <Callout tone="info" testId="live-hidden">{t('live.hidden')}</Callout>
      )}

      {!isError && (
        <div className="live-dock__actions">
          {name !== 'starting' && (
            <Button size="sm" variant="ghost" icon="mic" data-testid="live-mute" aria-pressed={muted} onClick={toggleMute}>
              {muted ? t('live.unmute') : t('live.mute')}
            </Button>
          )}
          {(name === 'thinking' || name === 'speaking' || name === 'blocked') && (
            <Button size="sm" variant="ghost" icon="stop" data-testid="live-stop-voice" onClick={live.stopVoice}>{t('live.stop')}</Button>
          )}
          <Button size="sm" variant="ghost" icon="keyboard" data-testid="live-type" onClick={onTypeInstead}>{t('live.typeInstead')}</Button>
          <Button size="sm" data-testid="live-end" onClick={onEnd || live.end}>{t('live.end')}</Button>
        </div>
      )}
    </div>
  )
}
