import { useState } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import PageHeader from '../components/PageHeader'
import { CAP_SOUND_GENERATION } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import GenerationProgress from '../components/GenerationProgress'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import MediaHistory from '../components/MediaHistory'
import WaveformPlayer from '../components/audio/WaveformPlayer'
import { soundApi } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'
import { useTranslation } from 'react-i18next'

export default function Sound() {
  const { t } = useTranslation('media')
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const [model, setModel] = useState(urlModel || '')
  const [mode, setMode] = useState('simple')
  const [text, setText] = useState('')
  const [instrumental, setInstrumental] = useState(false)
  const [vocalLanguage, setVocalLanguage] = useState('')
  const [caption, setCaption] = useState('')
  const [lyrics, setLyrics] = useState('')
  const [think, setThink] = useState(false)
  const [bpm, setBpm] = useState('')
  const [duration, setDuration] = useState('')
  const [keyscale, setKeyscale] = useState('')
  const [language, setLanguage] = useState('')
  const [timesignature, setTimesignature] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [audioUrl, setAudioUrl] = useState(null)
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('sound')

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!model) { addToast('Please select a model', 'warning'); return }

    const body = { model_id: model }

    if (mode === 'simple') {
      if (!text.trim()) { addToast('Please enter a description', 'warning'); return }
      body.text = text.trim()
      body.instrumental = instrumental
      if (vocalLanguage.trim()) body.vocal_language = vocalLanguage.trim()
    } else {
      if (!caption.trim() && !lyrics.trim()) { addToast('Please enter caption or lyrics', 'warning'); return }
      if (caption.trim()) body.caption = caption.trim()
      if (lyrics.trim()) body.lyrics = lyrics.trim()
      body.think = think
      if (bpm) body.bpm = parseInt(bpm)
      if (duration) body.duration_seconds = parseFloat(duration)
      if (keyscale.trim()) body.keyscale = keyscale.trim()
      if (language.trim()) body.language = language.trim()
      if (timesignature.trim()) body.timesignature = timesignature.trim()
    }

    setLoading(true)
    setAudioUrl(null)
    setError(null)

    try {
      const { blob, serverUrl } = await soundApi.generate(body)
      const url = URL.createObjectURL(blob)
      setAudioUrl(url)
      addToast('Sound generated', 'success')
      const promptText = mode === 'simple' ? text.trim() : (caption.trim() || lyrics.trim())
      if (serverUrl) {
        addEntry({ prompt: promptText, model, params: { mode }, results: [{ url: serverUrl }] })
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
        <PageHeader title={<><i className="fas fa-music" /> {t('sound.title')}</>} />

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">{t('sound.labels.model')}</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_SOUND_GENERATION} />
          </div>

          {/* Mode toggle */}
          <div className="segmented">
            <button type="button" className={`segmented__item${mode === 'simple' ? ' is-active' : ''}`} onClick={() => setMode('simple')}>{t('sound.labels.simple')}</button>
            <button type="button" className={`segmented__item${mode === 'advanced' ? ' is-active' : ''}`} onClick={() => setMode('advanced')}>{t('sound.labels.advanced')}</button>
          </div>

          {mode === 'simple' ? (
            <>
              <div className="form-group">
                <label className="form-label">{t('sound.labels.prompt')}</label>
                <textarea className="textarea" value={text} onChange={(e) => setText(e.target.value)} placeholder={t('sound.labels.promptPlaceholder')} rows={3} />
              </div>
              <div className="form-grid-2col">
                <label className="checkbox-row">
                  <input type="checkbox" checked={instrumental} onChange={(e) => setInstrumental(e.target.checked)} />
                  <span>{t('sound.labels.instrumental')}</span>
                </label>
                <div className="form-group">
                  <label className="form-label">{t('sound.labels.vocalLanguage')}</label>
                  <input className="input" value={vocalLanguage} onChange={(e) => setVocalLanguage(e.target.value)} placeholder={t('sound.labels.vocalLanguagePlaceholder')} />
                </div>
              </div>
            </>
          ) : (
            <>
              <div className="form-group">
                <label className="form-label">{t('sound.labels.caption')}</label>
                <textarea className="textarea" value={caption} onChange={(e) => setCaption(e.target.value)} rows={2} />
              </div>
              <div className="form-group">
                <label className="form-label">{t('sound.labels.lyrics')}</label>
                <textarea className="textarea" value={lyrics} onChange={(e) => setLyrics(e.target.value)} placeholder={t('sound.labels.lyricsPlaceholder')} rows={3} />
              </div>
              <div className="form-grid-2col">
                <div className="form-group"><label className="form-label">{t('sound.labels.bpm')}</label><input className="input" type="number" value={bpm} onChange={(e) => setBpm(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">{t('sound.labels.duration')}</label><input className="input" type="number" step="0.1" value={duration} onChange={(e) => setDuration(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">{t('sound.labels.keyscale')}</label><input className="input" value={keyscale} onChange={(e) => setKeyscale(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">{t('sound.labels.language')}</label><input className="input" value={language} onChange={(e) => setLanguage(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">{t('sound.labels.timesignature')}</label><input className="input" value={timesignature} onChange={(e) => setTimesignature(e.target.value)} /></div>
              </div>
              <label className="checkbox-row">
                <input type="checkbox" checked={think} onChange={(e) => setThink(e.target.checked)} />
                <span>{t('sound.labels.thinkMode')}</span>
              </label>
            </>
          )}

          <button type="submit" className="btn btn-primary btn-full" disabled={loading}>
            {loading ? <><LoadingSpinner size="sm" /> {t('sound.actions.generating')}</> : <><i className="fas fa-music" /> {t('sound.actions.generate')}</>}
          </button>
        </form>
        <MediaHistory {...historyProps} />
      </div>

      <div className="media-preview">
        <div className="media-result">
          {loading ? (
            <GenerationProgress label="Generating..." />
          ) : error ? (
            <ErrorWithTraceLink message={error} />
          ) : selectedEntry ? (
            <div className="audio-result">
              <WaveformPlayer src={selectedEntry.results[0]?.url} height={96} />
              <div className="result-quote">"{selectedEntry.prompt}"</div>
            </div>
          ) : audioUrl ? (
            <div className="audio-result">
              <WaveformPlayer
                src={audioUrl}
                height={96}
                download={`sound-${new Date().toISOString().slice(0, 10)}.wav`}
              />
            </div>
          ) : (
            <div className="media-empty">
              <i className="fas fa-music media-empty__icon" />
              <p>{t('sound.empty')}</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
