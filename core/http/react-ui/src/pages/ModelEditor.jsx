import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { useParams, useNavigate, useOutletContext, useSearchParams, useLocation } from 'react-router-dom'
import YAML from 'yaml'
import { modelsApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { useConfigMetadata } from '../hooks/useConfigMetadata'
import { useVramEstimate } from '../hooks/useVramEstimate'
import LoadingSpinner from '../components/LoadingSpinner'
import CodeEditor from '../components/CodeEditor'
import FieldBrowser from '../components/FieldBrowser'
import ConfigFieldRenderer from '../components/ConfigFieldRenderer'
import { FormContextProvider } from '../contexts/FormContext'
import TemplateSelector from '../components/TemplateSelector'
import MODEL_TEMPLATES from '../utils/modelTemplates'
import { useTranslation } from 'react-i18next'

const SECTION_ICONS = {
  general: 'fa-cog', llm: 'fa-microchip', parameters: 'fa-sliders',
  templates: 'fa-file-code', functions: 'fa-wrench', reasoning: 'fa-brain',
  diffusers: 'fa-image', tts: 'fa-volume-up', pipeline: 'fa-code-branch',
  grpc: 'fa-server', agent: 'fa-robot', mcp: 'fa-plug', router: 'fa-route', proxy: 'fa-cloud',
  mitm: 'fa-user-secret', pii: 'fa-user-shield', other: 'fa-ellipsis-h',
}

const SECTION_COLORS = {
  general: 'var(--color-primary)', llm: 'var(--color-accent)', parameters: 'var(--color-success)',
  templates: 'var(--color-warning)', functions: 'var(--color-info, var(--color-primary))',
  reasoning: 'var(--color-accent)', diffusers: 'var(--color-warning)', tts: 'var(--color-success)',
  pipeline: 'var(--color-accent)', grpc: 'var(--color-text-muted)', agent: 'var(--color-primary)',
  mcp: 'var(--color-accent)', router: 'var(--color-accent)', proxy: 'var(--color-info, var(--color-primary))',
  mitm: 'var(--color-warning)', pii: 'var(--color-error)', other: 'var(--color-text-muted)',
}

// flattenConfig turns a parsed YAML config into a flat { 'a.b.c': value }
// map keyed by the same dotted paths the field registry uses. leafPaths is
// the set of registered schema leaf paths: recursion STOPS at any of them so
// a map-typed field (e.g. pii_detection.entity_actions, a {GROUP: action}
// object) is stored whole at its own path. Without this guard a map's value
// was scattered into `pii_detection.entity_actions.SSN` etc. — paths that
// match no registered field — so the editor rendered neither the field nor
// its values, hiding per-entity policy like SSN→block from the operator.
function flattenConfig(obj, leafPaths, prefix = '') {
  const result = {}
  if (!obj || typeof obj !== 'object') return result
  for (const [key, val] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (leafPaths && leafPaths.has(path)) {
      result[path] = val
    } else if (val !== null && typeof val === 'object' && !Array.isArray(val)) {
      Object.assign(result, flattenConfig(val, leafPaths, path))
    } else {
      result[path] = val
    }
  }
  return result
}

function unflattenConfig(flat) {
  const result = Object.create(null)
  for (const [path, val] of Object.entries(flat)) {
    const keys = path.split('.')
    let obj = result
    for (let i = 0; i < keys.length - 1; i++) {
      if (!obj[keys[i]]) obj[keys[i]] = Object.create(null)
      obj = obj[keys[i]]
    }
    obj[keys[keys.length - 1]] = val
  }
  return result
}

function defaultForType(uiType) {
  switch (uiType) {
    case 'bool': return false
    case 'int': case 'float': return 0
    case '[]string': return []
    case 'map': return {}
    case 'object': return {}
    default: return ''
  }
}

export default function ModelEditor() {
  const { t } = useTranslation('modelEditor')
  const { name } = useParams()
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const location = useLocation()
  // Where the Back button returns to. Set by whichever page linked here (see
  // utils/editorNav); falls back to the historical defaults for direct visits.
  const backState = location.state && location.state.from ? location.state : null
  const { addToast } = useOutletContext()
  const { sections, fields, loading: metaLoading, error: metaError } = useConfigMetadata()

  // Registered schema leaf paths. flattenConfig stops recursing at these so
  // map-typed fields (e.g. pii_detection.entity_actions) bind as a whole
  // object to their registered editor instead of vanishing into sub-paths.
  const leafPaths = useMemo(() => new Set(fields.map(f => f.path)), [fields])

  // The parsed (not-yet-flattened) config loaded from the server. Flattening
  // is deferred to a separate effect keyed on leafPaths so the schema metadata
  // can arrive after the config without a fetch race re-clobbering values.
  const [loadedConfig, setLoadedConfig] = useState(null)

  const isCreateMode = !name
  const [selectedTemplate, setSelectedTemplate] = useState(null)

  const [tab, setTab] = useState('interactive') // 'interactive' | 'yaml'
  const [yamlText, setYamlText] = useState('')
  const [savedYamlText, setSavedYamlText] = useState('')
  const [values, setValues] = useState({})
  const [initialValues, setInitialValues] = useState({})
  const [activeFieldPaths, setActiveFieldPaths] = useState(new Set())
  const [collapsedSections, setCollapsedSections] = useState(new Set())
  const [configLoading, setConfigLoading] = useState(!isCreateMode)
  const [saving, setSaving] = useState(false)
  const [activeSection, setActiveSection] = useState(null)
  const [tabSwitchWarning, setTabSwitchWarning] = useState(false)

  const sectionRefs = useRef({})

  const vramEstimate = useVramEstimate({
    model: name,
    contextSize: values['context_size'],
    gpuLayers: values['gpu_layers'],
  })

  const handleSelectTemplate = useCallback((template) => {
    setSelectedTemplate(template)
    const flat = { ...template.fields }
    setValues(flat)
    setInitialValues({})
    setActiveFieldPaths(new Set(Object.keys(flat)))
  }, [])

  // Auto-select template from URL query param (e.g. ?template=pipeline)
  useEffect(() => {
    if (!isCreateMode) return
    const templateId = searchParams.get('template')
    if (templateId) {
      const t = MODEL_TEMPLATES.find(t => t.id === templateId)
      if (t) handleSelectTemplate(t)
    }
  }, [isCreateMode, searchParams, handleSelectTemplate])

  // Load raw YAML config (edit mode only). This only fetches + parses; the
  // flatten-into-form-values step is the separate effect below so it can
  // re-run when the schema metadata (leafPaths) resolves without re-fetching.
  useEffect(() => {
    if (!name) return
    modelsApi.getEditConfig(name)
      .then(data => {
        const raw = data?.config || ''
        setYamlText(raw)
        setSavedYamlText(raw)
        try {
          setLoadedConfig(YAML.parse(raw) || {})
        } catch {
          setLoadedConfig({})
        }
      })
      .catch(err => addToast(`Failed to load config: ${err.message}`, 'error'))
      .finally(() => setConfigLoading(false))
  }, [name, addToast])

  // Flatten the loaded config into form values. Keyed on leafPaths so a late
  // schema-metadata resolution re-flattens (keeping map fields whole) WITHOUT
  // re-fetching — avoiding a two-fetch race that could clobber values. Only
  // fires on (re)load: loadedConfig changes per model, leafPaths is stable
  // once metadata is in, so this never stomps in-progress edits.
  useEffect(() => {
    if (loadedConfig === null) return
    const flat = flattenConfig(loadedConfig, leafPaths)
    setValues(flat)
    setInitialValues(structuredClone(flat))
    setActiveFieldPaths(new Set(Object.keys(flat)))
  }, [loadedConfig, leafPaths])

  // Build field lookup
  const fieldsByPath = useMemo(() => {
    const map = {}
    for (const f of fields) map[f.path] = f
    return map
  }, [fields])

  // Sections with active fields
  const activeSections = useMemo(() => {
    const sectionSet = new Set()
    for (const path of activeFieldPaths) {
      if (isCreateMode && path === 'name') continue
      const field = fieldsByPath[path]
      if (field) sectionSet.add(field.section)
    }
    return sections
      .filter(s => sectionSet.has(s.id))
      .sort((a, b) => a.order - b.order)
  }, [sections, activeFieldPaths, fieldsByPath, isCreateMode])

  // Fields per section (skip 'name' in create mode — it has a dedicated input)
  const fieldsBySection = useMemo(() => {
    const result = {}
    for (const path of activeFieldPaths) {
      if (isCreateMode && path === 'name') continue
      const field = fieldsByPath[path]
      if (!field) continue
      if (!result[field.section]) result[field.section] = []
      result[field.section].push(field)
    }
    for (const arr of Object.values(result)) {
      arr.sort((a, b) => a.order - b.order)
    }
    return result
  }, [activeFieldPaths, fieldsByPath, isCreateMode])

  // Default to first active section
  useEffect(() => {
    if (!activeSection && activeSections.length > 0) {
      setActiveSection(activeSections[0].id)
    }
  }, [activeSection, activeSections])

  // Scroll tracking — the editor used to have its own overflow:auto pane
  // and listened to that container's scroll; the pane has been removed so
  // small screens don't have the global footer always clipping into the
  // form. Scrolling now happens at the window level, and the anchor for
  // "which section is at the top" is a fixed viewport offset (the sticky
  // sidebar sits roughly at the top of the editor area).
  useEffect(() => {
    if (tab !== 'interactive') return
    const onScroll = () => {
      const anchorY = 80 // viewport px below which a section is "active"
      let closest = activeSections[0]?.id
      let closestDist = Infinity
      for (const s of activeSections) {
        const el = sectionRefs.current[s.id]
        if (el) {
          const dist = Math.abs(el.getBoundingClientRect().top - anchorY)
          if (dist < closestDist) { closestDist = dist; closest = s.id }
        }
      }
      if (closest) setActiveSection(closest)
    }
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [activeSections, configLoading, metaLoading, tab])

  const scrollTo = (id) => {
    setActiveSection(id)
    sectionRefs.current[id]?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  const interactiveDirty = useMemo(() => {
    if (isCreateMode) return activeFieldPaths.size > 0
    return JSON.stringify(values) !== JSON.stringify(initialValues) ||
      [...activeFieldPaths].some(p => !(p in initialValues))
  }, [isCreateMode, values, initialValues, activeFieldPaths])

  const yamlDirty = useMemo(() => {
    if (isCreateMode) return yamlText.trim().length > 0
    return yamlText !== savedYamlText
  }, [isCreateMode, yamlText, savedYamlText])

  const isDirty = tab === 'interactive' ? interactiveDirty : yamlDirty

  const vramAnnotation = useMemo(() => {
    if (isCreateMode) return null
    if (vramEstimate.loading) {
      return (
        <div className="text-meta mt-xs">
          <i className="fas fa-spinner fa-spin icon-before" />
          Estimating VRAM...
        </div>
      )
    }
    if (vramEstimate.vramDisplay) {
      return (
        <div className="text-xs text-warning fw-medium mt-xs">
          <i className="fas fa-memory icon-before" />
          ~{vramEstimate.vramDisplay} VRAM
        </div>
      )
    }
    return null
  }, [isCreateMode, vramEstimate.loading, vramEstimate.vramDisplay])

  // Interactive save — uses PATCH (edit mode) or importConfig (create mode)
  const handleInteractiveSave = async () => {
    setSaving(true)
    try {
      const patchFlat = {}
      for (const path of activeFieldPaths) {
        if (path in values) patchFlat[path] = values[path]
      }
      const config = unflattenConfig(patchFlat)

      if (isCreateMode) {
        const modelName = values['name']
        if (!modelName?.trim()) { addToast('Model name is required', 'error'); setSaving(false); return }
        if (!/^[a-zA-Z0-9_.-]+$/.test(modelName.trim())) { addToast('Invalid model name — use only letters, numbers, hyphens, underscores, and dots', 'error'); setSaving(false); return }
        await modelsApi.importConfig(JSON.stringify(config), 'application/json')
        addToast('Model created successfully', 'success')
        // replace: the transient create URL shouldn't sit in history, so
        // Back (browser or in-page) skips it and returns to the linking page.
        navigate(`/app/model-editor/${encodeURIComponent(modelName.trim())}`, { replace: true, state: backState })
      } else {
        await modelsApi.patchConfig(name, config)
        setInitialValues(structuredClone(values))
        try {
          const data = await modelsApi.getEditConfig(name)
          const refreshedYaml = data?.config || ''
          setYamlText(refreshedYaml)
          setSavedYamlText(refreshedYaml)
        } catch { /* ignore refresh failure */ }
        setTabSwitchWarning(false)
        addToast('Configuration saved', 'success')
      }
    } catch (err) {
      addToast(`Save failed: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  // YAML save — sends raw text
  const handleYamlSave = async () => {
    setSaving(true)
    try {
      if (isCreateMode) {
        // In create mode, import the YAML as a new config
        await modelsApi.importConfig(yamlText, 'application/x-yaml')
        addToast('Model created successfully', 'success')
        try {
          const parsed = YAML.parse(yamlText)
          if (parsed?.name) navigate(`/app/model-editor/${encodeURIComponent(parsed.name)}`, { replace: true, state: backState })
          else navigate(backState ? backState.from : '/app/models?view=installed')
        } catch { navigate(backState ? backState.from : '/app/models?view=installed') }
      } else {
        const response = await fetch(apiUrl(`/models/edit/${encodeURIComponent(name)}`), {
          method: 'POST',
          headers: { 'Content-Type': 'application/x-yaml' },
          body: yamlText,
        })
        const data = await response.json()
        if (!response.ok || data.success === false) {
          throw new Error(data.error || `HTTP ${response.status}`)
        }
        // Refresh interactive state from saved YAML
        setSavedYamlText(yamlText)
        let parsedName = null
        try {
          const parsed = YAML.parse(yamlText)
          parsedName = parsed?.name ?? null
          const flat = flattenConfig(parsed || {}, leafPaths)
          setValues(flat)
          setInitialValues(structuredClone(flat))
          setActiveFieldPaths(new Set(Object.keys(flat)))
        } catch { /* ignore parse failure */ }
        setTabSwitchWarning(false)
        addToast('Config saved', 'success')
        // When the model was renamed via the YAML `name:` field, the current
        // editor URL points at a name that no longer exists on the backend.
        // Redirect so refreshes and subsequent saves hit the new name.
        if (parsedName && parsedName !== name) {
          navigate(`/app/model-editor/${encodeURIComponent(parsedName)}`, { replace: true, state: backState })
        }
      }
    } catch (err) {
      addToast(`Save failed: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  const createYamlPreview = useMemo(() => {
    if (!isCreateMode || tab !== 'yaml') return ''
    const patchFlat = {}
    for (const path of activeFieldPaths) {
      if (path in values && values[path] !== '' && values[path] !== null) patchFlat[path] = values[path]
    }
    try {
      return YAML.stringify(unflattenConfig(patchFlat))
    } catch { return '' }
  }, [isCreateMode, tab, values, activeFieldPaths])

  const handleAddField = (field) => {
    setActiveFieldPaths(prev => new Set(prev).add(field.path))
    if (!(field.path in values)) {
      setValues(prev => ({ ...prev, [field.path]: field.default ?? defaultForType(field.ui_type) }))
    }
    setCollapsedSections(prev => {
      const next = new Set(prev)
      next.delete(field.section)
      return next
    })
    setTimeout(() => {
      sectionRefs.current[field.section]?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }, 50)
  }

  const handleRemoveField = (path) => {
    setActiveFieldPaths(prev => {
      const next = new Set(prev)
      next.delete(path)
      return next
    })
  }

  const handleFieldChange = (path, val) => {
    setValues(prev => ({ ...prev, [path]: val }))
  }

  const toggleSection = (id) => {
    setCollapsedSections(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const loading = metaLoading || configLoading
  const showTemplateSelector = isCreateMode && !selectedTemplate

  if (loading) return <div className="page page--medium loading-center"><LoadingSpinner size="lg" /></div>
  if (metaError) return <div className="page page--medium"><div className="empty-state"><p className="empty-state-text">Failed to load config metadata: {metaError}</p></div></div>

  const backPage = isCreateMode && selectedTemplate ? t('actions.templates')
    : backState ? backState.fromLabel
    : t('actions.models')

  return (
    <FormContextProvider formData={values}>
    <div className="page page--medium p-0">
      {/* Header */}
      <div className="me-head">
        <div>
          <h1 className="page-title">{isCreateMode ? t('title.add') : t('title.edit')}</h1>
          <p className="page-subtitle">
            {isCreateMode
              ? (showTemplateSelector ? t('subtitle.chooseModelType') : `${t('subtitle.newModel')}${selectedTemplate ? ` — ${selectedTemplate.label}` : ''}`)
              : decodeURIComponent(name)}
          </p>
        </div>
        <div className="hstack">
          <button className="btn btn-secondary" onClick={() => {
            if (isCreateMode && selectedTemplate) { setSelectedTemplate(null); setValues({}); setActiveFieldPaths(new Set()) }
            else if (backState) navigate(backState.from)
            else navigate(isCreateMode ? '/app/models' : '/app/models?view=installed')
          }}>
            <i className="fas fa-arrow-left" /> {t('actions.backTo', {page: backPage})}
          </button>
          {!showTemplateSelector && tab === 'interactive' && (
            <button className={`btn ${isDirty ? 'btn-primary' : 'btn-secondary'}`} onClick={handleInteractiveSave} disabled={saving || !isDirty}>
              {saving ? <><LoadingSpinner size="sm" /> {t('actions.saving')}</> : <><i className="fas fa-save" /> {isCreateMode ? t('actions.createModel') : (isDirty ? t('actions.saveChanges') : t('actions.saved'))}</>}
            </button>
          )}
          {!showTemplateSelector && tab === 'yaml' && (
            <button className={`btn ${isDirty ? 'btn-primary' : 'btn-secondary'}`} onClick={handleYamlSave} disabled={saving || !isDirty}>
              {saving ? <><LoadingSpinner size="sm" /> {t('actions.saving')}</> : <><i className="fas fa-save" /> {isCreateMode ? t('actions.createModel') : (isDirty ? t('actions.saveChanges') : t('actions.saved'))}</>}
            </button>
          )}
        </div>
      </div>

      {/* Template selector (create mode, step 1) */}
      {showTemplateSelector && <TemplateSelector onSelect={handleSelectTemplate} />}

      {/* Tabs (hidden during template selection) */}
      {!showTemplateSelector && (
        <div>
          <div className="me-tabs">
            {['interactive', 'yaml'].map(tb => {
              const active = tab === tb
              const blocked = !active && isDirty
              return (
                <button
                  key={tb}
                  onClick={() => {
                    if (active) return
                    if (blocked) { setTabSwitchWarning(true); return }
                    setTabSwitchWarning(false)
                    setTab(tb)
                  }}
                  className={`me-tab${active ? ' me-tab--on' : ''}${blocked ? ' me-tab--blocked' : ''}`}
                >
                  <i className={`fas ${tb === 'interactive' ? 'fa-sliders' : 'fa-code'} icon-before`} />
                  {tb === 'interactive' ? t('tabs.interactive') : t('tabs.yaml')}
                </button>
              )
            })}
          </div>
          {tabSwitchWarning && isDirty && (
            <div className="me-warn">
              <i className="fas fa-exclamation-triangle" />
              <span>{t('actions.switchWarning')}</span>
              <button
                className="btn btn-secondary ml-auto pill-tiny"
                onClick={() => {
                  if (tab === 'yaml') {
                    setYamlText(savedYamlText)
                  } else {
                    setValues(structuredClone(initialValues))
                    setActiveFieldPaths(new Set(Object.keys(initialValues)))
                  }
                  setTabSwitchWarning(false)
                  setTab(tab === 'yaml' ? 'interactive' : 'yaml')
                }}
              >
                {t('actions.discardAndSwitch')}
              </button>
            </div>
          )}
        </div>
      )}

      {/* YAML Tab */}
      {!showTemplateSelector && tab === 'yaml' && (
        <div className="me-pad--bottom">
          {isCreateMode && (
            <p className="text-sub mb-sm">
              {t('tabs.yamlDescription')}
            </p>
          )}
          <CodeEditor
            value={isCreateMode ? (yamlText || createYamlPreview) : yamlText}
            onChange={setYamlText}
            minHeight="calc(100vh - 260px)"
            fields={fields}
          />
        </div>
      )}

      {/* Interactive Tab */}
      {!showTemplateSelector && tab === 'interactive' && (
        <>
          {/* Model name input (create mode) */}
          {isCreateMode && (
            <div className="me-pad mb-md">
              <div className="card pad-md">
                <label className="form-label fw-semibold">
                  <i className="fas fa-tag icon-before text-primary" />
                  {t('forms.modelName.label')}
                </label>
                <input
                  className="input max-w-400"
                  type="text"
                  value={values['name'] || ''}
                  onChange={e => handleFieldChange('name', e.target.value)}
                  placeholder={t('forms.modelName.placeholder')}
                />
                <p className="text-meta mt-xs">
                  {t('forms.modelName.hint')}
                </p>
              </div>
            </div>
          )}

          {/* Field browser */}
          <div className="me-pad">
            <FieldBrowser
              fields={fields}
              activeFieldPaths={activeFieldPaths}
              onAddField={handleAddField}
            />
          </div>

          {/* Two-column layout. Both columns flow at body-scroll height —
              no inner overflow:auto here, so the global footer ends up
              below the content (like every other page) instead of pinned
              to the viewport bottom, eating editing space on short screens. */}
          <div className="set-layout">
            {/* Sidebar — sticks to the top of the viewport as the body scrolls. */}
            <nav className="set-rail">
              {activeSections.map(s => (
                <button
                  key={s.id}
                  onClick={() => scrollTo(s.id)}
                  className={`set-rail__item${activeSection === s.id ? ' set-rail__item--on' : ''}`}
                >
                  <i
                    className={`fas ${SECTION_ICONS[s.id] || 'fa-cog'} set-rail__icon`}
                    style={activeSection === s.id ? { color: SECTION_COLORS[s.id] || 'var(--color-primary)' } : undefined}
                  />
                  {s.label}
                  <span className="ml-auto text-meta">
                    {fieldsBySection[s.id]?.length || 0}
                  </span>
                </button>
              ))}
              {activeSections.length === 0 && (
                <div className="text-note pad-sm">
                  {t('forms.empty.nav')}
                </div>
              )}
            </nav>

            {/* Content */}
            <div
              className="me-body"
            >
              {activeSections.length === 0 && (
                <div className="card loading-center text-center">
                  <i className="fas fa-sliders icon-xl text-muted mb-md" />
                  <h3 className="mb-sm">{t('forms.empty.title')}</h3>
                  <p className="text-base text-secondary">
                    {t('forms.empty.text')}
                  </p>
                </div>
              )}

              {activeSections.map(s => {
                const sectionFields = fieldsBySection[s.id] || []
                const isCollapsed = collapsedSections.has(s.id)
                return (
                  <div key={s.id} ref={el => sectionRefs.current[s.id] = el} className="mb-xl">
                    <h3
                      onClick={() => toggleSection(s.id)}
                      className={`me-section-head${isCollapsed ? ' me-section-head--collapsed' : ''}`}
                    >
                      <i className={`fas ${isCollapsed ? 'fa-chevron-right' : 'fa-chevron-down'} me-chevron`} />
                      <i className={`fas ${SECTION_ICONS[s.id] || 'fa-cog'}`}
                        style={{ color: SECTION_COLORS[s.id] || 'var(--color-primary)' }} />
                      {s.label}
                      <span className="text-xs fw-normal text-muted">
                        ({sectionFields.length})
                      </span>
                    </h3>
                    {!isCollapsed && (
                      <div className="card">
                        {sectionFields.map(field => (
                          <ConfigFieldRenderer
                            key={field.path}
                            field={field}
                            value={values[field.path]}
                            onChange={val => handleFieldChange(field.path, val)}
                            onRemove={handleRemoveField}
                            annotation={field.path === 'context_size' ? vramAnnotation : undefined}
                          />
                        ))}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        </>
      )}
    </div>
    </FormContextProvider>
  )
}
