/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useRef, useCallback, useEffect, useMemo } from 'react'
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { modelsApi, backendsApi } from '../utils/api'
import { formatBytes } from '../utils/format'
import { useResources } from '../hooks/useResources'
import { detectSource, fitFor, importChecks, importRequest, machineFacts, requestText } from '../utils/tools'
import { createTransferRateSampler } from '../utils/transferRate'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import CodeEditor from '../components/CodeEditor'
import SearchableSelect from '../components/SearchableSelect'
import AmbiguityAlert from '../components/AmbiguityAlert'
import ModalityChips from '../components/ModalityChips'
import Icon from '../components/Icon'
import ToolSteps from '../components/tools/ToolSteps'
import Checks from '../components/tools/Checks'
import '../components/tools/tools.css'
import './import.css'

// Fallback list used when /backends/known fails — keeps the form usable
// with auto-detect only rather than showing an empty dropdown.
const BACKENDS_FALLBACK_EMPTY = []

// Modality keys used as i18n keys under "modality.*" namespace; resolved
// at render time inside `buildBackendOptions`.
const MODALITY_KEYS = ['text', 'asr', 'tts', 'image', 'video', '3d', '3d_animation', 'embeddings', 'reranker', 'detection', 'vad']

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

// Shapes to start from. Choosing one fills the field with its placeholder text.
const EXAMPLES = [
  'huggingface://owner/repo',
  'https://example.com/model.gguf',
  'file:///models/model.gguf',
  'oci://registry.example.com/model:tag',
  'ollama://llama3.2:3b',
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
  // The 'tools' namespace is loaded up front: the check list below uses it, and
  // loading it on the first keystroke would suspend the page and drop the focus.
  const { t } = useTranslation(['importModel', 'tools'])

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
  // The finished import: { name }. Shown as the last step instead of leaving the page.
  const [done, setDone] = useState(null)
  const { resources } = useResources(20000)
  const facts = useMemo(() => machineFacts(resources), [resources])
  const [installedNames, setInstalledNames] = useState([])
  // Full poll payload for the running job, not just its message: the endpoint
  // already reports progress, phase and byte counts, and the page used to
  // render only `message`.
  const [job, setJob] = useState(null)
  const transferRateRef = useRef(createTransferRateSampler())

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

  // The names already in use, for the name check. A failed read just leaves the
  // check out.
  useEffect(() => {
    let cancelled = false
    modelsApi.listV1()
      .then(data => {
        if (cancelled) return
        const list = Array.isArray(data?.data) ? data.data : Array.isArray(data) ? data : []
        setInstalledNames(list.map(m => m?.id || m?.name).filter(Boolean))
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [])

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
    transferRateRef.current.retain([jobId])
    pollRef.current = setInterval(async () => {
      try {
        const data = await modelsApi.getJobStatus(jobId)
        if (data.completed) {
          transferRateRef.current.reset(jobId)
          clearInterval(pollRef.current)
          pollRef.current = null
          setIsSubmitting(false)
          setJob(null)
          setDone({ name: data.gallery_element_name || '' })
          addToast(t('toasts.imported'), 'success')
          return
        }
        if (data.error || (data.message && data.message.startsWith('error:'))) {
          transferRateRef.current.reset(jobId)
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
        const currentBytes = data.current_bytes
        const totalBytes = data.total_bytes
        const metrics = transferRateRef.current.sample(jobId, currentBytes, totalBytes)
        setJob({ ...data, currentBytes, totalBytes, ...metrics })
      } catch (err) {
        console.error('Error polling job status:', err)
      }
    }, 1000)
  }, [addToast, t])

  const handleImport = useCallback(async (overrideBackend) => {
    if (!importUri.trim()) { addToast(t('toasts.noUri'), 'error'); return }
    setIsSubmitting(true)
    setEstimate(null)
    setDone(null)
    try {
      const request = importRequest(importUri, prefs, customPrefs, overrideBackend)
      const result = await modelsApi.importUri(request)

      const hasSize = result.estimated_size_display && result.estimated_size_display !== '0 B'
      const hasVram = result.estimated_vram_display && result.estimated_vram_display !== '0 B'
      if (hasSize || hasVram) {
        setEstimate({
          sizeDisplay: result.estimated_size_display || '',
          vramDisplay: result.estimated_vram_display || '',
          sizeBytes: Number(result.estimated_size_bytes) || 0,
          vramBytes: Number(result.estimated_vram_bytes) || 0,
        })
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
      navigate('/app/models?view=installed')
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
          {t('actions.browseHF')} <Icon name="external-link" />
        </a>
      </p>
    </div>
  )

  const renderOptions = () => (
    <div id="import-options-panel" data-testid="import-options-panel" className="import-options__grid">
      <div className="import-field import-field--wide">
        <span className="dk-label">{t('form.backend')}</span>
        <p className="dk-hint">{t('form.backendHint')}</p>
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
          <p className="dk-hint import-warn">{t('form.backendErrorHint')}</p>
        )}
        {(() => {
          if (!prefs.backend) return null
          const selected = backends.find(b => b.name === prefs.backend)
          if (!selected || selected.installed) return null
          return (
            <p data-testid="auto-install-note" className="dk-hint import-note">
              <Icon name="download" />
              {t('form.backendNotInstalled')}
            </p>
          )
        })()}
      </div>

      <div className="import-field">
        <label className="dk-label" htmlFor="import-name">{t('form.modelName')}</label>
        <input className="dk-input" id="import-name" type="text" value={prefs.name} onChange={e => updatePref('name', e.target.value)} placeholder={t('form.modelNamePlaceholder')} disabled={isSubmitting} />
        <p className="dk-hint">{t('form.modelNameHint')}</p>
      </div>

      {showQuantizations && (
        <div className="import-field">
          <label className="dk-label" htmlFor="import-quantizations">{t('form.quantizations')}</label>
          <input className="dk-input" id="import-quantizations" type="text" value={prefs.quantizations} onChange={e => updatePref('quantizations', e.target.value)} placeholder={t('form.quantizationsPlaceholder')} disabled={isSubmitting} />
          <p className="dk-hint">{t('form.quantizationsHint')}</p>
        </div>
      )}

      {showMmprojQuantizations && (
        <div className="import-field">
          <label className="dk-label" htmlFor="import-mmproj">{t('form.mmprojQuantizations')}</label>
          <input className="dk-input" id="import-mmproj" type="text" value={prefs.mmproj_quantizations} onChange={e => updatePref('mmproj_quantizations', e.target.value)} placeholder={t('form.mmprojQuantizationsPlaceholder')} disabled={isSubmitting} />
          <p className="dk-hint">{t('form.mmprojQuantizationsHint')}</p>
        </div>
      )}

      {showModelType && (
        <div className="import-field">
          <label className="dk-label" htmlFor="import-type">{t('form.modelType')}</label>
          <input className="dk-input" id="import-type" type="text" value={prefs.type} onChange={e => updatePref('type', e.target.value)} placeholder={t('form.modelTypePlaceholder')} disabled={isSubmitting} />
          <p className="dk-hint">{t('form.modelTypeHint')}</p>
        </div>
      )}

      {prefs.backend === 'diffusers' && (
        <>
          <div className="import-field">
            <label className="dk-label" htmlFor="import-pipeline">{t('form.pipelineType')}</label>
            <input className="dk-input" id="import-pipeline" type="text" value={prefs.pipeline_type} onChange={e => updatePref('pipeline_type', e.target.value)} placeholder="StableDiffusionPipeline" disabled={isSubmitting} />
            <p className="dk-hint">{t('form.pipelineTypeHint')}</p>
          </div>
          <div className="import-field">
            <label className="dk-label" htmlFor="import-scheduler">{t('form.schedulerType')}</label>
            <input className="dk-input" id="import-scheduler" type="text" value={prefs.scheduler_type} onChange={e => updatePref('scheduler_type', e.target.value)} placeholder={t('form.schedulerTypePlaceholder')} disabled={isSubmitting} />
            <p className="dk-hint">{t('form.schedulerTypeHint')}</p>
          </div>
          <div className="import-field">
            <label className="dk-label" htmlFor="import-enable-params">{t('form.enableParameters')}</label>
            <input className="dk-input" id="import-enable-params" type="text" value={prefs.enable_parameters} onChange={e => updatePref('enable_parameters', e.target.value)} placeholder={t('form.enableParametersPlaceholder')} disabled={isSubmitting} />
            <p className="dk-hint">{t('form.enableParametersHint')}</p>
          </div>
        </>
      )}

      <div className="import-field import-field--wide">
        <label className="dk-label" htmlFor="import-description">{t('form.description')}</label>
        <textarea className="dk-textarea" id="import-description" rows={2} value={prefs.description} onChange={e => updatePref('description', e.target.value)} placeholder={t('form.descriptionPlaceholder')} disabled={isSubmitting} />
        <p className="dk-hint">{t('form.descriptionHint')}</p>
      </div>

      <div className="import-field import-field--wide">
        <label className="dk-choice">
          <input className="dk-check" type="checkbox" checked={prefs.embeddings} onChange={e => updatePref('embeddings', e.target.checked)} disabled={isSubmitting} />
          <span>{t('form.embeddings')}</span>
        </label>
        <p className="dk-hint import-check__hint">{t('form.embeddingsHint')}</p>
        {prefs.backend === 'diffusers' && (
          <>
            <label className="dk-choice">
              <input className="dk-check" type="checkbox" checked={prefs.cuda} onChange={e => updatePref('cuda', e.target.checked)} disabled={isSubmitting} />
              <span>{t('form.cuda')}</span>
            </label>
            <p className="dk-hint import-check__hint">{t('form.cudaHint')}</p>
          </>
        )}
      </div>

      <div className="import-field import-field--wide">
        <div className="import-field__row">
          <span className="dk-label">{t('form.customPreferences')}</span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={addCustomPref} disabled={isSubmitting}>
            <Icon name="plus" /> {t('actions.addCustom')}
          </button>
        </div>
        <p className="dk-hint">{t('form.customKeyValueHint')}</p>
        {customPrefs.map((cp, i) => (
          <div key={i} className="import-custom-row">
            <input
              className="dk-input"
              type="text"
              value={cp.key}
              onChange={e => updateCustomPref(i, 'key', e.target.value)}
              placeholder={t('form.key')}
              aria-label={t('form.preferenceKey', { index: i + 1 })}
              disabled={isSubmitting}
            />
            <input
              className="dk-input"
              type="text"
              value={cp.value}
              onChange={e => updateCustomPref(i, 'value', e.target.value)}
              placeholder={t('form.value')}
              aria-label={t('form.preferenceValue', { index: i + 1 })}
              disabled={isSubmitting}
            />
            <button
              type="button"
              className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm"
              onClick={() => removeCustomPref(i)}
              disabled={isSubmitting}
              aria-label={t('form.removePref')}
            >
              <Icon name="trash" />
            </button>
          </div>
        ))}
      </div>
    </div>
  )

  // Everything the poller already returns and the old status card threw away.
  const progressPct = Number.isFinite(job?.progress) ? Math.round(job.progress) : null
  const jobName = job?.file_name || job?.gallery_element_name || ''
  const jobBytes = Number.isFinite(job?.currentBytes) && Number.isFinite(job?.totalBytes) && job.totalBytes > 0
    ? `${formatBytes(job.currentBytes)} / ${formatBytes(job.totalBytes)}`
    : (job?.downloaded_size && job?.file_size
        ? `${job.downloaded_size} / ${job.file_size}`
        : '')
  const jobRate = Number.isFinite(job?.bytesPerSecond) && job.bytesPerSecond > 0
    ? `${formatBytes(job.bytesPerSecond)}/s`
    : ''

  // What the source says about itself, from its spelling alone. Nothing has been
  // contacted: the importer reads the repository when the import starts.
  const source = useMemo(() => detectSource(importUri), [importUri])
  const request = useMemo(() => importRequest(importUri, prefs, customPrefs), [importUri, prefs, customPrefs])
  const checks = useMemo(
    () => importChecks({ source, prefs, backends, installedNames, facts }),
    [source, prefs, backends, installedNames, facts],
  )
  const chosenBackend = backends.find(b => b.name === prefs.backend)

  // Where the import is: nothing typed, reviewing, running, finished.
  let current = 0
  if (done) current = 3
  else if (isSubmitting || job) current = 2
  else if (source) current = 1
  const steps = [
    { key: 'source', label: t('steps.source') },
    { key: 'review', label: t('steps.review') },
    { key: 'import', label: t('steps.import') },
    { key: 'done', label: t('steps.done') },
  ]

  // How the estimate the server returned sits against this machine.
  const freeMemory = facts ? (facts.cluster ? facts.cluster.total : facts.hasGpu ? facts.vramFree : facts.ramFree) : null
  const memoryFit = estimate ? fitFor(estimate.vramBytes, freeMemory) : null
  const diskShort = estimate && facts?.diskFree != null && estimate.sizeBytes > facts.diskFree

  const resetForAnother = () => {
    setDone(null)
    setEstimate(null)
    setImportUri('')
    setPrefs(DEFAULT_PREFS)
    setCustomPrefs([])
  }

  return (
    <div className="page page--medium import-page bt-page">
      <PageHeader
        title={t('title')}
        supporting={isYaml ? t('subtitle.yaml') : t('subtitle.source')}
        actions={
          <div className="dk-segmented" role="tablist" aria-label={t('tabs.ariaLabel')} data-testid="import-tabs">
            <button
              type="button"
              role="tab"
              aria-selected={!isYaml}
              className={`dk-seg${!isYaml ? ' is-active' : ''}`}
              onClick={() => setTab('source')}
              data-testid="import-tab-source"
            >
              {t('tabs.source')}
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={isYaml}
              className={`dk-seg${isYaml ? ' is-active' : ''}`}
              onClick={() => setTab('yaml')}
              data-testid="import-tab-yaml"
            >
              {t('tabs.yaml')}
            </button>
          </div>
        }
      />

      {!isYaml && <ToolSteps steps={steps} current={current} label={t('steps.label')} />}

      {!isYaml && (
        <div className={`import-split${isSplit ? '' : ' import-split--stacked'}`}>
          <form
            className="import-main"
            data-testid="import-form"
            onSubmit={(e) => { e.preventDefault(); handleImport() }}
          >
            {/* The source field is the page. It is monospace because it holds
                something you paste rather than something you compose. The
                action sits in the bar at the foot of this form, which states
                what it will do. */}
            <section className="import-source dk-card">
              <label className="dk-label" htmlFor="import-source-input">{t('form.modelUri')}</label>
              <div className="import-source__bar">
                <input
                  id="import-source-input"
                  data-testid="import-source-input"
                  className="dk-input dk-input--mono"
                  type="text"
                  value={importUri}
                  onChange={(e) => setImportUri(e.target.value)}
                  placeholder={t('form.uriPlaceholder')}
                  disabled={isSubmitting}
                  spellCheck="false"
                  autoComplete="off"
                />
              </div>
              <p className="import-source__kind" data-testid="import-source-kind" data-kind={source?.kind || ''}>
                {source
                  ? <><span className={`dk-badge${source.ok ? ' dk-badge--accent' : ' dk-badge--warn'}`}>{t(`found.kind.${source.kind}`)}</span> <span className="dk-hint">{t('found.untouched')}</span></>
                  : <span className="dk-hint">{t('form.uriHint')}</span>}
              </p>
              {!source && (
                <div className="import-examples" data-testid="import-examples">
                  <span className="dk-label">{t('found.examples')}</span>
                  <div className="import-examples__chips">
                    {EXAMPLES.map(example => (
                      <button key={example} type="button" className="dk-chip dk-mono" onClick={() => setImportUri(example)} disabled={isSubmitting}>{example}</button>
                    ))}
                  </div>
                </div>
              )}

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
                    <Icon name={`chevron-${showFormats ? 'down' : 'right'}`} />
                    {t('form.supportedFormats')}
                  </button>
                  {showFormats && <div id="import-formats-panel">{renderFormats()}</div>}
                </>
              )}
            </section>

            {ambiguity && (
              <AmbiguityAlert
                modality={ambiguity.modality}
                candidates={ambiguity.candidates}
                knownBackends={backends}
                onPick={pickAmbiguityCandidate}
                onDismiss={() => setAmbiguity(null)}
              />
            )}

            {/* Size and memory answer for the field above them. They arrive when
                the import starts, because the server reads the repository then
                and not before. */}
            {estimate && (
              <div className="import-estimate" data-testid="import-estimate">
                <div className="import-estimate__cells">
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
                {memoryFit && (
                  <p className="import-estimate__fit" data-fit={memoryFit} data-testid="import-fit">
                    <Icon name={memoryFit === 'fits' ? 'check-circle' : 'warning'} /> {t(`estimate.fit.${memoryFit}`, { free: formatBytes(freeMemory) })}
                  </p>
                )}
                {diskShort && (
                  <p className="import-estimate__fit" data-fit="over" data-testid="import-disk">
                    <Icon name="warning" /> {t('estimate.diskShort', { free: formatBytes(facts.diskFree) })}
                  </p>
                )}
              </div>
            )}

            {job && (
              <div className="import-progress dk-card" data-testid="import-progress">
                <div className="import-progress__row">
                  <span className="import-progress__name">{jobName || t('progress.working')}</span>
                  {progressPct !== null && (
                    <span className="import-progress__pct">{progressPct}%</span>
                  )}
                </div>
                {progressPct !== null && (
                  <div
                    className="dk-progress"
                    role="progressbar"
                    aria-valuenow={progressPct}
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-label={t('progress.label')}
                    style={{ '--dk-value': `${progressPct}%` }}
                  >
                    <span className="dk-progress-bar" />
                  </div>
                )}
                <div className="import-progress__row">
                  <span className="import-progress__meta">
                    {job.phase || job.message || t('progress.working')}
                    {jobBytes && ` · ${jobBytes}`}
                    {jobRate && ` · ${jobRate}`}
                  </span>
                </div>
              </div>
            )}

            {done && (
              <section className="bt-next dk-card" data-testid="import-done">
                <span className="bt-next__mark"><Icon name="check-circle" /></span>
                <div>
                  <h2 className="bt-h2">{done.name ? t('done.titleNamed', { name: done.name }) : t('done.title')}</h2>
                  <p className="dk-hint">{t('done.text')}</p>
                  <div className="bt-next__acts">
                    {done.name && <Link className="dk-btn dk-btn--primary" to={`/app/chat/${encodeURIComponent(done.name)}`}><Icon name="chat" /> {t('done.chat', { name: done.name })}</Link>}
                    <Link className={`dk-btn ${done.name ? 'dk-btn--secondary' : 'dk-btn--primary'}`} to="/app/models?view=installed" data-testid="import-open-models"><Icon name="cube" /> {t('done.models')}</Link>
                    <button type="button" className="dk-btn dk-btn--ghost" onClick={resetForAnother}>{t('done.another')}</button>
                  </div>
                </div>
              </section>
            )}

            {source && !done && !job && !isSubmitting && (
              <>
                <section className="import-found dk-card" aria-labelledby="import-found-title" data-testid="import-found">
                  <h2 className="bt-h2" id="import-found-title">{t('found.title')}</h2>
                  <dl className="dk-kv import-found__facts">
                    <dt>{t(`found.ref.${source.kind}`)}</dt>
                    <dd className="dk-mono">{source.ref}</dd>
                    {source.file && <><dt>{t('found.file')}</dt><dd className="dk-mono">{source.file}</dd></>}
                    <dt>{t('found.backend')}</dt>
                    <dd className="import-found__text">
                      {prefs.backend
                        ? <><span className="dk-mono">{prefs.backend}</span> <span className="dk-hint">{chosenBackend && !chosenBackend.installed ? t('found.backendDownload') : t('found.backendChosen')}</span></>
                        : <span>{t('found.backendAuto')}</span>}
                    </dd>
                  </dl>
                  <div className="import-found__acts">
                    <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setShowOptions(true)} data-testid="import-adjust">
                      <Icon name="sliders" /> {t('found.adjust')}
                    </button>
                  </div>
                </section>

                <section className="import-preview" aria-labelledby="import-preview-title" data-testid="import-preview">
                  <header className="bt-block__head">
                    <h2 className="bt-h2" id="import-preview-title">{t('preview.title')}</h2>
                    <p className="dk-hint">{t('preview.note')}</p>
                  </header>
                  <pre className="import-preview__code dk-mono" data-testid="import-preview-code">{requestText(request)}</pre>
                </section>

                <section className="bt-block" aria-labelledby="import-checks-title">
                  <header className="bt-block__head">
                    <h2 className="bt-h2" id="import-checks-title">{t('checks.heading')}</h2>
                    <p className="dk-hint">{t('checks.note')}</p>
                  </header>
                  <div className="dk-card bt-checks-card">
                    <Checks checks={checks} isAdmin label={t('checks.heading')} testId="import-checks" />
                  </div>
                </section>
              </>
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
                <Icon name={`chevron-${showOptions ? 'down' : 'right'}`} />
                {t('form.options')}
                {!showOptions && <span className="import-options__summary">{t('form.optionsSummary')}</span>}
              </button>
              {showOptions && renderOptions()}
            </div>

            {!done && (
            <div className="bt-bar" data-testid="import-bar">
              <p className="bt-bar__text" role="status">
                {isSubmitting
                  ? t('bar.working')
                  : source
                    ? t('bar.ready', { ref: source.ref || importUri.trim() })
                    : t('bar.empty')}
              </p>
              <div className="bt-bar__acts">
                <button
                  type="submit"
                  className="dk-btn dk-btn--primary"
                  data-testid="import-submit"
                  disabled={isSubmitting || !importUri.trim()}
                  aria-busy={isSubmitting || undefined}
                >
                  {isSubmitting
                    ? <><LoadingSpinner size="sm" /> {t('actions.importing')}</>
                    : <><Icon name="import" /> {t('actions.import')}</>}
                </button>
              </div>
            </div>
            )}
          </form>

          {isSplit && <aside className="import-aside">{renderFormats()}</aside>}
        </div>
      )}

      {isYaml && (
        <div className="import-yaml" data-testid="import-yaml">
          <div className="import-yaml__head">
            <span className="dk-label">{t('form.yamlEditor')}</span>
            <div className="import-yaml__acts">
              <button
                type="button"
                className="dk-btn dk-btn--ghost dk-btn--sm"
                onClick={() => { navigator.clipboard.writeText(yamlContent); addToast(t('toasts.copied'), 'success') }}
              >
                <Icon name="copy" /> {t('actions.copy')}
              </button>
              <button
                type="button"
                className="dk-btn dk-btn--primary dk-btn--sm"
                data-testid="import-create"
                onClick={handleYamlCreate}
                disabled={isSubmitting}
                aria-busy={isSubmitting || undefined}
              >
                {isSubmitting
                  ? <><LoadingSpinner size="sm" /> {t('actions.saving')}</>
                  : <><Icon name="plus" /> {t('actions.create')}</>}
              </button>
            </div>
          </div>
          <p className="dk-hint">{t('form.yamlHint')}</p>
          <CodeEditor value={yamlContent} onChange={setYamlContent} disabled={isSubmitting} minHeight="calc(100vh - 380px)" />
        </div>
      )}
    </div>
  )
}
