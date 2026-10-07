import { useState, useEffect, useMemo, useRef, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { useParams, useNavigate, useLocation, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentsApi, skillsApi, chatApi } from '../utils/api'
import { AGENT_TEMPLATES, diffConfig, displayValue, maskSecrets, summarise } from '../utils/agentConfigTools'
import './agents.css'
// eslint-disable-next-line no-unused-vars
import SearchableModelSelect from '../components/SearchableModelSelect'
// eslint-disable-next-line no-unused-vars
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
import { CAP_CHAT, CAP_TRANSCRIPT, CAP_TTS } from '../utils/capabilities'
// eslint-disable-next-line no-unused-vars
import Toggle from '../components/Toggle'
// eslint-disable-next-line no-unused-vars
import SettingRow from '../components/SettingRow'
import Icon from '../components/Icon'

// --- MCP STDIO helpers ---

function parseStdioServers(value) {
  if (!value) return []
  if (Array.isArray(value)) {
    return value.map(s => ({
      name: s.name || '',
      command: s.cmd || s.command || '',
      args: Array.isArray(s.args) ? [...s.args] : [],
      env: Array.isArray(s.env) ? [...s.env]
        : (s.env && typeof s.env === 'object') ? Object.entries(s.env).map(([k, v]) => `${k}=${v}`) : [],
    }))
  }
  if (typeof value === 'string') {
    try {
      const parsed = JSON.parse(value)
      if (parsed.mcpServers) {
        return Object.entries(parsed.mcpServers).map(([name, srv]) => ({
          name,
          command: srv.command || '',
          args: srv.args || [],
          env: Object.entries(srv.env || {}).map(([k, v]) => `${k}=${v}`),
        }))
      }
      if (Array.isArray(parsed)) return parseStdioServers(parsed)
    } catch { /* not valid JSON */ }
  }
  return []
}

function buildStdioJson(list) {
  const mcpServers = {}
  const usedKeys = new Set()
  list.forEach((item, index) => {
    let key = item.name?.trim() || `server${index}`
    while (usedKeys.has(key)) key = `${key}_${index}`
    usedKeys.add(key)
    const envMap = {}
    for (const e of (item.env || [])) {
      const eqIdx = e.indexOf('=')
      if (eqIdx > 0) envMap[e.slice(0, eqIdx)] = e.slice(eqIdx + 1)
    }
    mcpServers[key] = { command: item.command || '', args: item.args || [], env: envMap }
  })
  return JSON.stringify({ mcpServers }, null, 2)
}

// --- Form field components ---

// eslint-disable-next-line no-unused-vars
function FormField({ field, value, onChange, disabled }) {
  const id = `field-${field.name}`
  const label = field.required
    ? <>{field.label} <span className="text-error">*</span></>
    : field.label

  switch (field.type) {
    case 'checkbox':
      return (
        <SettingRow label={label} description={field.helpText}>
          <Toggle
            checked={value === true || value === 'true'}
            onChange={(v) => onChange(field.name, v)}
            disabled={disabled}
          />
        </SettingRow>
      )
    case 'select':
      return (
        <SettingRow label={label} description={field.helpText}>
          <select id={id} className="input col-w-200" value={value ?? ''} onChange={(e) => onChange(field.name, e.target.value)} disabled={disabled}>
            <option value="">— Select —</option>
            {(field.options || []).map(opt => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        </SettingRow>
      )
    case 'textarea':
      return (
        <div className="list-row">
          <div style={{ fontSize: '0.875rem', fontWeight: 500, marginBottom: 4 }}>{label}</div>
          {field.helpText && <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-xs)' }}>{field.helpText}</div>}
          <textarea
            id={id}
            className="textarea"
            value={value ?? ''}
            onChange={(e) => onChange(field.name, e.target.value)}
            placeholder={field.placeholder || ''}
            rows={5}
            disabled={disabled}
            style={field.name.includes('prompt') || field.name.includes('template') || field.name.includes('script')
              ? { fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' } : undefined}
          />
        </div>
      )
    case 'number':
      return (
        <SettingRow label={label} description={field.helpText}>
          <input
            id={id} className="input col-w-120" type="number"
            value={value ?? ''} onChange={(e) => onChange(field.name, e.target.value)}
            placeholder={field.placeholder || ''} min={field.min} max={field.max} step={field.step}
            disabled={disabled}
          />
        </SettingRow>
      )
    default: {
      const isModelField = /^(model|multimodal_model|transcription_model|tts_model|embedding_model)$/.test(field.name)
      if (isModelField && !disabled && !field.disabled) {
        const capabilityMap = {
          model: CAP_CHAT,
          multimodal_model: CAP_CHAT,
          transcription_model: CAP_TRANSCRIPT,
          tts_model: CAP_TTS,
          embedding_model: undefined,
        }
        return (
          <SettingRow label={label} description={field.helpText}>
            <SearchableModelSelect
              value={value ?? ''}
              onChange={(v) => onChange(field.name, v)}
              capability={capabilityMap[field.name]}
              placeholder={field.placeholder || 'Type or select a model...'}
              style={{ width: 250 }}
            />
          </SettingRow>
        )
      }
      return (
        <SettingRow label={label} description={field.helpText}>
          <input
            id={id} className="input" type={field.type === 'password' ? 'password' : 'text'}
            style={{ width: field.type === 'password' ? 200 : 250 }}
            value={value ?? ''} onChange={(e) => onChange(field.name, e.target.value)}
            placeholder={field.placeholder || ''} required={field.required}
            disabled={disabled || field.disabled}
          />
        </SettingRow>
      )
    }
  }
}

// --- ConfigForm for connectors/actions/filters/dynamic_prompts ---

// eslint-disable-next-line no-unused-vars
function ConfigForm({ items, fieldGroups, onChange, onRemove, onAdd, itemType, typeField, addButtonText }) {
  const typeOptions = [
    { value: '', label: `Select a ${itemType} type` },
    ...(fieldGroups || []).map(g => ({ value: g.name, label: g.label })),
  ]

  const parseConfig = (item) => {
    if (!item?.config) return {}
    try { return typeof item.config === 'string' ? JSON.parse(item.config || '{}') : item.config }
    catch { return {} }
  }

  const handleConfigFieldChange = (index, fieldName, fieldValue, fieldType) => {
    const config = parseConfig(items[index])
    config[fieldName] = fieldType === 'checkbox' ? (fieldValue ? 'true' : 'false') : String(fieldValue)
    onChange(index, { ...items[index], config: JSON.stringify(config) })
  }

  const label = itemType.charAt(0).toUpperCase() + itemType.slice(1).replace('_', ' ')

  if (!fieldGroups?.length) {
    return <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>No {itemType} types available.</p>
  }

  return (
    <div>
      {items.map((item, index) => {
        const typeName = (item || {})[typeField] || ''
        const fieldGroup = fieldGroups.find(g => g.name === typeName)
        const config = parseConfig(item)
        return (
          <div key={index} className="card pad-md mb-md">
            <div className="hstack hstack--between mb-md">
              <h4 style={{ margin: 0, fontWeight: 600 }}>{label} #{index + 1}</h4>
              <button type="button" className="btn btn-danger btn-sm" onClick={() => onRemove(index)}>
                <Icon name="close" />
              </button>
            </div>
            <div className="form-group">
              <label className="form-label">{label} Type</label>
              <select className="input" value={typeName} onChange={(e) => onChange(index, { ...items[index], [typeField]: e.target.value, config: '{}' })}>
                {typeOptions.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </div>
            {fieldGroup?.fields?.map(f => {
              const val = config[f.name] ?? ''
              const fieldLabel = <>{f.label}{f.required && <span className="text-error"> *</span>}</>
              if (f.type === 'checkbox') {
                return (
                  <SettingRow key={f.name} label={fieldLabel} description={f.helpText}>
                    <Toggle checked={val === 'true' || val === true} onChange={(v) => handleConfigFieldChange(index, f.name, v, 'checkbox')} />
                  </SettingRow>
                )
              }
              if (f.type === 'textarea') {
                return (
                  <div key={f.name} className="list-row">
                    <div style={{ fontSize: '0.875rem', fontWeight: 500, marginBottom: 4 }}>{fieldLabel}</div>
                    {f.helpText && <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-xs)' }}>{f.helpText}</div>}
                    <textarea className="textarea" value={val} onChange={(e) => handleConfigFieldChange(index, f.name, e.target.value, 'text')} rows={3} placeholder={f.placeholder} />
                  </div>
                )
              }
              if (f.type === 'select') {
                return (
                  <SettingRow key={f.name} label={fieldLabel} description={f.helpText}>
                    <select className="input col-w-200" value={val} onChange={(e) => handleConfigFieldChange(index, f.name, e.target.value, 'text')}>
                      <option value="">— Select —</option>
                      {(f.options || []).map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                    </select>
                  </SettingRow>
                )
              }
              return (
                <SettingRow key={f.name} label={fieldLabel} description={f.helpText}>
                  <input
                    className="input" type={f.type === 'number' ? 'number' : f.type === 'password' ? 'password' : 'text'}
                    style={{ width: f.type === 'number' ? 120 : 200 }}
                    value={val} onChange={(e) => handleConfigFieldChange(index, f.name, e.target.value, f.type)}
                    placeholder={f.placeholder} min={f.min} max={f.max} step={f.step}
                  />
                </SettingRow>
              )
            })}
          </div>
        )
      })}
      <button type="button" className="btn btn-secondary" onClick={onAdd}>
        <Icon name="plus" /> {addButtonText}
      </button>
    </div>
  )
}

// --- Section definitions ---

const SECTIONS = [
  { id: 'BasicInfo', icon: 'info', label: 'Basic Info' },
  { id: 'ModelSettings', icon: 'brain', label: 'Model Settings' },
  { id: 'MemorySettings', icon: 'database', label: 'Memory' },
  { id: 'PromptsGoals', icon: 'target', label: 'Prompts & Goals' },
  { id: 'AdvancedSettings', icon: 'settings', label: 'Advanced' },
  { id: 'MCP', icon: 'server', label: 'MCP Servers' },
  { id: 'connectors', icon: 'plug', label: 'Connectors' },
  { id: 'actions', icon: 'bolt', label: 'Actions' },
  { id: 'filters', icon: 'filter', label: 'Filters' },
  { id: 'dynamic_prompts', icon: 'sparkles', label: 'Dynamic Prompts' },
]

// Fields handled by custom editors in the MCP section
const CUSTOM_FIELDS = new Set(['mcp_stdio_servers'])

// Fields not implemented in the native executor (distributed mode).
// These are hidden from the form when meta.distributed is true.
const HIDDEN_IN_DISTRIBUTED = new Set([
  'mcp_prepare_script',
  'multimodal_model', 'transcription_model', 'transcription_language', 'tts_model',
  'plan_reviewer_model',
  'enable_planning', 'initiate_conversations', 'can_stop_itself',
  'scheduler_poll_interval', 'scheduler_task_template',
  'enable_reasoning', 'enable_reasoning_tool', // replaced by enable_reasoning_for_instruct
  'kb_auto_search', 'kb_as_tools', // replaced by kb_mode select
  'disable_sink_state', // always disabled in native executor
  'enable_kb_compaction', 'kb_compaction_interval', 'kb_compaction_summarize',
  'parallel_jobs', 'cancel_previous_on_new_message',
])

// --- Main component ---

export default function AgentCreate() {
  const { name } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('agents')
  const [searchParams] = useSearchParams()
  const userId = searchParams.get('user_id') || undefined
  const templateId = searchParams.get('template') || ''
  const isEdit = !!name
  const importedConfig = location.state?.importedConfig || null

  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  // Sections fold. Basics is open at first; the rest open on demand.
  const [openSections, setOpenSections] = useState(() => new Set(['BasicInfo']))
  const [savedConfig, setSavedConfig] = useState(null)
  const [previewOpen, setPreviewOpen] = useState(false)
  const [previewTab, setPreviewTab] = useState('config')
  const [draftText, setDraftText] = useState('')
  const [drafting, setDrafting] = useState(false)
  const [meta, setMeta] = useState(null)
  const [form, setForm] = useState({})
  // Snapshot of the form as first loaded, for the unsaved-changes guard.
  const initialFormRef = useRef(null)
  const [connectors, setConnectors] = useState([])
  const [actions, setActions] = useState([])
  const [filters, setFilters] = useState([])
  const [dynamicPrompts, setDynamicPrompts] = useState([])
  const [mcpHttpServers, setMcpHttpServers] = useState([])
  const [mcpJsonMode, setMcpJsonMode] = useState(false)
  const [mcpRawJson, setMcpRawJson] = useState('')
  const [stdioServers, setStdioServers] = useState([])
  const [availableSkills, setAvailableSkills] = useState([])
  const [selectedSkills, setSelectedSkills] = useState([])

  // Group metadata Fields by tags.section
  const fieldsBySection = useMemo(() => {
    if (!meta?.Fields) return {}
    const groups = {}
    for (const field of meta.Fields) {
      if (CUSTOM_FIELDS.has(field.name)) continue
      if (meta?.distributed && HIDDEN_IN_DISTRIBUTED.has(field.name)) continue
      const section = field.tags?.section || 'BasicInfo'
      if (!groups[section]) groups[section] = []
      groups[section].push(field)
    }
    return groups
  }, [meta])

  const visibleSections = useMemo(() => {
    let items = [...SECTIONS]
    // In distributed mode, hide LocalAGI-specific sections — use MCP Servers instead
    if (meta?.distributed) {
      const hiddenInDistributed = new Set(['actions', 'connectors', 'filters', 'dynamic_prompts'])
      items = items.filter(s => !hiddenInDistributed.has(s.id))
    }
    if (isEdit) items.push({ id: 'export', icon: 'download', label: 'Export' })
    return items
  }, [isEdit, meta])

  useEffect(() => {
    const init = async () => {
      try {
        const [metaData, config, skillsList] = await Promise.all([
          agentsApi.configMeta().catch(() => null),
          isEdit ? agentsApi.getConfig(name, userId).catch(() => null) : Promise.resolve(null),
          skillsApi.list().catch(() => null),
        ])
        if (metaData) setMeta(metaData)
        if (skillsList?.skills) setAvailableSkills(skillsList.skills)

        // Build defaults from metadata
        const initialForm = {}
        if (metaData?.Fields) {
          for (const field of metaData.Fields) {
            if (CUSTOM_FIELDS.has(field.name)) continue
            if (field.type === 'checkbox') {
              initialForm[field.name] = field.defaultValue != null ? !!field.defaultValue : false
            } else {
              initialForm[field.name] = field.defaultValue != null ? field.defaultValue : ''
            }
          }
        }

        // Override with existing config when editing or importing
        const sourceConfig = config || importedConfig
        if (sourceConfig) {
          for (const key of Object.keys(initialForm)) {
            if (sourceConfig[key] !== undefined && sourceConfig[key] !== null) {
              initialForm[key] = sourceConfig[key]
            }
          }
          if (!initialForm.name && name) initialForm.name = name
          setConnectors(Array.isArray(sourceConfig.connectors) ? sourceConfig.connectors : [])
          setActions(Array.isArray(sourceConfig.actions) ? sourceConfig.actions : [])
          setFilters(Array.isArray(sourceConfig.filters) ? sourceConfig.filters : [])
          setDynamicPrompts(Array.isArray(sourceConfig.dynamic_prompts) ? sourceConfig.dynamic_prompts : [])
          setMcpHttpServers(Array.isArray(sourceConfig.mcp_servers) ? sourceConfig.mcp_servers : [])
          setStdioServers(parseStdioServers(sourceConfig.mcp_stdio_servers))
          if (Array.isArray(sourceConfig.selected_skills)) setSelectedSkills(sourceConfig.selected_skills)
        }

        // A starting point fills the form; nothing is saved until the person saves.
        if (!sourceConfig && templateId) {
          const tpl = AGENT_TEMPLATES.find(x => x.id === templateId)
          if (tpl) {
            for (const key of ['name', 'description', 'system_prompt']) {
              if (key in initialForm && tpl[key]) initialForm[key] = tpl[key]
            }
          }
        }

        if (config) setSavedConfig(config)
        initialFormRef.current = initialForm
        setForm(initialForm)
      } catch (err) {
        addToast(`Failed to load configuration: ${err.message}`, 'error')
      } finally {
        setLoading(false)
      }
    }
    init()
  }, [name, isEdit, importedConfig, templateId, addToast])

  const updateField = (fieldName, value) => {
    setForm(prev => ({ ...prev, [fieldName]: value }))
  }

  // The config exactly as it is saved: the form, the lists and the MCP and
  // skills choices. The preview shows this object and Save sends it.
  const buildPayload = () => {
    const payload = { ...form }
    // Convert number fields
    if (meta?.Fields) {
      for (const field of meta.Fields) {
        if (field.type === 'number' && payload[field.name] !== '' && payload[field.name] != null) {
          payload[field.name] = Number(payload[field.name])
        }
      }
    }
    payload.connectors = connectors
    payload.actions = actions
    payload.filters = filters
    payload.dynamic_prompts = dynamicPrompts
    payload.mcp_servers = mcpHttpServers.filter(s => s.url)
    // Send STDIO servers as JSON string in expected format
    if (mcpJsonMode && mcpRawJson.trim()) {
      // In JSON editor mode, use the raw JSON directly
      payload.mcp_stdio_servers = mcpRawJson
    } else if (stdioServers.length > 0) {
      payload.mcp_stdio_servers = buildStdioJson(stdioServers)
    }
    // Send selected skills
    if (selectedSkills.length > 0) {
      payload.selected_skills = selectedSkills
    }
    return payload
  }

  const handleSubmit = async (e) => {
    e?.preventDefault?.()
    if (!form.name?.toString().trim()) {
      addToast('Agent name is required', 'warning')
      return
    }
    if (!form.model?.toString().trim()) {
      addToast('Model is required', 'warning')
      return
    }
    setSaving(true)
    try {
      const payload = buildPayload()
      if (isEdit) {
        await agentsApi.update(name, payload, userId)
        addToast(`Agent "${form.name}" updated`, 'success')
      } else {
        await agentsApi.create(payload)
        addToast(`Agent "${form.name}" created`, 'success')
      }
      navigate('/app/agents')
    } catch (err) {
      addToast(`Save failed: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  // --- STDIO server handlers ---
  const addStdioServer = () => setStdioServers(prev => [...prev, { name: '', command: '', args: [], env: [] }])
  const removeStdioServer = (idx) => setStdioServers(prev => prev.filter((_, i) => i !== idx))
  const updateStdio = (idx, key, val) => setStdioServers(prev => { const n = [...prev]; n[idx] = { ...n[idx], [key]: val }; return n })
  const addArg = (si) => setStdioServers(prev => { const n = [...prev]; n[si] = { ...n[si], args: [...(n[si].args || []), ''] }; return n })
  const updateArg = (si, ai, val) => setStdioServers(prev => { const n = [...prev]; const a = [...(n[si].args || [])]; a[ai] = val; n[si] = { ...n[si], args: a }; return n })
  const removeArg = (si, ai) => setStdioServers(prev => { const n = [...prev]; n[si] = { ...n[si], args: n[si].args.filter((_, i) => i !== ai) }; return n })
  const addEnv = (si) => setStdioServers(prev => { const n = [...prev]; n[si] = { ...n[si], env: [...(n[si].env || []), ''] }; return n })
  const updateEnv = (si, ei, val) => setStdioServers(prev => { const n = [...prev]; const e = [...(n[si].env || [])]; e[ei] = val; n[si] = { ...n[si], env: e }; return n })
  const removeEnv = (si, ei) => setStdioServers(prev => { const n = [...prev]; n[si] = { ...n[si], env: n[si].env.filter((_, i) => i !== ei) }; return n })

  // --- HTTP MCP server handlers ---
  const addMcpHttp = () => setMcpHttpServers(prev => [...prev, { url: '', token: '' }])
  const removeMcpHttp = (idx) => setMcpHttpServers(prev => prev.filter((_, i) => i !== idx))
  const updateMcpHttp = (idx, key, val) => setMcpHttpServers(prev => { const n = [...prev]; n[idx] = { ...n[idx], [key]: val }; return n })

  // --- Render helpers ---

  const renderFieldSection = (sectionId) => {
    const fields = fieldsBySection[sectionId] || []
    if (!fields.length) {
      return <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>No fields available for this section.</p>
    }
    return fields
      .filter(field => {
        // Hide fields whose depends_on parent is falsy
        if (field.tags?.depends_on && !form[field.tags.depends_on]) return false
        return true
      })
      .map(field => (
        <FormField
          key={field.name}
          field={field.name === 'name' && isEdit ? { ...field, disabled: true, helpText: 'Agent name cannot be changed after creation' } : field}
          value={form[field.name]}
          onChange={updateField}
          disabled={field.name === 'name' && isEdit}
        />
      ))
  }

  const renderSection = (activeSection) => {
    switch (activeSection) {
      case 'BasicInfo':
      case 'ModelSettings':
      case 'MemorySettings':
      case 'PromptsGoals':
      case 'AdvancedSettings':
        return (
          <>
            {renderFieldSection(activeSection)}
            {/* Skills picker — shown only in AdvancedSettings when enable_skills is checked */}
            {activeSection === 'AdvancedSettings' && form.enable_skills && availableSkills.length > 0 && (
              <div style={{ marginTop: 'var(--spacing-lg)', borderTop: '1px solid var(--color-border)', paddingTop: 'var(--spacing-lg)' }}>
                <h4 className="agent-subsection-title">
                  <Icon name="puzzle" className="text-primary icon-before" />
                  Select Skills
                </h4>
                <p className="agent-section-desc">Choose which skills this agent can use. If none selected, all available skills are included.</p>
                <div className="mb-sm">
                  <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', cursor: 'pointer', fontWeight: 600, fontSize: '0.85rem' }}>
                    <input
                      type="checkbox"
                      checked={selectedSkills.length === 0}
                      onChange={(e) => {
                        if (e.target.checked) setSelectedSkills([])
                        else setSelectedSkills(availableSkills.map(s => s.name))
                      }}
                    />
                    All Skills (default)
                  </label>
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(250px, 1fr))', gap: 'var(--spacing-sm)' }}>
                  {availableSkills.map(skill => (
                    <label key={skill.name} style={{
                      display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-xs)',
                      padding: 'var(--spacing-sm)', borderRadius: 'var(--radius-sm)',
                      border: '1px solid var(--color-border)', cursor: 'pointer',
                      background: selectedSkills.includes(skill.name) || selectedSkills.length === 0 ? 'var(--color-bg-secondary)' : 'transparent',
                    }}>
                      <input
                        type="checkbox"
                        checked={selectedSkills.length === 0 || selectedSkills.includes(skill.name)}
                        onChange={(e) => {
                          if (selectedSkills.length === 0) {
                            // Switching from "all" to specific — select all except this one
                            setSelectedSkills(availableSkills.filter(s => s.name !== skill.name).map(s => s.name))
                          } else if (e.target.checked) {
                            const updated = [...selectedSkills, skill.name]
                            // If all selected, go back to "all" mode (empty array)
                            if (updated.length === availableSkills.length) setSelectedSkills([])
                            else setSelectedSkills(updated)
                          } else {
                            setSelectedSkills(selectedSkills.filter(n => n !== skill.name))
                          }
                        }}
                        style={{ marginTop: '2px' }}
                      />
                      <div>
                        <div className="fw-semibold text-base">{skill.name}</div>
                        {skill.description && (
                          <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: '2px' }}>
                            {skill.description.length > 80 ? skill.description.slice(0, 80) + '...' : skill.description}
                          </div>
                        )}
                      </div>
                    </label>
                  ))}
                </div>
              </div>
            )}
          </>
        )

      case 'MCP':
        return (
          <>
            {/* Mode toggle + import buttons */}
            <div style={{ marginBottom: 'var(--spacing-md)', display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center', flexWrap: 'wrap' }}>
              <button type="button" className={`btn btn-sm ${mcpJsonMode ? 'btn-secondary' : 'btn-primary'}`} onClick={() => {
                if (mcpJsonMode) {
                  // Switching from JSON to form — parse the JSON back
                  try {
                    const parsed = JSON.parse(mcpRawJson)
                    const servers = parsed.mcpServers || parsed
                    const newStdio = []
                    const newHttp = []
                    for (const [name, srv] of Object.entries(servers)) {
                      if (srv.command || srv.cmd) {
                        newStdio.push({
                          name: name,
                          command: srv.command || srv.cmd || '',
                          args: srv.args || [],
                          env: srv.env ? Object.entries(srv.env).map(([k, v]) => ({ key: k, value: v })) : [],
                        })
                      } else if (srv.url) {
                        newHttp.push({ url: srv.url, token: srv.token || srv.apiKey || '' })
                      }
                    }
                    setStdioServers(newStdio)
                    setMcpHttpServers(newHttp)
                  } catch (e) {
                    addToast(`Invalid JSON: ${e.message}`, 'error')
                    return
                  }
                } else {
                  // Switching from form to JSON — serialize current config
                  const mcpServers = {}
                  for (const s of stdioServers) {
                    const envObj = {}
                    for (const e of (s.env || [])) {
                      if (e.key) envObj[e.key] = e.value || ''
                    }
                    mcpServers[s.name || `server-${Object.keys(mcpServers).length + 1}`] = {
                      command: s.command || '',
                      args: s.args || [],
                      ...(Object.keys(envObj).length > 0 ? { env: envObj } : {}),
                    }
                  }
                  for (const h of mcpHttpServers) {
                    mcpServers[`http-${Object.keys(mcpServers).length + 1}`] = {
                      url: h.url || '',
                      ...(h.token ? { token: h.token } : {}),
                    }
                  }
                  setMcpRawJson(JSON.stringify({ mcpServers }, null, 2))
                }
                setMcpJsonMode(!mcpJsonMode)
              }}>
                <Icon name={mcpJsonMode ? 'list' : 'code'} />
                {mcpJsonMode ? ' Form Editor' : ' JSON Editor'}
              </button>
              <span style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>
                {mcpJsonMode ? 'Edit as Claude Desktop JSON format' : 'Configure MCP servers visually'}
              </span>
            </div>

            {mcpJsonMode ? (
              <div className="form-group">
                <label className="form-label">MCP Configuration (Claude Desktop format)</label>
                <textarea
                  className="input"
                  value={mcpRawJson}
                  onChange={(e) => setMcpRawJson(e.target.value)}
                  rows={16}
                  style={{ fontFamily: 'var(--font-mono)', fontSize: '0.85rem', whiteSpace: 'pre' }}
                  placeholder={'{\n  "mcpServers": {\n    "my-server": {\n      "command": "npx",\n      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],\n      "env": {}\n    }\n  }\n}'}
                />
              </div>
            ) : (
              <>
                {/* Other MCP metadata fields (mcp_prepare_script, etc.) */}
                {renderFieldSection('MCP')}

            {/* STDIO Servers */}
            <div className="mt-lg">
              <h4 className="agent-subsection-title">
                <Icon name="terminal" className="text-primary icon-before" />
                STDIO Servers
              </h4>
              <p className="agent-section-desc">Local command-based MCP servers (e.g. docker run).</p>
              {stdioServers.map((server, idx) => (
                <div key={idx} className="card pad-md mb-md">
                  <div className="hstack hstack--between mb-sm">
                    <span className="fw-semibold text-base">Server #{idx + 1}</span>
                    <button type="button" className="btn btn-danger btn-sm" onClick={() => removeStdioServer(idx)}>
                      <Icon name="close" />
                    </button>
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--spacing-sm)' }}>
                    <div className="form-group">
                      <label className="form-label">Name</label>
                      <input className="input" value={server.name || ''} onChange={(e) => updateStdio(idx, 'name', e.target.value)} placeholder="server-name" />
                    </div>
                    <div className="form-group">
                      <label className="form-label">Command</label>
                      <input className="input" value={server.command || ''} onChange={(e) => updateStdio(idx, 'command', e.target.value)} placeholder="/usr/bin/node" />
                    </div>
                  </div>
                  <div className="form-group">
                    <label className="form-label" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                      Arguments
                      <button type="button" className="btn btn-secondary btn-sm" onClick={() => addArg(idx)} style={{ fontSize: '0.7rem', padding: '2px 8px' }}>
                        <Icon name="plus" /> Add
                      </button>
                    </label>
                    {(server.args || []).length === 0 && <p style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>No arguments.</p>}
                    {(server.args || []).map((arg, ai) => (
                      <div key={ai} style={{ display: 'flex', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-xs)' }}>
                        <input className="input flex-1" value={arg} onChange={(e) => updateArg(idx, ai, e.target.value)} placeholder="argument" />
                        <button type="button" className="btn btn-danger btn-sm" onClick={() => removeArg(idx, ai)}><Icon name="close" /></button>
                      </div>
                    ))}
                  </div>
                  <div className="form-group">
                    <label className="form-label" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                      Environment Variables
                      <button type="button" className="btn btn-secondary btn-sm" onClick={() => addEnv(idx)} style={{ fontSize: '0.7rem', padding: '2px 8px' }}>
                        <Icon name="plus" /> Add
                      </button>
                    </label>
                    {(server.env || []).length === 0 && <p style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>No environment variables.</p>}
                    {(server.env || []).map((env, ei) => (
                      <div key={ei} style={{ display: 'flex', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-xs)' }}>
                        <input className="input flex-1" value={env} onChange={(e) => updateEnv(idx, ei, e.target.value)} placeholder="KEY=VALUE" />
                        <button type="button" className="btn btn-danger btn-sm" onClick={() => removeEnv(idx, ei)}><Icon name="close" /></button>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
              <button type="button" className="btn btn-secondary" onClick={addStdioServer}>
                <Icon name="plus" /> Add STDIO Server
              </button>
            </div>

            {/* HTTP Servers */}
            <div className="mt-lg">
              <h4 className="agent-subsection-title">
                <Icon name="globe" className="text-primary icon-before" />
                HTTP Servers
              </h4>
              <p className="agent-section-desc">MCP servers connected over HTTP.</p>
              {mcpHttpServers.map((server, idx) => (
                <div key={idx} className="card pad-md mb-md">
                  <div className="hstack hstack--between mb-sm">
                    <span className="fw-semibold text-base">HTTP Server #{idx + 1}</span>
                    <button type="button" className="btn btn-danger btn-sm" onClick={() => removeMcpHttp(idx)}>
                      <Icon name="close" />
                    </button>
                  </div>
                  {(meta?.MCPServers || [{ name: 'url', label: 'URL', type: 'text' }, { name: 'token', label: 'API Key', type: 'password' }]).map(f => (
                    <div key={f.name} className="form-group">
                      <label className="form-label">{f.label}{f.required && <span className="text-error"> *</span>}</label>
                      <input
                        className="input" type={f.type === 'password' ? 'password' : 'text'}
                        value={server[f.name] || ''} onChange={(e) => updateMcpHttp(idx, f.name, e.target.value)}
                        placeholder={f.placeholder}
                      />
                    </div>
                  ))}
                </div>
              ))}
              <button type="button" className="btn btn-secondary" onClick={addMcpHttp}>
                <Icon name="plus" /> Add HTTP Server
              </button>
            </div>
              </>
            )}
          </>
        )

      case 'connectors':
        return (
          <>
            <p className="agent-section-desc">Configure connectors that this agent uses to communicate with external services.</p>
            <ConfigForm
              items={connectors}
              fieldGroups={meta?.Connectors}
              onChange={(idx, item) => { const n = [...connectors]; n[idx] = item; setConnectors(n) }}
              onRemove={(idx) => setConnectors(connectors.filter((_, i) => i !== idx))}
              onAdd={() => setConnectors([...connectors, { type: '', config: '{}' }])}
              typeField="type" itemType="connector" addButtonText="Add Connector"
            />
          </>
        )

      case 'actions':
        return (
          <>
            <p className="agent-section-desc">Configure actions the agent can perform.</p>
            <ConfigForm
              items={actions}
              fieldGroups={meta?.Actions}
              onChange={(idx, item) => { const n = [...actions]; n[idx] = item; setActions(n) }}
              onRemove={(idx) => setActions(actions.filter((_, i) => i !== idx))}
              onAdd={() => setActions([...actions, { name: '', config: '{}' }])}
              typeField="name" itemType="action" addButtonText="Add Action"
            />
          </>
        )

      case 'filters':
        return (
          <>
            <p className="agent-section-desc">Filters and triggers that control which messages the agent processes.</p>
            <ConfigForm
              items={filters}
              fieldGroups={meta?.Filters}
              onChange={(idx, item) => { const n = [...filters]; n[idx] = item; setFilters(n) }}
              onRemove={(idx) => setFilters(filters.filter((_, i) => i !== idx))}
              onAdd={() => setFilters([...filters, { type: '', config: '{}' }])}
              typeField="type" itemType="filter" addButtonText="Add Filter"
            />
          </>
        )

      case 'dynamic_prompts':
        return (
          <>
            <p className="agent-section-desc">Dynamic prompts that augment agent context at runtime.</p>
            <ConfigForm
              items={dynamicPrompts}
              fieldGroups={meta?.DynamicPrompts}
              onChange={(idx, item) => { const n = [...dynamicPrompts]; n[idx] = item; setDynamicPrompts(n) }}
              onRemove={(idx) => setDynamicPrompts(dynamicPrompts.filter((_, i) => i !== idx))}
              onAdd={() => setDynamicPrompts([...dynamicPrompts, { type: '', config: '{}' }])}
              typeField="type" itemType="dynamic prompt" addButtonText="Add Dynamic Prompt"
            />
          </>
        )

      case 'export':
        return (
          <div>
            <p className="agent-section-desc">Download the full agent configuration as a JSON file.</p>
            <a
              href={`/api/agents/${encodeURIComponent(name)}/export`}
              className="btn btn-primary"
              style={{ display: 'inline-flex', alignItems: 'center', textDecoration: 'none' }}
            >
              <Icon name="download" className="icon-before" /> Export Agent
            </a>
          </div>
        )

      default:
        return null
    }
  }

  if (loading) {
    return (
      <div className="page page--medium loading-center">
        <Icon name="spinner" spin className="icon-xl text-primary" />
      </div>
    )
  }

  const dirty = initialFormRef.current != null &&
    JSON.stringify(form) !== JSON.stringify(initialFormRef.current)

  const leave = () => navigate(isEdit ? `/app/agents/${encodeURIComponent(name)}${userId ? `?user_id=${encodeURIComponent(userId)}` : ''}` : '/app/agents')

  // What each fold says about itself: ready mark, one line, and, when editing,
  // how many of its fields differ from the saved agent.
  const payload = buildPayload()
  const baseline = {}
  if (savedConfig) for (const k of Object.keys(payload)) if (k in savedConfig) baseline[k] = savedConfig[k]
  const diff = savedConfig ? diffConfig(baseline, payload) : diffConfig({}, payload)
  const changedKeys = new Set(savedConfig ? diff.map(d => d.key) : [])
  const listOf = { connectors, actions, filters, dynamic_prompts: dynamicPrompts }

  const foldInfo = (s) => {
    const fields = fieldsBySection[s.id] || []
    let keys = fields.map(f => f.name)
    let summary = ''
    let state = 'empty'
    if (s.id in listOf) {
      const n = listOf[s.id].length
      keys = [s.id]
      summary = n ? t('create.countConfigured', { count: n }) : t('create.noneYet')
      state = n ? 'ready' : 'empty'
    } else if (s.id === 'MCP') {
      keys = [...keys, 'mcp_servers', 'mcp_stdio_servers']
      const n = stdioServers.length + mcpHttpServers.filter(x => x.url).length
      summary = n ? t('create.countServers', { count: n }) : t('create.noneYet')
      state = n ? 'ready' : 'empty'
    } else if (s.id === 'export') {
      summary = t('create.exportSummary')
      state = 'ready'
    } else {
      summary = summarise(fields, form)
      if (s.id === 'AdvancedSettings') {
        keys = [...keys, 'selected_skills']
        if (form.enable_skills && selectedSkills.length) summary = [summary, t('create.countSkills', { count: selectedSkills.length })].filter(Boolean).join(', ')
      }
      state = summary ? 'ready' : 'empty'
    }
    if (s.id === 'BasicInfo' && !form.name?.toString().trim()) { state = 'needs'; summary = t('create.needsName') }
    if (s.id === 'ModelSettings' && !form.model?.toString().trim()) { state = 'needs'; summary = t('create.needsModel') }
    const changed = keys.filter(k => changedKeys.has(k)).length
    return { summary, state, changed }
  }

  const toggleSection = (id) => setOpenSections(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  const applyTemplate = (tpl) => {
    setForm(prev => {
      const next = { ...prev }
      for (const key of ['name', 'description', 'system_prompt']) {
        if (!(key in prev)) continue
        if (key === 'name' && isEdit) continue
        next[key] = tpl[key] || ''
      }
      return next
    })
    setOpenSections(prev => new Set([...prev, 'BasicInfo']))
  }

  const draft = async () => {
    const sentence = draftText.trim()
    if (!sentence) return
    if (!form.model?.toString().trim()) {
      addToast(t('create.draftNeedsModel'), 'warning')
      setOpenSections(prev => new Set([...prev, 'ModelSettings']))
      return
    }
    setDrafting(true)
    try {
      const res = await chatApi.complete({
        model: form.model,
        temperature: 0.2,
        messages: [
          { role: 'system', content: 'You write the configuration of a software agent. Answer with one JSON object and nothing else. Keys: "name" (lowercase words joined by dashes, at most 30 characters), "description" (one sentence), "system_prompt" (the instructions the agent follows, three to six sentences).' },
          { role: 'user', content: sentence },
        ],
      })
      const text = res?.choices?.[0]?.message?.content || ''
      const match = text.match(/\{[\s\S]*\}/)
      const parsed = match ? JSON.parse(match[0]) : null
      if (!parsed || typeof parsed !== 'object') throw new Error(t('create.draftBad'))
      setForm(prev => {
        const next = { ...prev }
        for (const key of ['name', 'description', 'system_prompt']) {
          if (key in prev && typeof parsed[key] === 'string' && parsed[key].trim() && !(key === 'name' && isEdit)) next[key] = parsed[key].trim()
        }
        return next
      })
      setOpenSections(prev => new Set([...prev, 'BasicInfo', 'PromptsGoals']))
      addToast(t('create.drafted'), 'success')
    } catch (err) {
      addToast(t('create.draftFailed', { message: err.message }), 'error')
    } finally {
      setDrafting(false)
    }
  }

  const title = isEdit ? t('create.titleEdit', { name }) : importedConfig ? t('create.titleImport') : t('create.titleCreate')
  const saveLabel = isEdit ? t('create.save') : importedConfig ? t('create.titleImport') : t('create.titleCreate')

  return (
    <div className="page page--medium ag-page" data-testid="agent-edit">
      <UnsavedChangesGuard when={dirty && !saving} />
      <form onSubmit={handleSubmit} noValidate className="ag-edit">
        <header className="ag-edit__head">
          <div>
            <button type="button" className="ag-back" onClick={leave}><Icon name="arrow-left" /> {isEdit ? name : t('agent.back')}</button>
            <h1>{title}</h1>
            <p className="ag-edit__sub">{isEdit ? t('create.subEdit') : t('create.subCreate')}</p>
          </div>
          <div className="ag-edit__acts">
            <button type="button" className="btn btn-secondary" onClick={() => { setPreviewTab('config'); setPreviewOpen(true) }} data-testid="agent-preview-open">
              <Icon name="eye" /> {t('create.preview')}
              {diff.length > 0 && <span className="dk-badge dk-badge--count">{diff.length}</span>}
            </button>
            <button type="button" className="btn btn-secondary" onClick={leave}>
              {t('create.discard')}
            </button>
            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving
                ? <><Icon name="spinner" spin /> {t('create.saving')}</>
                : <><Icon name="save" /> {saveLabel}</>
              }
            </button>
          </div>
        </header>

        {!isEdit && (
          <section className="ag-start" aria-labelledby="ag-start-h" data-testid="agent-start">
            <h2 id="ag-start-h" className="ag-eyebrow">{t('create.startFrom')}</h2>
            <div className="ag-start__row">
              {AGENT_TEMPLATES.map(tpl => (
                <button key={tpl.id} type="button" className="dk-chip" onClick={() => applyTemplate(tpl)} data-template={tpl.id}>
                  {t(`templates.${tpl.id}.label`)}
                </button>
              ))}
            </div>
            <div className="ag-draft">
              <input
                className="input"
                type="text"
                value={draftText}
                aria-label={t('create.draftLabel')}
                placeholder={t('create.draftPlaceholder')}
                onChange={(e) => setDraftText(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); draft() } }}
              />
              <button type="button" className="btn btn-secondary" disabled={drafting || !draftText.trim()} onClick={draft}>
                {drafting ? <Icon name="spinner" spin /> : <Icon name="sparkles" />} {t('create.draft')}
              </button>
            </div>
            <p className="ag-note">{t('create.draftNote')}</p>
          </section>
        )}

        <div className="ag-folds">
          {visibleSections.map(s => {
            const open = openSections.has(s.id)
            const info = foldInfo(s)
            return (
              <section key={s.id} className="ag-fold" data-open={open || undefined} data-section={s.id}>
                <h2 className="ag-fold__h">
                  <button
                    type="button"
                    className="ag-fold__button"
                    aria-expanded={open}
                    aria-controls={`ag-fold-${s.id}`}
                    onClick={() => toggleSection(s.id)}
                  >
                    <span className="ag-ready" data-state={info.state} aria-hidden="true"><Icon name={info.state === 'needs' ? 'warning' : 'check'} /></span>
                    <span className="ag-fold__title">{s.label}</span>
                    <span className="ag-fold__sum">{info.summary}</span>
                    {info.changed > 0 ? <span className="ag-badge-changed">{t('create.changed', { count: info.changed })}</span> : <span />}
                    <Icon name="chevron-down" className="ag-fold__chev" />
                  </button>
                </h2>
                {open && (
                  <div className="ag-fold__body" id={`ag-fold-${s.id}`}>
                    {renderSection(s.id)}
                  </div>
                )}
              </section>
            )
          })}
        </div>

        <div className="ag-foot">
          <button type="button" className="btn btn-secondary" onClick={leave}>
            {t('create.discard')}
          </button>
          <button type="submit" className="btn btn-primary" disabled={saving}>
            {saving
              ? <><Icon name="spinner" spin /> {t('create.saving')}</>
              : <><Icon name="save" /> {saveLabel}</>
            }
          </button>
        </div>
      </form>

      {previewOpen && (
        <PreviewSheet
          tab={previewTab}
          onTab={setPreviewTab}
          payload={payload}
          diff={diff}
          isEdit={isEdit}
          saving={saving}
          onSave={() => { setPreviewOpen(false); handleSubmit() }}
          onClose={() => setPreviewOpen(false)}
        />
      )}
    </div>
  )
}

// The config as it will be saved, and what differs from the saved agent.
// Secret values are hidden here; they are saved as typed.
// eslint-disable-next-line no-unused-vars
function PreviewSheet({ tab, onTab, payload, diff, isEdit, saving, onSave, onClose }) {
  const { t } = useTranslation('agents')
  const sheetRef = useRef(null)
  const closeRef = useRef(null)
  const openerRef = useRef(null)

  useEffect(() => {
    openerRef.current = document.activeElement
    closeRef.current?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose(); return }
      if (e.key !== 'Tab' || !sheetRef.current) return
      const focusable = Array.from(sheetRef.current.querySelectorAll('button:not([disabled]), [tabindex]:not([tabindex="-1"])'))
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', onKey, true)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      const el = openerRef.current
      if (el && document.contains(el)) el.focus?.()
    }
  }, [onClose])

  const shown = useCallback((v) => displayValue(maskSecrets(v)), [])
  const json = useMemo(() => JSON.stringify(maskSecrets(payload), null, 2), [payload])

  return createPortal(
    <div className="dk-sheet-veil" data-state="open" onMouseDown={onClose}>
      <div
        ref={sheetRef}
        className="dk-sheet dk-sheet--wide"
        role="dialog"
        aria-modal="true"
        aria-labelledby="agent-preview-title"
        data-state="open"
        data-testid="agent-preview"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id="agent-preview-title">{t('create.previewTitle')}</h2>
            <p className="dk-sheet-desc">{isEdit ? t('create.previewEdit') : t('create.previewNew')}</p>
          </div>
          <button ref={closeRef} type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('create.close')} onClick={onClose}>
            <Icon name="close" />
          </button>
        </div>
        <div className="dk-sheet-body">
          <div className="dk-tabs ag-sheet-tabs" role="tablist" aria-label={t('create.previewTitle')}>
            <button type="button" role="tab" className="dk-tab" aria-selected={tab === 'config'} onClick={() => onTab('config')}>{t('create.tabConfig')}</button>
            <button type="button" role="tab" className="dk-tab" aria-selected={tab === 'changes'} onClick={() => onTab('changes')}>
              {t('create.tabChanges')}{diff.length > 0 && <span className="dk-badge dk-badge--count">{diff.length}</span>}
            </button>
          </div>
          {tab === 'config' ? (
            <div className="dk-tabpanel" role="tabpanel" data-testid="agent-preview-config">
              <p className="ag-note ag-note--bottom">{t('create.configNote')}</p>
              <pre className="ag-pre">{json}</pre>
            </div>
          ) : (
            <div className="dk-tabpanel" role="tabpanel" data-testid="agent-preview-changes">
              {diff.length === 0 ? (
                <p className="ag-note">{isEdit ? t('create.noChanges') : t('create.nothingSet')}</p>
              ) : (
                <>
                  {!isEdit && <p className="ag-note ag-note--bottom">{t('create.allNew')}</p>}
                  <div className="ag-diff">
                    {diff.map(d => (
                      <div key={d.key} className="ag-diff__item" data-key={d.key}>
                        <span className="ag-diff__key">{d.key}</span>
                        <div className="ag-diff__pair">
                          <div className="ag-diff__side" data-side="before"><span className="ag-diff__label">{isEdit ? t('create.saved') : t('create.default')}</span>{shown(d.before) || t('create.empty')}</div>
                          <div className="ag-diff__side" data-side="after"><span className="ag-diff__label">{t('create.new')}</span>{shown(d.after) || t('create.empty')}</div>
                        </div>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </div>
          )}
        </div>
        <div className="dk-sheet-foot">
          <button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>{t('create.close')}</button>
          <button type="button" className="dk-btn dk-btn--primary" disabled={saving} onClick={onSave}>
            {isEdit ? t('create.save') : t('create.titleCreate')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
