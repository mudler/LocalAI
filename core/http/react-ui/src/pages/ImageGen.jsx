import { useState, useRef } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import { CAP_IMAGE } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import MediaHistory from '../components/MediaHistory'
import { imageApi, fileToBase64 } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'

const SIZES = ['256x256', '512x512', '768x768', '1024x1024']

export default function ImageGen() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const [model, setModel] = useState(urlModel || '')
  const [prompt, setPrompt] = useState('')
  const [negativePrompt, setNegativePrompt] = useState('')
  const [size, setSize] = useState('512x512')
  const [count, setCount] = useState(1)
  const [steps, setSteps] = useState('')
  const [seed, setSeed] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [images, setImages] = useState([])
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [showImageInputs, setShowImageInputs] = useState(false)
  const [sourceImage, setSourceImage] = useState(null)
  const [refImages, setRefImages] = useState([])
  const sourceRef = useRef(null)
  const refRef = useRef(null)
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('image')

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!prompt.trim()) { addToast('Please enter a prompt', 'warning'); return }
    if (!model) { addToast('Please select a model', 'warning'); return }

    setLoading(true)
    setImages([])
    setError(null)

    let combinedPrompt = prompt.trim()
    if (negativePrompt.trim()) combinedPrompt += '|' + negativePrompt.trim()

    const body = { model, prompt: combinedPrompt, n: count, size }
    if (steps) body.step = parseInt(steps)
    if (seed) body.seed = parseInt(seed)
    if (sourceImage) body.file = sourceImage
    if (refImages.length > 0) body.ref_images = refImages

    try {
      const data = await imageApi.generate(body)
      const results = data?.data || []
      setImages(results)
      if (!results.length) {
        addToast('No images generated', 'warning')
      } else {
        const urlResults = results.filter(r => r.url && !r.url.startsWith('data:')).map(r => ({ url: r.url }))
        if (urlResults.length) {
          addEntry({ prompt: prompt.trim(), model, params: { size, count, steps, seed, negativePrompt: negativePrompt.trim() || undefined }, results: urlResults })
        }
        selectEntry(null)
      }
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  const handleSourceImage = async (e) => {
    if (e.target.files[0]) setSourceImage(await fileToBase64(e.target.files[0]))
  }

  const handleRefImages = async (e) => {
    const arr = []
    for (const f of e.target.files) arr.push(await fileToBase64(f))
    setRefImages(prev => [...prev, ...arr])
  }

  return (
    <div className="media-layout">
      <div className="media-controls">
        <div className="page-header">
          <h1 className="page-title"><i className="fas fa-image" /> Image Generation</h1>
        </div>

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">Model</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_IMAGE} />
          </div>
          <div className="form-group">
            <label className="form-label">Prompt</label>
            <textarea className="textarea" value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="Describe the image you want to generate..." rows={3} onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleGenerate(e) } }} />
          </div>
          <div className="form-group">
            <label className="form-label">Negative Prompt</label>
            <textarea className="textarea" value={negativePrompt} onChange={(e) => setNegativePrompt(e.target.value)} placeholder="What to avoid..." rows={2} />
          </div>

          <div className="form-grid-2col">
            <div className="form-group">
              <label className="form-label">Size</label>
              <select className="input btn-full" value={size} onChange={(e) => setSize(e.target.value)}>
                {SIZES.map(s => <option key={s} value={s}>{s}</option>)}
              </select>
            </div>
            <div className="form-group">
              <label className="form-label">Count (1-4)</label>
              <input className="input" type="number" min="1" max="4" value={count} onChange={(e) => setCount(parseInt(e.target.value) || 1)} />
            </div>
          </div>

          <div className={`collapsible-header ${showAdvanced ? 'open' : ''}`} onClick={() => setShowAdvanced(!showAdvanced)}>
            <i className="fas fa-chevron-right" /> Advanced Settings
          </div>
          {showAdvanced && (
            <div className="form-grid-2col">
              <div className="form-group"><label className="form-label">Steps</label><input className="input" type="number" value={steps} onChange={(e) => setSteps(e.target.value)} placeholder="20" /></div>
              <div className="form-group"><label className="form-label">Seed</label><input className="input" type="number" value={seed} onChange={(e) => setSeed(e.target.value)} placeholder="Random" /></div>
            </div>
          )}

          <div className={`collapsible-header ${showImageInputs ? 'open' : ''}`} onClick={() => setShowImageInputs(!showImageInputs)}>
            <i className="fas fa-chevron-right" /> Image Inputs
          </div>
          {showImageInputs && (
            <>
              <div className="form-group"><label className="form-label">Source Image (img2img)</label><input ref={sourceRef} type="file" accept="image/*" onChange={handleSourceImage} className="input" /></div>
              <div className="form-group">
                <label className="form-label">Reference Images</label>
                <input ref={refRef} type="file" accept="image/*" multiple onChange={handleRefImages} className="input" />
                {refImages.length > 0 && <span className="form-field__hint">{refImages.length} image(s) added</span>}
              </div>
            </>
          )}

          <button type="submit" className="btn btn-primary btn-full" disabled={loading}>
            {loading ? <><LoadingSpinner size="sm" /> Generating...</> : <><i className="fas fa-wand-magic-sparkles" /> Generate</>}
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
            <div className="media-result-grid">
              {selectedEntry.results.map((r, i) => (
                <div key={i}>
                  <img src={r.url} alt={selectedEntry.prompt} style={{ width: '100%', borderRadius: 'var(--radius-md)' }} />
                </div>
              ))}
            </div>
          ) : images.length > 0 ? (
            <div className="media-result-grid">
              {images.map((img, i) => (
                <div key={i}>
                  <img src={img.url || `data:image/png;base64,${img.b64_json}`} alt={prompt} style={{ width: '100%', borderRadius: 'var(--radius-md)' }} />
                </div>
              ))}
            </div>
          ) : (
            <div style={{ textAlign: 'center', color: 'var(--color-text-muted)' }}>
              <i className="fas fa-image" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)', opacity: 0.4 }} />
              <p>Generated images will appear here</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
