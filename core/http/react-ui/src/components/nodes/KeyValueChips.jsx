import { useState } from 'react'

/**
 * Controlled chip-builder for { key: value } maps. Replaces the prior
 * comma-separated-string Node Selector input AND the bespoke Labels editor
 * in the node drawer — both were rendering the same chip pattern with
 * subtly different markup.
 *
 * Fully controlled: parent owns the map and decides what onAdd/onRemove
 * does (form state for the scheduling form; API calls for the live
 * labels editor). The component just renders chips and a key/value input
 * row.
 *
 * Props:
 *   pairs       — current map of key → value
 *   onAdd(k,v)  — called when the user adds a pair (parent handles dedup
 *                 and persistence side effects)
 *   onRemove(k) — called when a chip's × is clicked
 *   placeholderKey, placeholderValue — input hints
 *   ariaLabel   — accessible name for the section
 */
export default function KeyValueChips({ pairs, onAdd, onRemove, placeholderKey = 'key', placeholderValue = 'value', ariaLabel }) {
  const [k, setK] = useState('')
  const [v, setV] = useState('')

  const add = () => {
    const key = k.trim()
    if (!key) return
    onAdd(key, v.trim())
    setK(''); setV('')
  }
  const onKeyDown = (e) => {
    if (e.key === 'Enter') { e.preventDefault(); add() }
  }

  const entries = pairs ? Object.entries(pairs) : []
  return (
    <div aria-label={ariaLabel}>
      {entries.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 'var(--spacing-xs)' }}>
          {entries.map(([key, val]) => (
            <span key={key} style={{
              display: 'inline-flex', alignItems: 'center', gap: 4,
              fontSize: '0.75rem', padding: '2px 8px',
              borderRadius: 'var(--radius-sm)',
              background: 'var(--color-bg-tertiary)',
              border: '1px solid var(--color-border-subtle)',
              fontFamily: 'var(--font-mono)',
            }}>
              {key}={val}
              <button
                type="button"
                onClick={(e) => { e.stopPropagation(); onRemove(key) }}
                aria-label={`Remove ${key}`}
                title="Remove"
                style={{
                  background: 'none', border: 'none', cursor: 'pointer',
                  color: 'var(--color-text-muted)', fontSize: '0.625rem', padding: 0,
                }}
              >
                <i className="fas fa-times" />
              </button>
            </span>
          ))}
        </div>
      )}
      <div style={{ display: 'flex', gap: 'var(--spacing-xs)', alignItems: 'stretch' }}>
        <input
          className="input"
          type="text"
          placeholder={placeholderKey}
          value={k}
          onChange={e => setK(e.target.value)}
          onKeyDown={onKeyDown}
          style={{ flex: 1 }}
        />
        <input
          className="input"
          type="text"
          placeholder={placeholderValue}
          value={v}
          onChange={e => setV(e.target.value)}
          onKeyDown={onKeyDown}
          style={{ flex: 1 }}
        />
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={add}
          disabled={!k.trim()}
          style={{ minHeight: 36 }}
        >
          <i className="fas fa-plus" /> Add
        </button>
      </div>
    </div>
  )
}
