import { useState, useEffect, useMemo } from 'react'
import { useParams, useNavigate, useLocation, useOutletContext, useSearchParams } from 'react-router-dom'
import { agentsApi } from '../utils/api'
import SearchableModelSelect from '../components/SearchableModelSelect'
import Toggle from '../components/Toggle'
import SettingRow from '../components/SettingRow'

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

function FormField({ field, value, onChange, disabled }) {
  const id = `field-${field.name}`
  const label = field.required
    ? <>{field.label} <span style={{ color: 'var(--color-error)' }}>*</span></>
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
          <select id={id} className="input" style={{ width: 200 }} value={value ?? ''} onChange={(e) => onChange(field.name, e.target.value)} disabled={disabled}>
            <option value="">— Select —</option>
            {(field.options || []).map(opt => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        </SettingRow>
      )
    case 'textarea':
      return (
        <div style={{ padding: 'var(--spacing-sm) 0', borderBottom: '1px solid var(--color-border-subtle)' }}>
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
              ? { fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' } : undefined}
          />
        </div>
      )
    case 'number':
      return (
        <SettingRow label={label} description={field.helpText}>
          <input
            id={id} className="input" type="number" style={{ width: 120 }}
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
          model: 'FLAG_CHAT',
          multimodal_model: 'FLAG_CHAT',
          transcription_model: 'FLAG_TRANSCRIPT',
          tts_model: 'FLAG_TTS',
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
          <div key={index} className="card" style={{ marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-md)' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-md)' }}>
              <h4 style={{ margin: 0, fontWeight: 600 }}>{label} #{index + 1}</h4>
              <button type="button" className="btn btn-danger btn-sm" onClick={() => onRemove(index)}>
                <i className="fas fa-times" />
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
              const fieldLabel = <>{f.label}{f.required && <span style={{ color: 'var(--color-error)' }}> *</span>}</>
              if (f.type === 'checkbox') {
                return (
                  <SettingRow key={f.name} label={fieldLabel} description={f.helpText}>
                    <Toggle checked={val === 'true' || val === true} onChange={(v) => handleConfigFieldChange(index, f.name, v, 'checkbox')} />
                  </SettingRow>
                )
              }
              if (f.type === 'textarea') {
                return (
                  <div key={f.name} style={{ padding: 'var(--spacing-sm) 0', borderBottom: '1px solid var(--color-border-subtle)' }}>
                    <div style={{ fontSize: '0.875rem', fontWeight: 500, marginBottom: 4 }}>{fieldLabel}</div>
                    {f.helpText && <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-xs)' }}>{f.helpText}</div>}
                    <textarea className="textarea" value={val} onChange={(e) => handleConfigFieldChange(index, f.name, e.target.value, 'text')} rows={3} placeholder={f.placeholder} />
                  </div>
                )
              }
              if (f.type === 'select') {
                return (
                  <SettingRow key={f.name} label={fieldLabel} description={f.helpText}>
                    <select className="input" style={{ width: 200 }} value={val} onChange={(e) => handleConfigFieldChange(index, f.name, e.target.value, 'text')}>
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
        <i className="fas fa-plus" /> {addButtonText}
      </button>
    </div>
  )
}

// --- Section definitions ---

const SECTIONS = [
  { id: 'BasicInfo', icon: 'fa-info-circle', label: 'Basic Info' },
  { id: 'ModelSettings', icon: 'fa-brain', label: 'Model Settings' },
  { id: 'MemorySettings', icon: 'fa-database', label: 'Memory' },
  { id: 'PromptsGoals', icon: 'fa-bullseye', label: 'Prompts & Goals' },
  { id: 'AdvancedSettings', icon: 'fa-cog', label: 'Advanced' },
  { id: 'MCP', icon: 'fa-server', label: 'MCP Servers' },
  { id: 'connectors', icon: 'fa-plug', label: 'Connectors' },
  { id: 'actions', icon: 'fa-bolt', label: 'Actions' },
  { id: 'filters', icon: 'fa-filter', label: 'Filters' },
  { id: 'dynamic_prompts', icon: 'fa-wand-magic-sparkles', label: 'Dynamic Prompts' },
]

// Fields handled by custom editors in the MCP section
const CUSTOM_FIELDS = new Set(['mcp_stdio_servers'])

// --- Main component ---

export default function AgentCreate() {
  const { name } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const { addToast } = useOutletContext()
  const [searchParams] = useSearchParams()
  const userId = searchParams.get('user_id') || undefined
  const isEdit = !!name
  const importedConfig = location.state?.importedConfig || null

  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [activeSection, setActiveSection] = useState('BasicInfo')
  const [meta, setMeta] = useState(null)
  const [form, setForm] = useState({})
  const [connectors, setConnectors] = useState([])
  const [actions, setActions] = useState([])
  const [filters, setFilters] = useState([])
  const [dynamicPrompts, setDynamicPrompts] = useState([])
  const [mcpHttpServers, setMcpHttpServers] = useState([])
  const [stdioServers, setStdioServers] = useState([])

  // Group metadata Fields by tags.section
  const fieldsBySection = useMemo(() => {
    if (!meta?.Fields) return {}
    const groups = {}
    for (const field of meta.Fields) {
      if (CUSTOM_FIELDS.has(field.name)) continue
      const section = field.tags?.section || 'BasicInfo'
      if (!groups[section]) groups[section] = []
      groups[section].push(field)
    }
    return groups
  }, [meta])

  const visibleSections = useMemo(() => {
    const items = [...SECTIONS]
    if (isEdit) items.push({ id: 'export', icon: 'fa-download', label: 'Export' })
    return items
  }, [isEdit])

  useEffect(() => {
    const init = async () => {
      try {
        const [metaData, config] = await Promise.all([
          agentsApi.configMeta().catch(() => null),
          isEdit ? agentsApi.getConfig(name, userId).catch(() => null) : Promise.resolve(null),
        ])
        if (metaData) setMeta(metaData)

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
        }

        setForm(initialForm)
      } catch (err) {
        addToast(`Failed to load configuration: ${err.message}`, 'error')
      } finally {
        setLoading(false)
      }
    }
    init()
  }, [name, isEdit, importedConfig, addToast])

  const updateField = (fieldName, value) => {
    setForm(prev => ({ ...prev, [fieldName]: value }))
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!form.name?.toString().trim()) {
      addToast('Agent name is required', 'warning')
      return
    }
    setSaving(true)
    try {
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
      if (stdioServers.length > 0) {
        payload.mcp_stdio_servers = buildStdioJson(stdioServers)
      }

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
    return fields.map(field => (
      <FormField
        key={field.name}
        field={field.name === 'name' && isEdit ? { ...field, disabled: true, helpText: 'Agent name cannot be changed after creation' } : field}
        value={form[field.name]}
        onChange={updateField}
        disabled={field.name === 'name' && isEdit}
      />
    ))
  }

  const renderSection = () => {
    switch (activeSection) {
      case 'BasicInfo':
      case 'ModelSettings':
      case 'MemorySettings':
      case 'PromptsGoals':
      case 'AdvancedSettings':
        return renderFieldSection(activeSection)

      case 'MCP':
        return (
          <>
            {/* Other MCP metadata fields (mcp_prepare_script, etc.) */}
            {renderFieldSection('MCP')}

            {/* STDIO Servers */}
            <div style={{ marginTop: 'var(--spacing-lg)' }}>
              <h4 className="agent-subsection-title">
                <i className="fas fa-terminal" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
                STDIO Servers
              </h4>
              <p className="agent-section-desc">Local command-based MCP servers (e.g. docker run).</p>
              {stdioServers.map((server, idx) => (
                <div key={idx} className="card" style={{ marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-md)' }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-sm)' }}>
                    <span style={{ fontWeight: 600, fontSize: '0.85rem' }}>Server #{idx + 1}</span>
                    <button type="button" className="btn btn-danger btn-sm" onClick={() => removeStdioServer(idx)}>
                      <i className="fas fa-times" />
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
                        <i className="fas fa-plus" /> Add
                      </button>
                    </label>
                    {(server.args || []).length === 0 && <p style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>No arguments.</p>}
                    {(server.args || []).map((arg, ai) => (
                      <div key={ai} style={{ display: 'flex', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-xs)' }}>
                        <input className="input" value={arg} onChange={(e) => updateArg(idx, ai, e.target.value)} placeholder="argument" style={{ flex: 1 }} />
                        <button type="button" className="btn btn-danger btn-sm" onClick={() => removeArg(idx, ai)}><i className="fas fa-times" /></button>
                      </div>
                    ))}
                  </div>
                  <div className="form-group">
                    <label className="form-label" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                      Environment Variables
                      <button type="button" className="btn btn-secondary btn-sm" onClick={() => addEnv(idx)} style={{ fontSize: '0.7rem', padding: '2px 8px' }}>
                        <i className="fas fa-plus" /> Add
                      </button>
                    </label>
                    {(server.env || []).length === 0 && <p style={{ fontSize: '0.8rem', color: 'var(--color-text-muted)' }}>No environment variables.</p>}
                    {(server.env || []).map((env, ei) => (
                      <div key={ei} style={{ display: 'flex', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-xs)' }}>
                        <input className="input" value={env} onChange={(e) => updateEnv(idx, ei, e.target.value)} placeholder="KEY=VALUE" style={{ flex: 1 }} />
                        <button type="button" className="btn btn-danger btn-sm" onClick={() => removeEnv(idx, ei)}><i className="fas fa-times" /></button>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
              <button type="button" className="btn btn-secondary" onClick={addStdioServer}>
                <i className="fas fa-plus" /> Add STDIO Server
              </button>
            </div>

            {/* HTTP Servers */}
            <div style={{ marginTop: 'var(--spacing-lg)' }}>
              <h4 className="agent-subsection-title">
                <i className="fas fa-globe" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
                HTTP Servers
              </h4>
              <p className="agent-section-desc">MCP servers connected over HTTP.</p>
              {mcpHttpServers.map((server, idx) => (
                <div key={idx} className="card" style={{ marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-md)' }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-sm)' }}>
                    <span style={{ fontWeight: 600, fontSize: '0.85rem' }}>HTTP Server #{idx + 1}</span>
                    <button type="button" className="btn btn-danger btn-sm" onClick={() => removeMcpHttp(idx)}>
                      <i className="fas fa-times" />
                    </button>
                  </div>
                  {(meta?.MCPServers || [{ name: 'url', label: 'URL', type: 'text' }, { name: 'token', label: 'API Key', type: 'password' }]).map(f => (
                    <div key={f.name} className="form-group">
                      <label className="form-label">{f.label}{f.required && <span style={{ color: 'var(--color-error)' }}> *</span>}</label>
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
                <i className="fas fa-plus" /> Add HTTP Server
              </button>
            </div>
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
              <i className="fas fa-download" style={{ marginRight: 'var(--spacing-xs)' }} /> Export Agent
            </a>
          </div>
        )

      default:
        return null
    }
  }

  if (loading) {
    return (
      <div className="page" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
        <i className="fas fa-spinner fa-spin" style={{ fontSize: '2rem', color: 'var(--color-primary)' }} />
      </div>
    )
  }

  return (
    <div className="page">
      <style>{`
        .agent-form-container {
          display: flex;
          gap: var(--spacing-lg);
          min-height: 500px;
        }
        .agent-wizard-sidebar {
          width: 220px;
          flex-shrink: 0;
        }
        .agent-wizard-nav {
          list-style: none;
          padding: 0;
          margin: 0;
          position: sticky;
          top: var(--spacing-md);
        }
        .agent-wizard-nav-item {
          display: flex;
          align-items: center;
          gap: var(--spacing-sm);
          padding: var(--spacing-sm) var(--spacing-md);
          border-radius: var(--radius-md);
          cursor: pointer;
          font-size: 0.875rem;
          color: var(--color-text-secondary);
          transition: background 0.15s, color 0.15s;
          user-select: none;
          margin-bottom: 2px;
          border-left: 3px solid transparent;
        }
        .agent-wizard-nav-item:hover {
          background: var(--color-primary-light);
          color: var(--color-text-primary);
        }
        .agent-wizard-nav-item.active {
          background: var(--color-primary-light);
          color: var(--color-primary);
          border-left-color: var(--color-primary);
          font-weight: 500;
        }
        .agent-wizard-nav-item i {
          width: 18px;
          text-align: center;
          font-size: 0.8125rem;
        }
        .agent-wizard-badge {
          margin-left: auto;
          font-size: 0.7rem;
          background: var(--color-primary);
          color: white;
          border-radius: 999px;
          padding: 1px 6px;
          min-width: 18px;
          text-align: center;
        }
        .agent-form-content {
          flex: 1;
          min-width: 0;
        }
        .agent-section-title {
          font-weight: 600;
          font-size: 1.1rem;
          margin-bottom: var(--spacing-md);
          display: flex;
          align-items: center;
          gap: var(--spacing-xs);
        }
        .agent-subsection-title {
          font-weight: 600;
          font-size: 0.95rem;
          margin-bottom: var(--spacing-sm);
        }
        .agent-section-desc {
          font-size: 0.8125rem;
          color: var(--color-text-muted);
          margin-bottom: var(--spacing-md);
        }
        .agent-form-help-text {
          font-size: 0.75rem;
          color: var(--color-text-muted);
          margin-top: var(--spacing-xs);
          margin-bottom: 0;
        }
        @media (max-width: 768px) {
          .agent-form-container {
            flex-direction: column;
          }
          .agent-wizard-sidebar {
            width: 100%;
          }
          .agent-wizard-nav {
            display: flex;
            flex-wrap: wrap;
            gap: var(--spacing-xs);
            position: static;
          }
          .agent-wizard-nav-item {
            font-size: 0.8125rem;
            padding: var(--spacing-xs) var(--spacing-sm);
            border-left: none;
            border-bottom: 3px solid transparent;
          }
          .agent-wizard-nav-item.active {
            border-left-color: transparent;
            border-bottom-color: var(--color-primary);
          }
        }
      `}</style>

      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h1 className="page-title">{isEdit ? `Edit Agent: ${name}` : importedConfig ? 'Import Agent' : 'Create Agent'}</h1>
        <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/agents')}>
          <i className="fas fa-arrow-left" /> Back
        </button>
      </div>

      <form onSubmit={handleSubmit} noValidate>
        <div className="agent-form-container">
          <div className="agent-wizard-sidebar">
            <div className="card" style={{ padding: 'var(--spacing-sm)' }}>
              <ul className="agent-wizard-nav">
                {visibleSections.map(s => {
                  let count = 0
                  if (s.id === 'connectors') count = connectors.length
                  else if (s.id === 'actions') count = actions.length
                  else if (s.id === 'filters') count = filters.length
                  else if (s.id === 'dynamic_prompts') count = dynamicPrompts.length
                  return (
                    <li
                      key={s.id}
                      className={`agent-wizard-nav-item ${activeSection === s.id ? 'active' : ''}`}
                      onClick={() => setActiveSection(s.id)}
                    >
                      <i className={`fas ${s.icon}`} />
                      {s.label}
                      {count > 0 && <span className="agent-wizard-badge">{count}</span>}
                    </li>
                  )
                })}
              </ul>
            </div>
          </div>

          <div className="agent-form-content">
            <div className="card" style={{ padding: 'var(--spacing-lg)' }}>
              <h3 className="agent-section-title">
                <i className={`fas ${visibleSections.find(s => s.id === activeSection)?.icon || 'fa-cog'}`} style={{ color: 'var(--color-primary)' }} />
                {visibleSections.find(s => s.id === activeSection)?.label || activeSection}
              </h3>
              {renderSection()}
            </div>

            <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'flex-end', marginTop: 'var(--spacing-md)' }}>
              <button type="button" className="btn btn-secondary" onClick={() => navigate('/app/agents')}>
                <i className="fas fa-times" /> Cancel
              </button>
              <button type="submit" className="btn btn-primary" disabled={saving}>
                {saving
                  ? <><i className="fas fa-spinner fa-spin" /> Saving...</>
                  : <><i className="fas fa-save" /> {isEdit ? 'Save Changes' : importedConfig ? 'Import Agent' : 'Create Agent'}</>
                }
              </button>
            </div>
          </div>
        </div>
      </form>
    </div>
  )
}
