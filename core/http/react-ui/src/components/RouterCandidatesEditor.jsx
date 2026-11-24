import { useMemo } from 'react'
import { useFormContext } from '../contexts/FormContext'
import ModelPicker from './ModelPicker'
import { Button } from './ui'
import './listeditors.css'

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
    <div className="le">
      {items.length === 0 && (
        <div className="le__hint">
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

      <Button size="sm" icon="plus" className="self-start" onClick={add}>
        Add candidate
      </Button>
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
      className="card le__cand"
    >
      <div className="le__cand-head">
        <span className="fw-semibold">#{index + 1}</span>
        <Button icon="arrow-up" size="sm" onClick={() => onMove(-1)} disabled={index === 0} title="Move up (smaller priority)" />
        <Button icon="arrow-down" size="sm" onClick={() => onMove(1)} disabled={index === total - 1} title="Move down" />
        <span className="le__cand-note">
          {index === 0 ? 'tried first' : index === total - 1 ? 'tried last (fallback-class)' : ''}
        </span>
      </div>

      <div className="le__cand-model">
        <ModelPicker
          variant="field" allowCustom searchable
          value={row?.model || ''}
          onChange={(v) => onChange((r) => ({ ...r, model: v }))}
          placeholder="downstream model..."
        />
        <Button icon="trash" size="sm" onClick={onRemove} title="Remove candidate" />
      </div>

      <div>
        <div className="le__cand-help">
          {visible.length === 0
            ? 'No policies defined yet — add policies above before assigning labels.'
            : 'Labels this model can serve. The middleware requires the candidate to cover every label the classifier activates.'}
        </div>
        <div className="le__labels">
          {visible.map((label) => {
            const on = labels.includes(label)
            const known = knownLabelSet.has(label)
            return (
              <button
                key={label}
                type="button"
                onClick={() => toggleLabel(label)}
                title={known ? '' : 'Not in router.policies — typo or stale'}
                className={`le__label${on ? ' is-on' : ''}${known ? '' : ' is-unknown'}`}
                aria-pressed={on}
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
