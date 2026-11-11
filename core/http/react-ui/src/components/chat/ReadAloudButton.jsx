import { useEffect, useState, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { useWellKnown } from '../../hooks/useWellKnown'
import { installedModels, readAloudState, speak, stopReadAloud, subscribeReadAloud, ttsModel } from '../../utils/readAloud'
import { readAloudText, voiceOf } from '../../utils/sound'

// The speaker under a reply: reads it aloud with the default text-to-speech
// model of the user. It shows only when ui.json voice.readAloud is button or
// auto. A click is the gesture the browser needs. The models are read on the
// click, not for every message.
export default function ReadAloudButton({ id, text, className = 'cx-icobtn' }) {
  const { t } = useTranslation('chat')
  const ui = useWellKnown('ui').doc?.data
  const defaults = useWellKnown('defaults').doc?.data
  const voice = voiceOf(ui)
  const snap = useSyncExternalStore(subscribeReadAloud, readAloudState)
  const [problem, setProblem] = useState('')
  const mine = snap.id === id
  const status = mine ? snap.status : 'idle'
  useEffect(() => { if (status === 'playing') setProblem('') }, [status])
  if (voice.readAloud === 'off' || !text) return null

  const click = async () => {
    if (status === 'playing' || status === 'loading') { stopReadAloud(); return }
    setProblem('')
    try {
      const pick = ttsModel(defaults, ui, await installedModels())
      if (!pick.model || pick.state === 'ask' || pick.state === 'missing' || pick.state === 'none') { setProblem(t(`readAloud.${pick.state === 'ask' ? 'ask' : pick.state === 'none' ? 'none' : 'missing'}`)); return }
      const words = readAloudText(text, { skipCode: voice.skipCode, omitted: (kind) => t(`readAloud.omitted.${kind}`) })
      if (!words) return
      await speak(id, words, { model: pick.model, voice: voice.voice, speed: voice.speed })
    } catch {
      setProblem(t('readAloud.failed'))
    }
  }
  const label = status === 'playing' || status === 'loading' ? t('readAloud.stop') : t('readAloud.read')
  return (
    <>
      <button type="button" className={className} onClick={click} title={label} aria-label={label} aria-pressed={status === 'playing' || status === 'loading'} data-testid="message-read-aloud" data-state={status}>
        <Icon name={status === 'loading' ? 'spinner' : status === 'playing' ? 'stop' : 'volume'} />
      </button>
      {problem && <span className="cx-help" role="status" data-testid="message-read-aloud-problem">{problem}</span>}
    </>
  )
}
