import { useMemo } from 'react'

// RouterPoliciesEditor renders the label vocabulary the score
// classifier ranks for each request. The shape mirrors
// core/config.RouterPolicy:
//
//   { label: string, description: string }
//
// The description ends up verbatim in the routing system prompt fed
// to the classifier model. Short, action-oriented sentences ("writing
// or debugging code", "small talk") consistently produce cleaner
// label distributions on Arch-Router-style scorers than longer
// taxonomies — keep them tight.

export default function RouterPoliciesEditor({ value, onChange }) {
  const items = Array.isArray(value) ? value : []

  const duplicateLabels = useMemo(() => {
    const seen = new Set()
    const dup = new Set()
    for (const it of items) {
      const label = it?.label
      if (!label) continue
      if (seen.has(label)) dup.add(label)
      else seen.add(label)
    }
    return dup
  }, [items])

  const update = (index, mut) => {
    const next = items.map((it, i) => (i === index ? mut({ ...it }) : it))
    onChange(next)
  }
  const remove = (index) => onChange(items.filter((_, i) => i !== index))
  const add = () => onChange([...items, { label: '', description: '' }])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)', width: '100%' }}>
      {items.length === 0 && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', padding: 'var(--spacing-sm) 0' }}>
          No policies defined. Add at least one — the classifier needs a label vocabulary to rank over,
          and candidates reference these labels.
        </div>
      )}

      {items.map((row, i) => (
        <PolicyRow
          key={i}
          row={row}
          duplicate={!!row?.label && duplicateLabels.has(row.label)}
          onChange={(mut) => update(i, mut)}
          onRemove={() => remove(i)}
        />
      ))}

      <button
        type="button"
        className="btn btn-secondary btn-sm"
        onClick={add}
        style={{ alignSelf: 'flex-start' }}
      >
        <i className="fas fa-plus" /> Add policy
      </button>
    </div>
  )
}

function PolicyRow({ row, duplicate, onChange, onRemove }) {
  return (
    <div
      className="card"
      style={{
        padding: 'var(--spacing-sm)',
        display: 'grid',
        gridTemplateColumns: '180px 1fr auto',
        gap: 'var(--spacing-sm)',
        alignItems: 'center',
        border: '1px solid ' + (duplicate ? 'var(--color-warning, #d97706)' : 'var(--color-border)'),
      }}
    >
      <input
        className="input"
        type="text"
        placeholder="label (e.g. code-generation)"
        value={row?.label || ''}
        onChange={(e) => onChange((r) => ({ ...r, label: e.target.value }))}
        style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}
        title={duplicate ? 'Duplicate label — candidates won\'t be able to distinguish them' : ''}
      />
      <input
        className="input"
        type="text"
        placeholder="short description fed verbatim to the classifier prompt"
        value={row?.description || ''}
        onChange={(e) => onChange((r) => ({ ...r, description: e.target.value }))}
        style={{ fontSize: '0.8125rem' }}
      />
      <button
        type="button"
        className="btn btn-secondary btn-sm"
        onClick={onRemove}
        title="Remove policy"
      >
        <i className="fas fa-trash" />
      </button>
    </div>
  )
}

