import { useState, useRef, useCallback, useEffect, useMemo } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { modelsApi, backendsApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import CodeEditor from '../components/CodeEditor'
import SearchableSelect from '../components/SearchableSelect'
import AmbiguityAlert from '../components/AmbiguityAlert'
import SimplePowerSwitch from '../components/SimplePowerSwitch'
import ModalityChips from '../components/ModalityChips'

// Fallback list used when /backends/known fails — keeps the form usable
// with auto-detect only rather than showing an empty dropdown.
const BACKENDS_FALLBACK_EMPTY = []

// Modality keys used as i18n keys under "modality.*" namespace; resolved
// at render time inside `buildBackendOptions`.
const MODALITY_KEYS = ['text', 'asr', 'tts', 'image', 'embeddings', 'reranker', 'detection', 'vad']

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
    const label = MODALITY_KEYS.includes(key) ? t(`modality.${key}`) : (key ? t('modality.other') : t('modality.other'))
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

// URI_FORMATS describes the example list rendered in the format guide.
// Title + description strings are i18n keys, resolved at render time.
const URI_FORMATS = [
  {
    icon: 'fab fa-hubspot', color: 'var(--color-accent)', titleKey: 'uriFormats.huggingface.title',
    examples: [
      { prefix: 'huggingface://', suffix: 'TheBloke/Llama-2-7B-Chat-GGUF', descKey: 'uriFormats.huggingface.standard' },
      { prefix: 'hf://', suffix: 'TheBloke/Llama-2-7B-Chat-GGUF', descKey: 'uriFormats.huggingface.short' },
      { prefix: 'https://huggingface.co/', suffix: 'TheBloke/Llama-2-7B-Chat-GGUF', descKey: 'uriFormats.huggingface.fullUrl' },
    ],
  },
  {
    icon: 'fas fa-globe', color: 'var(--color-primary)', titleKey: 'uriFormats.http.title',
    examples: [
      { prefix: 'https://', suffix: 'example.com/model.gguf', descKey: 'uriFormats.http.direct' },
    ],
  },
  {
    icon: 'fas fa-file', color: 'var(--color-warning)', titleKey: 'uriFormats.local.title',
    examples: [
      { prefix: 'file://', suffix: '/path/to/model.gguf', descKey: 'uriFormats.local.filePath' },
      { prefix: '', suffix: '/path/to/model.yaml', descKey: 'uriFormats.local.directYaml' },
    ],
  },
  {
    icon: 'fas fa-box', color: 'var(--color-data-8)', titleKey: 'uriFormats.oci.title',
    examples: [
      { prefix: 'oci://', suffix: 'registry.example.com/model:tag', descKey: 'uriFormats.oci.registry' },
      { prefix: 'ocifile://', suffix: '/path/to/image.tar', descKey: 'uriFormats.oci.tarball' },
    ],
  },
  {
    icon: 'fas fa-cube', color: 'var(--color-data-1)', titleKey: 'uriFormats.ollama.title',
    examples: [
      { prefix: 'ollama://', suffix: 'llama2:7b', descKey: 'uriFormats.ollama.model' },
    ],
  },
  {
    icon: 'fas fa-code', color: 'var(--color-data-7)', titleKey: 'uriFormats.yaml.title',
    examples: [
      { prefix: '', suffix: 'https://example.com/model.yaml', descKey: 'uriFormats.yaml.remote' },
      { prefix: 'file://', suffix: '/path/to/config.yaml', descKey: 'uriFormats.yaml.local' },
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

// Preference keys considered "advanced" — anything the Simple-mode Options
// disclosure does NOT expose. `hasCustomPrefs` uses this list to decide
// whether switching Power -> Simple should warn the user.
const ADVANCED_PREF_KEYS = [
  'quantizations', 'mmproj_quantizations', 'embeddings', 'type',
  'pipeline_type', 'scheduler_type', 'enable_parameters', 'cuda',
]

const hintStyle = { marginTop: 'var(--spacing-xs)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }

// hasCustomPrefs returns true when the user has set any preference beyond
// backend/name/description, added a custom key-value pref with a non-empty
// key, or edited the YAML away from its default. That triggers the switch
// warning so Simple mode never silently hides state.
function hasCustomPrefs(prefs, customPrefs, yamlContent) {
  for (const key of ADVANCED_PREF_KEYS) {
    const v = prefs[key]
    if (typeof v === 'boolean' ? v : (typeof v === 'string' ? v.trim() !== '' : v != null && v !== '')) {
      return true
    }
  }
  if (Array.isArray(customPrefs) && customPrefs.some(cp => (cp.key || '').trim() !== '')) {
    return true
  }
  if (typeof yamlContent === 'string' && yamlContent !== DEFAULT_YAML) {
    return true
  }
  return false
}

// PowerTabs renders the in-page Preferences/YAML tab strip. Kept inline
// (not a separate component) — the strip is tiny and lives inside the
// Power-mode card so extracting it would just add indirection.
function PowerTabs({ value, onChange }) {
  const { t } = useTranslation('importModel')
  return (
    <div
      className="segmented"
      role="tablist"
      aria-label={t('powerTabs.ariaLabel')}
      data-testid="power-tabs"
      style={{ marginBottom: 'var(--spacing-md)' }}
    >
      <button
        type="button"
        role="tab"
        aria-selected={value === 'preferences'}
        className={`segmented__item${value === 'preferences' ? ' is-active' : ''}`}
        onClick={() => onChange('preferences')}
        data-testid="power-tab-preferences"
      >
        <i className="fas fa-sliders" aria-hidden="true" />
        {t('powerTabs.preferences')}
      </button>
      <button
        type="button"
        role="tab"
        aria-selected={value === 'yaml'}
        className={`segmented__item${value === 'yaml' ? ' is-active' : ''}`}
        onClick={() => onChange('yaml')}
        data-testid="power-tab-yaml"
      >
        <i className="fas fa-code" aria-hidden="true" />
        {t('powerTabs.yaml')}
      </button>
    </div>
  )
}

// SwitchModeDialog — 3-button confirmation that fires when switching from
// Power -> Simple with custom prefs. Not using ConfirmDialog because that
// component is 2-button (confirm/cancel); the UX here needs Keep / Discard
// / Cancel with distinct semantics.
function SwitchModeDialog({ onKeep, onDiscard, onCancel }) {
  const { t } = useTranslation('importModel')
  const keepRef = useRef(null)
  useEffect(() => {
    keepRef.current?.focus()
    const handleKey = (e) => { if (e.key === 'Escape') onCancel?.() }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [onCancel])

  return (
    <div
      className="confirm-dialog-backdrop"
      onClick={onCancel}
      data-testid="switch-mode-dialog"
    >
      <div
        className="confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="switch-mode-title"
        aria-describedby="switch-mode-body"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="confirm-dialog-header">
          <span id="switch-mode-title" className="confirm-dialog-title">{t('switchDialog.title')}</span>
        </div>
        <div id="switch-mode-body" className="confirm-dialog-body">
          {t('switchDialog.body')}
        </div>
        <div className="confirm-dialog-actions">
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            onClick={onCancel}
            data-testid="switch-mode-cancel"
          >
            {t('switchDialog.cancel')}
          </button>
          <button
            type="button"
            className="btn btn-danger btn-sm"
            onClick={onDiscard}
            data-testid="switch-mode-discard"
          >
            {t('switchDialog.discard')}
          </button>
          <button
            ref={keepRef}
            type="button"
            className="btn btn-primary btn-sm"
            onClick={onKeep}
            data-testid="switch-mode-keep"
          >
            {t('switchDialog.keep')}
          </button>
        </div>
      </div>
    </div>
  )
}

export default function ImportModel() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('importModel')

  // Mode + tab state. Persisted to localStorage so reloads keep the user
  // on the same surface they last picked. `showOptions` is Simple-mode
  // local state — no need to persist (it's a one-click expansion).
  const [mode, setMode] = useState(() => {
    try { return localStorage.getItem('import-form-mode') || 'simple' } catch { return 'simple' }
  })
  const [powerTab, setPowerTab] = useState(() => {
    try { return localStorage.getItem('import-form-power-tab') || 'preferences' } catch { return 'preferences' }
  })
  const [showOptions, setShowOptions] = useState(false)
  // null | { onKeep, onDiscard, onCancel } — when non-null the dialog renders.
  const [switchDialog, setSwitchDialog] = useState(null)

  const [importUri, setImportUri] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [showGuide, setShowGuide] = useState(false)
  const [yamlContent, setYamlContent] = useState(DEFAULT_YAML)
  const [estimate, setEstimate] = useState(null)
  const [jobProgress, setJobProgress] = useState(null)

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

  const pollRef = useRef(null)

  useEffect(() => {
    return () => { if (pollRef.current) clearInterval(pollRef.current) }
  }, [])

  useEffect(() => {
    try { localStorage.setItem('import-form-mode', mode) } catch { /* ignore quota / privacy mode */ }
  }, [mode])

  useEffect(() => {
    try { localStorage.setItem('import-form-power-tab', powerTab) } catch { /* ignore */ }
  }, [powerTab])

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

  // requestModeSwitch — routed through the SimplePowerSwitch onChange. When
  // going Power -> Simple we gate on custom prefs so the user never loses
  // hidden state silently.
  const requestModeSwitch = useCallback((next) => {
    if (next === mode) return
    if (mode === 'power' && next === 'simple' && hasCustomPrefs(prefs, customPrefs, yamlContent)) {
      setSwitchDialog({
        onKeep: () => { setSwitchDialog(null); setMode('simple') },
        onDiscard: () => {
          setSwitchDialog(null)
          setPrefs(DEFAULT_PREFS)
          setCustomPrefs([])
          setYamlContent(DEFAULT_YAML)
          setMode('simple')
        },
        onCancel: () => setSwitchDialog(null),
      })
      return
    }
    setMode(next)
  }, [mode, prefs, customPrefs, yamlContent])

  const startJobPolling = useCallback((jobId) => {
    if (pollRef.current) clearInterval(pollRef.current)
    pollRef.current = setInterval(async () => {
      try {
        const data = await modelsApi.getJobStatus(jobId)
        if (data.processed || data.progress) {
          setJobProgress(data.message || data.progress || 'Processing...')
        }
        if (data.completed) {
          clearInterval(pollRef.current)
          pollRef.current = null
          setIsSubmitting(false)
          setJobProgress(null)
          addToast(t('toasts.imported'), 'success')
          navigate('/app/manage')
        } else if (data.error || (data.message && data.message.startsWith('error:'))) {
          clearInterval(pollRef.current)
          pollRef.current = null
          setIsSubmitting(false)
          setJobProgress(null)
          let msg = 'Unknown error'
          if (typeof data.error === 'string') msg = data.error
          else if (data.error?.message) msg = data.error.message
          else if (data.message) msg = data.message
          if (msg.startsWith('error: ')) msg = msg.substring(7)
          addToast(t('toasts.importFailed', { message: msg }), 'error')
        }
      } catch (err) {
        console.error('Error polling job status:', err)
      }
    }, 1000)
  }, [addToast, navigate, t])

  const handleSimpleImport = useCallback(async (overrideBackend) => {
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

      const parts = []
      if (hasSize) parts.push(`${t('estimate.download', { size: result.estimated_size_display })}`)
      if (hasVram) parts.push(`${t('estimate.vram', { vram: result.estimated_vram_display })}`)
      const msg = parts.length
        ? t('toasts.startedWithMeta', { meta: parts.join(' \u00b7 ') })
        : t('toasts.started')
      addToast(msg, 'success')
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
    // handleSimpleImport would otherwise see the stale prefs.backend.
    handleSimpleImport(backend)
  }, [handleSimpleImport])

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
  // browses manually. Leaving `modalityFilter` as-is on dismiss / pick /
  // URI change matches the spec.
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

  const handleAdvancedImport = async () => {
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

  const isSimple = mode === 'simple'
  const isPowerYaml = mode === 'power' && powerTab === 'yaml'

  const subtitle = isSimple
    ? t('subtitle.simple')
    : (powerTab === 'yaml' ? t('subtitle.powerYaml') : t('subtitle.powerPrefs'))

  // The Ambiguity alert + URI input live at the top of both Simple and
  // Power/Preferences modes. Extracted so both branches stay readable.
  const renderUriAndAmbiguity = () => (
    <>
      {ambiguity && (
        <AmbiguityAlert
          modality={ambiguity.modality}
          candidates={ambiguity.candidates}
          knownBackends={backends}
          onPick={pickAmbiguityCandidate}
          onDismiss={() => setAmbiguity(null)}
        />
      )}

      <div className="form-group">
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-xs)' }}>
          <label className="form-label" style={{ marginBottom: 0 }}>
            {t('form.modelUri')}
          </label>
          <a href="https://huggingface.co/models?sort=trending" target="_blank" rel="noreferrer"
            className="btn btn-secondary" style={{ fontSize: '0.7rem', padding: '3px 8px' }}>
            {t('actions.browseHF')} <i className="fas fa-external-link-alt" aria-hidden="true" style={{ marginLeft: 'var(--spacing-xs)' }} />
          </a>
        </div>
        <input
          className="input"
          type="text"
          value={importUri}
          onChange={(e) => setImportUri(e.target.value)}
          placeholder={t('form.uriPlaceholder')}
          disabled={isSubmitting}
        />
        <p style={hintStyle}>{t('form.uriHint')}</p>

        <button
          type="button"
          onClick={() => setShowGuide(!showGuide)}
          style={{ marginTop: 'var(--spacing-sm)', background: 'none', border: 'none', color: 'var(--color-text-secondary)', cursor: 'pointer', fontSize: '0.8125rem', display: 'flex', alignItems: 'center', gap: '6px', padding: 0 }}
        >
          <i className={`fas ${showGuide ? 'fa-chevron-down' : 'fa-chevron-right'}`} aria-hidden="true" />
          <i className="fas fa-info-circle" aria-hidden="true" />
          {t('form.supportedFormats')}
        </button>
        {showGuide && (
          <div style={{ marginTop: 'var(--spacing-sm)', padding: 'var(--spacing-md)', background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-default)', borderRadius: 'var(--radius-md)' }}>
            {URI_FORMATS.map((fmt, i) => (
              <div key={i} style={{ marginBottom: i < URI_FORMATS.length - 1 ? 'var(--spacing-md)' : 0 }}>
                <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: '6px', display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <i className={fmt.icon} aria-hidden="true" style={{ color: fmt.color }} />
                  {t(fmt.titleKey)}
                </h4>
                <div style={{ paddingLeft: '20px', fontSize: '0.75rem', fontFamily: 'var(--font-mono)' }}>
                  {fmt.examples.map((ex, j) => (
                    <div key={j} style={{ marginBottom: 'var(--spacing-xs)' }}>
                      <code style={{ color: 'var(--color-success)' }}>{ex.prefix}</code>
                      <span style={{ color: 'var(--color-text-secondary)' }}>{ex.suffix}</span>
                      <p style={{ color: 'var(--color-text-muted)', marginTop: '1px', fontFamily: 'inherit' }}>{t(ex.descKey)}</p>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </>
  )

  // Backend dropdown + auto-install note — shared between Simple/Options
  // and Power/Preferences.
  const renderBackendField = () => (
    <div className="form-group" style={{ marginBottom: 0 }}>
      <label className="form-label">{t('form.backend')}</label>
      <SearchableSelect
        value={prefs.backend}
        onChange={(v) => updatePref('backend', v)}
        options={backendOptions}
        allOption={t('form.backendAuto')}
        placeholder={backendsLoading ? t('form.backendLoading') : t('form.backendAuto')}
        searchPlaceholder={t('form.backendSearch')}
        disabled={isSubmitting || backendsLoading}
      />
      <p style={hintStyle}>
        {t('form.backendHint')}
        {backendsError && (
          <span style={{ color: 'var(--color-warning)', marginLeft: '6px' }}>
            {t('form.backendErrorHint')}
          </span>
        )}
      </p>
      {(() => {
        if (!prefs.backend) return null
        const selected = backends.find(b => b.name === prefs.backend)
        if (!selected || selected.installed) return null
        return (
          <p
            data-testid="auto-install-note"
            style={{ ...hintStyle, display: 'flex', alignItems: 'center', gap: '6px', marginTop: '6px' }}
          >
            <i className="fas fa-download" aria-hidden="true" />
            {t('form.backendNotInstalled')}
          </p>
        )
      })()}
    </div>
  )

  const renderNameField = () => (
    <div className="form-group" style={{ marginBottom: 0 }}>
      <label className="form-label">{t('form.modelName')}</label>
      <input className="input" type="text" value={prefs.name} onChange={e => updatePref('name', e.target.value)} placeholder={t('form.modelNamePlaceholder')} disabled={isSubmitting} />
      <p style={hintStyle}>{t('form.modelNameHint')}</p>
    </div>
  )

  const renderDescriptionField = () => (
    <div className="form-group" style={{ marginBottom: 0 }}>
      <label className="form-label">{t('form.description')}</label>
      <textarea className="textarea" rows={2} value={prefs.description} onChange={e => updatePref('description', e.target.value)} placeholder={t('form.descriptionPlaceholder')} disabled={isSubmitting} />
      <p style={hintStyle}>{t('form.descriptionHint')}</p>
    </div>
  )

  // Full preferences panel — identical to the previous Simple-mode panel.
  const renderFullPreferences = () => (
    <div style={{ marginTop: 'var(--spacing-lg)' }}>
      <div style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-sm)' }}>
        <i className="fas fa-cog" aria-hidden="true" style={{ marginRight: '6px' }} />{t('form.preferences')}
      </div>

      <ModalityChips
        value={modalityFilter}
        onChange={handleModalityChange}
        disabled={isSubmitting || backendsLoading}
      />

      <div style={{ padding: 'var(--spacing-md)', background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-default)', borderRadius: 'var(--radius-md)' }}>
        <h3 style={{ fontSize: '0.8125rem', fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)', display: 'flex', alignItems: 'center', gap: '6px' }}>
          <i className="fas fa-sliders" style={{ color: 'var(--color-primary)' }} aria-hidden="true" />
          {t('form.commonPreferences')}
        </h3>

        <div style={{ display: 'grid', gap: 'var(--spacing-md)' }}>
          {renderBackendField()}
          {renderNameField()}
          {renderDescriptionField()}

          {showQuantizations && (
            <div className="form-group" style={{ marginBottom: 0 }}>
              <label className="form-label">{t('form.quantizations')}</label>
              <input className="input" type="text" value={prefs.quantizations} onChange={e => updatePref('quantizations', e.target.value)} placeholder={t('form.quantizationsPlaceholder')} disabled={isSubmitting} />
              <p style={hintStyle}>{t('form.quantizationsHint')}</p>
            </div>
          )}

          {showMmprojQuantizations && (
            <div className="form-group" style={{ marginBottom: 0 }}>
              <label className="form-label">{t('form.mmprojQuantizations')}</label>
              <input className="input" type="text" value={prefs.mmproj_quantizations} onChange={e => updatePref('mmproj_quantizations', e.target.value)} placeholder={t('form.mmprojQuantizationsPlaceholder')} disabled={isSubmitting} />
              <p style={hintStyle}>{t('form.mmprojQuantizationsHint')}</p>
            </div>
          )}

          <div>
            <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', cursor: 'pointer' }}>
              <input type="checkbox" checked={prefs.embeddings} onChange={e => updatePref('embeddings', e.target.checked)} disabled={isSubmitting} />
              <span style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--color-text-secondary)' }}>
                {t('form.embeddings')}
              </span>
            </label>
            <p style={{ ...hintStyle, marginLeft: '28px' }}>{t('form.embeddingsHint')}</p>
          </div>

          {showModelType && (
            <div className="form-group" style={{ marginBottom: 0 }}>
              <label className="form-label">{t('form.modelType')}</label>
              <input className="input" type="text" value={prefs.type} onChange={e => updatePref('type', e.target.value)} placeholder={t('form.modelTypePlaceholder')} disabled={isSubmitting} />
              <p style={hintStyle}>{t('form.modelTypeHint')}</p>
            </div>
          )}

          {prefs.backend === 'diffusers' && (
            <>
              <div className="form-group" style={{ marginBottom: 0 }}>
                <label className="form-label">{t('form.pipelineType')}</label>
                <input className="input" type="text" value={prefs.pipeline_type} onChange={e => updatePref('pipeline_type', e.target.value)} placeholder="StableDiffusionPipeline" disabled={isSubmitting} />
                <p style={hintStyle}>{t('form.pipelineTypeHint')}</p>
              </div>
              <div className="form-group" style={{ marginBottom: 0 }}>
                <label className="form-label">{t('form.schedulerType')}</label>
                <input className="input" type="text" value={prefs.scheduler_type} onChange={e => updatePref('scheduler_type', e.target.value)} placeholder={t('form.schedulerTypePlaceholder')} disabled={isSubmitting} />
                <p style={hintStyle}>{t('form.schedulerTypeHint')}</p>
              </div>
              <div className="form-group" style={{ marginBottom: 0 }}>
                <label className="form-label">{t('form.enableParameters')}</label>
                <input className="input" type="text" value={prefs.enable_parameters} onChange={e => updatePref('enable_parameters', e.target.value)} placeholder={t('form.enableParametersPlaceholder')} disabled={isSubmitting} />
                <p style={hintStyle}>{t('form.enableParametersHint')}</p>
              </div>
              <div>
                <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', cursor: 'pointer' }}>
                  <input type="checkbox" checked={prefs.cuda} onChange={e => updatePref('cuda', e.target.checked)} disabled={isSubmitting} />
                  <span style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--color-text-secondary)' }}>
                    {t('form.cuda')}
                  </span>
                </label>
                <p style={{ ...hintStyle, marginLeft: '28px' }}>{t('form.cudaHint')}</p>
              </div>
            </>
          )}
        </div>
      </div>

      {/* Custom Preferences */}
      <div style={{ marginTop: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-sm)' }}>
          <span style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--color-text-secondary)' }}>
            <i className="fas fa-plus-circle" style={{ marginRight: '6px' }} aria-hidden="true" />{t('form.customPreferences')}
          </span>
          <button className="btn btn-secondary" onClick={addCustomPref} disabled={isSubmitting} style={{ fontSize: '0.75rem' }}>
            <i className="fas fa-plus" aria-hidden="true" /> {t('actions.addCustom')}
          </button>
        </div>
        {customPrefs.map((cp, i) => (
          <div key={i} style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center', marginBottom: 'var(--spacing-xs)' }}>
            <input
              className="input"
              type="text"
              value={cp.key}
              onChange={e => updateCustomPref(i, 'key', e.target.value)}
              placeholder={t('form.key')}
              aria-label={t('form.preferenceKey', { index: i + 1 })}
              disabled={isSubmitting}
              style={{ flex: 1 }}
            />
            <span style={{ color: 'var(--color-text-secondary)' }}>:</span>
            <input
              className="input"
              type="text"
              value={cp.value}
              onChange={e => updateCustomPref(i, 'value', e.target.value)}
              placeholder={t('form.value')}
              aria-label={t('form.preferenceValue', { index: i + 1 })}
              disabled={isSubmitting}
              style={{ flex: 1 }}
            />
            <button
              className="btn btn-secondary"
              onClick={() => removeCustomPref(i)}
              disabled={isSubmitting}
              aria-label={t('form.removePref')}
              style={{ color: 'var(--color-error)' }}
            >
              <i className="fas fa-trash" aria-hidden="true" />
            </button>
          </div>
        ))}
        <p style={hintStyle}>{t('form.customKeyValueHint')}</p>
      </div>
    </div>
  )

  return (
    <div className="page page--narrow">
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--spacing-sm)' }}>
        <div>
          <h1 className="page-title">{t('title')}</h1>
          <p className="page-subtitle">{subtitle}</p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)', flexWrap: 'wrap', alignItems: 'center' }}>
          <SimplePowerSwitch value={mode} onChange={requestModeSwitch} disabled={isSubmitting} />
          {isPowerYaml ? (
            <button className="btn btn-primary" onClick={handleAdvancedImport} disabled={isSubmitting}>
              {isSubmitting ? <><LoadingSpinner size="sm" /> {t('actions.saving')}</> : <><i className="fas fa-save" aria-hidden="true" /> {t('actions.create')}</>}
            </button>
          ) : (
            <button className="btn btn-primary" onClick={() => handleSimpleImport()} disabled={isSubmitting || !importUri.trim()}>
              {isSubmitting ? <><LoadingSpinner size="sm" /> {t('actions.importing')}</> : <><i className="fas fa-upload" aria-hidden="true" /> {t('actions.import')}</>}
            </button>
          )}
        </div>
      </div>

      {/* Estimate banner */}
      {!isPowerYaml && estimate && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-md)', borderColor: 'var(--color-primary)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', fontSize: '0.875rem', flexWrap: 'wrap' }}>
            <i className="fas fa-memory" aria-hidden="true" style={{ color: 'var(--color-primary)' }} />
            <strong>{t('estimate.title')}</strong>
            {estimate.sizeDisplay && estimate.sizeDisplay !== '0 B' && (
              <span><i className="fas fa-download" aria-hidden="true" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />{t('estimate.download', { size: estimate.sizeDisplay })}</span>
            )}
            {estimate.vramDisplay && estimate.vramDisplay !== '0 B' && (
              <span><i className="fas fa-microchip" aria-hidden="true" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />{t('estimate.vram', { vram: estimate.vramDisplay })}</span>
            )}
          </div>
        </div>
      )}

      {/* Job progress */}
      {jobProgress && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-md)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', fontSize: '0.875rem' }}>
            <LoadingSpinner size="sm" />
            <span>{jobProgress}</span>
          </div>
        </div>
      )}

      {/* Simple mode */}
      {isSimple && (
        <div className="card" style={{ padding: 'var(--spacing-lg)' }}>
          {/* Wrapping the Simple-mode content in a <form> gives us Enter-to-
              submit for free: focus in the URI input triggers onSubmit without
              a keyDown handler. The Import button in the page header submits
              by calling handleSimpleImport directly (type="button") — it lives
              outside this form, so the form owns keyboard submit only. */}
          <form
            data-testid="simple-form"
            onSubmit={(e) => { e.preventDefault(); handleSimpleImport() }}
          >
            {renderUriAndAmbiguity()}

            <div style={{ marginTop: 'var(--spacing-md)' }}>
              <button
                type="button"
                onClick={() => setShowOptions(v => !v)}
                data-testid="simple-options-toggle"
                aria-expanded={showOptions}
                aria-controls="simple-options-panel"
                style={{
                  background: 'none',
                  border: 'none',
                  color: 'var(--color-text-secondary)',
                  cursor: 'pointer',
                  fontSize: '0.8125rem',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '6px',
                  padding: 0,
                }}
              >
                <i className={`fas ${showOptions ? 'fa-chevron-down' : 'fa-chevron-right'}`} aria-hidden="true" />
                <i className="fas fa-sliders" aria-hidden="true" />
                {t('form.options')}
              </button>

              {showOptions && (
                <div
                  id="simple-options-panel"
                  data-testid="simple-options-panel"
                  style={{
                    marginTop: 'var(--spacing-sm)',
                    padding: 'var(--spacing-md)',
                    background: 'var(--color-bg-primary)',
                    border: '1px solid var(--color-border-default)',
                    borderRadius: 'var(--radius-md)',
                    display: 'grid',
                    gap: 'var(--spacing-md)',
                  }}
                >
                  <ModalityChips
                    value={modalityFilter}
                    onChange={handleModalityChange}
                    disabled={isSubmitting || backendsLoading}
                  />
                  {renderBackendField()}
                  {renderNameField()}
                  {renderDescriptionField()}
                </div>
              )}
            </div>
            {/* Hidden submit button — required because the visible Import
                button lives outside this <form> in the page header. Browsers
                only trigger implicit Enter submit when the form contains at
                least one submit-capable element; this keeps the behaviour
                consistent even if the form ever holds a single text input. */}
            <button type="submit" aria-hidden="true" tabIndex={-1} style={{ display: 'none' }} />
          </form>
        </div>
      )}

      {/* Power mode */}
      {mode === 'power' && (
        <div className="card" style={{ padding: isPowerYaml ? 0 : 'var(--spacing-lg)', overflow: 'hidden' }}>
          {!isPowerYaml && (
            <>
              <PowerTabs value={powerTab} onChange={setPowerTab} />
              {renderUriAndAmbiguity()}
              {renderFullPreferences()}
            </>
          )}
          {isPowerYaml && (
            <>
              <div style={{ padding: 'var(--spacing-md)' }}>
                <PowerTabs value={powerTab} onChange={setPowerTab} />
              </div>
              <div style={{ padding: 'var(--spacing-md)', borderTop: '1px solid var(--color-border-default)', borderBottom: '1px solid var(--color-border-default)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <h2 style={{ fontSize: '1.125rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
                  <i className="fas fa-code" aria-hidden="true" style={{ color: 'var(--color-data-3)' }} />
                  {t('form.yamlEditor')}
                </h2>
                <button className="btn btn-secondary" style={{ fontSize: '0.75rem' }} onClick={() => { navigator.clipboard.writeText(yamlContent); addToast(t('toasts.copied'), 'success') }}>
                  <i className="fas fa-copy" aria-hidden="true" /> {t('actions.copy')}
                </button>
              </div>
              <CodeEditor value={yamlContent} onChange={setYamlContent} disabled={isSubmitting} minHeight="calc(100vh - 400px)" />
            </>
          )}
        </div>
      )}

      {switchDialog && (
        <SwitchModeDialog
          onKeep={switchDialog.onKeep}
          onDiscard={switchDialog.onDiscard}
          onCancel={switchDialog.onCancel}
        />
      )}
    </div>
  )
}
