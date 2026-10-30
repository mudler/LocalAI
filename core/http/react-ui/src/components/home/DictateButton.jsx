import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { buttonClass } from '../ui/buttonClass'
import { useModels } from '../../hooks/useModels'
import { CAP_TRANSCRIPT } from '../../utils/capabilities'
import { audioApi } from '../../utils/api'

// Dictation for the composer: the first click records from the microphone,
// the second stops and sends the recording to /v1/audio/transcriptions with
// the first installed transcription model. The text is handed to onText; the
// user reads and sends it like any typed message. Without a transcription
// model, or without a microphone API in the browser, the button is not shown.
export default function DictateButton({ onText, onError }) {
  const { t } = useTranslation('home')
  const { models } = useModels(CAP_TRANSCRIPT)
  const [state, setState] = useState('idle') // idle | recording | transcribing
  const recorder = useRef(null)
  const chunks = useRef([])
  const model = models[0]?.id

  // Release the microphone when the composer goes away mid-recording.
  useEffect(() => () => {
    const r = recorder.current
    if (r && r.state !== 'inactive') {
      r.onstop = null
      r.stop()
    }
    r?.stream?.getTracks().forEach(tr => tr.stop())
  }, [])

  const transcribe = useCallback(async (blob) => {
    setState('transcribing')
    try {
      const fd = new FormData()
      fd.append('file', blob, `dictation.${(blob.type.split('/')[1] || 'webm').split(';')[0]}`)
      fd.append('model', model)
      const data = await audioApi.transcribe(fd)
      const text = (data?.text || '').trim()
      if (text) onText(text)
      else onError?.(t('input.dictateEmpty'))
    } catch (err) {
      onError?.(t('input.dictateFailed', { error: err?.message || String(err) }))
    } finally {
      setState('idle')
    }
  }, [model, onText, onError, t])

  const toggle = useCallback(async () => {
    if (state === 'recording') {
      recorder.current?.stop()
      return
    }
    if (state !== 'idle') return
    let stream
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch (err) {
      onError?.(t('input.dictateNoMic', { error: err?.message || String(err) }))
      return
    }
    const r = new MediaRecorder(stream)
    chunks.current = []
    r.ondataavailable = (e) => { if (e.data?.size) chunks.current.push(e.data) }
    r.onstop = () => {
      stream.getTracks().forEach(tr => tr.stop())
      const blob = new Blob(chunks.current, { type: r.mimeType || 'audio/webm' })
      if (blob.size) transcribe(blob)
      else setState('idle')
    }
    recorder.current = r
    r.start()
    setState('recording')
  }, [state, transcribe, onError, t])

  if (!model || typeof navigator === 'undefined' || !navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === 'undefined') {
    return null
  }
  const label = state === 'recording' ? t('input.dictateStop') : state === 'transcribing' ? t('input.dictateBusy') : t('input.dictate')
  return (
    <button
      type="button"
      className={buttonClass({ variant: 'ghost', size: 'sm', iconOnly: true, className: 'home-attach-btn' })}
      data-testid="chat-dictate"
      data-state={state}
      aria-pressed={state === 'recording'}
      disabled={state === 'transcribing'}
      onClick={toggle}
      title={label}
      aria-label={label}
    >
      <Icon name={state === 'recording' ? 'stop' : state === 'transcribing' ? 'spinner' : 'mic'} />
    </button>
  )
}
