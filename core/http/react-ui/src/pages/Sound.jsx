import { useState } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import { CAP_SOUND_GENERATION } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import MediaHistory from '../components/MediaHistory'
import WaveformPlayer from '../components/audio/WaveformPlayer'
import { soundApi } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'

export default function Sound() {
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
        <div className="page-header">
          <h1 className="page-title"><i className="fas fa-music" /> Sound Generation</h1>
        </div>

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">Model</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_SOUND_GENERATION} />
          </div>

          {/* Mode toggle */}
          <div className="segmented">
            <button type="button" className={`segmented__item${mode === 'simple' ? ' is-active' : ''}`} onClick={() => setMode('simple')}>Simple</button>
            <button type="button" className={`segmented__item${mode === 'advanced' ? ' is-active' : ''}`} onClick={() => setMode('advanced')}>Advanced</button>
          </div>

          {mode === 'simple' ? (
            <>
              <div className="form-group">
                <label className="form-label">Description</label>
                <textarea className="textarea" value={text} onChange={(e) => setText(e.target.value)} placeholder="Describe the sound..." rows={3} />
              </div>
              <div className="form-grid-2col">
                <label className="checkbox-row">
                  <input type="checkbox" checked={instrumental} onChange={(e) => setInstrumental(e.target.checked)} />
                  <span>Instrumental</span>
                </label>
                <div className="form-group">
                  <label className="form-label">Vocal language</label>
                  <input className="input" value={vocalLanguage} onChange={(e) => setVocalLanguage(e.target.value)} placeholder="e.g. English" />
                </div>
              </div>
            </>
          ) : (
            <>
              <div className="form-group">
                <label className="form-label">Caption</label>
                <textarea className="textarea" value={caption} onChange={(e) => setCaption(e.target.value)} rows={2} />
              </div>
              <div className="form-group">
                <label className="form-label">Lyrics</label>
                <textarea className="textarea" value={lyrics} onChange={(e) => setLyrics(e.target.value)} rows={3} />
              </div>
              <div className="form-grid-2col">
                <div className="form-group"><label className="form-label">BPM</label><input className="input" type="number" value={bpm} onChange={(e) => setBpm(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">Duration (s)</label><input className="input" type="number" step="0.1" value={duration} onChange={(e) => setDuration(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">Key/Scale</label><input className="input" value={keyscale} onChange={(e) => setKeyscale(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">Language</label><input className="input" value={language} onChange={(e) => setLanguage(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">Time Signature</label><input className="input" value={timesignature} onChange={(e) => setTimesignature(e.target.value)} /></div>
              </div>
              <label className="checkbox-row">
                <input type="checkbox" checked={think} onChange={(e) => setThink(e.target.checked)} />
                <span>Think mode</span>
              </label>
            </>
          )}

          <button type="submit" className="btn btn-primary btn-full" disabled={loading}>
            {loading ? <><LoadingSpinner size="sm" /> Generating...</> : <><i className="fas fa-music" /> Generate Sound</>}
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
              <p>Generated sound will appear here</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
