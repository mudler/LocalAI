import { useEffect, useMemo, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import RequestPanel from '../components/RequestPanel'
import { useParams, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { CAP_3D, CAP_3D_ANIMATION } from '../utils/capabilities'
import { useModels } from '../hooks/useModels'
// eslint-disable-next-line no-unused-vars
import AnimationOptions from '../components/AnimationOptions'
// eslint-disable-next-line no-unused-vars
import AnimationViewer from '../components/AnimationViewer'
// eslint-disable-next-line no-unused-vars
import LoadingSpinner from '../components/LoadingSpinner'
// eslint-disable-next-line no-unused-vars
import GlbViewer from '../components/GlbViewer'
// eslint-disable-next-line no-unused-vars
import MediaInput from '../components/biometrics/MediaInput'
import { threeDApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { use3DHistory } from '../hooks/use3DHistory'
import { useStudioHandoff, useHandoffSource, blobToImageInput } from '../hooks/useStudioHandoff'
// eslint-disable-next-line no-unused-vars
import HandoffNote from '../components/studio/HandoffNote'
import {
  // eslint-disable-next-line no-unused-vars
  Workspace, ComposeCard, ChipSelect, ModelChip, Field, Fold, RunArea, JobCard, FailedCard, ResultCard, ResultsStrip, EmptyRun,
} from '../components/studio/Workspace'
import { foldSummary, useWorkspace } from '../hooks/useWorkspace'
import useObjectUrl from '../hooks/useObjectUrl'
import Icon from '../components/Icon'

const QUALITIES = ['auto', 'coarse', '512', '1024']
const BACKGROUNDS = ['auto', 'keep', 'black', 'white']
const MAX_3D_INPUT_BYTES = 32 * 1024 * 1024
const REMESH_DETAIL_COARSE = 2.5
const REMESH_DETAIL_FINE = 0.35

function remeshDetail(sliderValue) {
  const position = Number(sliderValue) / 100
  return REMESH_DETAIL_COARSE * Math.pow(REMESH_DETAIL_FINE / REMESH_DETAIL_COARSE, position)
}

function remeshedName(name = '3d-model.glb') {
  return `${name.replace(/\.glb$/i, '')}-remeshed.glb`
}

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
  // Opened from the Studio front page, a picture it was made from becomes the
  // conditioning image and the model it chose is selected.
  const handoff = useStudioHandoff()
  const [model, setModel] = useState(urlModel || handoff.model || '')
  const { models, loading: modelsLoading } = useModels()
  const modelNames = useMemo(() => models.filter(item => item.capabilities?.some(cap => cap === CAP_3D || cap === CAP_3D_ANIMATION)).map(item => item.id), [models])
  const selectedModel = models.find(item => item.id === model)
  const animation = selectedModel?.three_d_operations?.find(operation => operation.id === 'animate')
  const multiview = selectedModel?.three_d_operations?.some(operation => operation.id === 'generate_multiview')
  const [views, setViews] = useState({})
  const [meshScale, setMeshScale] = useState('')
  const [animationInputs, setAnimationInputs] = useState({})
  const [animationParams, setAnimationParams] = useState({})
  const [requestEndpoint, setRequestEndpoint] = useState('/3d/generations')
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
  // What was actually sent, so the panel records rather than predicts.
  const [lastRequest, setLastRequest] = useState(null)
  const [result, setResult] = useState(null) // { blob, name, model }
  const [lastId, setLastId] = useState(null)
  const [remeshSlider, setRemeshSlider] = useState(82)
  const [remeshState, setRemeshState] = useState(null) // { sourceBlob, blob?, name?, error? }
  const [remeshLoading, setRemeshLoading] = useState(false)
  const { entries, addEntry, deleteEntry, clearAll, selectEntry, selectedId, selectedEntry } = use3DHistory()
  const ws = useWorkspace({ type: 'threed', entries })
  const wantsSource = handoff.edge === 'to-3d'
  const handoffSource = useHandoffSource(handoff, wantsSource)
  useEffect(() => {
    if (handoffSource.status !== 'ready') return
    blobToImageInput(handoffSource.blob).then(setImage).catch(() => {})
  }, [handoffSource])

  const source = selectedEntry
    ? { ...selectedEntry, blob: selectedEntry.glb }
    : result
  const showingRemesh = !!source && remeshState?.sourceBlob === source.blob && !!remeshState.blob
  const active = showingRemesh ? { blob: remeshState.blob, name: remeshState.name } : source
  const remeshError = source && remeshState?.sourceBlob === source.blob ? remeshState.error : null
  const detail = remeshDetail(remeshSlider)
  const downloadUrl = useObjectUrl(active?.blob)
  const sourceModel = models.find(item => item.id === source?.model)
  const canRemesh = source?.outputType !== 'skeleton_animation' && (sourceModel?.three_d_operations?.some(operation => operation.id === 'remesh') ||
    (!sourceModel?.three_d_operations && sourceModel?.capabilities?.includes(CAP_3D)))
  const activeEntry = selectedEntry || (lastId ? entries.find(e => e.id === lastId) : null) || null
  const item = ws.itemById(activeEntry?.id)

  const changeModel = (next) => {
    setModel(next)
    setAnimationInputs({})
    setAnimationParams({})
    setImage(null)
    setViews({})
    setMeshScale('')
    setQuality('auto')
    setBackground('auto')
    setSteps('')
    setTextureSteps('')
    setGuidance('')
    setSeed('')
  }

  const restoreHistory = (id) => {
    selectEntry(id)
    const entry = entries.find(item => item.id === id)
    if (entry?.operation === 'animate') {
      setModel(entry.model)
      setAnimationInputs(entry.inputs || {})
      setAnimationParams(entry.params || {})
    }
  }

  const run = async () => {
    if (multiview && (['front', 'right', 'back', 'left'].some(name => !views[name]?.base64) || !Number.isFinite(Number(meshScale)) || Number(meshScale) <= 0)) {
      addToast('Provide all four RGBA PNG views and a positive mesh scale.', 'warning'); return
    }
    if (!animation && !multiview && !image?.base64) { addToast(t('threed.toasts.noImage'), 'warning'); return }
    if (!model) { addToast(t('threed.toasts.noModel'), 'warning'); return }

    setLoading(true)
    setResult(null)
    setRemeshState(null)
    setError(null)
    setLastId(null)

    const params = Object.fromEntries(Object.entries(animationParams).filter(([key, value]) => value !== '' && animation?.parameters.some(parameter => parameter.name === key)))
    const body = animation
      ? { model, inputs: animationInputs, params, response_format: 'url' }
      : multiview ? { model, images: ['front', 'right', 'back', 'left'].map(name => views[name].base64), mesh_scale: Number(meshScale), quality: '1024', response_format: 'url' }
      : { model, image: image.base64, quality, background, response_format: 'url' }
    if (!animation && !multiview) {
      if (steps) body.step = parseInt(steps)
      if (textureSteps) body.texture_steps = parseInt(textureSteps)
      if (guidance) body.cfg_scale = parseFloat(guidance)
      if (seed) body.seed = parseInt(seed)
    }

    // RequestPanel renders and copies its body. Keeping a multi-megabyte image
    // there duplicates the upload in React and can starve the result render.
    setLastRequest(animation ? body : multiview ? { ...body, images: ['front', 'right', 'back', 'left'].map(name => `<${name} RGBA PNG omitted>`) } : { ...body, image: `<base64 ${image.mime || 'image'} omitted>` })
    setRequestEndpoint(animation ? '/3d/animate' : '/3d/generations')

    try {
      const data = await (animation ? threeDApi.animate(body) : threeDApi.generate(body))
      const url = data?.data?.[0]?.url
      if (!url) {
        addToast(t('threed.toasts.noResults'), 'warning')
        return
      }
      const glbResp = await fetch(apiUrl(url))
      if (!glbResp.ok) throw new Error(`fetching the generated GLB failed: HTTP ${glbResp.status}`)
      const glb = await glbResp.blob()
      const name = url.split('/').pop()
      const outputType = animation?.output || 'mesh'
      setResult({ blob: glb, name, model, outputType })
      selectEntry(null)
      const inputThumb = !animation && image?.dataUrl ? await makeThumb(image.dataUrl) : null
      const saved = await addEntry({
        model,
        params: animation ? params : multiview ? { mesh_scale: Number(meshScale), quality: '1024' } : { quality, background, steps, textureSteps, guidance, seed },
        inputs: animation ? animationInputs : undefined,
        operation: animation ? 'animate' : 'generate',
        outputType,
        inputThumb,
        glb,
        name,
        parentId: handoff.from || undefined,
        edge: handoff.edge || undefined,
        label: handoffSource.item?.title || undefined,
      })
      setLastId(saved?.id || null)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  const handleRemesh = async () => {
    if (!source?.blob || !source.model) return
    if (showingRemesh) {
      setRemeshState(null)
      return
    }

    const sourceBlob = source.blob
    setRemeshLoading(true)
    setRemeshState({ sourceBlob, error: null })
    try {
      const blob = await threeDApi.remesh(sourceBlob, source.model, detail)
      setRemeshState({ sourceBlob, blob, name: remeshedName(source.name), error: null })
    } catch (err) {
      setRemeshState({ sourceBlob, error: err.message })
    } finally {
      setRemeshLoading(false)
    }
  }

  const handleRemeshDetail = (value) => {
    setRemeshSlider(value)
    if (source && remeshState?.sourceBlob === source.blob) setRemeshState(null)
  }

  const installed = ws.installed.byType.threed
  const noModel = !ws.installed.loading && !ws.installed.error && installed.length === 0
  const why = noModel ? t('studio.composer.whyModel', { type: t('studio.tabs.threed') })
    : (!animation && !multiview && !image?.base64) ? t('studio.workspace.threed.whyImage') : ''
  const f = (key) => t(`studio.workspace.fields.${key}`)
  const advancedSet = [steps && `${t('threed.labels.steps')} ${steps}`, textureSteps && `${t('threed.labels.textureSteps')} ${textureSteps}`, guidance && `${f('cfg')} ${guidance}`, seed && `${f('seed')} ${seed}`].filter(Boolean)
  const qualityOptions = QUALITIES.map(q => ({ value: q, label: t(`threed.labels.quality_${q}`) }))
  const backgroundOptions = BACKGROUNDS.map(b => ({ value: b, label: t(`threed.labels.background_${b}`) }))

  return (
    <Workspace type="threed">
      <ComposeCard
        ws={ws}
        icon="cube"
        title={t('threed.title')}
        lede={t('studio.workspace.lede.threed')}
        onSubmit={(e) => { e.preventDefault(); run() }}
        handoff={<HandoffNote source={handoffSource} handoff={handoff} wanted={wantsSource} onClear={() => setImage(null)} />}
        model={model}
        noModel={noModel ? { type: 'threed', label: t('studio.tabs.threed'), onChanged: ws.installed.refetch } : null}
        options={<>
          <ModelChip value={model} onChange={changeModel} options={modelNames} loading={modelsLoading} disabled={loading} capability={CAP_3D} />
          {!animation && !multiview && <>
            <ChipSelect label={t('threed.labels.quality')} value={quality} onChange={setQuality} options={qualityOptions} mono={false} testId="ws-quality" />
            <ChipSelect label={t('threed.labels.background')} value={background} onChange={setBackground} options={backgroundOptions} mono={false} testId="ws-background" />
          </>}
        </>}
        fold={animation || multiview ? null : (
          <Fold
            label={t('threed.labels.advanced')}
            summary={foldSummary(advancedSet, [t('threed.labels.steps'), t('threed.labels.textureSteps'), t('threed.labels.guidance'), t('threed.labels.seed')])}
            open={showAdvanced}
            onToggle={() => setShowAdvanced(v => !v)}
            id="threed-advanced-options"
          >
            <div className="ws-grid">
              <Field label={t('threed.labels.steps')} htmlFor="threed-steps"><input id="threed-steps" className="dk-input" type="number" min="1" value={steps} onChange={(e) => setSteps(e.target.value)} placeholder="12" /></Field>
              <Field label={t('threed.labels.textureSteps')} htmlFor="threed-texture-steps"><input id="threed-texture-steps" className="dk-input" type="number" min="1" value={textureSteps} onChange={(e) => setTextureSteps(e.target.value)} placeholder="12" /></Field>
              <Field label={t('threed.labels.guidance')} htmlFor="threed-guidance"><input id="threed-guidance" className="dk-input" type="number" step="0.1" value={guidance} onChange={(e) => setGuidance(e.target.value)} placeholder="7.5" /></Field>
              <Field label={t('threed.labels.seed')} htmlFor="threed-seed"><input id="threed-seed" className="dk-input" type="number" value={seed} onChange={(e) => setSeed(e.target.value)} placeholder={t('threed.labels.seedPlaceholder')} /></Field>
            </div>
          </Fold>
        )}
        submit={{ label: t('threed.actions.generate'), busyLabel: t('threed.actions.generating'), busy: loading, disabled: !model || noModel || (!animation && !multiview && !image?.base64), why, icon: 'cube' }}
      >
        {animation ? (
          <div className="ws-inputs ws-inputs--one">
            <AnimationOptions key={model} operation={animation} inputs={animationInputs} onInputsChange={setAnimationInputs} params={animationParams} onParamsChange={setAnimationParams} />
          </div>
        ) : multiview ? (
         <>
           <p className="form-hint">Upload four pre-matted RGBA PNGs of the same object in the canonical turntable framing. Resolution: 1024.</p>
           {['front', 'right', 'back', 'left'].map(name => (
             <MediaInput key={name} mode="image" label={`${name[0].toUpperCase()}${name.slice(1)} RGBA PNG`} value={views[name] || null}
               onChange={value => setViews(current => ({ ...current, [name]: value }))}
               onError={err => addToast(err.message, 'error')} maxBytes={MAX_3D_INPUT_BYTES} idPrefix={`threed-${name}`} />
           ))}
           <div className="form-group">
             <label className="form-label" htmlFor="threed-mesh-scale">Mesh scale</label>
             <input id="threed-mesh-scale" className="input" type="number" step="any" min="0" required value={meshScale} onChange={e => setMeshScale(e.target.value)} />
             <p className="form-hint">Enter the positive scale used to frame these views. Pixal3D does not estimate it.</p>
           </div>
         </>
        ) : (
          <div className="ws-inputs ws-inputs--one">
            <MediaInput
              mode="image"
              label={t('threed.labels.image')}
              value={image}
              onChange={setImage}
              onError={(err) => addToast(err.message, 'error')}
              maxBytes={MAX_3D_INPUT_BYTES}
              idPrefix="threed"
            />
          </div>
        )}
      </ComposeCard>

      <RunArea>
        {loading ? (
          <JobCard label={t('threed.actions.generating')} detail={[model, !animation && quality !== 'auto' ? quality : ''].filter(Boolean).join(' · ')} />
        ) : error ? (
          <FailedCard message={error} onRetry={run} />
        ) : active?.blob ? (
          <ResultCard
            ws={ws}
            item={item}
            title={item?.title || active.name || t('studio.work.untitled.threed')}
            meta={[source?.model, source?.params?.quality ? t(`threed.labels.quality_${source.params.quality}`, { defaultValue: source.params.quality }) : '']}
            download={{ href: downloadUrl, name: active.name || `3d-${model || 'model'}.glb`, testId: 'glb-download' }}
            onRerun={() => {}}
          >
            <div className="ws-viewer">
              {active.outputType === 'skeleton_animation' ? <AnimationViewer blob={active.blob} /> : <GlbViewer blob={active.blob} />}
              {canRemesh && <div className="threed-remesh-controls">
                <div className="threed-remesh-heading">
                  <span>{t('threed.remesh.title')}</span>
                  <output htmlFor="threed-remesh-detail">{detail.toFixed(2)}%</output>
                </div>
                <input
                  id="threed-remesh-detail"
                  type="range"
                  min="0"
                  max="100"
                  step="1"
                  value={remeshSlider}
                  onChange={(e) => handleRemeshDetail(e.target.value)}
                  disabled={remeshLoading}
                  aria-label={t('threed.remesh.detail')}
                />
                <div className="threed-remesh-scale" aria-hidden="true">
                  <span>{t('threed.remesh.coarser')}</span>
                  <span>{t('threed.remesh.finer')}</span>
                </div>
                <p className="form-hint">{t('threed.remesh.hint')}</p>
                <button
                  type="button"
                  className="dk-btn dk-btn--secondary dk-btn--sm"
                  onClick={handleRemesh}
                  disabled={remeshLoading}
                  data-testid="glb-remesh"
                >
                  {remeshLoading
                    ? <><LoadingSpinner size="sm" /> {t('threed.actions.remeshing')}</>
                    : showingRemesh
                      ? <><Icon name="undo" /> {t('threed.actions.showOriginal')}</>
                      : <><Icon name="boxes" /> {t('threed.actions.remesh')}</>}
                </button>
                {remeshError && <p className="form-error" role="alert">{remeshError}</p>}
                {showingRemesh && <p className="threed-remesh-ready">{t('threed.remesh.ready')}</p>}
              </div>}
            </div>
          </ResultCard>
        ) : (
          <EmptyRun icon="cube" text={t('threed.empty')} />
        )}
        <RequestPanel endpoint={requestEndpoint} body={lastRequest} />
      </RunArea>

      <ResultsStrip
        ws={ws}
        selectedId={selectedId}
        activeId={activeEntry?.id}
        onSelect={restoreHistory}
        onDelete={deleteEntry}
        onClear={clearAll}
      />
    </Workspace>
  )
}
