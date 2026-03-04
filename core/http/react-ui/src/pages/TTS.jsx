import { useState, useRef } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import LoadingSpinner from '../components/LoadingSpinner'
import { ttsApi } from '../utils/api'

export default function TTS() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const [model, setModel] = useState(urlModel || '')
  const [text, setText] = useState('')
  const [loading, setLoading] = useState(false)
  const [audioUrl, setAudioUrl] = useState(null)
  const audioRef = useRef(null)

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!text.trim()) { addToast('Please enter text', 'warning'); return }
    if (!model) { addToast('Please select a model', 'warning'); return }

    setLoading(true)
    setAudioUrl(null)

    try {
      const blob = await ttsApi.generate({ model, input: text.trim() })
      const url = URL.createObjectURL(blob)
      setAudioUrl(url)
      addToast('Audio generated', 'success')
      // Auto-play
      setTimeout(() => audioRef.current?.play(), 100)
    } catch (err) {
      addToast(`Error: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="media-layout">
      <div className="media-controls">
        <div className="page-header">
          <h1 className="page-title">Text to Speech</h1>
        </div>

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">Model</label>
            <ModelSelector value={model} onChange={setModel} />
          </div>
          <div className="form-group">
            <label className="form-label">Text</label>
            <textarea
              className="textarea"
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder="Enter text to convert to speech..."
              rows={5}
            />
          </div>
          <button type="submit" className="btn btn-primary" disabled={loading} style={{ width: '100%' }}>
            {loading ? <><LoadingSpinner size="sm" /> Generating...</> : <><i className="fas fa-headphones" /> Generate Audio</>}
          </button>
        </form>
      </div>

      <div className="media-preview">
        <div className="media-result">
          {loading ? (
            <LoadingSpinner size="lg" />
          ) : audioUrl ? (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 'var(--spacing-md)', width: '100%' }}>
              <audio ref={audioRef} controls src={audioUrl} style={{ width: '100%' }} />
              <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
                <a href={audioUrl} download={`tts-${model}-${new Date().toISOString().slice(0, 10)}.mp3`} className="btn btn-primary btn-sm">
                  <i className="fas fa-download" /> Download
                </a>
                <button className="btn btn-secondary btn-sm" onClick={() => audioRef.current?.play()}>
                  <i className="fas fa-rotate-right" /> Replay
                </button>
              </div>
              <div style={{ padding: 'var(--spacing-sm)', background: 'var(--color-bg-tertiary)', borderRadius: 'var(--radius-md)', color: 'var(--color-text-secondary)', fontStyle: 'italic', textAlign: 'center' }}>
                "{text}"
              </div>
            </div>
          ) : (
            <div style={{ textAlign: 'center', color: 'var(--color-text-muted)' }}>
              <i className="fas fa-headphones" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)' }} />
              <p>Generated audio will appear here</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
