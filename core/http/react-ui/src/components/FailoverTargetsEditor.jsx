import { useMemo } from 'react'
import { useFormContext } from '../contexts/FormContext'
import SearchableModelSelect from './SearchableModelSelect'
import Toggle from './Toggle'
import Icon from './Icon'

// FailoverTargetsEditor renders the ordered member list of a failover
// chain. Each row binds a downstream model plus a warm flag that keeps a
// local target loaded rather than cold-started the first time it serves.
//
// Schema mirrors core/config.FailoverTarget:
//   { model: string, warm: bool }
//
// Three conditions make the chain useless even though the YAML stays
// valid, so they are surfaced as inline warnings rather than blocked
// outright (the admin may be mid-edit):
//   - fewer than two targets: nothing to fail over TO.
//   - the same model listed twice: the prober would track one backend
//     under two identities.
//   - a target equal to the chain's OWN name: the alias would point at
//     itself and never resolve.
//
// The model list from useModels() (what SearchableModelSelect searches)
// does not carry each entry's backend, so a remote/proxy target can't be
// told apart from a local one here to disable warm for it. The server
// already rejects/warns on warm for a backend that can't be kept loaded,
// so this editor leaves the toggle enabled for every row.
export default function FailoverTargetsEditor({ value, onChange }) {
  const items = Array.isArray(value) ? value : []
  const ownName = useOwnModelName()

  const duplicateModels = useMemo(() => {
    const seen = new Set()
    const dup = new Set()
    for (const it of items) {
      const model = it?.model
      if (!model) continue
      if (seen.has(model)) dup.add(model)
      else seen.add(model)
    }
    return dup
  }, [items])

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
  const add = () => onChange([...items, { model: '', warm: false }])

  return (
    <div className="fte-list">
      {items.length === 0 && (
        <div className="fte-empty">
          No targets yet. Add at least two — the first healthy one answers, the rest take over in order.
        </div>
      )}

      {items.length === 1 && (
        <div className="fte-warning">
          <Icon name="warning" className="icon-before" />
          Add at least one more target — a chain with a single target has nothing to fail over to.
        </div>
      )}

      {items.map((row, i) => (
        <TargetRow
          key={i}
          index={i}
          total={items.length}
          row={row}
          duplicate={!!row?.model && duplicateModels.has(row.model)}
          isOwnName={!!row?.model && !!ownName && row.model === ownName}
          onChange={(mut) => update(i, mut)}
          onRemove={() => remove(i)}
          onMove={(dir) => move(i, dir)}
        />
      ))}

      <button
        type="button"
        className="btn btn-secondary btn-sm self-start"
        onClick={add}
      >
        <Icon name="plus" /> Add target
      </button>
    </div>
  )
}

function TargetRow({ index, total, row, duplicate, isOwnName, onChange, onRemove, onMove }) {
  const error = isOwnName
    ? "This is the chain's own name — it would point at itself and never resolve."
    : duplicate
      ? 'Duplicate target — the prober would track the same backend under two identities.'
      : null

  return (
    <div className={`card fte-row${error ? ' fte-row--error' : ''}`}>
      <div className="hstack hstack--xs fte-row__head">
        <span className="fw-semibold">#{index + 1}</span>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={() => onMove(-1)}
          disabled={index === 0}
          title="Move up (tried earlier)"
        >
          <Icon name="arrow-up" />
        </button>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={() => onMove(1)}
          disabled={index === total - 1}
          title="Move down"
        >
          <Icon name="arrow-down" />
        </button>
        <span className="ml-auto text-meta">
          {index === 0 ? 'served first' : 'fallback'}
        </span>
      </div>

      <div className="hstack hstack--md fte-row__body">
        <SearchableModelSelect
          value={row?.model || ''}
          onChange={(v) => onChange((r) => ({ ...r, model: v }))}
          placeholder="target model..."
        />
        <span className="hstack hstack--xs">
          <Toggle checked={!!row?.warm} onChange={(v) => onChange((r) => ({ ...r, warm: v }))} />
          <span className="text-meta">Warm</span>
        </span>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={onRemove}
          title="Remove target"
        >
          <Icon name="trash" />
        </button>
      </div>

      {error && (
        <div className="text-error text-xs">
          <Icon name="warning" className="icon-before" />
          {error}
        </div>
      )}
    </div>
  )
}

// useOwnModelName reads the edited model's own name from the surrounding
// form so a target that points back at the chain itself is flagged.
// Returns null without a FormContext (e.g. a preview render) or before a
// name has been typed.
function useOwnModelName() {
  const ctx = useFormContext()
  const name = ctx?.formData?.name
  return typeof name === 'string' && name.trim() ? name.trim() : null
}
