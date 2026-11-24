/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import './live.css'

// The Live button of the attach row. It starts Live (the click is the gesture
// the browser needs to allow the voice later). Without a secure page or a
// microphone it is disabled and says why next to it. Without a transcription
// or a speech model it stays usable and asks for the setup instead.
//
//   live         the result of useLive
//   onNeedSetup  called when a model is missing (opens the setup sheet)
//   buttonRef    reaches the button, to give it focus after Live ends
export default function LiveButton({ live, onNeedSetup, buttonRef }) {
  const { t } = useTranslation('chat')
  const reason = live.unavailableReason
  const blocked = reason === 'insecure' || reason === 'no-mic'
  const missing = reason === 'no-stt' || reason === 'no-tts'
  const click = () => {
    if (missing) onNeedSetup?.(reason)
    else live.start()
  }
  return (
    <>
      <Button
        ref={buttonRef}
        variant="ghost"
        size="sm"
        icon="waveform"
        className="home-attach-btn"
        data-testid="live-button"
        disabled={blocked}
        onClick={click}
        title={blocked ? t(`live.reason.${reason}.detail`) : t('live.title')}
        aria-describedby={blocked ? 'live-reason' : undefined}
      >
        {t('live.button')}
      </Button>
      {blocked && (
        <span className="live-reason" id="live-reason" data-testid="live-reason">{t(`live.reason.${reason}.short`)}</span>
      )}
    </>
  )
}
