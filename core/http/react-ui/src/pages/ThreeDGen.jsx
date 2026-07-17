import { useState } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ModelSelector from '../components/ModelSelector'
import PageHeader from '../components/PageHeader'
import { CAP_3D } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import GenerationProgress from '../components/GenerationProgress'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import ThreeDHistory from '../components/ThreeDHistory'
import GlbViewer from '../components/GlbViewer'
import MediaInput from '../components/biometrics/MediaInput'
import { threeDApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { use3DHistory } from '../hooks/use3DHistory'
import useObjectUrl from '../hooks/useObjectUrl'

const QUALITIES = ['auto', 'coarse', '512', '1024']
const BACKGROUNDS = ['auto', 'keep', 'black', 'white']
const MAX_3D_INPUT_BYTES = 32 * 1024 * 1024

// Small thumbnail of the conditioning image for the history list — full-size
// data URLs would bloat every IndexedDB entry for no visual gain.
async function makeThumb(dataUrl, size = 96) {
  try {
    const img = new Image()
    await new Promise((resolve, reject) => {
      img.onload = resolve
      img.onerror = reject
      img.src = dataUrl
    })
    const scale = size / Math.max(img.width, img.height, 1)
    const canvas = document.createElement('canvas')
    canvas.width = Math.max(1, Math.round(img.width * scale))
    canvas.height = Math.max(1, Math.round(img.height * scale))
    canvas.getContext('2d').drawImage(img, 0, 0, canvas.width, canvas.height)
    return canvas.toDataURL('image/jpeg', 0.7)
  } catch {
    return null
  }
}

export default function ThreeDGen() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('media')
  const [model, setModel] = useState(urlModel || '')
  const [image, setImage] = useState(null)
  const [quality, setQuality] = useState('auto')
  const [background, setBackground] = useState('auto')
  const [steps, setSteps] = useState('')
  const [textureSteps, setTextureSteps] = useState('')
  const [guidance, setGuidance] = useState('')
  const [seed, setSeed] = useState('')
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [result, setResult] = useState(null) // { blob, name }
  const { entries, addEntry, deleteEntry, clearAll, selectEntry, selectedId, selectedEntry } = use3DHistory()

  const active = selectedEntry ? { blob: selectedEntry.glb, name: selectedEntry.name } : result
  const downloadUrl = useObjectUrl(active?.blob)

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!image?.base64) { addToast(t('threed.toasts.noImage'), 'warning'); return }
    if (!model) { addToast(t('threed.toasts.noModel'), 'warning'); return }

    setLoading(true)
    setResult(null)
    setError(null)

    const body = { model, image: image.base64, quality, background, response_format: 'url' }
    if (steps) body.step = parseInt(steps)
    if (textureSteps) body.texture_steps = parseInt(textureSteps)
    if (guidance) body.cfg_scale = parseFloat(guidance)
    if (seed) body.seed = parseInt(seed)

    try {
      const data = await threeDApi.generate(body)
      const url = data?.data?.[0]?.url
      if (!url) {
        addToast(t('threed.toasts.noResults'), 'warning')
        return
      }
      const glbResp = await fetch(apiUrl(url))
      if (!glbResp.ok) throw new Error(`fetching the generated GLB failed: HTTP ${glbResp.status}`)
      const glb = await glbResp.blob()
      const name = url.split('/').pop()
      setResult({ blob: glb, name })
      selectEntry(null)
      const inputThumb = image.dataUrl ? await makeThumb(image.dataUrl) : null
      await addEntry({
        model,
        params: { quality, background, steps, textureSteps, guidance, seed },
        inputThumb,
        glb,
        name,
      })
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="media-layout">
      <div className="media-controls">
        <PageHeader title={<><i className="fas fa-cube" /> {t('threed.title')}</>} />

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">{t('threed.labels.model')}</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_3D} />
          </div>

          <MediaInput
            mode="image"
            label={t('threed.labels.image')}
            value={image}
            onChange={setImage}
            onError={(err) => addToast(err.message, 'error')}
            maxBytes={MAX_3D_INPUT_BYTES}
            idPrefix="threed"
          />

          <div className="form-grid-2col">
            <div className="form-group">
              <label className="form-label">{t('threed.labels.quality')}</label>
              <select className="input btn-full" value={quality} onChange={(e) => setQuality(e.target.value)}>
                {QUALITIES.map(q => <option key={q} value={q}>{t(`threed.labels.quality_${q}`)}</option>)}
              </select>
            </div>
            <div className="form-group">
              <label className="form-label">{t('threed.labels.background')}</label>
              <select className="input btn-full" value={background} onChange={(e) => setBackground(e.target.value)}>
                {BACKGROUNDS.map(b => <option key={b} value={b}>{t(`threed.labels.background_${b}`)}</option>)}
              </select>
            </div>
          </div>

          <button
            type="button"
            className={`collapsible-header ${showAdvanced ? 'open' : ''}`}
            aria-expanded={showAdvanced}
            aria-controls="threed-advanced-options"
            onClick={() => setShowAdvanced(!showAdvanced)}
          >
            <i className="fas fa-chevron-right" aria-hidden="true" /> {t('threed.labels.advanced')}
          </button>
          {showAdvanced && (
            <div id="threed-advanced-options" className="form-grid-2col">
              <div className="form-group"><label className="form-label">{t('threed.labels.steps')}</label><input className="input" type="number" min="1" value={steps} onChange={(e) => setSteps(e.target.value)} placeholder="12" /></div>
              <div className="form-group"><label className="form-label">{t('threed.labels.textureSteps')}</label><input className="input" type="number" min="1" value={textureSteps} onChange={(e) => setTextureSteps(e.target.value)} placeholder="12" /></div>
              <div className="form-group"><label className="form-label">{t('threed.labels.guidance')}</label><input className="input" type="number" step="0.1" value={guidance} onChange={(e) => setGuidance(e.target.value)} placeholder="7.5" /></div>
              <div className="form-group"><label className="form-label">{t('threed.labels.seed')}</label><input className="input" type="number" value={seed} onChange={(e) => setSeed(e.target.value)} placeholder={t('threed.labels.seedPlaceholder')} /></div>
            </div>
          )}

          <button type="submit" className="btn btn-primary btn-full" disabled={loading}>
            {loading ? <><LoadingSpinner size="sm" /> {t('threed.actions.generating')}</> : <><i className="fas fa-cube" /> {t('threed.actions.generate')}</>}
          </button>
        </form>
        <ThreeDHistory
          entries={entries}
          selectedId={selectedId}
          onSelect={selectEntry}
          onDelete={deleteEntry}
          onClearAll={clearAll}
        />
      </div>

      <div className="media-preview">
        <div className="media-result">
          {loading ? (
            <GenerationProgress label={t('threed.actions.generating')} />
          ) : error ? (
            <ErrorWithTraceLink message={error} />
          ) : active?.blob ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)', width: '100%' }}>
              <GlbViewer blob={active.blob} />
              <a
                className="btn btn-secondary"
                href={downloadUrl}
                download={active.name || `3d-${model || 'model'}.glb`}
                data-testid="glb-download"
              >
                <i className="fas fa-download" /> {t('threed.actions.download')}
              </a>
            </div>
          ) : (
            <div style={{ textAlign: 'center', color: 'var(--color-text-muted)' }}>
              <i className="fas fa-cube" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)', opacity: 0.4 }} />
              <p>{t('threed.empty')}</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
