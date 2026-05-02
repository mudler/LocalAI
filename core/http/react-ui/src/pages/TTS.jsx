import { useState, useRef } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ModelSelector from '../components/ModelSelector'
import { CAP_TTS } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import MediaHistory from '../components/MediaHistory'
import { ttsApi } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'

export default function TTS() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('media')
  const [model, setModel] = useState(urlModel || '')
  const [text, setText] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [audioUrl, setAudioUrl] = useState(null)
  const audioRef = useRef(null)
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('tts')

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!text.trim()) { addToast(t('tts.toasts.noText'), 'warning'); return }
    if (!model) { addToast(t('tts.toasts.noModel'), 'warning'); return }

    setLoading(true)
    setAudioUrl(null)
    setError(null)

    try {
      const { blob, serverUrl } = await ttsApi.generate({ model, input: text.trim() })
      const url = URL.createObjectURL(blob)
      setAudioUrl(url)
      addToast(t('tts.actions.generate'), 'success')
      if (serverUrl) {
        addEntry({ prompt: text.trim(), model, params: {}, results: [{ url: serverUrl }] })
      }
      selectEntry(null)
      setTimeout(() => audioRef.current?.play(), 100)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="media-layout">
      <div className="media-controls">
        <div className="page-header">
          <h1 className="page-title"><i className="fas fa-headphones" /> {t('tts.title')}</h1>
        </div>

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">{t('tts.labels.model')}</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_TTS} />
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
            <LoadingSpinner size="lg" />
          ) : error ? (
            <ErrorWithTraceLink message={error} />
          ) : selectedEntry ? (
            <div className="audio-result">
              <audio controls src={selectedEntry.results[0]?.url} className="audio-result__player" data-testid="history-audio" />
              <div className="result-quote">"{selectedEntry.prompt}"</div>
            </div>
          ) : audioUrl ? (
            <div className="audio-result">
              <audio ref={audioRef} controls src={audioUrl} className="audio-result__player" />
              <div className="audio-result__actions">
                <a href={audioUrl} download={`tts-${model}-${new Date().toISOString().slice(0, 10)}.mp3`} className="btn btn-primary btn-sm">
                  <i className="fas fa-download" /> <span>Download</span>
                </a>
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => audioRef.current?.play()}>
                  <i className="fas fa-rotate-right" /> <span>Replay</span>
                </button>
              </div>
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
