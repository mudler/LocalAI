import { useState, useRef, useCallback, useEffect, useMemo } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { modelsApi, backendsApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import CodeEditor from '../components/CodeEditor'
import SearchableSelect from '../components/SearchableSelect'
import AmbiguityAlert from '../components/AmbiguityAlert'
import ModalityChips from '../components/ModalityChips'

// Fallback list used when /backends/known fails — keeps the form usable
// with auto-detect only rather than showing an empty dropdown.
const BACKENDS_FALLBACK_EMPTY = []

// Modality keys used as i18n keys under "modality.*" namespace; resolved
// at render time inside `buildBackendOptions`.
const MODALITY_KEYS = ['text', 'asr', 'tts', 'image', 'video', 'embeddings', 'reranker', 'detection', 'vad']

// buildBackendOptions groups known backends by modality and tags
// auto_detect=false entries with a muted "manual pick" badge so users
// understand auto-detect won't route to them. When modalityFilter is set
// the list is narrowed before grouping so the dropdown shows only
// backends the user asked about — grouping is preserved even if the
// result ends up being a single section.
function buildBackendOptions(list, modalityFilter, t) {
  if (!Array.isArray(list) || list.length === 0) return BACKENDS_FALLBACK_EMPTY
  const filtered = modalityFilter
    ? list.filter(b => b && b.modality === modalityFilter)
    : list
  if (filtered.length === 0) return BACKENDS_FALLBACK_EMPTY
  const groups = new Map()
  for (const b of filtered) {
    const key = b.modality || 'other'
    if (!groups.has(key)) groups.set(key, [])
    groups.get(key).push(b)
  }
  const keys = Array.from(groups.keys()).sort()
  const out = []
  for (const key of keys) {
    const label = MODALITY_KEYS.includes(key) ? t(`modality.${key}`) : t('modality.other')
    out.push({ value: `__header_${key}`, label, isHeader: true })
    const sorted = groups.get(key).slice().sort((a, b) => a.name.localeCompare(b.name))
    for (const b of sorted) {
      const opt = { value: b.name, label: b.name }
      if (b.auto_detect === false) {
        opt.badge = t('form.manualPick')
        opt.badgeTooltip = t('form.manualPickTooltip')
      }
      out.push(opt)
    }
  }
  return out
}

// URI_FORMATS drives the format reference. On a wide viewport it is a column
// beside the form rather than a disclosure: what a first-time admin needs to
// know is exactly which of these schemes to paste, and the answer used to be
// collapsed by default. Title + description strings are i18n keys.
const URI_FORMATS = [
  {
    titleKey: 'uriFormats.huggingface.title',
    examples: [
      { prefix: 'huggingface://', suffix: 'owner/repo' },
      { prefix: 'hf://', suffix: 'owner/repo' },
      { prefix: 'https://huggingface.co/', suffix: 'owner/repo' },
    ],
  },
  {
    titleKey: 'uriFormats.http.title',
    examples: [
      { prefix: 'https://', suffix: 'example.com/model.gguf' },
    ],
  },
  {
    titleKey: 'uriFormats.local.title',
    examples: [
      { prefix: 'file://', suffix: '/models/model.gguf' },
      { prefix: '', suffix: '/models/config.yaml' },
    ],
  },
  {
    titleKey: 'uriFormats.oci.title',
    examples: [
      { prefix: 'oci://', suffix: 'registry.example.com/model:tag' },
      { prefix: 'ocifile://', suffix: '/path/to/image.tar' },
    ],
  },
  {
    titleKey: 'uriFormats.ollama.title',
    examples: [
      { prefix: 'ollama://', suffix: 'llama2:7b' },
    ],
  },
]

const DEFAULT_YAML = `name: my-model
backend: llama-cpp
parameters:
  model: /path/to/model.gguf
`

const DEFAULT_PREFS = {
  backend: '', name: '', description: '', quantizations: '',
  mmproj_quantizations: '', embeddings: false, type: '',
  pipeline_type: '', scheduler_type: '', enable_parameters: '', cuda: false,
}

// Below this width the format reference cannot hold its own column, so it
// becomes a disclosure under the field instead. Matches --bp-tablet minus the
// sidebar; kept in JS because the two renderings differ structurally, not just
// visually, and a media query cannot swap a column for a disclosure.
const SPLIT_MIN_WIDTH = 1024

export default function ImportModel() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('importModel')

  // Which kind of input the user is giving: a source to resolve, or a YAML
  // document to write. These are genuinely different inputs, unlike the
  // Simple/Power modes they replace, which were the same form at two
  // different lengths.
  const [tab, setTab] = useState(() => {
    try { return localStorage.getItem('import-form-tab') === 'yaml' ? 'yaml' : 'source' } catch { return 'source' }
  })
  const [showOptions, setShowOptions] = useState(() => {
    try { return localStorage.getItem('import-form-options') === 'open' } catch { return false }
  })

  const [importUri, setImportUri] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [yamlContent, setYamlContent] = useState(DEFAULT_YAML)
  const [estimate, setEstimate] = useState(null)
  // Full poll payload for the running job, not just its message: the endpoint
  // already reports progress, phase and byte counts, and the page used to
  // render only `message`.
  const [job, setJob] = useState(null)

  const [prefs, setPrefs] = useState(DEFAULT_PREFS)
  const [customPrefs, setCustomPrefs] = useState([])
  // ambiguity state: { modality, candidates } when the server returns 400
  // with a structured ambiguity body. Cleared on pick, dismiss, URI change,
  // or a manual backend pick.
  const [ambiguity, setAmbiguity] = useState(null)
  // modalityFilter narrows the Backend dropdown to entries whose modality
  // matches. Empty string means "Any" — no filter. Auto-populated when
  // the server returns an ambiguity alert so the dropdown is already
  // scoped if the user dismisses the alert and browses manually.
  const [modalityFilter, setModalityFilter] = useState('')

  const [backends, setBackends] = useState([])
  const [backendsLoading, setBackendsLoading] = useState(true)
  const [backendsError, setBackendsError] = useState(false)

  // Wide enough for the reference to sit beside the form. Tracked in state
  // rather than read at render so a resize re-renders the page.
  const [isSplit, setIsSplit] = useState(() => (
    typeof window === 'undefined' ? true : window.innerWidth >= SPLIT_MIN_WIDTH
  ))
  const [showFormats, setShowFormats] = useState(false)

  const pollRef = useRef(null)

  useEffect(() => {
    return () => { if (pollRef.current) clearInterval(pollRef.current) }
  }, [])

  useEffect(() => {
    const onResize = () => setIsSplit(window.innerWidth >= SPLIT_MIN_WIDTH)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  useEffect(() => {
    try { localStorage.setItem('import-form-tab', tab) } catch { /* ignore quota / privacy mode */ }
  }, [tab])

  useEffect(() => {
    try { localStorage.setItem('import-form-options', showOptions ? 'open' : 'closed') } catch { /* ignore */ }
  }, [showOptions])

  useEffect(() => {
    let cancelled = false
    setBackendsLoading(true)
    setBackendsError(false)
    backendsApi.listKnown()
      .then(data => {
        if (cancelled) return
        setBackends(Array.isArray(data) ? data : [])
      })
      .catch(err => {
        if (cancelled) return
        console.error('Failed to load /backends/known:', err)
        setBackendsError(true)
        setBackends([])
        addToast(t('toasts.backendsLoadFailed'), 'warning')
      })
      .finally(() => {
        if (!cancelled) setBackendsLoading(false)
      })
    return () => { cancelled = true }
  }, [addToast, t])

  const backendOptions = useMemo(
    () => buildBackendOptions(backends, modalityFilter, t),
    [backends, modalityFilter, t]
  )

  // Progressive disclosure — hide preference fields that don't apply to the
  // currently selected backend. When the backend is unset we keep everything
  // visible so users exploring the form can see the full menu. Hidden
  // fields' state is preserved (we guard the JSX, not the state) so a user
  // flipping backends back and forth doesn't lose input.
  const showQuantizations = useMemo(() => {
    if (!prefs.backend) return true
    return ['llama-cpp', 'ik-llama-cpp', 'turboquant', 'stablediffusion-ggml'].includes(prefs.backend)
  }, [prefs.backend])
  const showMmprojQuantizations = useMemo(() => {
    if (!prefs.backend) return true
    return ['llama-cpp', 'ik-llama-cpp', 'turboquant'].includes(prefs.backend)
  }, [prefs.backend])
  const showModelType = useMemo(() => {
    if (!prefs.backend) return true
    return ['transformers', 'sentencetransformers', 'rerankers', 'rfdetr'].includes(prefs.backend)
  }, [prefs.backend])

  const updatePref = (key, value) => setPrefs(p => ({ ...p, [key]: value }))
  const addCustomPref = () => setCustomPrefs(p => [...p, { key: '', value: '' }])
  const removeCustomPref = (i) => setCustomPrefs(p => p.filter((_, idx) => idx !== i))
  const updateCustomPref = (i, field, value) => {
    setCustomPrefs(p => p.map((item, idx) => idx === i ? { ...item, [field]: value } : item))
  }

  const startJobPolling = useCallback((jobId) => {
    if (pollRef.current) clearInterval(pollRef.current)
    pollRef.current = setInterval(async () => {
      try {
        const data = await modelsApi.getJobStatus(jobId)
        if (data.completed) {
          clearInterval(pollRef.current)
          pollRef.current = null
          setIsSubmitting(false)
          setJob(null)
          addToast(t('toasts.imported'), 'success')
          navigate('/app/manage')
          return
        }
        if (data.error || (data.message && data.message.startsWith('error:'))) {
          clearInterval(pollRef.current)
          pollRef.current = null
          setIsSubmitting(false)
          setJob(null)
          let msg = 'Unknown error'
          if (typeof data.error === 'string') msg = data.error
          else if (data.error?.message) msg = data.error.message
          else if (data.message) msg = data.message
          if (msg.startsWith('error: ')) msg = msg.substring(7)
          addToast(t('toasts.importFailed', { message: msg }), 'error')
          return
        }
        // Keep the whole status. /api/operations carries the same job (the
        // import endpoint registers it in the opcache) but drops it the moment
        // it finishes, which is indistinguishable from a cancel — so terminal
        // detection stays on this endpoint and only the rendering gets richer.
        setJob(data)
      } catch (err) {
        console.error('Error polling job status:', err)
      }
    }, 1000)
  }, [addToast, navigate, t])

  const handleImport = useCallback(async (overrideBackend) => {
    if (!importUri.trim()) { addToast(t('toasts.noUri'), 'error'); return }
    setIsSubmitting(true)
    setEstimate(null)
    try {
      const prefsObj = {}
      const effectiveBackend = overrideBackend !== undefined ? overrideBackend : prefs.backend
      if (effectiveBackend) prefsObj.backend = effectiveBackend
      if (prefs.name.trim()) prefsObj.name = prefs.name.trim()
      if (prefs.description.trim()) prefsObj.description = prefs.description.trim()
      if (prefs.quantizations.trim()) prefsObj.quantizations = prefs.quantizations.trim()
      if (prefs.mmproj_quantizations.trim()) prefsObj.mmproj_quantizations = prefs.mmproj_quantizations.trim()
      if (prefs.embeddings) prefsObj.embeddings = 'true'
      if (prefs.type.trim()) prefsObj.type = prefs.type.trim()
      if (prefs.pipeline_type.trim()) prefsObj.pipeline_type = prefs.pipeline_type.trim()
      if (prefs.scheduler_type.trim()) prefsObj.scheduler_type = prefs.scheduler_type.trim()
      if (prefs.enable_parameters.trim()) prefsObj.enable_parameters = prefs.enable_parameters.trim()
      if (prefs.cuda) prefsObj.cuda = true
      customPrefs.forEach(cp => {
        if (cp.key.trim() && cp.value.trim()) prefsObj[cp.key.trim()] = cp.value.trim()
      })

      const result = await modelsApi.importUri({
        uri: importUri.trim(),
        preferences: Object.keys(prefsObj).length > 0 ? prefsObj : null,
      })

      const hasSize = result.estimated_size_display && result.estimated_size_display !== '0 B'
      const hasVram = result.estimated_vram_display && result.estimated_vram_display !== '0 B'
      if (hasSize || hasVram) {
        setEstimate({ sizeDisplay: result.estimated_size_display || '', vramDisplay: result.estimated_vram_display || '' })
      }

      const jobId = result.uuid || result.ID
      if (!jobId) throw new Error('No job ID returned from server')

      addToast(t('toasts.started'), 'success')
      // Clear any prior ambiguity alert once the server accepts the import.
      setAmbiguity(null)
      startJobPolling(jobId)
    } catch (err) {
      // Structured ambiguity response — render the inline picker instead of
      // a toast. The server returns HTTP 400 with { error, modality,
      // candidates } which api.handleResponse attaches as err.body.
      if (err?.status === 400 && err?.body && err.body.error === 'ambiguous import') {
        setAmbiguity({
          modality: err.body.modality || '',
          candidates: Array.isArray(err.body.candidates) ? err.body.candidates : [],
        })
        setIsSubmitting(false)
        return
      }
      addToast(t('toasts.startImportFailed', { message: err.message }), 'error')
      setIsSubmitting(false)
    }
  }, [importUri, prefs, customPrefs, addToast, startJobPolling, t])

  const pickAmbiguityCandidate = useCallback((backend) => {
    setPrefs(p => ({ ...p, backend }))
    setAmbiguity(null)
    // Resubmit immediately so the user only has to click the chip once.
    // Pass the picked backend as an override — setPrefs is async so
    // handleImport would otherwise see the stale prefs.backend.
    handleImport(backend)
  }, [handleImport])

  // Clear stale ambiguity alerts when the URI changes (fresh attempt) or
  // the user picks a backend manually — in both cases the alert's context
  // no longer applies.
  useEffect(() => { setAmbiguity(null) }, [importUri])
  useEffect(() => {
    if (prefs.backend) setAmbiguity(null)
  }, [prefs.backend])

  // Auto-activate the matching modality chip whenever an ambiguity alert
  // fires. The server already told us which modality it detected, so the
  // dropdown should scope itself even if the user dismisses the alert and
  // browses manually.
  useEffect(() => {
    if (ambiguity && ambiguity.modality) {
      setModalityFilter(ambiguity.modality)
    }
  }, [ambiguity])

  // handleModalityChange drops a mismatched backend selection when the
  // user narrows the filter so the dropdown doesn't display a selection
  // that can no longer be found inside the list. A toast explains the
  // auto-clear so the change is visible.
  const handleModalityChange = useCallback((next) => {
    setModalityFilter(next)
    if (!next) return
    const selected = backends.find(b => b.name === prefs.backend)
    if (selected && selected.modality !== next) {
      setPrefs(p => ({ ...p, backend: '' }))
      const label = MODALITY_KEYS.includes(next) ? t(`modality.${next}`) : next
      addToast(t('toasts.modalityClearedBackend', { label }), 'info')
    }
  }, [backends, prefs.backend, addToast, t])

  const handleYamlCreate = async () => {
    if (!yamlContent.trim()) { addToast(t('toasts.noYaml'), 'error'); return }
    setIsSubmitting(true)
    try {
      await modelsApi.importConfig(yamlContent, 'application/x-yaml')
      addToast(t('toasts.importedYaml'), 'success')
      navigate('/app/manage')
    } catch (err) {
      addToast(t('toasts.importFailed', { message: err.message }), 'error')
    } finally {
      setIsSubmitting(false)
    }
  }

  const isYaml = tab === 'yaml'

  // The format reference. Rendered as a sibling column when there is room and
  // as a disclosure when there is not, so nothing is amputated on a narrow
  // window — only re-housed.
  const renderFormats = () => (
    <div className="import-formats" data-testid="import-formats">
      <p className="import-formats__head">{t('form.supportedFormats')}</p>
      <ul className="import-formats__list">
        {URI_FORMATS.map((fmt) => (
          <li key={fmt.titleKey} className="import-formats__row">
            <span className="import-formats__kind">{t(fmt.titleKey)}</span>
            {fmt.examples.map((ex, j) => (
              <span key={j} className="import-formats__uri">
                {ex.prefix && <b>{ex.prefix}</b>}
                <em>{ex.suffix}</em>
              </span>
            ))}
          </li>
        ))}
      </ul>
      <p className="import-formats__foot">
        <a href="https://huggingface.co/models?sort=trending" target="_blank" rel="noreferrer">
          {t('actions.browseHF')} <i className="fas fa-external-link-alt" aria-hidden="true" />
        </a>
      </p>
    </div>
  )

  const renderOptions = () => (
    <div id="import-options-panel" data-testid="import-options-panel" className="import-options__grid">
      <div className="import-field import-field--wide">
        <span className="form-label">{t('form.backend')}</span>
        <p className="form-hint-sm import-field__lead">{t('form.backendHint')}</p>
        <ModalityChips
          value={modalityFilter}
          onChange={handleModalityChange}
          disabled={isSubmitting || backendsLoading}
        />
        <SearchableSelect
          value={prefs.backend}
          onChange={(v) => updatePref('backend', v)}
          options={backendOptions}
          allOption={t('form.backendAuto')}
          placeholder={backendsLoading ? t('form.backendLoading') : t('form.backendAuto')}
          searchPlaceholder={t('form.backendSearch')}
          disabled={isSubmitting || backendsLoading}
        />
        {backendsError && (
          <p className="form-hint-sm text-warning">{t('form.backendErrorHint')}</p>
        )}
        {(() => {
          if (!prefs.backend) return null
          const selected = backends.find(b => b.name === prefs.backend)
          if (!selected || selected.installed) return null
          return (
            <p data-testid="auto-install-note" className="form-hint-sm hstack hstack--xs">
              <i className="fas fa-download" aria-hidden="true" />
              {t('form.backendNotInstalled')}
            </p>
          )
        })()}
      </div>

      <div className="import-field">
        <label className="form-label" htmlFor="import-name">{t('form.modelName')}</label>
        <input className="input" id="import-name" type="text" value={prefs.name} onChange={e => updatePref('name', e.target.value)} placeholder={t('form.modelNamePlaceholder')} disabled={isSubmitting} />
        <p className="form-hint-sm">{t('form.modelNameHint')}</p>
      </div>

      {showQuantizations && (
        <div className="import-field">
          <label className="form-label" htmlFor="import-quantizations">{t('form.quantizations')}</label>
          <input className="input" id="import-quantizations" type="text" value={prefs.quantizations} onChange={e => updatePref('quantizations', e.target.value)} placeholder={t('form.quantizationsPlaceholder')} disabled={isSubmitting} />
          <p className="form-hint-sm">{t('form.quantizationsHint')}</p>
        </div>
      )}

      {showMmprojQuantizations && (
        <div className="import-field">
          <label className="form-label" htmlFor="import-mmproj">{t('form.mmprojQuantizations')}</label>
          <input className="input" id="import-mmproj" type="text" value={prefs.mmproj_quantizations} onChange={e => updatePref('mmproj_quantizations', e.target.value)} placeholder={t('form.mmprojQuantizationsPlaceholder')} disabled={isSubmitting} />
          <p className="form-hint-sm">{t('form.mmprojQuantizationsHint')}</p>
        </div>
      )}

      {showModelType && (
        <div className="import-field">
          <label className="form-label" htmlFor="import-type">{t('form.modelType')}</label>
          <input className="input" id="import-type" type="text" value={prefs.type} onChange={e => updatePref('type', e.target.value)} placeholder={t('form.modelTypePlaceholder')} disabled={isSubmitting} />
          <p className="form-hint-sm">{t('form.modelTypeHint')}</p>
        </div>
      )}

      {prefs.backend === 'diffusers' && (
        <>
          <div className="import-field">
            <label className="form-label" htmlFor="import-pipeline">{t('form.pipelineType')}</label>
            <input className="input" id="import-pipeline" type="text" value={prefs.pipeline_type} onChange={e => updatePref('pipeline_type', e.target.value)} placeholder="StableDiffusionPipeline" disabled={isSubmitting} />
            <p className="form-hint-sm">{t('form.pipelineTypeHint')}</p>
          </div>
          <div className="import-field">
            <label className="form-label" htmlFor="import-scheduler">{t('form.schedulerType')}</label>
            <input className="input" id="import-scheduler" type="text" value={prefs.scheduler_type} onChange={e => updatePref('scheduler_type', e.target.value)} placeholder={t('form.schedulerTypePlaceholder')} disabled={isSubmitting} />
            <p className="form-hint-sm">{t('form.schedulerTypeHint')}</p>
          </div>
          <div className="import-field">
            <label className="form-label" htmlFor="import-enable-params">{t('form.enableParameters')}</label>
            <input className="input" id="import-enable-params" type="text" value={prefs.enable_parameters} onChange={e => updatePref('enable_parameters', e.target.value)} placeholder={t('form.enableParametersPlaceholder')} disabled={isSubmitting} />
            <p className="form-hint-sm">{t('form.enableParametersHint')}</p>
          </div>
        </>
      )}

      <div className="import-field import-field--wide">
        <label className="form-label" htmlFor="import-description">{t('form.description')}</label>
        <textarea className="textarea" id="import-description" rows={2} value={prefs.description} onChange={e => updatePref('description', e.target.value)} placeholder={t('form.descriptionPlaceholder')} disabled={isSubmitting} />
        <p className="form-hint-sm">{t('form.descriptionHint')}</p>
      </div>

      <div className="import-field import-field--wide">
        <label className="import-check">
          <input type="checkbox" checked={prefs.embeddings} onChange={e => updatePref('embeddings', e.target.checked)} disabled={isSubmitting} />
          <span>{t('form.embeddings')}</span>
        </label>
        <p className="form-hint-sm import-check__hint">{t('form.embeddingsHint')}</p>
        {prefs.backend === 'diffusers' && (
          <>
            <label className="import-check">
              <input type="checkbox" checked={prefs.cuda} onChange={e => updatePref('cuda', e.target.checked)} disabled={isSubmitting} />
              <span>{t('form.cuda')}</span>
            </label>
            <p className="form-hint-sm import-check__hint">{t('form.cudaHint')}</p>
          </>
        )}
      </div>

      <div className="import-field import-field--wide">
        <div className="hstack hstack--between">
          <span className="form-label">{t('form.customPreferences')}</span>
          <button type="button" className="btn btn-secondary btn-sm" onClick={addCustomPref} disabled={isSubmitting}>
            <i className="fas fa-plus" aria-hidden="true" /> {t('actions.addCustom')}
          </button>
        </div>
        <p className="form-hint-sm import-field__lead">{t('form.customKeyValueHint')}</p>
        {customPrefs.map((cp, i) => (
          <div key={i} className="import-custom-row">
            <input
              className="input"
              type="text"
              value={cp.key}
              onChange={e => updateCustomPref(i, 'key', e.target.value)}
              placeholder={t('form.key')}
              aria-label={t('form.preferenceKey', { index: i + 1 })}
              disabled={isSubmitting}
            />
            <input
              className="input"
              type="text"
              value={cp.value}
              onChange={e => updateCustomPref(i, 'value', e.target.value)}
              placeholder={t('form.value')}
              aria-label={t('form.preferenceValue', { index: i + 1 })}
              disabled={isSubmitting}
            />
            <button
              type="button"
              className="btn btn-secondary btn-sm text-error"
              onClick={() => removeCustomPref(i)}
              disabled={isSubmitting}
              aria-label={t('form.removePref')}
            >
              <i className="fas fa-trash" aria-hidden="true" />
            </button>
          </div>
        ))}
      </div>
    </div>
  )

  // Everything the poller already returns and the old status card threw away.
  const progressPct = Number.isFinite(job?.progress) ? Math.round(job.progress) : null
  const jobName = job?.file_name || job?.gallery_element_name || ''
  const jobBytes = job?.downloaded_size && job?.file_size
    ? `${job.downloaded_size} / ${job.file_size}`
    : ''

  return (
    <div className="page page--medium import-page">
      <PageHeader
        title={t('title')}
        supporting={isYaml ? t('subtitle.yaml') : t('subtitle.source')}
        actions={
          <div className="segmented mb-0" role="tablist" aria-label={t('tabs.ariaLabel')} data-testid="import-tabs">
            <button
              type="button"
              role="tab"
              aria-selected={!isYaml}
              className={`segmented__item${!isYaml ? ' is-active' : ''}`}
              onClick={() => setTab('source')}
              data-testid="import-tab-source"
            >
              <i className="fas fa-link" aria-hidden="true" />
              {t('tabs.source')}
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={isYaml}
              className={`segmented__item${isYaml ? ' is-active' : ''}`}
              onClick={() => setTab('yaml')}
              data-testid="import-tab-yaml"
            >
              <i className="fas fa-code" aria-hidden="true" />
              {t('tabs.yaml')}
            </button>
          </div>
        }
      />

      {!isYaml && (
        <div className={`import-split${isSplit ? '' : ' import-split--stacked'}`}>
          <form
            className="import-main"
            data-testid="import-form"
            onSubmit={(e) => { e.preventDefault(); handleImport() }}
          >
            {/* The source field is the page. It is monospace because it holds
                something you paste rather than something you compose, and it
                carries its own commit button — the action used to live in the
                page header, outside the form, with a hidden submit button
                standing in so Enter still worked. */}
            <div className="import-source">
              <label className="form-label" htmlFor="import-source-input">{t('form.modelUri')}</label>
              <div className="import-source__bar">
                <input
                  id="import-source-input"
                  data-testid="import-source-input"
                  className="import-source__input"
                  type="text"
                  value={importUri}
                  onChange={(e) => setImportUri(e.target.value)}
                  placeholder={t('form.uriPlaceholder')}
                  disabled={isSubmitting}
                  spellCheck="false"
                  autoComplete="off"
                />
                <button
                  type="submit"
                  className="btn btn-primary import-source__btn"
                  data-testid="import-submit"
                  disabled={isSubmitting || !importUri.trim()}
                >
                  {isSubmitting
                    ? <><LoadingSpinner size="sm" /> {t('actions.importing')}</>
                    : <><i className="fas fa-file-import" aria-hidden="true" /> {t('actions.import')}</>}
                </button>
              </div>
              <p className="form-hint-sm">{t('form.uriHint')}</p>

              {!isSplit && (
                <>
                  <button
                    type="button"
                    className="import-disclosure"
                    data-testid="import-formats-toggle"
                    aria-expanded={showFormats}
                    aria-controls="import-formats-panel"
                    onClick={() => setShowFormats(v => !v)}
                  >
                    <i className={`fas fa-chevron-${showFormats ? 'down' : 'right'}`} aria-hidden="true" />
                    {t('form.supportedFormats')}
                  </button>
                  {showFormats && <div id="import-formats-panel">{renderFormats()}</div>}
                </>
              )}
            </div>

            {ambiguity && (
              <AmbiguityAlert
                modality={ambiguity.modality}
                candidates={ambiguity.candidates}
                knownBackends={backends}
                onPick={pickAmbiguityCandidate}
                onDismiss={() => setAmbiguity(null)}
              />
            )}

            {/* Size and VRAM answer for the field above them. They used to be a
                banner pinned above the page header, furthest from the control
                that produced them. */}
            {estimate && (
              <div className="import-estimate" data-testid="import-estimate">
                {estimate.sizeDisplay && estimate.sizeDisplay !== '0 B' && (
                  <span className="import-estimate__cell">
                    <span className="import-estimate__k">{t('estimate.downloadLabel')}</span>
                    <span className="import-estimate__v">{estimate.sizeDisplay}</span>
                  </span>
                )}
                {estimate.vramDisplay && estimate.vramDisplay !== '0 B' && (
                  <span className="import-estimate__cell">
                    <span className="import-estimate__k">{t('estimate.vramLabel')}</span>
                    <span className="import-estimate__v">{estimate.vramDisplay}</span>
                  </span>
                )}
              </div>
            )}

            {job && (
              <div className="import-progress" data-testid="import-progress">
                <div className="import-progress__row">
                  <span className="import-progress__name">{jobName || t('progress.working')}</span>
                  {progressPct !== null && (
                    <span className="import-progress__pct">{progressPct}%</span>
                  )}
                </div>
                {progressPct !== null && (
                  <div
                    className="import-progress__track"
                    role="progressbar"
                    aria-valuenow={progressPct}
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-label={t('progress.label')}
                  >
                    {/* Runtime percentage — the one width a stylesheet cannot know. */}
                    <span className="import-progress__fill" style={{ width: `${progressPct}%` }} />
                  </div>
                )}
                <div className="import-progress__row">
                  <span className="import-progress__meta">
                    {job.phase || job.message || t('progress.working')}
                    {jobBytes && ` · ${jobBytes}`}
                  </span>
                </div>
              </div>
            )}

            <div className="import-options">
              <button
                type="button"
                className="import-disclosure"
                data-testid="import-options-toggle"
                aria-expanded={showOptions}
                aria-controls="import-options-panel"
                onClick={() => setShowOptions(v => !v)}
              >
                <i className={`fas fa-chevron-${showOptions ? 'down' : 'right'}`} aria-hidden="true" />
                {t('form.options')}
                {!showOptions && <span className="import-options__summary">{t('form.optionsSummary')}</span>}
              </button>
              {showOptions && renderOptions()}
            </div>
          </form>

          {isSplit && <aside className="import-aside">{renderFormats()}</aside>}
        </div>
      )}

      {isYaml && (
        <div className="import-yaml" data-testid="import-yaml">
          <div className="import-yaml__head">
            <span className="form-label mb-0">{t('form.yamlEditor')}</span>
            <div className="hstack">
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={() => { navigator.clipboard.writeText(yamlContent); addToast(t('toasts.copied'), 'success') }}
              >
                <i className="fas fa-copy" aria-hidden="true" /> {t('actions.copy')}
              </button>
              <button
                type="button"
                className="btn btn-primary btn-sm"
                data-testid="import-create"
                onClick={handleYamlCreate}
                disabled={isSubmitting}
              >
                {isSubmitting
                  ? <><LoadingSpinner size="sm" /> {t('actions.saving')}</>
                  : <><i className="fas fa-plus" aria-hidden="true" /> {t('actions.create')}</>}
              </button>
            </div>
          </div>
          <CodeEditor value={yamlContent} onChange={setYamlContent} disabled={isSubmitting} minHeight="calc(100vh - 320px)" />
        </div>
      )}
    </div>
  )
}
