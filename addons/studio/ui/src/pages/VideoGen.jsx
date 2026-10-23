import { useState, useEffect } from 'react'
// eslint-disable-next-line no-unused-vars
import RequestPanel from '../components/RequestPanel'
import { useParams, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { CAP_VIDEO } from '../utils/capabilities'
// eslint-disable-next-line no-unused-vars
import MediaInput from '../components/biometrics/MediaInput'
import { videoApi } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'
import { useStudioHandoff, useHandoffSource, blobToImageInput } from '../hooks/useStudioHandoff'
// eslint-disable-next-line no-unused-vars
import HandoffNote from '../components/studio/HandoffNote'
import {
  // eslint-disable-next-line no-unused-vars
  Workspace, ComposeCard, ChipSelect, ChipInput, ModelChip, Field, Fold, SourceChip, PanelChip, Starters, RunArea, JobCard, FailedCard, ResultCard, ResultsStrip, EmptyRun,
} from '../components/studio/Workspace'
import { foldSummary, useWorkspace } from '../hooks/useWorkspace'

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
  const [showAvatar, setShowAvatar] = useState(false)
  const [startImage, setStartImage] = useState(null)
  const [startName, setStartName] = useState('')
  const [startPreview, setStartPreview] = useState(null)
  const [endPreview, setEndPreview] = useState(null)
  const [endImage, setEndImage] = useState(null)
  const [endName, setEndName] = useState('')
  const [audioInput, setAudioInput] = useState(null)
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('video')
  const ws = useWorkspace({ type: 'video', entries: historyProps.entries })
  const [lastId, setLastId] = useState(null)
  // Animating a picture starts from it as the start image.
  const wantsSource = handoff.edge === 'animate'
  const source = useHandoffSource(handoff, wantsSource)
  useEffect(() => {
    if (source.status !== 'ready') return
    blobToImageInput(source.blob).then((img) => { setStartImage(img.base64); setStartPreview(img.dataUrl); setStartName(source.item?.title || '') }).catch(() => {})
  }, [source])

  const activeEntry = selectedEntry || (lastId ? historyProps.entries.find(e => e.id === lastId) : null) || null
  const item = ws.itemById(activeEntry?.id)
  const shown = selectedEntry
    ? selectedEntry.results.map(r => r.url)
    : videos.map(v => v.url || `data:video/mp4;base64,${v.b64_json}`)

  const run = async () => {
    if (!prompt.trim()) { addToast(t('video.toasts.noPrompt'), 'warning'); return }
    if (!model) { addToast(t('video.toasts.noModel'), 'warning'); return }

    setLoading(true)
    setVideos([])
    setError(null)
    setLastId(null)

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
          setLastId(addEntry({ prompt: prompt.trim(), model, params: { size, fps, seconds, frames, steps, seed, cfgScale, negativePrompt: negativePrompt.trim() || undefined }, results: urlResults, parentId: handoff.from || undefined, edge: handoff.edge || undefined }))
        }
        selectEntry(null)
        ws.end()
      }
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  const rerun = (it) => {
    const entry = historyProps.entries.find(e => e.id === it.id)
    if (!entry) return
    const p = entry.params || {}
    const next = {
      prompt: entry.prompt || '', model: entry.model || model, size: SIZES.includes(p.size) ? p.size : size,
      seconds: p.seconds ? String(p.seconds) : '', fps: p.fps ? String(p.fps) : '16', frames: p.frames ? String(p.frames) : '',
      steps: p.steps ? String(p.steps) : '', seed: p.seed ? String(p.seed) : '', cfg: p.cfgScale ? String(p.cfgScale) : '',
      negative: p.negativePrompt || '',
    }
    setPrompt(next.prompt); setModel(next.model); setSize(next.size); setSeconds(next.seconds); setFps(next.fps)
    setFrames(next.frames); setSteps(next.steps); setSeed(next.seed); setCfgScale(next.cfg); setNegativePrompt(next.negative)
    if (next.frames || next.steps || next.seed || next.cfg || next.negative) setShowAdvanced(true)
    selectEntry(null)
    ws.begin(next)
  }

  const f = (key) => t(`studio.workspace.fields.${key}`)
  const labels = {
    prompt: f('prompt'), model: f('model'), size: f('size'), seconds: f('duration'), fps: f('fps'), frames: f('frames'),
    steps: f('steps'), seed: f('seed'), cfg: f('cfg'), negative: f('negative'),
  }
  const current = { prompt, model, size, seconds, fps, frames, steps, seed, cfg: cfgScale, negative: negativePrompt }
  const changes = ws.changes(current, labels)
  const changed = new Set(changes.map(c => c.field))

  const installed = ws.installed.byType.video
  const noModel = !ws.installed.loading && !ws.installed.error && installed.length === 0
  const why = noModel ? t('studio.composer.whyModel', { type: t('studio.tabs.video') })
    : !prompt.trim() ? t('studio.composer.whyPrompt') : ''

  return (
    <Workspace type="video">
      <ComposeCard
        ws={ws}
        icon="video"
        title={t('video.title')}
        lede={t('studio.workspace.lede.video')}
        onSubmit={(e) => { e.preventDefault(); run() }}
        handoff={<HandoffNote source={source} handoff={handoff} wanted={wantsSource} onClear={() => { setStartImage(null); setStartName('') }} />}
        sources={<>
          <SourceChip
            label={t('video.labels.startImage')}
            accept="image/*"
            name={startImage ? (startName || t('studio.workspace.sources.chosen')) : ''}
            preview={startImage ? startPreview : undefined}
            onFiles={async (file) => { const img = await blobToImageInput(file); setStartImage(img.base64); setStartPreview(img.dataUrl); setStartName(file.name) }}
            onClear={() => { setStartImage(null); setStartName('') }}
          />
          <SourceChip
            label={t('video.labels.endImage')}
            accept="image/*"
            name={endImage ? (endName || t('studio.workspace.sources.chosen')) : ''}
            preview={endImage ? endPreview : undefined}
            onFiles={async (file) => { const img = await blobToImageInput(file); setEndImage(img.base64); setEndPreview(img.dataUrl); setEndName(file.name) }}
            onClear={() => { setEndImage(null); setEndName('') }}
          />
          <PanelChip label={t('video.labels.avatarAudio')} open={showAvatar} onToggle={() => setShowAvatar(v => !v)} filled={!!audioInput?.base64} controls="video-avatar-panel" />
        </>}
        panels={showAvatar && (
          <div id="video-avatar-panel" className="ws-panel">
            <MediaInput mode="audio" label={t('video.labels.avatarAudio')} value={audioInput} onChange={setAudioInput} idPrefix="video-avatar" />
          </div>
        )}
        model={model}
        noModel={noModel ? { type: 'video', label: t('studio.tabs.video'), onChanged: ws.installed.refetch } : null}
        options={<>
          <ModelChip value={model} onChange={setModel} capability={CAP_VIDEO} changed={changed.has('model')} />
          <ChipSelect label={t('video.labels.size')} value={size} onChange={setSize} options={SIZES.map(s => ({ value: s, label: s.replace('x', ' × ') }))} changed={changed.has('size')} testId="ws-size" />
          <ChipInput label={t('video.labels.duration')} value={seconds} onChange={setSeconds} placeholder={t('studio.workspace.auto')} changed={changed.has('seconds')} testId="ws-duration" />
          <ChipInput label={t('video.labels.fps')} type="number" value={fps} onChange={setFps} changed={changed.has('fps')} testId="ws-fps" />
        </>}
        fold={
          <Fold
            label={t('video.labels.advanced')}
            summary={foldSummary([steps && `${labels.steps} ${steps}`, seed && `${labels.seed} ${seed}`, cfgScale && `${labels.cfg} ${cfgScale}`, frames && `${labels.frames} ${frames}`, negativePrompt.trim() && labels.negative].filter(Boolean), [labels.negative, labels.steps, labels.seed, labels.cfg, labels.frames])}
            open={showAdvanced}
            onToggle={() => setShowAdvanced(v => !v)}
            id="video-advanced-options"
          >
            <Field label={t('image.labels.negativePrompt')} htmlFor="video-negative" changed={changed.has('negative')}>
              <textarea id="video-negative" className="textarea" value={negativePrompt} onChange={(e) => setNegativePrompt(e.target.value)} rows={2} />
            </Field>
            <div className="ws-grid">
              <Field label={t('image.labels.steps')} htmlFor="video-steps" changed={changed.has('steps')}><input id="video-steps" className="dk-input" type="number" value={steps} onChange={(e) => setSteps(e.target.value)} /></Field>
              <Field label={t('video.labels.seed')} htmlFor="video-seed" changed={changed.has('seed')}><input id="video-seed" className="dk-input" type="number" value={seed} onChange={(e) => setSeed(e.target.value)} placeholder={t('video.labels.seedPlaceholder')} /></Field>
              <Field label="CFG Scale" htmlFor="video-cfg" changed={changed.has('cfg')}><input id="video-cfg" className="dk-input" type="number" step="0.1" value={cfgScale} onChange={(e) => setCfgScale(e.target.value)} /></Field>
              <Field label={t('video.labels.frames')} htmlFor="video-frames" changed={changed.has('frames')}><input id="video-frames" className="dk-input" type="number" min="1" value={frames} onChange={(e) => setFrames(e.target.value)} /></Field>
            </div>
          </Fold>
        }
        changes={changes}
        submit={{ label: t('video.actions.generate'), busyLabel: t('video.actions.generating'), busy: loading, disabled: !model || !prompt.trim() || noModel, why, icon: 'video' }}
      >
        <textarea
          className="ws-prompt textarea"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          placeholder={t('video.labels.promptPlaceholder')}
          aria-label={t('video.labels.prompt')}
          rows={3}
          data-changed={changed.has('prompt') || undefined}
        />
        {!prompt && <Starters type="video" onPick={setPrompt} />}
      </ComposeCard>

      <RunArea>
        {loading ? (
          <JobCard label={t('video.actions.generating')} detail={[model, size, `${fps} ${t('video.labels.fps').toLowerCase()}`].join(' · ')} />
        ) : error ? (
          <FailedCard message={error} onRetry={run} />
        ) : shown.length > 0 ? (
          <ResultCard
            ws={ws}
            item={item}
            title={item?.title || prompt}
            meta={[item?.model || model, item?.params?.size || size, item?.params?.fps ? `${item.params.fps} ${t('video.labels.fps').toLowerCase()}` : '', item?.params?.seed ? `${labels.seed} ${item.params.seed}` : '']}
            download={shown.length === 1 ? { href: shown[0], name: `video-${Date.now()}.mp4` } : null}
            onRerun={rerun}
          >
            {shown.map((src, i) => <video key={i} controls src={src} />)}
          </ResultCard>
        ) : (
          <EmptyRun icon="video" text={t('video.empty')} />
        )}
        <RequestPanel endpoint="/video" body={lastRequest} />
      </RunArea>

      <ResultsStrip
        ws={ws}
        selectedId={historyProps.selectedId}
        activeId={activeEntry?.id}
        onSelect={historyProps.onSelect}
        onDelete={historyProps.onDelete}
        onClear={historyProps.onClearAll}
      />
    </Workspace>
  )
}
