import { useMemo } from 'react'
import SearchableSelect from './SearchableSelect'
import { Button } from './ui'
import './listeditors.css'

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
    <div className="le">
      <div className="text-meta">
        Restricted regex: literals, <code>[…]</code> classes, <code>\w \d \s</code>, <code>?*+{'{m,n}'}</code>, anchors.
        Each pattern must contain a fixed literal run of ≥3 characters (e.g. <code>sk-prefix-</code>);
        <code>.</code> and capturing groups are not allowed. Matches report under the pattern name.
      </div>

      {rows.length === 0 && (
        <div className="text-meta">
          No custom patterns. Enable built-ins above, or add a pattern for an internal credential
          format (e.g. <code>tok-[A-Za-z0-9]{'{32,64}'}</code>).
        </div>
      )}

      {rows.map((r, i) => (
        <div key={i} className="le__row">
          <input
            className="dk-input le__in le__in--name"
            value={r.name || ''}
            placeholder="Name (group), e.g. INTERNAL_TOKEN"
            onChange={e => update(i, { name: e.target.value })}
            aria-label="Pattern name"
          />
          <input
            className="dk-input le__in le__in--match"
            value={r.match || ''}
            placeholder="match, e.g. tok-[A-Za-z0-9]{32,64}"
            onChange={e => update(i, { match: e.target.value })}
            aria-label="Pattern match"
          />
          <SearchableSelect
            value={r.action || ''}
            onChange={v => update(i, { action: v })}
            options={ACTION_OPTIONS}
            placeholder="Action..."
            className="le__select le__select--action"
          />
          <input
            className="dk-input le__in le__in--num"
            type="number"
            min={0}
            value={r.min_len || 0}
            title="Minimum match length (0 = no floor)"
            // min={0} only constrains the spinner, not keyboard entry. Clamp a
            // typed negative to 0 (a negative floor is meaningless and would
            // disable the length filter). When we clamp, force the DOM value
            // too: the resulting 0->0 state change is a no-op, so React's
            // controlled input would otherwise keep displaying the rejected
            // "-5" even though the saved value is 0.
            onChange={e => {
              const parsed = parseInt(e.target.value, 10)
              const n = Math.max(0, parsed || 0)
              if (parsed < 0) e.target.value = String(n)
              update(i, { min_len: n })
            }}
            aria-label="Minimum length"
          />
          <Button icon="close" size="sm" onClick={() => remove(i)} aria-label="Remove pattern" />
        </div>
      ))}

      <Button size="sm" icon="plus" className="self-start text-xs" onClick={add}>
        Add pattern
      </Button>
    </div>
  )
}
