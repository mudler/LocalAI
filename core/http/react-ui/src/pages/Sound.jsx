import { useState, useRef } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import { CAP_SOUND_GENERATION } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import MediaHistory from '../components/MediaHistory'
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
  const audioRef = useRef(null)
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
      setTimeout(() => audioRef.current?.play().catch(() => {}), 100)
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
          <h1 className="page-title"><i className="fas fa-music" style={{ marginRight: 8, color: 'var(--color-accent)' }} />Sound Generation</h1>
        </div>

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">Model</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_SOUND_GENERATION} />
          </div>

          {/* Mode toggle */}
          <div className="form-group">
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <button type="button" className={`filter-btn ${mode === 'simple' ? 'active' : ''}`} onClick={() => setMode('simple')}>Simple</button>
              <button type="button" className={`filter-btn ${mode === 'advanced' ? 'active' : ''}`} onClick={() => setMode('advanced')}>Advanced</button>
            </div>
          </div>

          {mode === 'simple' ? (
            <>
              <div className="form-group">
                <label className="form-label">Description</label>
                <textarea className="textarea" value={text} onChange={(e) => setText(e.target.value)} placeholder="Describe the sound..." rows={3} />
              </div>
              <div style={{ display: 'flex', gap: 'var(--spacing-md)', alignItems: 'center', marginBottom: 'var(--spacing-md)' }}>
                <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.875rem', color: 'var(--color-text-secondary)' }}>
                  <input type="checkbox" checked={instrumental} onChange={(e) => setInstrumental(e.target.checked)} /> Instrumental
                </label>
                <div className="form-group" style={{ flex: 1, margin: 0 }}>
                  <input className="input" value={vocalLanguage} onChange={(e) => setVocalLanguage(e.target.value)} placeholder="Vocal language" />
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
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--spacing-sm)' }}>
                <div className="form-group"><label className="form-label">BPM</label><input className="input" type="number" value={bpm} onChange={(e) => setBpm(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">Duration (s)</label><input className="input" type="number" step="0.1" value={duration} onChange={(e) => setDuration(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">Key/Scale</label><input className="input" value={keyscale} onChange={(e) => setKeyscale(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">Language</label><input className="input" value={language} onChange={(e) => setLanguage(e.target.value)} /></div>
                <div className="form-group"><label className="form-label">Time Signature</label><input className="input" value={timesignature} onChange={(e) => setTimesignature(e.target.value)} /></div>
              </div>
              <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.875rem', color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)' }}>
                <input type="checkbox" checked={think} onChange={(e) => setThink(e.target.checked)} /> Think mode
              </label>
            </>
          )}

          <button type="submit" className="btn btn-primary" disabled={loading} style={{ width: '100%' }}>
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
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 'var(--spacing-md)', width: '100%' }}>
              <audio controls src={selectedEntry.results[0]?.url} style={{ width: '100%', maxWidth: '400px' }} data-testid="history-audio" />
              <div style={{ padding: 'var(--spacing-sm)', background: 'var(--color-bg-tertiary)', borderRadius: 'var(--radius-md)', color: 'var(--color-text-secondary)', fontStyle: 'italic', textAlign: 'center' }}>
                "{selectedEntry.prompt}"
              </div>
            </div>
          ) : audioUrl ? (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 'var(--spacing-md)', width: '100%' }}>
              <audio ref={audioRef} controls src={audioUrl} style={{ width: '100%', maxWidth: '400px' }} />
              <a href={audioUrl} download={`sound-${new Date().toISOString().slice(0, 10)}.wav`} className="btn btn-primary btn-sm">
                <i className="fas fa-download" /> Download
              </a>
            </div>
          ) : (
            <div style={{ textAlign: 'center', color: 'var(--color-text-muted)' }}>
              <i className="fas fa-music" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)', opacity: 0.4 }} />
              <p>Generated sound will appear here</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
