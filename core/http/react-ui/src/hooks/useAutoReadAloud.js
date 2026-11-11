import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useWellKnown } from './useWellKnown'
import { messageParts } from '../utils/chatAttachments'
import { installedModels, speak, ttsModel } from '../utils/readAloud'
import { readAloudText, voiceOf } from '../utils/sound'

// With voice.readAloud "auto" in ui.json, the reply that just finished is read
// aloud. It runs once per reply, when the chat goes from running to idle, and
// does nothing when the browser has not had a gesture yet (the speech then
// fails quietly; the speaker under the reply still works). The text goes to the
// default text-to-speech model, which the Sound tab names.
export function useAutoReadAloud({ running, messages, activeId, failed }) {
  const { t } = useTranslation('chat')
  const ui = useWellKnown('ui').doc?.data
  const defaults = useWellKnown('defaults').doc?.data
  const was = useRef(false)
  const spoken = useRef('')
  const latest = useRef({})
  latest.current = { messages, ui, defaults, activeId, failed, t }

  useEffect(() => {
    const ended = was.current && !running
    was.current = running
    if (!ended) return
    const { messages: list, ui: u, defaults: d, activeId: chatId, failed: bad, t: tr } = latest.current
    const voice = voiceOf(u)
    if (voice.readAloud !== 'auto' || bad) return
    const last = (list || [])[list.length - 1]
    if (!last || last.role !== 'assistant') return
    const id = `${chatId}:${last.seq}`
    if (spoken.current === id) return
    const body = messageParts(last).text
    if (!body) return
    spoken.current = id
    installedModels().then((models) => {
      const pick = ttsModel(d, u, models)
      if (!pick.model || pick.state === 'ask' || pick.state === 'missing' || pick.state === 'none') return
      const words = readAloudText(body, { skipCode: voice.skipCode, omitted: (kind) => tr(`readAloud.omitted.${kind}`) })
      if (!words) return
      return speak(id, words, { model: pick.model, voice: voice.voice, speed: voice.speed })
    }).catch(() => {})
  }, [running])
}
