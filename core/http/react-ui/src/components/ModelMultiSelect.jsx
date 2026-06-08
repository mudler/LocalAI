import SearchableModelSelect from './SearchableModelSelect'

// Editor for a list of model names (value is []string). Selected models render
// as compact removable chips; a single capability-filtered, commit-only picker
// adds new ones. Used for pii.detectors / the instance-wide default detector,
// where every entry must be a token_classify model. Already-selected models are
// guarded against so each appears at most once.
//
// The picker is commit-only on purpose: typing a partial query must never be
// treated as a chosen model (otherwise each keystroke would add a bogus entry),
// and selecting one input box per detector wastes vertical space.
export default function ModelMultiSelect({ value, onChange, capability, placeholder }) {
  const items = Array.isArray(value) ? value : []

  const remove = (index) => onChange(items.filter((_, i) => i !== index))
  const add = (v) => {
    if (!v || items.includes(v)) return
    onChange([...items, v])
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, width: '100%' }}>
      {items.length === 0 ? (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
          No detectors — PII is enabled but nothing scans requests. Add a token-classification
          (NER) model below; its <code>pii_detection</code> block supplies the policy.
        </div>
      ) : (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {items.map((name, i) => (
            <span key={i} style={{
              display: 'inline-flex', alignItems: 'center', gap: 6,
              padding: '2px 4px 2px 10px', fontSize: '0.8125rem',
              fontFamily: 'var(--font-mono)', background: 'var(--color-bg-tertiary)',
              borderRadius: 'var(--radius-md)',
            }}>
              {name}
              <button type="button" className="btn btn-secondary btn-sm"
                onClick={() => remove(i)}
                style={{ padding: '0 6px', fontSize: '0.75rem', lineHeight: 1.6 }}
                aria-label={`Remove ${name}`}>
                <i className="fas fa-times" />
              </button>
            </span>
          ))}
        </div>
      )}

      {/* Size by width only. The container is a flex column, so a flex-basis
          here would set the wrapper's HEIGHT — which the dropdown anchors to
          (top: 100%), opening it far below the input. */}
      <SearchableModelSelect
        value=""
        onChange={add}
        commitOnly
        capability={capability}
        placeholder={placeholder || '+ Add detector model...'}
        style={{ width: '100%', maxWidth: 360 }}
      />
    </div>
  )
}
