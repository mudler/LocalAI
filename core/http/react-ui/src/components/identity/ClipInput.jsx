import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMediaCapture } from '../../hooks/useMediaCapture'
import useObjectUrl from '../../hooks/useObjectUrl'
import { fileToBase64 } from '../../utils/api'
import { judgeClip } from '../../utils/identity'
// eslint-disable-next-line no-unused-vars
import WaveformPlayer from '../audio/WaveformPlayer'
import Icon from '../Icon'

// Read a clip in the browser: how long it is and how loud. Nothing is sent.
async function readClip(blob) {
  const Ctx = window.AudioContext || window.webkitAudioContext
  if (!Ctx || !blob) return null
  const ctx = new Ctx()
  try {
    const buf = await ctx.decodeAudioData(await blob.arrayBuffer())
    const data = buf.getChannelData(0)
    let peak = 0
    let hot = 0
    for (let i = 0; i < data.length; i += 1) {
      const v = Math.abs(data[i])
      if (v > peak) peak = v
      if (v >= 0.99) hot += 1
    }
    return { seconds: buf.duration, peak, clipped: hot / data.length > 0.001 }
  } catch {
    return null
  } finally {
    ctx.close().catch(() => {})
  }
}

// A permission state read without asking: 'denied', 'prompt', 'granted' or ''.
function useMicState(enabled) {
  const [state, setState] = useState('')
  useEffect(() => {
    if (!enabled || !navigator.permissions?.query) return undefined
    let status
    let live = true
    navigator.permissions.query({ name: 'microphone' }).then((s) => {
      if (!live) return
      status = s
      setState(s.state)
      s.onchange = () => setState(s.state)
    }).catch(() => {})
    return () => { live = false; if (status) status.onchange = null }
  }, [enabled])
  return state
}

// One sample, in the kit's own look: a drop of choices before there is a
// sample, a card with the facts after. It is the input for both the Voices and
// the Faces pages.
//
//   kind        'audio' or 'image'
//   value       null or { base64, blob, dataUrl, mime, source, name }
//   onChange    gets the same shape, or null on Remove
//   idPrefix    the file input is `${idPrefix}-${kind}-file`
export default function ClipInput({ kind, label, value, onChange, idPrefix, hint, prompt, onFacts }) {
  const { t } = useTranslation('biometrics')
  const fileRef = useRef(null)
  const cap = useMediaCapture(kind)
  const [facts, setFacts] = useState(null)
  const [busy, setBusy] = useState(false)
  const micState = useMicState(kind === 'audio')
  const secure = typeof window === 'undefined' || (window.isSecureContext ?? true)
  const live = kind === 'audio' ? t('clip.mic') : t('clip.camera')

  useEffect(() => {
    let current = true
    setFacts(null)
    onFacts?.(null)
    if (kind === 'audio' && value?.blob) readClip(value.blob).then((f) => { if (current) { setFacts(f); onFacts?.(f) } })
    return () => { current = false }
  }, [kind, value]) // eslint-disable-line react-hooks/exhaustive-deps

  const take = async (file, source = 'file') => {
    setBusy(true)
    try {
      const base64 = await fileToBase64(file)
      const dataUrl = await new Promise((resolve, reject) => {
        const reader = new FileReader()
        reader.onerror = () => reject(reader.error)
        reader.onload = () => resolve(reader.result)
        reader.readAsDataURL(file)
      })
      const name = source === 'paste' ? `pasted-image.${(file.type.split('/')[1] || 'png').replace('+xml', '')}` : file.name
      onChange({ base64, blob: file, dataUrl, mime: file.type, source, name })
    } finally {
      setBusy(false)
    }
  }

  const onFile = (e) => {
    const f = e.target.files?.[0]
    if (f) take(f)
    else onChange(null)
  }

  const onPaste = (e) => {
    if (kind !== 'image') return
    const item = Array.from(e.clipboardData?.items || []).find(entry => entry.type.startsWith('image/'))
    const f = item?.getAsFile()
    if (!f) return
    e.preventDefault()
    take(f, 'paste')
  }

  const record = async () => {
    if (cap.recording) { cap.stopRecording(); return }
    const pending = cap.startRecording()
    if (!pending) return
    const result = await pending
    cap.stop()
    onChange({ ...result, source: 'live', name: 'recording.wav' })
  }

  const snap = () => {
    const shot = cap.snap()
    if (shot) { cap.stop(); onChange({ ...shot, source: 'live', name: 'photo.png' }) }
  }

  const clear = () => {
    onChange(null)
    if (fileRef.current) fileRef.current.value = ''
  }

  const playUrl = useObjectUrl(kind === 'audio' ? value?.blob : null)
  const verdict = facts ? judgeClip(facts) : null
  const blocked = cap.errorKind === 'denied' || micState === 'denied'
  const inputId = `${idPrefix}-${kind}-file`

  return (
    <div className="idn-clip" onPaste={onPaste} data-state={value ? 'filled' : 'empty'} data-testid={`${idPrefix}-clip`}>
      <div className="idn-clip__head">
        <span className="dk-eyebrow">{label}</span>
        {value && <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={clear}><Icon name="close" /> {t('clip.remove')}</button>}
      </div>

      <input ref={fileRef} id={inputId} type="file" className="dk-sr-only" accept={kind === 'audio' ? 'audio/*' : 'image/*'} onChange={onFile} aria-label={label} />

      {!value && !cap.active && (
        <div className="idn-clip__choices">
          {prompt && <p className="idn-clip__prompt"><span className="dk-eyebrow">{t('clip.readAloud')}</span>{prompt}</p>}
          <div className="idn-clip__buttons">
            {cap.supported && !blocked && (
              <button type="button" className="dk-btn dk-btn--secondary" onClick={cap.start}>
                <Icon name={kind === 'audio' ? 'mic' : 'camera'} /> {kind === 'audio' ? t('clip.record') : t('clip.takePhoto')}
              </button>
            )}
            <button type="button" className="dk-btn dk-btn--secondary" onClick={() => fileRef.current?.click()} disabled={busy}>
              <Icon name="upload" /> {kind === 'audio' ? t('clip.chooseAudio') : t('clip.choosePhoto')}
            </button>
          </div>
          {(!cap.supported || blocked || cap.error) && (
            <p className="idn-clip__notice" role={blocked ? 'alert' : 'status'} data-testid="clip-notice">
              <Icon name={blocked || !secure ? 'lock' : 'info'} />
              <span>
                {blocked && t('clip.blocked', { thing: live })}
                {!blocked && !cap.supported && !secure && t('clip.insecure', { thing: live, origin: window.location.origin })}
                {!blocked && !cap.supported && secure && t('clip.unsupported', { thing: live })}
                {!blocked && cap.supported && cap.error && t(cap.errorKind === 'missing' ? 'clip.missing' : 'clip.failed', { thing: live })}
              </span>
            </p>
          )}
          {cap.supported && !blocked && !cap.error && micState === 'granted' && kind === 'audio' && (
            <p className="idn-clip__hint" data-testid="mic-allowed"><Icon name="check" /> {t('clip.allowed')}</p>
          )}
          {hint && <p className="idn-clip__hint">{hint}</p>}
        </div>
      )}

      {!value && cap.active && kind === 'image' && (
        <div className="idn-clip__live">
          <video ref={cap.videoRef} autoPlay muted playsInline className="idn-clip__video" />
          <div className="idn-clip__buttons">
            <button type="button" className="dk-btn dk-btn--primary" onClick={snap}><Icon name="circle-dot" /> {t('clip.capture')}</button>
            <button type="button" className="dk-btn dk-btn--ghost" onClick={cap.stop}>{t('clip.cancel')}</button>
          </div>
        </div>
      )}

      {!value && cap.active && kind === 'audio' && (
        <div className="idn-clip__live">
          <p className="idn-clip__rec" role="status"><span className="dk-dot dk-dot--error" data-live={cap.recording ? 'true' : undefined} /> {cap.recording ? t('clip.recording', { s: cap.elapsed.toFixed(1) }) : t('clip.micReady')}</p>
          <div className="idn-clip__buttons">
            <button type="button" className={`dk-btn ${cap.recording ? 'dk-btn--primary' : 'dk-btn--secondary'}`} onClick={record}>
              <Icon name={cap.recording ? 'stop' : 'circle'} /> {cap.recording ? t('clip.stop') : t('clip.start')}
            </button>
            <button type="button" className="dk-btn dk-btn--ghost" onClick={cap.stop} disabled={cap.recording}>{t('clip.cancel')}</button>
          </div>
        </div>
      )}

      {value && (
        <div className="idn-clip__card">
          <div className="idn-clip__meta">
            <Icon name={value.source === 'live' ? (kind === 'audio' ? 'mic' : 'camera') : kind === 'audio' ? 'file' : 'image'} />
            <strong className="idn-clip__name">{value.source === 'live' ? t(kind === 'audio' ? 'clip.recorded' : 'clip.photographed') : value.name || t('clip.uploaded')}</strong>
            {facts && <span className="dk-chip dk-chip--sm dk-mono">{facts.seconds.toFixed(1)} s</span>}
            {verdict?.level === 'good' && <span className="dk-badge dk-badge--ok"><Icon name="check" /> {t('clip.goodLength')}</span>}
            {verdict?.level === 'warn' && <span className="dk-badge dk-badge--warn"><Icon name="warning" /> {facts.seconds < 3 ? t('clip.short') : t('clip.checkLevel')}</span>}
          </div>
          {kind === 'audio'
            ? <WaveformPlayer src={playUrl || value.dataUrl} height={56} label={t('clip.preview')} />
            : <img className="idn-clip__img" src={value.dataUrl} alt={t('clip.photoAlt')} />}
          {verdict?.level === 'warn' && <ul className="idn-clip__notes">{verdict.notes.map(n => <li key={n}>{n}</li>)}</ul>}
        </div>
      )}
    </div>
  )
}
