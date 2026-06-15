import { useMemo } from 'react'
import { useFormContext } from '../contexts/FormContext'
import SearchableModelSelect from './SearchableModelSelect'

// RouterCandidatesEditor renders the routing table for a router model.
// Each row binds a downstream model to a SET of policy labels it can
// serve. The middleware picks the first candidate whose labels are a
// superset of the active label set from the classifier, so admins
// order candidates smallest → largest.
//
// Schema mirrors core/config.RouterCandidate:
//   { model: string, labels: []string }
//
// Labels are picked from the parent form's router.policies (a multi-
// select rather than a free-text input) so a typo in one place doesn't
// silently disable a candidate. Labels typed manually are still kept
// — useful when admins paste a config before defining the policies.

export default function RouterCandidatesEditor({ value, onChange }) {
  const items = Array.isArray(value) ? value : []
  const knownLabels = usePolicyLabels()
  const knownLabelSet = useMemo(() => new Set(knownLabels), [knownLabels])

  const update = (index, mut) => {
    const next = items.map((it, i) => (i === index ? mut({ ...it }) : it))
    onChange(next)
  }
  const remove = (index) => onChange(items.filter((_, i) => i !== index))
  const move = (index, dir) => {
    const j = index + dir
    if (j < 0 || j >= items.length) return
    const next = items.slice()
    ;[next[index], next[j]] = [next[j], next[index]]
    onChange(next)
  }
  const add = () => onChange([...items, { model: '', labels: [] }])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)', width: '100%' }}>
      {items.length === 0 && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', padding: 'var(--spacing-sm) 0' }}>
          No candidates yet. Add at least one — order from smallest model to largest.
          The middleware picks the FIRST candidate whose labels superset the active set.
        </div>
      )}

      {items.map((row, i) => (
        <CandidateRow
          key={i}
          index={i}
          total={items.length}
          row={row}
          knownLabels={knownLabels}
          knownLabelSet={knownLabelSet}
          onChange={(mut) => update(i, mut)}
          onRemove={() => remove(i)}
          onMove={(dir) => move(i, dir)}
        />
      ))}

      <button
        type="button"
        className="btn btn-secondary btn-sm"
        onClick={add}
        style={{ alignSelf: 'flex-start' }}
      >
        <i className="fas fa-plus" /> Add candidate
      </button>
    </div>
  )
}

function CandidateRow({ index, total, row, knownLabels, knownLabelSet, onChange, onRemove, onMove }) {
  const labels = Array.isArray(row?.labels) ? row.labels : []
  const toggleLabel = (label) => onChange((r) => ({
    ...r,
    labels: labels.includes(label) ? labels.filter(l => l !== label) : [...labels, label],
  }))

  // Row-local labels not in the parent policy list are still surfaced
  // (with a warning chip) so a stale row doesn't silently lose its
  // labels while the policy list is being edited.
  const unknownOnRow = labels.filter(l => !knownLabelSet.has(l))
  const visible = [...knownLabels, ...unknownOnRow]

  return (
    <div
      className="card"
      style={{
        padding: 'var(--spacing-sm)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--spacing-xs)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div style={{ display: 'flex', gap: 4, alignItems: 'center', fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
        <span style={{ fontWeight: 600 }}>#{index + 1}</span>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={() => onMove(-1)}
          disabled={index === 0}
          title="Move up (smaller priority)"
          style={{ padding: '0 6px' }}
        >
          <i className="fas fa-arrow-up" />
        </button>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={() => onMove(1)}
          disabled={index === total - 1}
          title="Move down"
          style={{ padding: '0 6px' }}
        >
          <i className="fas fa-arrow-down" />
        </button>
        <span style={{ marginLeft: 'auto' }}>
          {index === 0 ? 'tried first' : index === total - 1 ? 'tried last (fallback-class)' : ''}
        </span>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr auto', gap: 'var(--spacing-sm)', alignItems: 'center' }}>
        <SearchableModelSelect
          value={row?.model || ''}
          onChange={(v) => onChange((r) => ({ ...r, model: v }))}
          placeholder="downstream model..."
        />
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={onRemove}
          title="Remove candidate"
        >
          <i className="fas fa-trash" />
        </button>
      </div>

      <div>
        <div style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', marginBottom: 4 }}>
          {visible.length === 0
            ? 'No policies defined yet — add policies above before assigning labels.'
            : 'Labels this model can serve. The middleware requires the candidate to cover every label the classifier activates.'}
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {visible.map((label) => {
            const on = labels.includes(label)
            const known = knownLabelSet.has(label)
            return (
              <button
                key={label}
                type="button"
                onClick={() => toggleLabel(label)}
                title={known ? '' : 'Not in router.policies — typo or stale'}
                style={{
                  padding: '4px 10px',
                  borderRadius: 'var(--radius-md)',
                  fontSize: '0.75rem',
                  fontFamily: 'var(--font-mono)',
                  border: '1px solid ' + (on ? 'var(--color-primary)' : 'var(--color-border)'),
                  background: on ? 'var(--color-primary)' : 'transparent',
                  color: on ? 'var(--color-bg-primary)' : (known ? 'var(--color-text-primary)' : 'var(--color-warning)'),
                  cursor: 'pointer',
                }}
              >
                {label}{!known && ' ⚠'}
              </button>
            )
          })}
        </div>
      </div>
    </div>
  )
}

// usePolicyLabels reads router.policies from the surrounding form state
// and returns the list of declared labels. Falls back to [] when no
// FormContext is present (e.g. preview render).
function usePolicyLabels() {
  const ctx = useFormContext()
  const policies = ctx?.formData?.['router.policies']
  if (!Array.isArray(policies)) return []
  return policies.map(p => p?.label).filter(Boolean)
}
