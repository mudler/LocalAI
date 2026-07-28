import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ModelSelector from '../components/ModelSelector'
import PageHeader from '../components/PageHeader'
import { CAP_TTS } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import GenerationProgress from '../components/GenerationProgress'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import MediaHistory from '../components/MediaHistory'
import WaveformPlayer from '../components/audio/WaveformPlayer'
import { ttsApi } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'
import { useModels } from '../hooks/useModels'
import { useVoiceProfiles } from '../hooks/useVoiceProfiles'
import { useAuth } from '../context/AuthContext'

function formatProfileDuration(milliseconds) {
  const seconds = Math.round((milliseconds || 0) / 1000)
  return seconds >= 60 ? `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}` : `${seconds}s`
}

export default function TTS() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('media')
  const { isAdmin } = useAuth()
  const [searchParams] = useSearchParams()
  const requestedVoiceID = searchParams.get('voice') || ''
  const [model, setModel] = useState(urlModel || '')
  const [manualVoice, setManualVoice] = useState('')
  const [voiceProfileID, setVoiceProfileID] = useState(requestedVoiceID)
  const [text, setText] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [audioUrl, setAudioUrl] = useState(null)
  const appliedVoiceLinkRef = useRef('')
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('tts')
  const { models } = useModels(CAP_TTS)
  const selectedModel = useMemo(() => models.find(item => item.id === model), [models, model])
  const compatibleModels = useMemo(() => models.filter(item => item.voice_cloning), [models])
  const supportsVoiceProfiles = !!selectedModel?.voice_cloning
  const { profiles, loading: profilesLoading, error: profilesError } = useVoiceProfiles({ enabled: supportsVoiceProfiles })
  const selectedProfile = profiles.find(profile => profile.id === voiceProfileID) || null

  // A library deep-link should land on a compatible model even when the last
  // TTS model used was a named-speaker or non-cloning variant. Apply it once
  // per requested profile so the user can still change models afterwards.
  useEffect(() => {
    if (!requestedVoiceID || models.length === 0 || appliedVoiceLinkRef.current === requestedVoiceID) return
    const targetModel = selectedModel?.voice_cloning ? selectedModel : compatibleModels[0]
    if (!targetModel) return
    if (targetModel.id !== model) setModel(targetModel.id)
    setVoiceProfileID(requestedVoiceID)
    appliedVoiceLinkRef.current = requestedVoiceID
  }, [requestedVoiceID, models, model, selectedModel, compatibleModels])

  useEffect(() => {
    if (selectedModel && !selectedModel.voice_cloning) setVoiceProfileID('')
  }, [selectedModel])

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!text.trim()) { addToast(t('tts.toasts.noText'), 'warning'); return }
    if (!model) { addToast(t('tts.toasts.noModel'), 'warning'); return }

    setLoading(true)
    setAudioUrl(null)
    setError(null)

    try {
      const selectedVoice = supportsVoiceProfiles ? selectedProfile?.voice : manualVoice.trim()
      const request = { model, input: text.trim() }
      if (selectedVoice) request.voice = selectedVoice
      const { blob, serverUrl } = await ttsApi.generate(request)
      const url = URL.createObjectURL(blob)
      setAudioUrl(url)
      addToast(t('tts.toasts.generated'), 'success')
      if (serverUrl) {
        addEntry({
          prompt: text.trim(),
          model,
          params: selectedProfile
            ? { voice: selectedProfile.name }
            : (!supportsVoiceProfiles && manualVoice.trim() ? { voice: manualVoice.trim() } : {}),
          results: [{ url: serverUrl }],
        })
      }
      selectEntry(null)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="media-layout">
      <div className="media-controls">
        <PageHeader title={<><i className="fas fa-headphones" /> {t('tts.title')}</>} />

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">{t('tts.labels.model')}</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_TTS} />
          </div>
          <div className="form-group">
            <div className="tts-voice-label-row">
              <label className="form-label" htmlFor="tts-voice">{t('tts.labels.voice')}</label>
              {supportsVoiceProfiles && <span className="badge badge-success">{t('tts.voiceLibrary.cloningReady')}</span>}
            </div>
            {supportsVoiceProfiles ? (
              <div className="tts-voice-picker">
                <select
                  id="tts-voice"
                  className="input"
                  value={voiceProfileID}
                  disabled={profilesLoading}
                  onChange={(event) => setVoiceProfileID(event.target.value)}
                >
                  <option value="">{profilesLoading ? t('tts.voiceLibrary.loading') : t('tts.voiceLibrary.modelDefault')}</option>
                  {profiles.map(profile => (
                    <option key={profile.id} value={profile.id}>
                      {profile.name}{profile.language ? ` · ${profile.language}` : ''}
                    </option>
                  ))}
                </select>
                {selectedProfile && (
                  <div className="tts-voice-picker__selection">
                    <span className="tts-voice-picker__avatar">{selectedProfile.name.slice(0, 2).toUpperCase()}</span>
                    <span><strong>{selectedProfile.name}</strong><small>{selectedProfile.language || t('voiceLibrary.metadata.languageUnknown')} · {formatProfileDuration(selectedProfile.audio?.duration_ms)}</small></span>
                    <i className="fas fa-wave-square" aria-hidden="true" />
                  </div>
                )}
                {!profilesLoading && profiles.length === 0 && (
                  <p className="tts-voice-picker__hint">
                    {t('tts.voiceLibrary.empty')}{' '}
                    {isAdmin && <Link to="/app/voice-library/new">{t('tts.voiceLibrary.create')}</Link>}
                  </p>
                )}
                {profilesError && <p className="tts-voice-picker__error" role="alert">{profilesError}</p>}
                {isAdmin && profiles.length > 0 && <Link className="tts-voice-picker__manage" to="/app/voice-library">{t('tts.voiceLibrary.manage')} <i className="fas fa-arrow-right" aria-hidden="true" /></Link>}
              </div>
            ) : (
              <>
                <input
                  id="tts-voice"
                  className="input"
                  value={manualVoice}
                  onChange={(event) => setManualVoice(event.target.value)}
                  placeholder={t('tts.labels.voicePlaceholder')}
                />
                <p className="form-hint">{t('tts.voiceLibrary.namedVoiceHint')}</p>
              </>
            )}
          </div>
          <div className="form-group">
            <label className="form-label">{t('tts.labels.input')}</label>
            <textarea
              className="textarea"
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder={t('tts.labels.inputPlaceholder')}
              rows={5}
            />
          </div>
          <button type="submit" className="btn btn-primary btn-full" disabled={loading}>
            {loading ? <><LoadingSpinner size="sm" /> {t('tts.actions.generating')}</> : <><i className="fas fa-headphones" /> {t('tts.actions.generate')}</>}
          </button>
        </form>
        <MediaHistory {...historyProps} />
      </div>

      <div className="media-preview">
        <div className="media-result">
          {loading ? (
            <GenerationProgress label={t('tts.actions.generating')} />
          ) : error ? (
            <ErrorWithTraceLink message={error} />
          ) : selectedEntry ? (
            <div className="audio-result">
              <WaveformPlayer src={selectedEntry.results[0]?.url} height={96} audioTestId="history-audio" />
              <div className="result-quote">"{selectedEntry.prompt}"</div>
            </div>
          ) : audioUrl ? (
            <div className="audio-result">
              <WaveformPlayer
                src={audioUrl}
                height={96}
                download={`tts-${model}-${new Date().toISOString().slice(0, 10)}.mp3`}
              />
              <div className="result-quote">"{text}"</div>
            </div>
          ) : (
            <div className="media-empty">
              <i className="fas fa-headphones media-empty__icon" />
              <p>{t('tts.empty')}</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
