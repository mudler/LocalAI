import { useState } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import { CAP_VIDEO } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import MediaHistory from '../components/MediaHistory'
import { videoApi, fileToBase64 } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'

const SIZES = ['256x256', '512x512', '768x768', '1024x1024']

export default function VideoGen() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const [model, setModel] = useState(urlModel || '')
  const [prompt, setPrompt] = useState('')
  const [negativePrompt, setNegativePrompt] = useState('')
  const [size, setSize] = useState('512x512')
  const [seconds, setSeconds] = useState('')
  const [fps, setFps] = useState('16')
  const [frames, setFrames] = useState('')
  const [steps, setSteps] = useState('')
  const [seed, setSeed] = useState('')
  const [cfgScale, setCfgScale] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [videos, setVideos] = useState([])
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [showImageInputs, setShowImageInputs] = useState(false)
  const [startImage, setStartImage] = useState(null)
  const [endImage, setEndImage] = useState(null)
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('video')

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!prompt.trim()) { addToast('Please enter a prompt', 'warning'); return }
    if (!model) { addToast('Please select a model', 'warning'); return }

    setLoading(true)
    setVideos([])
    setError(null)

    const [w, h] = size.split('x').map(Number)
    const body = { model, prompt: prompt.trim(), width: w, height: h, fps: parseInt(fps) || 16 }
    if (negativePrompt.trim()) body.negative_prompt = negativePrompt.trim()
    if (seconds) body.seconds = seconds
    if (frames) body.num_frames = parseInt(frames)
    if (steps) body.step = parseInt(steps)
    if (seed) body.seed = parseInt(seed)
    if (cfgScale) body.cfg_scale = parseFloat(cfgScale)
    if (startImage) body.start_image = startImage
    if (endImage) body.end_image = endImage

    try {
      const data = await videoApi.generate(body)
      const results = data?.data || []
      setVideos(results)
      if (!results.length) {
        addToast('No videos generated', 'warning')
      } else {
        const urlResults = results.filter(r => r.url && !r.url.startsWith('data:')).map(r => ({ url: r.url }))
        if (urlResults.length) {
          addEntry({ prompt: prompt.trim(), model, params: { size, fps, seconds, frames, steps, seed, cfgScale, negativePrompt: negativePrompt.trim() || undefined }, results: urlResults })
        }
        selectEntry(null)
      }
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  const handleImageUpload = async (e, setter) => {
    if (e.target.files[0]) setter(await fileToBase64(e.target.files[0]))
  }

  return (
    <div className="media-layout">
      <div className="media-controls">
        <div className="page-header">
          <h1 className="page-title"><i className="fas fa-video" /> Video Generation</h1>
        </div>

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">Model</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_VIDEO} />
          </div>
          <div className="form-group">
            <label className="form-label">Prompt</label>
            <textarea className="textarea" value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="Describe the video..." rows={3} />
          </div>
          <div className="form-group">
            <label className="form-label">Negative Prompt</label>
            <textarea className="textarea" value={negativePrompt} onChange={(e) => setNegativePrompt(e.target.value)} rows={2} />
          </div>

          <div className="form-grid-3col">
            <div className="form-group">
              <label className="form-label">Size</label>
              <select className="input btn-full" value={size} onChange={(e) => setSize(e.target.value)}>
                {SIZES.map(s => <option key={s} value={s}>{s}</option>)}
              </select>
            </div>
            <div className="form-group">
              <label className="form-label">Duration (s)</label>
              <input className="input" type="text" value={seconds} onChange={(e) => setSeconds(e.target.value)} placeholder="Auto" />
            </div>
            <div className="form-group">
              <label className="form-label">FPS</label>
              <input className="input" type="number" value={fps} onChange={(e) => setFps(e.target.value)} />
            </div>
          </div>

          <div className={`collapsible-header ${showAdvanced ? 'open' : ''}`} onClick={() => setShowAdvanced(!showAdvanced)}>
            <i className="fas fa-chevron-right" /> Advanced
          </div>
          {showAdvanced && (
            <div className="form-grid-3col">
              <div className="form-group"><label className="form-label">Steps</label><input className="input" type="number" value={steps} onChange={(e) => setSteps(e.target.value)} /></div>
              <div className="form-group"><label className="form-label">Seed</label><input className="input" type="number" value={seed} onChange={(e) => setSeed(e.target.value)} /></div>
              <div className="form-group"><label className="form-label">CFG Scale</label><input className="input" type="number" step="0.1" value={cfgScale} onChange={(e) => setCfgScale(e.target.value)} /></div>
            </div>
          )}

          <div className={`collapsible-header ${showImageInputs ? 'open' : ''}`} onClick={() => setShowImageInputs(!showImageInputs)}>
            <i className="fas fa-chevron-right" /> Image Inputs
          </div>
          {showImageInputs && (
            <div className="form-grid-2col">
              <div className="form-group"><label className="form-label">Start Image</label><input type="file" accept="image/*" onChange={(e) => handleImageUpload(e, setStartImage)} className="input" /></div>
              <div className="form-group"><label className="form-label">End Image</label><input type="file" accept="image/*" onChange={(e) => handleImageUpload(e, setEndImage)} className="input" /></div>
            </div>
          )}

          <button type="submit" className="btn btn-primary btn-full" disabled={loading}>
            {loading ? <><LoadingSpinner size="sm" /> Generating...</> : <><i className="fas fa-video" /> Generate Video</>}
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
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)', width: '100%' }}>
              {selectedEntry.results.map((r, i) => (
                <video key={i} controls className="media-result" style={{ minHeight: 0 }} src={r.url} />
              ))}
            </div>
          ) : videos.length > 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)', width: '100%' }}>
              {videos.map((v, i) => (
                <video key={i} controls className="media-result" style={{ minHeight: 0 }} src={v.url || `data:video/mp4;base64,${v.b64_json}`} />
              ))}
            </div>
          ) : (
            <div style={{ textAlign: 'center', color: 'var(--color-text-muted)' }}>
              <i className="fas fa-video" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)', opacity: 0.4 }} />
              <p>Generated videos will appear here</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
