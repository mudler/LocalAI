import SearchableModelSelect from './SearchableModelSelect'

// Editor for a list of model names (value is []string), each chosen via a
// capability-filtered SearchableModelSelect. Used for pii.detectors, where
// every entry must be a token_classify model. Already-selected models are
// excluded from the add picker so each appears at most once.
export default function ModelMultiSelect({ value, onChange, capability, placeholder }) {
  const items = Array.isArray(value) ? value : []

  const update = (index, v) => {
    if (!v) return
    onChange(items.map((it, i) => (i === index ? v : it)))
  }
  const remove = (index) => onChange(items.filter((_, i) => i !== index))
  const add = (v) => {
    if (!v || items.includes(v)) return
    onChange([...items, v])
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, width: '100%' }}>
      {items.length === 0 && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
          No detectors — PII is enabled but nothing scans requests. Add a token-classification
          (NER) model below; its <code>pii_detection</code> block supplies the policy.
        </div>
      )}

      {items.map((name, i) => (
        <div key={i} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <SearchableModelSelect
            value={name || ''}
            onChange={v => update(i, v)}
            capability={capability}
            placeholder={placeholder || 'Select detector model...'}
            style={{ flex: '1 1 260px', minWidth: 220 }}
          />
          <button type="button" className="btn btn-secondary btn-sm"
            onClick={() => remove(i)}
            style={{ padding: '2px 8px', fontSize: '0.75rem' }}
            aria-label="Remove detector">
            <i className="fas fa-times" />
          </button>
        </div>
      ))}

      <SearchableModelSelect
        value=""
        onChange={v => add(v)}
        capability={capability}
        placeholder="+ Add detector model..."
        style={{ flex: '1 1 260px', minWidth: 220 }}
      />
    </div>
  )
}
