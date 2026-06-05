import { useMemo } from 'react'
import SearchableSelect from './SearchableSelect'

// Editor for a pattern detector's pii_detection.patterns: a list of
// operator-defined secret patterns. Value is an array of
// { name, match, action?, min_len? }; this renders one row per pattern and
// emits a fresh array on every change. Patterns use a restricted regex subset
// validated server-side at save (an invalid pattern surfaces as the save
// error), so no regex engine is shipped to the client.

const ACTION_OPTIONS = [
  { value: '', label: 'default (use Default Action)' },
  { value: 'mask', label: 'mask — replace the span' },
  { value: 'block', label: 'block — reject the request' },
  { value: 'allow', label: 'allow — detect & log only' },
]

function emptyPattern() {
  return { name: '', match: '', action: '', min_len: 0 }
}

export default function PatternListEditor({ value, onChange }) {
  const rows = useMemo(() => (Array.isArray(value) ? value : []), [value])

  const update = (index, patch) => {
    onChange(rows.map((r, i) => (i === index ? { ...r, ...patch } : r)))
  }
  const remove = (index) => onChange(rows.filter((_, i) => i !== index))
  const add = () => onChange([...rows, emptyPattern()])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8, width: '100%' }}>
      <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
        Restricted regex: literals, <code>[…]</code> classes, <code>\w \d \s</code>, <code>?*+{'{m,n}'}</code>, anchors.
        Each pattern must contain a fixed literal run of ≥3 characters (e.g. <code>sk-prefix-</code>);
        <code>.</code> and capturing groups are not allowed. Matches report under the pattern name.
      </div>

      {rows.length === 0 && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
          No custom patterns. Enable built-ins above, or add a pattern for an internal credential
          format (e.g. <code>tok-[A-Za-z0-9]{'{32,64}'}</code>).
        </div>
      )}

      {rows.map((r, i) => (
        <div key={i} style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
          <input
            className="input"
            value={r.name || ''}
            placeholder="Name (group), e.g. INTERNAL_TOKEN"
            onChange={e => update(i, { name: e.target.value })}
            style={{ flex: '1 1 180px', minWidth: 150, fontSize: '0.8125rem' }}
            aria-label="Pattern name"
          />
          <input
            className="input input-mono"
            value={r.match || ''}
            placeholder="match, e.g. tok-[A-Za-z0-9]{32,64}"
            onChange={e => update(i, { match: e.target.value })}
            style={{ flex: '2 1 240px', minWidth: 200, fontSize: '0.8125rem', fontFamily: 'var(--font-mono)' }}
            aria-label="Pattern match"
          />
          <SearchableSelect
            value={r.action || ''}
            onChange={v => update(i, { action: v })}
            options={ACTION_OPTIONS}
            placeholder="Action..."
            style={{ flex: '1 1 200px', minWidth: 180 }}
          />
          <input
            className="input"
            type="number"
            min={0}
            value={r.min_len || 0}
            title="Minimum match length (0 = no floor)"
            onChange={e => update(i, { min_len: parseInt(e.target.value, 10) || 0 })}
            style={{ width: 80, fontSize: '0.8125rem' }}
            aria-label="Minimum length"
          />
          <button type="button" className="btn btn-secondary btn-sm"
            onClick={() => remove(i)}
            style={{ padding: '2px 8px', fontSize: '0.75rem' }}
            aria-label="Remove pattern">
            <i className="fas fa-times" />
          </button>
        </div>
      ))}

      <button type="button" className="btn btn-secondary btn-sm" onClick={add}
        style={{ alignSelf: 'flex-start', fontSize: '0.75rem' }}>
        <i className="fas fa-plus" /> Add pattern
      </button>
    </div>
  )
}
