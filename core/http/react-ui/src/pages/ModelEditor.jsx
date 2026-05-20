import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { useParams, useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import YAML from 'yaml'
import { modelsApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { useConfigMetadata } from '../hooks/useConfigMetadata'
import { useVramEstimate } from '../hooks/useVramEstimate'
import LoadingSpinner from '../components/LoadingSpinner'
import CodeEditor from '../components/CodeEditor'
import FieldBrowser from '../components/FieldBrowser'
import ConfigFieldRenderer from '../components/ConfigFieldRenderer'
import TemplateSelector from '../components/TemplateSelector'
import MODEL_TEMPLATES from '../utils/modelTemplates'

const SECTION_ICONS = {
  general: 'fa-cog', llm: 'fa-microchip', parameters: 'fa-sliders',
  templates: 'fa-file-code', functions: 'fa-wrench', reasoning: 'fa-brain',
  diffusers: 'fa-image', tts: 'fa-volume-up', pipeline: 'fa-code-branch',
  grpc: 'fa-server', agent: 'fa-robot', mcp: 'fa-plug', other: 'fa-ellipsis-h',
}

const SECTION_COLORS = {
  general: 'var(--color-primary)', llm: 'var(--color-accent)', parameters: 'var(--color-success)',
  templates: 'var(--color-warning)', functions: 'var(--color-info, var(--color-primary))',
  reasoning: 'var(--color-accent)', diffusers: 'var(--color-warning)', tts: 'var(--color-success)',
  pipeline: 'var(--color-accent)', grpc: 'var(--color-text-muted)', agent: 'var(--color-primary)',
  mcp: 'var(--color-accent)', other: 'var(--color-text-muted)',
}

function flattenConfig(obj, prefix = '') {
  const result = {}
  if (!obj || typeof obj !== 'object') return result
  for (const [key, val] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (val !== null && typeof val === 'object' && !Array.isArray(val)) {
      Object.assign(result, flattenConfig(val, path))
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
  const { name } = useParams()
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { sections, fields, loading: metaLoading, error: metaError } = useConfigMetadata()

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

  const contentRef = useRef(null)
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

  // Load raw YAML config (edit mode only)
  useEffect(() => {
    if (!name) return
    modelsApi.getEditConfig(name)
      .then(data => {
        const raw = data?.config || ''
        setYamlText(raw)
        setSavedYamlText(raw)

        // Parse YAML to get only the fields actually present in the file
        try {
          const parsed = YAML.parse(raw)
          const flat = flattenConfig(parsed || {})
          const active = new Set(Object.keys(flat))
          setValues(flat)
          setInitialValues(structuredClone(flat))
          setActiveFieldPaths(active)
        } catch {
          // If YAML parsing fails, start with empty state
          setValues({})
          setInitialValues({})
          setActiveFieldPaths(new Set())
        }
      })
      .catch(err => addToast(`Failed to load config: ${err.message}`, 'error'))
      .finally(() => setConfigLoading(false))
  }, [name, addToast])

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

  // Scroll tracking
  useEffect(() => {
    const container = contentRef.current
    if (!container || tab !== 'interactive') return
    const onScroll = () => {
      const containerTop = container.getBoundingClientRect().top
      let closest = activeSections[0]?.id
      let closestDist = Infinity
      for (const s of activeSections) {
        const el = sectionRefs.current[s.id]
        if (el) {
          const dist = Math.abs(el.getBoundingClientRect().top - containerTop - 8)
          if (dist < closestDist) { closestDist = dist; closest = s.id }
        }
      }
      if (closest) setActiveSection(closest)
    }
    container.addEventListener('scroll', onScroll, { passive: true })
    return () => container.removeEventListener('scroll', onScroll)
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
        <div style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', marginTop: 4 }}>
          <i className="fas fa-spinner fa-spin" style={{ marginRight: 4 }} />
          Estimating VRAM...
        </div>
      )
    }
    if (vramEstimate.vramDisplay) {
      return (
        <div style={{ fontSize: '0.6875rem', color: 'var(--color-warning)', marginTop: 4, fontWeight: 500 }}>
          <i className="fas fa-memory" style={{ marginRight: 4 }} />
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
        navigate(`/app/model-editor/${encodeURIComponent(modelName.trim())}`)
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
          if (parsed?.name) navigate(`/app/model-editor/${encodeURIComponent(parsed.name)}`)
          else navigate('/app/manage')
        } catch { navigate('/app/manage') }
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
          const flat = flattenConfig(parsed || {})
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
          navigate(`/app/model-editor/${encodeURIComponent(parsedName)}`, { replace: true })
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

  if (loading) return <div className="page page--medium" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>
  if (metaError) return <div className="page page--medium"><div className="empty-state"><p className="empty-state-text">Failed to load config metadata: {metaError}</p></div></div>

  return (
    <div className="page page--medium" style={{ padding: 0 }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: 'var(--spacing-lg) var(--spacing-lg) var(--spacing-md)',
      }}>
        <div>
          <h1 className="page-title">{isCreateMode ? 'Add Model' : 'Model Editor'}</h1>
          <p className="page-subtitle">
            {isCreateMode
              ? (showTemplateSelector ? 'Choose a model type to get started' : `New model${selectedTemplate ? ` — ${selectedTemplate.label}` : ''}`)
              : decodeURIComponent(name)}
          </p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
          <button className="btn btn-secondary" onClick={() => {
            if (isCreateMode && selectedTemplate) { setSelectedTemplate(null); setValues({}); setActiveFieldPaths(new Set()) }
            else navigate(isCreateMode ? '/app/models' : '/app/manage')
          }}>
            <i className="fas fa-arrow-left" /> Back
          </button>
          {!showTemplateSelector && tab === 'interactive' && (
            <button className={`btn ${isDirty ? 'btn-primary' : 'btn-secondary'}`} onClick={handleInteractiveSave} disabled={saving || !isDirty}>
              {saving ? <><LoadingSpinner size="sm" /> Saving...</> : <><i className="fas fa-save" /> {isCreateMode ? 'Create Model' : (isDirty ? 'Save Changes' : 'Saved')}</>}
            </button>
          )}
          {!showTemplateSelector && tab === 'yaml' && (
            <button className={`btn ${isDirty ? 'btn-primary' : 'btn-secondary'}`} onClick={handleYamlSave} disabled={saving || !isDirty}>
              {saving ? <><LoadingSpinner size="sm" /> Saving...</> : <><i className="fas fa-save" /> {isCreateMode ? 'Create Model' : (isDirty ? 'Save Changes' : 'Saved')}</>}
            </button>
          )}
        </div>
      </div>

      {/* Template selector (create mode, step 1) */}
      {showTemplateSelector && <TemplateSelector onSelect={handleSelectTemplate} />}

      {/* Tabs (hidden during template selection) */}
      {!showTemplateSelector && (
        <div>
          <div style={{
            display: 'flex', gap: 0, padding: '0 var(--spacing-lg)',
            borderBottom: '1px solid var(--color-border)',
          }}>
            {['interactive', 'yaml'].map(t => {
              const active = tab === t
              const blocked = !active && isDirty
              return (
                <button
                  key={t}
                  onClick={() => {
                    if (active) return
                    if (blocked) { setTabSwitchWarning(true); return }
                    setTabSwitchWarning(false)
                    setTab(t)
                  }}
                  style={{
                    padding: 'var(--spacing-sm) var(--spacing-md)', border: 'none',
                    cursor: blocked ? 'not-allowed' : 'pointer',
                    background: 'transparent', fontSize: '0.875rem',
                    fontWeight: active ? 600 : 400,
                    opacity: blocked ? 0.5 : 1,
                    color: active ? 'var(--color-primary)' : 'var(--color-text-secondary)',
                    borderBottom: active ? '2px solid var(--color-primary)' : '2px solid transparent',
                    transition: 'all 150ms',
                  }}
                >
                  <i className={`fas ${t === 'interactive' ? 'fa-sliders' : 'fa-code'}`} style={{ marginRight: 6 }} />
                  {t === 'interactive' ? 'Interactive' : 'YAML'}
                </button>
              )
            })}
          </div>
          {tabSwitchWarning && isDirty && (
            <div style={{
              display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
              padding: 'var(--spacing-sm) var(--spacing-lg)',
              fontSize: '0.8125rem', color: 'var(--color-warning, #f59e0b)',
              background: 'var(--color-warning-light, rgba(245, 158, 11, 0.08))',
            }}>
              <i className="fas fa-exclamation-triangle" />
              <span>Save or discard changes before switching tabs.</span>
              <button
                className="btn btn-secondary"
                style={{ marginLeft: 'auto', padding: '2px 10px', fontSize: '0.75rem' }}
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
                Discard &amp; Switch
              </button>
            </div>
          )}
        </div>
      )}

      {/* YAML Tab */}
      {!showTemplateSelector && tab === 'yaml' && (
        <div style={{ padding: '0 var(--spacing-lg) var(--spacing-lg)' }}>
          {isCreateMode && (
            <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-sm)' }}>
              Edit the YAML directly. The model name must be set in the YAML for create to work.
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
            <div style={{ padding: '0 var(--spacing-lg)', marginBottom: 'var(--spacing-md)' }}>
              <div className="card" style={{ padding: 'var(--spacing-md)' }}>
                <label className="form-label" style={{ fontWeight: 600 }}>
                  <i className="fas fa-tag" style={{ marginRight: '6px', color: 'var(--color-primary)' }} />
                  Model Name
                </label>
                <input
                  className="input"
                  type="text"
                  value={values['name'] || ''}
                  onChange={e => handleFieldChange('name', e.target.value)}
                  placeholder="my-model-name"
                  style={{ maxWidth: 400 }}
                />
                <p style={{ marginTop: 'var(--spacing-xs)', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
                  Use letters, numbers, hyphens, underscores, and dots only.
                </p>
              </div>
            </div>
          )}

          {/* Field browser */}
          <div style={{ padding: '0 var(--spacing-lg)' }}>
            <FieldBrowser
              fields={fields}
              activeFieldPaths={activeFieldPaths}
              onAddField={handleAddField}
            />
          </div>

          {/* Two-column layout */}
          <div style={{ display: 'flex', gap: 0, minHeight: 'calc(100vh - 340px)' }}>
            {/* Sidebar */}
            <nav style={{
              width: 180, flexShrink: 0, padding: '0 var(--spacing-sm)',
              position: 'sticky', top: 0, alignSelf: 'flex-start',
            }}>
              {activeSections.map(s => (
                <button
                  key={s.id}
                  onClick={() => scrollTo(s.id)}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
                    width: '100%', padding: '8px 12px',
                    background: activeSection === s.id ? 'var(--color-primary-light)' : 'transparent',
                    border: 'none', borderRadius: 'var(--radius-md)', cursor: 'pointer',
                    color: activeSection === s.id ? 'var(--color-primary)' : 'var(--color-text-secondary)',
                    fontSize: '0.8125rem', fontWeight: activeSection === s.id ? 600 : 400,
                    textAlign: 'left', transition: 'all 150ms', marginBottom: 2,
                    borderLeft: activeSection === s.id ? '2px solid var(--color-primary)' : '2px solid transparent',
                  }}
                >
                  <i className={`fas ${SECTION_ICONS[s.id] || 'fa-cog'}`} style={{
                    width: 16, textAlign: 'center', fontSize: '0.75rem',
                    color: activeSection === s.id ? (SECTION_COLORS[s.id] || 'var(--color-primary)') : 'var(--color-text-muted)',
                  }} />
                  {s.label}
                  <span style={{ marginLeft: 'auto', fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
                    {fieldsBySection[s.id]?.length || 0}
                  </span>
                </button>
              ))}
              {activeSections.length === 0 && (
                <div style={{ padding: '12px', fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>
                  Use the search bar above to add fields
                </div>
              )}
            </nav>

            {/* Content */}
            <div
              ref={contentRef}
              style={{
                flex: 1, overflow: 'auto', padding: '0 var(--spacing-lg) var(--spacing-xl) var(--spacing-md)',
                maxHeight: 'calc(100vh - 340px)',
              }}
            >
              {activeSections.length === 0 && (
                <div className="card" style={{ padding: 'var(--spacing-xl)', textAlign: 'center' }}>
                  <i className="fas fa-sliders" style={{ fontSize: '2rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-md)' }} />
                  <h3 style={{ marginBottom: 'var(--spacing-sm)' }}>No fields configured</h3>
                  <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem' }}>
                    Use the search bar above to find and add configuration fields.
                  </p>
                </div>
              )}

              {activeSections.map(s => {
                const sectionFields = fieldsBySection[s.id] || []
                const isCollapsed = collapsedSections.has(s.id)
                return (
                  <div key={s.id} ref={el => sectionRefs.current[s.id] = el} style={{ marginBottom: 'var(--spacing-xl)' }}>
                    <h3
                      onClick={() => toggleSection(s.id)}
                      style={{
                        fontSize: '1rem', fontWeight: 700, cursor: 'pointer', userSelect: 'none',
                        display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
                        marginBottom: isCollapsed ? 0 : 'var(--spacing-md)',
                      }}
                    >
                      <i className={`fas ${isCollapsed ? 'fa-chevron-right' : 'fa-chevron-down'}`}
                        style={{ fontSize: '0.625rem', width: 12, color: 'var(--color-text-muted)' }} />
                      <i className={`fas ${SECTION_ICONS[s.id] || 'fa-cog'}`}
                        style={{ color: SECTION_COLORS[s.id] || 'var(--color-primary)' }} />
                      {s.label}
                      <span style={{ fontSize: '0.75rem', fontWeight: 400, color: 'var(--color-text-muted)' }}>
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
  )
}
