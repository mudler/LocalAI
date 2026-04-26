import { useState } from 'react'
import SettingRow from './SettingRow'
import Toggle from './Toggle'
import SearchableSelect from './SearchableSelect'
import SearchableModelSelect from './SearchableModelSelect'
import AutocompleteInput from './AutocompleteInput'
import CodeEditor from './CodeEditor'

// Map autocomplete provider to SearchableModelSelect capability
const PROVIDER_TO_CAPABILITY = {
  'models:chat': 'FLAG_CHAT',
  'models:tts': 'FLAG_TTS',
  'models:transcript': 'FLAG_TRANSCRIPT',
  'models:vad': 'FLAG_VAD',
}

function coerceValue(raw, uiType) {
  if (raw === '' || raw === null || raw === undefined) return raw
  if (uiType === 'int') return parseInt(raw, 10) || 0
  if (uiType === 'float') return parseFloat(raw) || 0
  return raw
}

function StringListEditor({ value, onChange, options }) {
  const items = Array.isArray(value) ? value : []

  const update = (index, val) => {
    const next = [...items]
    next[index] = val
    onChange(next)
  }
  const add = () => onChange([...items, ''])
  const remove = (index) => onChange(items.filter((_, i) => i !== index))

  // When options are available, filter out already-selected values
  const availableOptions = options
    ? options.filter(o => !items.includes(o.value))
    : null

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, width: '100%' }}>
      {items.map((item, i) => (
        <div key={i} style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
          {options ? (
            <SearchableSelect
              value={item}
              onChange={val => update(i, val)}
              options={[
                // Include the current value so it shows as selected
                ...(item ? [options.find(o => o.value === item) || { value: item, label: item }] : []),
                ...availableOptions,
              ]}
              placeholder="Select..."
              style={{ flex: 1 }}
            />
          ) : (
            <input className="input" value={item} onChange={e => update(i, e.target.value)}
              style={{ flex: 1, fontSize: '0.8125rem' }} />
          )}
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => remove(i)}
            style={{ padding: '2px 6px', fontSize: '0.75rem' }}>
            <i className="fas fa-times" />
          </button>
        </div>
      ))}
      {(!options || availableOptions.length > 0) && (
        <button type="button" className="btn btn-secondary btn-sm" onClick={add}
          style={{ alignSelf: 'flex-start', fontSize: '0.75rem' }}>
          <i className="fas fa-plus" /> Add
        </button>
      )}
    </div>
  )
}

function MapEditor({ value, onChange }) {
  const entries = value && typeof value === 'object' && !Array.isArray(value)
    ? Object.entries(value) : []

  const update = (index, key, val) => {
    const next = [...entries]
    next[index] = [key, val]
    onChange(Object.fromEntries(next))
  }
  const add = () => onChange({ ...value, '': '' })
  const remove = (index) => {
    const next = entries.filter((_, i) => i !== index)
    onChange(Object.fromEntries(next))
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, width: '100%' }}>
      {entries.map(([k, v], i) => (
        <div key={i} style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
          <input className="input" value={k} placeholder="key"
            onChange={e => update(i, e.target.value, v)}
            style={{ flex: 1, fontSize: '0.8125rem' }} />
          <input className="input" value={typeof v === 'string' ? v : JSON.stringify(v)} placeholder="value"
            onChange={e => update(i, k, e.target.value)}
            style={{ flex: 1, fontSize: '0.8125rem' }} />
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => remove(i)}
            style={{ padding: '2px 6px', fontSize: '0.75rem' }}>
            <i className="fas fa-times" />
          </button>
        </div>
      ))}
      <button type="button" className="btn btn-secondary btn-sm" onClick={add}
        style={{ alignSelf: 'flex-start', fontSize: '0.75rem' }}>
        <i className="fas fa-plus" /> Add
      </button>
    </div>
  )
}

function JsonEditor({ value, onChange }) {
  const [text, setText] = useState(() =>
    typeof value === 'string' ? value : JSON.stringify(value, null, 2) || ''
  )
  const [parseError, setParseError] = useState(null)

  const handleChange = (val) => {
    setText(val)
    try {
      const parsed = JSON.parse(val)
      setParseError(null)
      onChange(parsed)
    } catch {
      setParseError('Invalid JSON')
    }
  }

  return (
    <div style={{ width: '100%' }}>
      <textarea
        className="input"
        value={text}
        onChange={e => handleChange(e.target.value)}
        style={{ width: '100%', minHeight: 80, fontFamily: 'var(--font-mono)', fontSize: '0.8125rem', resize: 'vertical' }}
      />
      {parseError && <div style={{ color: 'var(--color-error)', fontSize: '0.75rem', marginTop: 2 }}>{parseError}</div>}
    </div>
  )
}

function FieldLabel({ field }) {
  return (
    <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
      {field.label}
      {field.vram_impact && (
        <span style={{ fontSize: '0.625rem', padding: '1px 4px', borderRadius: 'var(--radius-sm)',
          background: 'var(--color-warning-light, rgba(245,158,11,0.15))', color: 'var(--color-warning)' }}>
          VRAM
        </span>
      )}
      {field.advanced && (
        <span style={{ fontSize: '0.625rem', padding: '1px 4px', borderRadius: 'var(--radius-sm)',
          background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>
          Advanced
        </span>
      )}
    </span>
  )
}

export default function ConfigFieldRenderer({ field, value, onChange, onRemove, annotation }) {
  const handleChange = (raw) => {
    onChange(coerceValue(raw, field.ui_type))
  }

  const removeBtn = (
    <button type="button" onClick={() => onRemove(field.path)}
      title="Remove field"
      style={{
        background: 'none', border: 'none', cursor: 'pointer', padding: '2px 4px',
        color: 'var(--color-text-muted)', fontSize: '0.75rem',
      }}>
      <i className="fas fa-times" />
    </button>
  )

  const description = (
    <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
      {field.description || field.path}
      {removeBtn}
    </span>
  )

  const component = field.component

  // Toggle
  if (component === 'toggle') {
    return (
      <SettingRow label={<FieldLabel field={field} />} description={description}>
        <Toggle checked={!!value} onChange={handleChange} />
      </SettingRow>
    )
  }

  // Model-select
  if (component === 'model-select') {
    const cap = PROVIDER_TO_CAPABILITY[field.autocomplete_provider] || undefined
    return (
      <SettingRow label={<FieldLabel field={field} />} description={description}>
        <SearchableModelSelect
          value={value || ''}
          onChange={handleChange}
          capability={cap}
          placeholder={field.placeholder || 'Select model...'}
          style={{ width: 220 }}
        />
      </SettingRow>
    )
  }

  // Select with autocomplete provider (dynamic)
  if ((component === 'select' || component === 'input') && field.autocomplete_provider) {
    return (
      <SettingRow label={<FieldLabel field={field} />} description={description}>
        <AutocompleteInput
          value={value || ''}
          onChange={handleChange}
          provider={field.autocomplete_provider}
          placeholder={field.placeholder || 'Type or select...'}
          style={{ width: 220 }}
        />
      </SettingRow>
    )
  }

  // Select with static options
  if (component === 'select' && field.options?.length > 0) {
    return (
      <SettingRow label={<FieldLabel field={field} />} description={description}>
        <SearchableSelect
          value={value || ''}
          onChange={handleChange}
          options={field.options.map(o => ({ value: o.value, label: o.label }))}
          placeholder={field.placeholder || 'Select...'}
          style={{ width: 220 }}
        />
      </SettingRow>
    )
  }

  // Slider
  if (component === 'slider') {
    const min = field.min ?? 0
    const max = field.max ?? 1
    const step = field.step ?? 0.1
    return (
      <SettingRow label={<FieldLabel field={field} />} description={description}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <input type="range" min={min} max={max} step={step}
            value={value ?? min}
            onChange={e => handleChange(parseFloat(e.target.value))}
            style={{ width: 120 }}
          />
          <span style={{ fontSize: '0.8125rem', minWidth: 40, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>
            {value ?? min}
          </span>
        </div>
      </SettingRow>
    )
  }

  // Number
  if (component === 'number') {
    return (
      <SettingRow label={<FieldLabel field={field} />} description={description}>
        <>
          <input className="input" type="number"
            value={value ?? ''}
            onChange={e => handleChange(e.target.value)}
            min={field.min} max={field.max} step={field.step}
            placeholder={field.placeholder}
            style={{ width: 120, fontSize: '0.8125rem' }}
          />
          {annotation}
        </>
      </SettingRow>
    )
  }

  // Textarea
  if (component === 'textarea') {
    return (
      <div style={{ padding: 'var(--spacing-sm) 0', borderBottom: '1px solid var(--color-border-subtle)' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
          <div>
            <div style={{ fontSize: '0.875rem', fontWeight: 500 }}><FieldLabel field={field} /></div>
            <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{description}</div>
          </div>
        </div>
        <textarea className="input" value={value || ''}
          onChange={e => handleChange(e.target.value)}
          placeholder={field.placeholder}
          style={{ width: '100%', minHeight: 80, fontSize: '0.8125rem', resize: 'vertical' }}
        />
      </div>
    )
  }

  // Code editor
  if (component === 'code-editor') {
    return (
      <div style={{ padding: 'var(--spacing-sm) 0', borderBottom: '1px solid var(--color-border-subtle)' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
          <div>
            <div style={{ fontSize: '0.875rem', fontWeight: 500 }}><FieldLabel field={field} /></div>
            <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{description}</div>
          </div>
        </div>
        <CodeEditor value={value || ''} onChange={handleChange} minHeight="80px" />
      </div>
    )
  }

  // String list
  if (component === 'string-list') {
    return (
      <div style={{ padding: 'var(--spacing-sm) 0', borderBottom: '1px solid var(--color-border-subtle)' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
          <div>
            <div style={{ fontSize: '0.875rem', fontWeight: 500 }}><FieldLabel field={field} /></div>
            <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{description}</div>
          </div>
        </div>
        <StringListEditor value={value} onChange={handleChange} options={field.options?.length > 0 ? field.options : null} />
      </div>
    )
  }

  // JSON editor
  if (component === 'json-editor') {
    return (
      <div style={{ padding: 'var(--spacing-sm) 0', borderBottom: '1px solid var(--color-border-subtle)' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
          <div>
            <div style={{ fontSize: '0.875rem', fontWeight: 500 }}><FieldLabel field={field} /></div>
            <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{description}</div>
          </div>
        </div>
        <JsonEditor value={value} onChange={handleChange} />
      </div>
    )
  }

  // Map editor
  if (component === 'map-editor') {
    return (
      <div style={{ padding: 'var(--spacing-sm) 0', borderBottom: '1px solid var(--color-border-subtle)' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
          <div>
            <div style={{ fontSize: '0.875rem', fontWeight: 500 }}><FieldLabel field={field} /></div>
            <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{description}</div>
          </div>
        </div>
        <MapEditor value={value} onChange={handleChange} />
      </div>
    )
  }

  // Default: text input
  return (
    <SettingRow label={<FieldLabel field={field} />} description={description}>
      <input className="input" value={value ?? ''}
        onChange={e => handleChange(e.target.value)}
        placeholder={field.placeholder}
        style={{ width: 220, fontSize: '0.8125rem' }}
      />
    </SettingRow>
  )
}
