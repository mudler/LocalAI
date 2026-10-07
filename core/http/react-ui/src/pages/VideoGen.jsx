import { useState, useEffect } from 'react'
import RequestPanel from '../components/RequestPanel'
import { useParams, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ModelSelector from '../components/ModelSelector'
import PageHeader from '../components/PageHeader'
import { CAP_VIDEO } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import GenerationProgress from '../components/GenerationProgress'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import MediaHistory from '../components/MediaHistory'
import MediaInput from '../components/biometrics/MediaInput'
import { videoApi, fileToBase64 } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'
import { useStudioHandoff, useHandoffSource } from '../hooks/useStudioHandoff'
// eslint-disable-next-line no-unused-vars
import HandoffNote from '../components/studio/HandoffNote'
import Icon from '../components/Icon'

const SIZES = ['256x256', '512x512', '768x768', '1024x1024', '832x480', '1280x720']

export default function VideoGen() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('media')
  // Opened from the Studio front page, the form starts from what it sent.
  const handoff = useStudioHandoff()
  const [model, setModel] = useState(urlModel || handoff.model || '')
  const [prompt, setPrompt] = useState(handoff.prompt)
  const [negativePrompt, setNegativePrompt] = useState('')
  const [size, setSize] = useState(SIZES.includes(handoff.size) ? handoff.size : '512x512')
  const [seconds, setSeconds] = useState('')
  const [fps, setFps] = useState('16')
  const [frames, setFrames] = useState('')
  const [steps, setSteps] = useState('')
  const [seed, setSeed] = useState('')
  const [cfgScale, setCfgScale] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  // What was actually sent, so the panel records rather than predicts.
  const [lastRequest, setLastRequest] = useState(null)
  const [videos, setVideos] = useState([])
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [showMediaInputs, setShowMediaInputs] = useState(false)
  const [startImage, setStartImage] = useState(null)
  const [endImage, setEndImage] = useState(null)
  const [audioInput, setAudioInput] = useState(null)
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('video')
  // Animating a picture starts from it as the start image.
  const wantsSource = handoff.edge === 'animate'
  const source = useHandoffSource(handoff, wantsSource)
  useEffect(() => {
    if (source.status !== 'ready') return
    fileToBase64(source.blob).then(setStartImage).catch(() => {})
  }, [source])

  const handleGenerate = async (e) => {
    e.preventDefault()
    if (!prompt.trim()) { addToast(t('video.toasts.noPrompt'), 'warning'); return }
    if (!model) { addToast(t('video.toasts.noModel'), 'warning'); return }

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
    if (audioInput?.base64) body.audio = audioInput.base64

    setLastRequest(body)

    try {
      const data = await videoApi.generate(body)
      const results = data?.data || []
      setVideos(results)
      if (!results.length) {
        addToast(t('video.toasts.noResults'), 'warning')
      } else {
        const urlResults = results.filter(r => r.url && !r.url.startsWith('data:')).map(r => ({ url: r.url }))
        if (urlResults.length) {
          addEntry({ prompt: prompt.trim(), model, params: { size, fps, seconds, frames, steps, seed, cfgScale, negativePrompt: negativePrompt.trim() || undefined }, results: urlResults, parentId: handoff.from || undefined, edge: handoff.edge || undefined })
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
        <PageHeader title={<><Icon name="video" /> {t('video.title')}</>} />
        <HandoffNote source={source} handoff={handoff} wanted={wantsSource} onClear={() => setStartImage(null)} />

        <form onSubmit={handleGenerate}>
          <div className="form-group">
            <label className="form-label">{t('video.labels.model')}</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_VIDEO} />
          </div>
          <div className="form-group">
            <label className="form-label">{t('video.labels.prompt')}</label>
            <textarea className="textarea" value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder={t('video.labels.promptPlaceholder')} rows={3} />
          </div>
          <div className="form-group">
            <label className="form-label">{t('image.labels.negativePrompt')}</label>
            <textarea className="textarea" value={negativePrompt} onChange={(e) => setNegativePrompt(e.target.value)} rows={2} />
          </div>

          <div className="form-grid-3col">
            <div className="form-group">
              <label className="form-label">{t('video.labels.size')}</label>
              <select className="input btn-full" value={size} onChange={(e) => setSize(e.target.value)}>
                {SIZES.map(s => <option key={s} value={s}>{s}</option>)}
              </select>
            </div>
            <div className="form-group">
              <label className="form-label">{t('video.labels.duration')}</label>
              <input className="input" type="text" value={seconds} onChange={(e) => setSeconds(e.target.value)} placeholder="Auto" />
            </div>
            <div className="form-group">
              <label className="form-label">{t('video.labels.fps')}</label>
              <input className="input" type="number" value={fps} onChange={(e) => setFps(e.target.value)} />
            </div>
          </div>

          <button
            type="button"
            className={`collapsible-header ${showAdvanced ? 'open' : ''}`}
            aria-expanded={showAdvanced}
            aria-controls="video-advanced-options"
            onClick={() => setShowAdvanced(!showAdvanced)}
          >
            <Icon name="chevron-right" /> {t('video.labels.advanced')}
          </button>
          {showAdvanced && (
            <div id="video-advanced-options" className="form-grid-3col">
              <div className="form-group"><label className="form-label">{t('image.labels.steps')}</label><input className="input" type="number" value={steps} onChange={(e) => setSteps(e.target.value)} /></div>
              <div className="form-group"><label className="form-label">{t('video.labels.seed')}</label><input className="input" type="number" value={seed} onChange={(e) => setSeed(e.target.value)} /></div>
              <div className="form-group"><label className="form-label">CFG Scale</label><input className="input" type="number" step="0.1" value={cfgScale} onChange={(e) => setCfgScale(e.target.value)} /></div>
              <div className="form-group"><label className="form-label">{t('video.labels.frames')}</label><input className="input" type="number" min="1" value={frames} onChange={(e) => setFrames(e.target.value)} /></div>
            </div>
          )}

          <button
            type="button"
            className={`collapsible-header ${showMediaInputs ? 'open' : ''}`}
            aria-expanded={showMediaInputs}
            aria-controls="video-reference-media"
            onClick={() => setShowMediaInputs(!showMediaInputs)}
          >
            <Icon name="chevron-right" /> {t('video.labels.referenceMedia')}
          </button>
          {showMediaInputs && (
            <div id="video-reference-media">
              <div className="form-grid-2col">
                <div className="form-group"><label className="form-label">{t('video.labels.startImage')}</label><input type="file" accept="image/*" onChange={(e) => handleImageUpload(e, setStartImage)} className="input" /></div>
                <div className="form-group"><label className="form-label">{t('video.labels.endImage')}</label><input type="file" accept="image/*" onChange={(e) => handleImageUpload(e, setEndImage)} className="input" /></div>
              </div>
              <MediaInput
                mode="audio"
                label={t('video.labels.avatarAudio')}
                value={audioInput}
                onChange={setAudioInput}
                idPrefix="video-avatar"
              />
            </div>
          )}

          <button type="submit" className="btn btn-primary btn-full" disabled={loading}>
            {loading ? <><LoadingSpinner size="sm" /> {t('video.actions.generating')}</> : <><Icon name="video" /> {t('video.actions.generate')}</>}
          </button>
        </form>
        <MediaHistory {...historyProps} />
      </div>

      <div className="media-preview">
        <RequestPanel endpoint="/video" body={lastRequest} />
        <div className="media-result">
          {loading ? (
            <GenerationProgress label={t('video.actions.generating')} />
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
            <div className="text-center text-muted">
              <Icon name="video" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)', opacity: 0.4 }} />
              <p>{t('video.empty')}</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
