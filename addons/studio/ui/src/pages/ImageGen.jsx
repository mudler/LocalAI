import { useState, useEffect } from 'react'
// eslint-disable-next-line no-unused-vars
import RequestPanel from '../components/RequestPanel'
import { useParams, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { CAP_IMAGE } from '../utils/capabilities'
// eslint-disable-next-line no-unused-vars
import Lightbox from '../components/Lightbox'
import { imageApi, fileToBase64 } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'
import { useStudioHandoff, useHandoffSource, blobToImageInput } from '../hooks/useStudioHandoff'
import { parseSize } from '../utils/studioWork'
// eslint-disable-next-line no-unused-vars
import HandoffNote from '../components/studio/HandoffNote'
import {
  // eslint-disable-next-line no-unused-vars
  Workspace, ComposeCard, ChipSelect, ModelChip, Field, Fold, SourceChip, Starters, RunArea, JobCard, FailedCard, ResultCard, ResultsStrip, EmptyRun,
} from '../components/studio/Workspace'
import { foldSummary, useWorkspace } from '../hooks/useWorkspace'
import Icon from '../components/Icon'

const SIZES = ['256x256', '512x512', '768x768', '1024x1024']

export default function ImageGen() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('media')
  // Opened from the Studio front page, the form starts from what it sent.
  const handoff = useStudioHandoff()
  const [model, setModel] = useState(urlModel || handoff.model || '')
  const [prompt, setPrompt] = useState(handoff.prompt)
  const [negativePrompt, setNegativePrompt] = useState('')
  const [size, setSize] = useState(SIZES.includes(handoff.size) ? handoff.size : '512x512')
  const [count, setCount] = useState(Math.min(4, handoff.count || 1))
  const [steps, setSteps] = useState('')
  const [seed, setSeed] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [images, setImages] = useState([])
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [sourceImage, setSourceImage] = useState(null)
  const [sourceName, setSourceName] = useState('')
  const [sourcePreview, setSourcePreview] = useState(null)
  const [refImages, setRefImages] = useState([])
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('image')
  const ws = useWorkspace({ type: 'images', entries: historyProps.entries })
  const [lastId, setLastId] = useState(null)
  // A variation starts from the picture it was made from, as the source image.
  const wantsSource = handoff.edge === 'variation'
  const source = useHandoffSource(handoff, wantsSource)
  useEffect(() => {
    if (source.status !== 'ready') return
    blobToImageInput(source.blob).then((img) => { setSourceImage(img.base64); setSourcePreview(img.dataUrl); setSourceName(source.item?.title || '') }).catch(() => {})
  }, [source])
  const [lightboxIdx, setLightboxIdx] = useState(null)
  // The body of the last request, kept so the panel can show what was actually
  // sent rather than what the form currently holds.
  const [lastRequest, setLastRequest] = useState(null)

  // The images currently on screen (a picked history entry, else the latest run).
  const displayImages = selectedEntry
    ? selectedEntry.results.map(r => ({ url: r.url, alt: selectedEntry.prompt }))
    : images.map(img => ({ url: img.url || `data:image/png;base64,${img.b64_json}`, alt: prompt }))
  const activeEntry = selectedEntry || (lastId ? historyProps.entries.find(e => e.id === lastId) : null) || null
  const item = ws.itemById(activeEntry?.id)

  const run = async () => {
    if (!prompt.trim()) { addToast(t('image.toasts.noPrompt'), 'warning'); return }
    if (!model) { addToast(t('image.toasts.noModel'), 'warning'); return }

    setLoading(true)
    setImages([])
    setError(null)
    setLastId(null)

    let combinedPrompt = prompt.trim()
    if (negativePrompt.trim()) combinedPrompt += '|' + negativePrompt.trim()

    const body = { model, prompt: combinedPrompt, n: count, size }
    if (steps) body.step = parseInt(steps)
    if (seed) body.seed = parseInt(seed)
    if (sourceImage) body.file = sourceImage
    if (refImages.length > 0) body.ref_images = refImages

    setLastRequest(body)

    try {
      const data = await imageApi.generate(body)
      const results = data?.data || []
      setImages(results)
      if (!results.length) {
        addToast(t('image.toasts.noResults'), 'warning')
      } else {
        const urlResults = results.filter(r => r.url && !r.url.startsWith('data:')).map(r => ({ url: r.url }))
        if (urlResults.length) {
          setLastId(addEntry({ prompt: prompt.trim(), model, params: { size, count, steps, seed, negativePrompt: negativePrompt.trim() || undefined }, results: urlResults, parentId: handoff.from || undefined, edge: handoff.edge || undefined }))
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

  const handleRefImages = async (files) => {
    const arr = []
    for (const f of files) arr.push(await fileToBase64(f))
    setRefImages(prev => [...prev, ...arr])
  }

  // Re-run with edits: this result's values go back into the form, and the form
  // remembers them so what changes can be listed.
  const rerun = (it) => {
    const entry = historyProps.entries.find(e => e.id === it.id)
    if (!entry) return
    const p = entry.params || {}
    const next = {
      prompt: entry.prompt || '',
      model: entry.model || model,
      size: SIZES.includes(p.size) ? p.size : size,
      count: String(Math.min(4, Number(p.count) || 1)),
      steps: p.steps ? String(p.steps) : '',
      seed: p.seed ? String(p.seed) : '',
      negative: p.negativePrompt || '',
    }
    setPrompt(next.prompt); setModel(next.model); setSize(next.size); setCount(Number(next.count))
    setSteps(next.steps); setSeed(next.seed); setNegativePrompt(next.negative)
    if (next.steps || next.seed || next.negative) setShowAdvanced(true)
    selectEntry(null)
    ws.begin(next)
  }

  const labels = {
    prompt: t('studio.workspace.fields.prompt'), model: t('studio.workspace.fields.model'), size: t('studio.workspace.fields.size'),
    count: t('studio.workspace.fields.count'), steps: t('studio.workspace.fields.steps'), seed: t('studio.workspace.fields.seed'),
    negative: t('studio.workspace.fields.negative'),
  }
  const current = { prompt, model, size, count: String(count), steps, seed, negative: negativePrompt }
  const changes = ws.changes(current, labels)
  const changed = new Set(changes.map(c => c.field))

  const installed = ws.installed.byType.images
  const noModel = !ws.installed.loading && !ws.installed.error && installed.length === 0
  const why = noModel ? t('studio.composer.whyModel', { type: t('studio.tabs.images') })
    : !prompt.trim() ? t('studio.composer.whyPrompt') : ''
  const dims = parseSize(size)

  return (
    <Workspace type="images">
      <ComposeCard
        ws={ws}
        icon="image"
        title={t('image.title')}
        lede={t('studio.workspace.lede.images')}
        onSubmit={(e) => { e.preventDefault(); run() }}
        handoff={<HandoffNote source={source} handoff={handoff} wanted={wantsSource} onClear={() => { setSourceImage(null); setSourceName('') }} />}
        sources={<>
          <SourceChip
            label={t('studio.workspace.sources.startImage')}
            accept="image/*"
            name={sourceImage ? (sourceName || t('studio.workspace.sources.chosen')) : ''}
            preview={sourceImage ? sourcePreview : undefined}
            onFiles={async (file) => { const img = await blobToImageInput(file); setSourceImage(img.base64); setSourcePreview(img.dataUrl); setSourceName(file.name) }}
            onClear={() => { setSourceImage(null); setSourceName('') }}
          />
          <SourceChip
            label={t('image.labels.refImages')}
            accept="image/*"
            multiple
            count={refImages.length}
            name={refImages.length ? t('image.labels.refImagesAdded', { count: refImages.length }) : ''}
            onFiles={handleRefImages}
            onClear={() => setRefImages([])}
          />
        </>}
        model={model}
        noModel={noModel ? { type: 'images', label: t('studio.tabs.images'), onChanged: ws.installed.refetch } : null}
        options={<>
          <ModelChip value={model} onChange={setModel} capability={CAP_IMAGE} changed={changed.has('model')} />
          <ChipSelect label={t('image.labels.size')} value={size} onChange={setSize} options={SIZES.map(s => ({ value: s, label: s.replace('x', ' × ') }))} changed={changed.has('size')} testId="ws-size" />
          <ChipSelect label={t('image.labels.count')} value={String(count)} onChange={(v) => setCount(parseInt(v) || 1)} options={[1, 2, 3, 4].map(n => ({ value: String(n), label: t('studio.composer.countOption', { count: n }) }))} mono={false} changed={changed.has('count')} testId="ws-count" />
        </>}
        fold={
          <Fold
            label={t('image.labels.advanced')}
            summary={foldSummary([steps && `${labels.steps} ${steps}`, seed && `${labels.seed} ${seed}`, negativePrompt.trim() && t('image.labels.negativePrompt').toLowerCase()].filter(Boolean), [t('image.labels.negativePrompt').toLowerCase(), labels.steps, labels.seed])}
            open={showAdvanced}
            onToggle={() => setShowAdvanced(v => !v)}
            id="image-advanced-options"
          >
            <Field label={t('image.labels.negativePrompt')} htmlFor="image-negative" changed={changed.has('negative')}>
              <textarea id="image-negative" className="textarea" value={negativePrompt} onChange={(e) => setNegativePrompt(e.target.value)} placeholder={t('image.labels.negativePromptPlaceholder')} rows={2} />
            </Field>
            <div className="ws-grid">
              <Field label={t('image.labels.steps')} htmlFor="image-steps" changed={changed.has('steps')}>
                <input id="image-steps" className="dk-input" type="number" value={steps} onChange={(e) => setSteps(e.target.value)} placeholder={t('image.labels.stepsPlaceholder')} />
              </Field>
              <Field label={t('image.labels.seed')} htmlFor="image-seed" changed={changed.has('seed')}>
                <input id="image-seed" className="dk-input" type="number" value={seed} onChange={(e) => setSeed(e.target.value)} placeholder={t('image.labels.seedPlaceholder')} />
              </Field>
            </div>
          </Fold>
        }
        changes={changes}
        submit={{ label: t('image.actions.generate'), busyLabel: t('image.actions.generating'), busy: loading, disabled: !model || !prompt.trim() || noModel, why }}
      >
        <textarea
          className="ws-prompt textarea"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          placeholder={t('image.labels.promptPlaceholder')}
          aria-label={t('image.labels.prompt')}
          rows={3}
          data-changed={changed.has('prompt') || undefined}
          onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); run() } }}
        />
        {!prompt && <Starters type="images" onPick={setPrompt} />}
      </ComposeCard>

      <RunArea>
        {loading ? (
          <JobCard label={t('image.actions.generating')} detail={[model, size, t('studio.composer.countOption', { count })].join(' · ')} tiles={count} ratio={dims ? dims.width / dims.height : 1} />
        ) : error ? (
          <FailedCard message={error} onRetry={run} />
        ) : displayImages.length > 0 ? (
          <ResultCard
            ws={ws}
            item={item}
            title={item?.title || prompt}
            meta={[item?.model || model, item?.params?.size || size, item?.params?.seed ? `${labels.seed} ${item.params.seed}` : '']}
            download={displayImages.length === 1 ? { href: displayImages[0].url, name: `image-${Date.now()}.png` } : null}
            onRerun={rerun}
          >
            <div className={`ws-grid-images${displayImages.length === 1 ? ' ws-grid-images--one' : ''} media-result-grid`}>
              {displayImages.map((im, i) => (
                <button type="button" key={i} className="media-result-thumb" onClick={() => setLightboxIdx(i)} title={t('image.actions.view')} aria-label={t('image.actions.view')}>
                  <img src={im.url} alt={im.alt} />
                  <span className="media-result-thumb__zoom" aria-hidden="true"><Icon name="maximize" /></span>
                </button>
              ))}
            </div>
          </ResultCard>
        ) : (
          <EmptyRun icon="image" text={t('image.empty')} />
        )}
        <RequestPanel endpoint="/v1/images/generations" body={lastRequest} />
      </RunArea>

      <ResultsStrip
        ws={ws}
        selectedId={historyProps.selectedId}
        activeId={activeEntry?.id}
        onSelect={historyProps.onSelect}
        onDelete={historyProps.onDelete}
        onClear={historyProps.onClearAll}
      />
      {lightboxIdx !== null && (
        <Lightbox images={displayImages} index={lightboxIdx} onIndex={setLightboxIdx} onClose={() => setLightboxIdx(null)} />
      )}
    </Workspace>
  )
}
