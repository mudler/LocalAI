import { useState } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import LoadingSpinner from '../components/LoadingSpinner'
import { videoApi, fileToBase64 } from '../utils/api'

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
  const [videos, setVideos] = useState([])
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [showImageInputs, setShowImageInputs] = useState(false)
  const [startImage, setStartImage] = useState(null)
  const [endImage, setEndImage] = useState(null)

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!prompt.trim()) { addToast('Please enter a prompt', 'warning'); return }
    if (!model) { addToast('Please select a model', 'warning'); return }

    setLoading(true)
    setVideos([])

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
      setVideos(data?.data || [])
      if (!data?.data?.length) addToast('No videos generated', 'warning')
    } catch (err) {
      addToast(`Error: ${err.message}`, 'error')
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
          <h1 className="page-title"><i className="fas fa-video" style={{ marginRight: 8, color: 'var(--color-accent)' }} />Video Generation</h1>
        </div>

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">Model</label>
            <ModelSelector value={model} onChange={setModel} capability="FLAG_VIDEO" />
          </div>
          <div className="form-group">
            <label className="form-label">Prompt</label>
            <textarea className="textarea" value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="Describe the video..." rows={3} />
          </div>
          <div className="form-group">
            <label className="form-label">Negative Prompt</label>
            <textarea className="textarea" value={negativePrompt} onChange={(e) => setNegativePrompt(e.target.value)} rows={2} />
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 'var(--spacing-sm)' }}>
            <div className="form-group">
              <label className="form-label">Size</label>
              <select className="model-selector" value={size} onChange={(e) => setSize(e.target.value)} style={{ width: '100%' }}>
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
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)' }}>
              <div className="form-group"><label className="form-label">Steps</label><input className="input" type="number" value={steps} onChange={(e) => setSteps(e.target.value)} /></div>
              <div className="form-group"><label className="form-label">Seed</label><input className="input" type="number" value={seed} onChange={(e) => setSeed(e.target.value)} /></div>
              <div className="form-group"><label className="form-label">CFG Scale</label><input className="input" type="number" step="0.1" value={cfgScale} onChange={(e) => setCfgScale(e.target.value)} /></div>
            </div>
          )}

          <div className={`collapsible-header ${showImageInputs ? 'open' : ''}`} onClick={() => setShowImageInputs(!showImageInputs)}>
            <i className="fas fa-chevron-right" /> Image Inputs
          </div>
          {showImageInputs && (
            <div style={{ marginBottom: 'var(--spacing-md)' }}>
              <div className="form-group"><label className="form-label">Start Image</label><input type="file" accept="image/*" onChange={(e) => handleImageUpload(e, setStartImage)} className="input" /></div>
              <div className="form-group"><label className="form-label">End Image</label><input type="file" accept="image/*" onChange={(e) => handleImageUpload(e, setEndImage)} className="input" /></div>
            </div>
          )}

          <button type="submit" className="btn btn-primary" disabled={loading} style={{ width: '100%' }}>
            {loading ? <><LoadingSpinner size="sm" /> Generating...</> : <><i className="fas fa-video" /> Generate Video</>}
          </button>
        </form>
      </div>

      <div className="media-preview">
        <div className="media-result">
          {loading ? (
            <LoadingSpinner size="lg" />
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
