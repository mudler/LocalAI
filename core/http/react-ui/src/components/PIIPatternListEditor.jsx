import { useState, useEffect, useMemo } from 'react'
import { apiUrl } from '../utils/basePath'
import SearchableSelect from './SearchableSelect'

const ACTION_OPTIONS = [
  { value: 'mask', label: 'Mask — replace with a [REDACTED:id] placeholder' },
  { value: 'block', label: 'Block — reject the request (request side) / mask in stream' },
  { value: 'route_local', label: 'Route local — keep text, force local-only routing' },
]

export default function PIIPatternListEditor({ value, onChange }) {
  const items = Array.isArray(value) ? value : []

  const [catalog, setCatalog] = useState([])
  const [loadError, setLoadError] = useState(null)

  useEffect(() => {
    let cancelled = false
    fetch(apiUrl('/api/pii/patterns'))
      .then(r => r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`)))
      .then(data => { if (!cancelled) setCatalog(data?.patterns || []) })
      .catch(err => { if (!cancelled) setLoadError(err.message) })
    return () => { cancelled = true }
  }, [])

  const idOptions = useMemo(() =>
    catalog.map(p => ({
      value: p.id,
      label: p.description ? `${p.id} — ${p.description}` : p.id,
    })),
    [catalog]
  )

  // Patterns already chosen — exclude from the "add row" select so each
  // pattern only appears once per model.
  const usedIDs = new Set(items.map(it => it?.id).filter(Boolean))
  const availableForAdd = idOptions.filter(o => !usedIDs.has(o.value))

  const update = (index, key, val) => {
    const next = items.map((it, i) =>
      i === index ? { ...it, [key]: val } : it
    )
    onChange(next)
  }

  const remove = (index) => {
    onChange(items.filter((_, i) => i !== index))
  }

  const add = (id) => {
    const cat = catalog.find(c => c.id === id)
    onChange([...items, { id, action: cat?.action || 'mask' }])
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, width: '100%' }}>
      {loadError && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-error)' }}>
          Could not load pattern catalog: {loadError}. You can still type IDs manually.
        </div>
      )}

      {items.length === 0 && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
          No overrides — every pattern uses its global default action. Add a row below to
          tighten or relax the action for a specific pattern on this model.
        </div>
      )}

      {items.map((row, i) => {
        const cat = catalog.find(c => c.id === row?.id)
        const idLabel = cat?.description ? `${row.id} — ${cat.description}` : (row?.id || '')
        // Show the chosen id even if the catalog hasn't loaded yet (or
        // the YAML references an unknown pattern), so users can edit
        // without losing context.
        const idItems = [
          ...(row?.id && !idOptions.some(o => o.value === row.id)
            ? [{ value: row.id, label: idLabel }]
            : []),
          ...idOptions.filter(o => o.value === row?.id || !usedIDs.has(o.value)),
        ]
        return (
          <div key={i} style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
            <SearchableSelect
              value={row?.id || ''}
              onChange={v => update(i, 'id', v)}
              options={idItems}
              placeholder="Pattern..."
              style={{ flex: '1 1 220px', minWidth: 200 }}
            />
            <SearchableSelect
              value={row?.action || 'mask'}
              onChange={v => update(i, 'action', v)}
              options={ACTION_OPTIONS}
              placeholder="Action..."
              style={{ flex: '1 1 240px', minWidth: 220 }}
            />
            <button type="button" className="btn btn-secondary btn-sm"
              onClick={() => remove(i)}
              style={{ padding: '2px 8px', fontSize: '0.75rem' }}>
              <i className="fas fa-times" />
            </button>
          </div>
        )
      })}

      {availableForAdd.length > 0 && (
        <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <SearchableSelect
            value=""
            onChange={v => v && add(v)}
            options={availableForAdd}
            placeholder="+ Add pattern override..."
            style={{ flex: '1 1 220px', minWidth: 200 }}
          />
        </div>
      )}
    </div>
  )
}
