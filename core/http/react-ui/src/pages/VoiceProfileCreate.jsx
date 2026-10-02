import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
import MediaInput from '../components/biometrics/MediaInput'
import WaveformPlayer from '../components/audio/WaveformPlayer'
import { audioBufferToWavBlob } from '../hooks/useMediaCapture'
import ModelSelector from '../components/ModelSelector'
import { CAP_TTS } from '../utils/capabilities'
import { ttsApi, voiceProfilesApi } from '../utils/api'

const MAX_AUDIO_BYTES = 50 * 1024 * 1024
const REFERENCE_SAMPLE_RATE = 24000

function base64ToArrayBuffer(value) {
  const binary = window.atob(value)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index)
  return bytes.buffer
}

async function normalizeAudioSample(sample) {
  const source = sample.blob?.arrayBuffer
    ? await sample.blob.arrayBuffer()
    : base64ToArrayBuffer(sample.base64)
  const AudioCtx = window.AudioContext || window.webkitAudioContext
  if (!AudioCtx) throw new Error('Web Audio API is not available in this browser')
  const context = new AudioCtx()
  try {
    const decoded = await context.decodeAudioData(source.slice(0))
    const blob = audioBufferToWavBlob(decoded, REFERENCE_SAMPLE_RATE)
    if (blob.size > MAX_AUDIO_BYTES) throw new Error('Normalized audio is larger than 50 MiB')
    return {
      ...sample,
      blob,
      dataUrl: URL.createObjectURL(blob),
      objectUrl: true,
      mime: 'audio/wav',
      duration: decoded.duration,
      sampleRate: REFERENCE_SAMPLE_RATE,
      name: sample.name || 'recording.wav',
    }
  } finally {
    await context.close().catch(() => {})
  }
}

function ReadinessItem({ ready, warning, children }) {
  const tone = ready ? 'ready' : warning ? 'warning' : 'pending'
  return (
    <li className={`voice-readiness__item voice-readiness__item--${tone}`}>
      <i className={`fas ${ready ? 'fa-check' : warning ? 'fa-triangle-exclamation' : 'fa-minus'}`} aria-hidden="true" />
      <span>{children}</span>
    </li>
  )
}

export default function VoiceProfileCreate() {
  const { t } = useTranslation('media')
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const [referenceMode, setReferenceMode] = useState('upload')
  const [designModel, setDesignModel] = useState('')
  const [instructions, setInstructions] = useState('')
  const [sampleText, setSampleText] = useState('')
  const [generating, setGenerating] = useState(false)
  const generationId = useRef(0)
  const [audio, setAudio] = useState(null)
  const [audioProcessing, setAudioProcessing] = useState(false)
  const [audioError, setAudioError] = useState('')
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [language, setLanguage] = useState('')
  const [transcript, setTranscript] = useState('')
  const [additionalReferences, setAdditionalReferences] = useState([])
  const [consent, setConsent] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => () => {
    if (audio?.objectUrl) URL.revokeObjectURL(audio.dataUrl)
  }, [audio])

  useEffect(() => () => { generationId.current += 1 }, [])

  const designBusy = generating || audioProcessing || submitting
  const canGenerate = !!(designModel && instructions.trim() && sampleText.trim()) && !designBusy

  const clearReference = () => {
    setAudio(null)
    setTranscript('')
    setAudioError('')
  }

  const changeDesign = (setter, value) => {
    clearReference()
    setter(value)
  }

  const generateReference = async () => {
    if (!canGenerate) return
    const currentGeneration = ++generationId.current
    const input = sampleText.trim()
    clearReference()
    setGenerating(true)
    try {
      const { blob } = await ttsApi.generate({ model: designModel, input, instructions: instructions.trim() })
      if (currentGeneration !== generationId.current) return
      const normalized = await normalizeAudioSample({ blob, name: 'designed-reference.wav' })
      if (currentGeneration !== generationId.current) {
        URL.revokeObjectURL(normalized.dataUrl)
        return
      }
      if (normalized.duration < 1 || normalized.duration > 120) {
        URL.revokeObjectURL(normalized.dataUrl)
        throw new Error(t('voiceCreate.audio.durationError'))
      }
      setAudio(normalized)
      setTranscript(input)
    } catch (err) {
      if (currentGeneration === generationId.current) setAudioError(err.message || t('voiceCreate.design.error'))
    } finally {
      if (currentGeneration === generationId.current) setGenerating(false)
    }
  }

  const durationValid = audio?.duration >= 1 && audio?.duration <= 120
  const durationRecommended = audio?.duration >= 6 && audio?.duration <= 30
  const additionalReady = additionalReferences.every(reference => reference.audio && reference.transcript.trim())
  const formReady = !!audio && durationValid && name.trim() && transcript.trim() && additionalReady && consent && !audioProcessing && !generating

  const readiness = useMemo(() => ({
    audio: !!audio && durationValid,
    quality: !!audio && durationRecommended,
    name: !!name.trim(),
    transcript: !!transcript.trim(),
    consent,
  }), [audio, durationValid, durationRecommended, name, transcript, consent])

  const handleAudio = async (sample) => {
    if (!sample) {
      setAudio(null)
      setAudioError('')
      return
    }
    setAudioProcessing(true)
    setAudioError('')
    try {
      const normalized = await normalizeAudioSample(sample)
      if (normalized.duration < 1 || normalized.duration > 120) {
        if (normalized.objectUrl) URL.revokeObjectURL(normalized.dataUrl)
        throw new Error(t('voiceCreate.audio.durationError'))
      }
      setAudio(normalized)
    } catch (err) {
      setAudio(null)
      setAudioError(err.message || t('voiceCreate.audio.decodeError'))
    } finally {
      setAudioProcessing(false)
    }
  }

  const handleAdditionalAudio = async (index, sample) => {
    if (!sample) {
      setAdditionalReferences(current => current.map((reference, itemIndex) => itemIndex === index ? { ...reference, audio: null } : reference))
      return
    }
    setAudioProcessing(true)
    try {
      const normalized = await normalizeAudioSample(sample)
      if (normalized.duration < 1 || normalized.duration > 120) throw new Error(t('voiceCreate.audio.durationError'))
      setAdditionalReferences(current => current.map((reference, itemIndex) => itemIndex === index ? { ...reference, audio: normalized } : reference))
    } catch (err) {
      addToast(err.message || t('voiceCreate.audio.decodeError'), 'error')
    } finally {
      setAudioProcessing(false)
    }
  }

  const submit = async (event) => {
    event.preventDefault()
    if (!formReady) return
    setSubmitting(true)
    try {
      const formData = new FormData()
      formData.append('name', name.trim())
      formData.append('description', description.trim())
      formData.append('language', language.trim())
      formData.append('transcript', transcript.trim())
      formData.append('consent_confirmed', 'true')
      formData.append('audio', audio.blob, 'reference.wav')
      additionalReferences.forEach((reference, index) => {
        formData.append('transcript', reference.transcript.trim())
        formData.append('audio', reference.audio.blob, `reference-${index + 2}.wav`)
      })
      const profile = await voiceProfilesApi.create(formData)
      addToast(t('voiceCreate.toasts.created', { name: profile.name }), 'success')
      navigate(`/app/voice-library?selected=${encodeURIComponent(profile.id)}`)
    } catch (err) {
      addToast(err.message, 'error')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="voice-create-page">
      <UnsavedChangesGuard
        when={!submitting && !!(audio || additionalReferences.length || name || description || language || transcript || consent || instructions || sampleText)}
      />
      <PageHeader
        eyebrow={t('voiceCreate.eyebrow')}
        title={<><i className="fas fa-microphone-lines" aria-hidden="true" /> {t('voiceCreate.title')}</>}
        supporting={t('voiceCreate.subtitle')}
        actions={<Link className="btn btn-secondary" to="/app/voice-library"><i className="fas fa-arrow-left" aria-hidden="true" /> {t('voiceCreate.actions.back')}</Link>}
      />

      <form className="voice-create-grid" onSubmit={submit}>
        <div className="voice-create-form">
          <section className="voice-create-section" aria-labelledby="voice-reference-heading">
            <div className="voice-create-section__heading">
              <span>01</span>
              <div><h2 id="voice-reference-heading">{t('voiceCreate.sections.reference.title')}</h2><p>{t('voiceCreate.sections.reference.body')}</p></div>
            </div>
            <div className="hstack" role="group" aria-label={t('voiceCreate.design.source')}>
              <button type="button" className={referenceMode === 'upload' ? 'btn btn-primary' : 'btn btn-secondary'} aria-pressed={referenceMode === 'upload'} disabled={designBusy} onClick={() => { if (referenceMode !== 'upload') { clearReference(); setReferenceMode('upload') } }}>{t('voiceCreate.design.upload')}</button>
              <button type="button" className={referenceMode === 'design' ? 'btn btn-primary' : 'btn btn-secondary'} aria-pressed={referenceMode === 'design'} disabled={designBusy} onClick={() => { if (referenceMode !== 'design') { clearReference(); setReferenceMode('design') } }}>{t('voiceCreate.design.title')}</button>
            </div>
            {referenceMode === 'design' ? (
              <div className="stack">
                <p className="form-hint">{t('voiceCreate.design.hint')}</p>
                <div className="form-group" role="group" aria-label={t('voiceCreate.design.model')}>
                  <span className="form-label">{t('voiceCreate.design.model')}</span>
                  <ModelSelector value={designModel} onChange={(value) => changeDesign(setDesignModel, value)} capability={CAP_TTS} disabled={designBusy} />
                </div>
                <div className="form-group">
                  <label className="form-label" htmlFor="voice-design-instructions">{t('voiceCreate.design.instructions')}</label>
                  <textarea id="voice-design-instructions" className="textarea" rows={3} value={instructions} disabled={designBusy} onChange={(event) => changeDesign(setInstructions, event.target.value)} />
                </div>
                <div className="form-group">
                  <label className="form-label" htmlFor="voice-design-text">{t('voiceCreate.design.text')}</label>
                  <textarea id="voice-design-text" className="textarea" rows={3} maxLength={4000} value={sampleText} disabled={designBusy} onChange={(event) => changeDesign(setSampleText, event.target.value)} />
                </div>
                <button type="button" className="btn btn-secondary" disabled={!canGenerate} onClick={generateReference}>
                  {generating ? <><LoadingSpinner size="sm" /> {t('voiceCreate.design.generating')}</> : t('voiceCreate.design.generate')}
                </button>
              </div>
            ) : <MediaInput
              mode="audio"
              label={t('voiceCreate.audio.label')}
              value={audio}
              onChange={handleAudio}
              onError={(error) => setAudioError(error.message)}
              maxBytes={MAX_AUDIO_BYTES}
              preferBlob
              idPrefix="voice-profile"
            />}
            {audioProcessing && <div className="voice-create-processing"><LoadingSpinner size="sm" /> {t('voiceCreate.audio.normalizing')}</div>}
            {audioError && <p className="form-error" role="alert">{audioError}</p>}
            {audio && (
              <div className="voice-create-preview">
                <WaveformPlayer src={audio.dataUrl} height={72} label={t('voiceCreate.audio.preview')} />
                <div className="voice-create-preview__meta">
                  <span><i className="fas fa-clock" aria-hidden="true" /> {audio.duration.toFixed(1)}s</span>
                  <span><i className="fas fa-wave-square" aria-hidden="true" /> {Math.round(audio.sampleRate / 1000)} kHz · PCM WAV</span>
                  <span className={durationRecommended ? 'tone-success' : 'tone-warning'}>
                    {durationRecommended ? t('voiceCreate.audio.qualityReady') : t('voiceCreate.audio.qualityHint')}
                  </span>
                </div>
              </div>
            )}
            {additionalReferences.map((reference, index) => (
              <div className="voice-create-preview" key={index}>
                <div className="voice-create-label-row">
                  <strong>{t('voiceCreate.references.additional', { number: index + 2 })}</strong>
                  <button type="button" className="btn btn-secondary btn-sm" onClick={() => {
                    if (reference.audio?.objectUrl) URL.revokeObjectURL(reference.audio.dataUrl)
                    setAdditionalReferences(current => current.filter((_, itemIndex) => itemIndex !== index))
                  }}>{t('voiceCreate.references.remove')}</button>
                </div>
                <MediaInput mode="audio" label={t('voiceCreate.audio.label')} value={reference.audio} onChange={(sample) => handleAdditionalAudio(index, sample)} maxBytes={MAX_AUDIO_BYTES} preferBlob idPrefix={`voice-profile-${index + 2}`} />
                <label className="form-label" htmlFor={`voice-profile-transcript-${index + 2}`}>{t('voiceCreate.fields.transcript')}</label>
                <textarea id={`voice-profile-transcript-${index + 2}`} className="textarea" rows={3} maxLength={4000} value={reference.transcript} onChange={(event) => setAdditionalReferences(current => current.map((item, itemIndex) => itemIndex === index ? { ...item, transcript: event.target.value } : item))} required />
              </div>
            ))}
            {additionalReferences.length < 9 && <button type="button" className="btn btn-secondary" onClick={() => setAdditionalReferences(current => [...current, { audio: null, transcript: '' }])}><i className="fas fa-plus" aria-hidden="true" /> {t('voiceCreate.references.add')}</button>}
          </section>

          <section className="voice-create-section" aria-labelledby="voice-details-heading">
            <div className="voice-create-section__heading">
              <span>02</span>
              <div><h2 id="voice-details-heading">{t('voiceCreate.sections.details.title')}</h2><p>{t('voiceCreate.sections.details.body')}</p></div>
            </div>
            <div className="form-grid-2col">
              <div className="form-group">
                <label className="form-label" htmlFor="voice-profile-name">{t('voiceCreate.fields.name')}</label>
                <input id="voice-profile-name" className="input" value={name} maxLength={80} onChange={(event) => setName(event.target.value)} placeholder={t('voiceCreate.fields.namePlaceholder')} required />
              </div>
              <div className="form-group">
                <label className="form-label" htmlFor="voice-profile-language">{t('voiceCreate.fields.language')}</label>
                <input id="voice-profile-language" className="input" value={language} maxLength={35} onChange={(event) => setLanguage(event.target.value)} placeholder={t('voiceCreate.fields.languagePlaceholder')} />
              </div>
            </div>
            <div className="form-group">
              <label className="form-label" htmlFor="voice-profile-description">{t('voiceCreate.fields.description')}</label>
              <input id="voice-profile-description" className="input" value={description} maxLength={500} onChange={(event) => setDescription(event.target.value)} placeholder={t('voiceCreate.fields.descriptionPlaceholder')} />
            </div>
            <div className="form-group">
              <div className="voice-create-label-row">
                <label className="form-label" htmlFor="voice-profile-transcript">{t('voiceCreate.fields.transcript')}</label>
                <span>{transcript.length}/4000</span>
              </div>
              <textarea id="voice-profile-transcript" className="textarea" rows={5} maxLength={4000} value={transcript} onChange={(event) => setTranscript(event.target.value)} placeholder={t('voiceCreate.fields.transcriptPlaceholder')} required />
              <p className="form-hint">{t('voiceCreate.fields.transcriptHint')}</p>
            </div>
          </section>

          <section className="voice-create-section" aria-labelledby="voice-consent-heading">
            <div className="voice-create-section__heading">
              <span>03</span>
              <div><h2 id="voice-consent-heading">{t('voiceCreate.sections.consent.title')}</h2><p>{t('voiceCreate.sections.consent.body')}</p></div>
            </div>
            <label className="voice-consent-check">
              <input type="checkbox" checked={consent} onChange={(event) => setConsent(event.target.checked)} />
              <span className="voice-consent-check__box"><i className="fas fa-check" aria-hidden="true" /></span>
              <span><strong>{t('voiceCreate.consent.title')}</strong><small>{t('voiceCreate.consent.body')}</small></span>
            </label>
          </section>

          <div className="voice-create-actions">
            <Link className="btn btn-secondary" to="/app/voice-library">{t('voiceCreate.actions.cancel')}</Link>
            <button type="submit" className="btn btn-primary" disabled={!formReady || submitting}>
              {submitting ? <><LoadingSpinner size="sm" /> {t('voiceCreate.actions.saving')}</> : <><i className="fas fa-floppy-disk" aria-hidden="true" /> {t('voiceCreate.actions.save')}</>}
            </button>
          </div>
        </div>

        <aside className="voice-readiness" aria-label={t('voiceCreate.readiness.title')}>
          <div className="voice-readiness__header">
            <span className="voice-readiness__icon"><i className="fas fa-shield-halved" aria-hidden="true" /></span>
            <div><h2>{t('voiceCreate.readiness.title')}</h2><p>{t('voiceCreate.readiness.body')}</p></div>
          </div>
          <ul>
            <ReadinessItem ready={readiness.audio}>{t('voiceCreate.readiness.audio')}</ReadinessItem>
            <ReadinessItem ready={readiness.quality} warning={!!audio && !readiness.quality}>{t('voiceCreate.readiness.quality')}</ReadinessItem>
            <ReadinessItem ready={readiness.name}>{t('voiceCreate.readiness.name')}</ReadinessItem>
            <ReadinessItem ready={readiness.transcript}>{t('voiceCreate.readiness.transcript')}</ReadinessItem>
            <ReadinessItem ready={readiness.consent}>{t('voiceCreate.readiness.consent')}</ReadinessItem>
          </ul>
          <div className="voice-readiness__privacy">
            <i className="fas fa-lock" aria-hidden="true" />
            <p><strong>{t('voiceCreate.privacy.title')}</strong>{t('voiceCreate.privacy.body')}</p>
          </div>
        </aside>
      </form>
    </main>
  )
}
