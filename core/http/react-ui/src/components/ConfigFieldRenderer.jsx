import { useState } from 'react'
import SettingRow from './SettingRow'
import Toggle from './Toggle'
import SearchableSelect from './SearchableSelect'
import SearchableModelSelect from './SearchableModelSelect'
import AutocompleteInput from './AutocompleteInput'
import CodeEditor from './CodeEditor'
import StructuredCodeEditor from './StructuredCodeEditor'
import EntityActionListEditor from './EntityActionListEditor'
import PatternListEditor from './PatternListEditor'
import ModelMultiSelect from './ModelMultiSelect'
import RouterCandidatesEditor from './RouterCandidatesEditor'
import RouterPoliciesEditor from './RouterPoliciesEditor'

// Map autocomplete provider to SearchableModelSelect capability
const PROVIDER_TO_CAPABILITY = {
  'models:chat': 'FLAG_CHAT',
  'models:tts': 'FLAG_TTS',
  'models:transcript': 'FLAG_TRANSCRIPT',
  'models:vad': 'FLAG_VAD',
  'models:score': 'FLAG_SCORE',
  'models:token_classify': 'FLAG_TOKEN_CLASSIFY',
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
    <div className="cfr-list">
      {items.map((item, i) => (
        <div key={i} className="cfr-row">
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
              className="flex-1"
            />
          ) : (
            <input className="input cfr-input" value={item} onChange={e => update(i, e.target.value)} />
          )}
          <button type="button" className="btn btn-secondary btn-sm pill-tiny" onClick={() => remove(i)}>
            <i className="fas fa-times" />
          </button>
        </div>
      ))}
      {(!options || availableOptions.length > 0) && (
        <button type="button" className="btn btn-secondary btn-sm self-start text-xs" onClick={add}>
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
    <div className="cfr-list">
      {entries.map(([k, v], i) => (
        <div key={i} className="cfr-row">
          <input className="input cfr-input" value={k} placeholder="key" onChange={e => update(i, e.target.value, v)} />
          <input className="input cfr-input" value={typeof v === 'string' ? v : JSON.stringify(v)} placeholder="value" onChange={e => update(i, k, e.target.value)} />
          <button type="button" className="btn btn-secondary btn-sm pill-tiny" onClick={() => remove(i)}>
            <i className="fas fa-times" />
          </button>
        </div>
      ))}
      <button type="button" className="btn btn-secondary btn-sm self-start text-xs" onClick={add}>
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
    <div className="w-full">
      <textarea
        className="input cfr-textarea cfr-textarea--mono"
        value={text}
        onChange={e => handleChange(e.target.value)}
      />
      {parseError && <div className="text-error text-xs mt-xs">{parseError}</div>}
    </div>
  )
}

function FieldLabel({ field }) {
  return (
    <span className="hstack hstack--xs">
      {field.label}
      {field.vram_impact && (
        <span className="cfr-tag cfr-tag--vram">
          VRAM
        </span>
      )}
      {field.advanced && (
        <span className="cfr-tag cfr-tag--advanced">
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
      className="cfr-clear">
      <i className="fas fa-times" />
    </button>
  )

  const description = (
    <span className="hstack hstack--xs">
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
          className="col-w-220"
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
          className="col-w-220"
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
          className="col-w-220"
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
        <div className="hstack">
          <input type="range" min={min} max={max} step={step}
            value={value ?? min}
            onChange={e => handleChange(parseFloat(e.target.value))}
            className="col-w-120"
          />
          <span className="cfr-slider-value">
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
          <input className="input cfr-num" type="number"
            value={value ?? ''}
            onChange={e => handleChange(e.target.value)}
            min={field.min} max={field.max} step={field.step}
            placeholder={field.placeholder}
          />
          {annotation}
        </>
      </SettingRow>
    )
  }

  // Textarea
  if (component === 'textarea') {
    return (
      <div className="list-row">
        <div className="hstack hstack--between mb-xs">
          <div>
            <div className="text-base fw-medium"><FieldLabel field={field} /></div>
            <div className="text-meta mt-xs">{description}</div>
          </div>
        </div>
        <textarea className="input cfr-textarea" value={value || ''}
          onChange={e => handleChange(e.target.value)}
          placeholder={field.placeholder}
        />
      </div>
    )
  }

  // Code editor. Two flavours:
  //   - Plain CodeEditor when the form value is a string (Go template
  //     blobs etc. — what the original `code-editor` shipped for).
  //   - StructuredCodeEditor when the form value is a structured
  //     object/array (e.g. `router.candidates`, where the canonical
  //     value is `[{label, model, rules}, ...]`). The wrapper keeps a
  //     YAML representation in the textarea while publishing the
  //     parsed structure back to form state, so the save flow can
  //     unflatten it into the YAML file cleanly.
  if (component === 'code-editor') {
    const isStructured = value !== null && value !== undefined && typeof value !== 'string'
    return (
      <div className="list-row">
        <div className="hstack hstack--between mb-xs">
          <div>
            <div className="text-base fw-medium"><FieldLabel field={field} /></div>
            <div className="text-meta mt-xs">{description}</div>
          </div>
        </div>
        {isStructured
          ? <StructuredCodeEditor value={value} onChange={handleChange} minHeight="80px" />
          : <CodeEditor value={value || ''} onChange={handleChange} minHeight="80px" language={field.language} />}
      </div>
    )
  }

  // String list
  if (component === 'string-list') {
    return (
      <div className="list-row">
        <div className="hstack hstack--between mb-xs">
          <div>
            <div className="text-base fw-medium"><FieldLabel field={field} /></div>
            <div className="text-meta mt-xs">{description}</div>
          </div>
        </div>
        <StringListEditor value={value} onChange={handleChange} options={field.options?.length > 0 ? field.options : null} />
      </div>
    )
  }

  // JSON editor
  if (component === 'json-editor') {
    return (
      <div className="list-row">
        <div className="hstack hstack--between mb-xs">
          <div>
            <div className="text-base fw-medium"><FieldLabel field={field} /></div>
            <div className="text-meta mt-xs">{description}</div>
          </div>
        </div>
        <JsonEditor value={value} onChange={handleChange} />
      </div>
    )
  }

  // Router candidates — routing table editor. Each row is
  // {model, labels[]}; the labels picker reads from router.policies
  // via FormContext so candidate labels match the declared vocabulary.
  if (component === 'router-candidates') {
    return (
      <div className="list-row">
        <div className="hstack hstack--between mb-xs">
          <div>
            <div className="text-base fw-medium"><FieldLabel field={field} /></div>
            <div className="text-meta mt-xs">{description}</div>
          </div>
        </div>
        <RouterCandidatesEditor value={value} onChange={handleChange} />
      </div>
    )
  }

  // Router policies — label vocabulary editor. Each row is
  // {label, description}; the description ends up verbatim in the
  // routing system prompt sent to the classifier model.
  if (component === 'router-policies') {
    return (
      <div className="list-row">
        <div className="hstack hstack--between mb-xs">
          <div>
            <div className="text-base fw-medium"><FieldLabel field={field} /></div>
            <div className="text-meta mt-xs">{description}</div>
          </div>
        </div>
        <RouterPoliciesEditor value={value} onChange={handleChange} />
      </div>
    )
  }

  // PII detectors — a capability-filtered multi-select of token_classify
  // models (the consuming model's pii.detectors list).
  if (component === 'model-multi-select') {
    const cap = PROVIDER_TO_CAPABILITY[field.autocomplete_provider] || undefined
    return (
      <div className="list-row">
        <div className="hstack hstack--between mb-xs">
          <div>
            <div className="text-base fw-medium"><FieldLabel field={field} /></div>
            <div className="text-meta mt-xs">{description}</div>
          </div>
        </div>
        <ModelMultiSelect value={value} onChange={handleChange} capability={cap} placeholder={field.placeholder} />
      </div>
    )
  }

  // PII detection entity-action map — a detector model's
  // pii_detection.entity_actions (entity group -> mask|block|allow).
  if (component === 'entity-action-list') {
    return (
      <div className="list-row">
        <div className="hstack hstack--between mb-xs">
          <div>
            <div className="text-base fw-medium"><FieldLabel field={field} /></div>
            <div className="text-meta mt-xs">{description}</div>
          </div>
        </div>
        <EntityActionListEditor value={value} onChange={handleChange} />
      </div>
    )
  }

  // PII built-in secret patterns — a checklist of named built-in patterns
  // (pii_detection.builtins). value is an array of selected names.
  if (component === 'pii-builtins-select') {
    const selected = Array.isArray(value) ? value : []
    const toggle = (name) => {
      handleChange(selected.includes(name) ? selected.filter(n => n !== name) : [...selected, name])
    }
    return (
      <div className="list-row">
        <div className="mb-xs">
          <div className="text-base fw-medium"><FieldLabel field={field} /></div>
          <div className="text-meta mt-xs">{description}</div>
        </div>
        <div className="stack stack--xs">
          {(field.options || []).map(opt => (
            <label key={opt.value} className="cfr-radio">
              <input type="checkbox" checked={selected.includes(opt.value)} onChange={() => toggle(opt.value)} />
              {opt.label || opt.value}
            </label>
          ))}
        </div>
      </div>
    )
  }

  // PII custom secret patterns — operator-defined restricted-regex rules
  // (pii_detection.patterns). value is an array of {name, match, action, min_len}.
  if (component === 'pii-pattern-list') {
    return (
      <div className="list-row">
        <div className="mb-xs">
          <div className="text-base fw-medium"><FieldLabel field={field} /></div>
          <div className="text-meta mt-xs">{description}</div>
        </div>
        <PatternListEditor value={value} onChange={handleChange} />
      </div>
    )
  }

  // Map editor
  if (component === 'map-editor') {
    return (
      <div className="list-row">
        <div className="hstack hstack--between mb-xs">
          <div>
            <div className="text-base fw-medium"><FieldLabel field={field} /></div>
            <div className="text-meta mt-xs">{description}</div>
          </div>
        </div>
        <MapEditor value={value} onChange={handleChange} />
      </div>
    )
  }

  // Default: text input
  return (
    <SettingRow label={<FieldLabel field={field} />} description={description}>
      <input className="input cfr-select" value={value ?? ''}
        onChange={e => handleChange(e.target.value)}
        placeholder={field.placeholder}
      />
    </SettingRow>
  )
}
