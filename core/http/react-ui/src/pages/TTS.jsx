import { useState, useRef } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
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
  const [model, setModel] = useState(urlModel || '')
  const [text, setText] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [audioUrl, setAudioUrl] = useState(null)
  const audioRef = useRef(null)
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('tts')

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!text.trim()) { addToast('Please enter text', 'warning'); return }
    if (!model) { addToast('Please select a model', 'warning'); return }

    setLoading(true)
    setAudioUrl(null)
    setError(null)

    try {
      const { blob, serverUrl } = await ttsApi.generate({ model, input: text.trim() })
      const url = URL.createObjectURL(blob)
      setAudioUrl(url)
      addToast('Audio generated', 'success')
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
          <h1 className="page-title"><i className="fas fa-headphones" style={{ marginRight: 8, color: 'var(--color-accent)' }} />Text to Speech</h1>
        </div>

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">Model</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_TTS} />
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
              <audio controls src={selectedEntry.results[0]?.url} style={{ width: '100%' }} data-testid="history-audio" />
              <div style={{ padding: 'var(--spacing-sm)', background: 'var(--color-bg-tertiary)', borderRadius: 'var(--radius-md)', color: 'var(--color-text-secondary)', fontStyle: 'italic', textAlign: 'center' }}>
                "{selectedEntry.prompt}"
              </div>
            </div>
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
              <i className="fas fa-headphones" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)', opacity: 0.4 }} />
              <p>Generated audio will appear here</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
