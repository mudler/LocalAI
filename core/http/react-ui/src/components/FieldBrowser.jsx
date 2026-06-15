import { useState, useEffect, useRef, useMemo } from 'react'

export default function FieldBrowser({ fields, activeFieldPaths, onAddField }) {
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [focusIndex, setFocusIndex] = useState(-1)
  const wrapperRef = useRef(null)
  const listRef = useRef(null)

  useEffect(() => {
    const handler = (e) => {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const available = useMemo(() =>
    fields.filter(f => !activeFieldPaths.has(f.path)),
    [fields, activeFieldPaths]
  )

  const filtered = useMemo(() => {
    if (!query) return available.slice(0, 30)
    const q = query.toLowerCase()
    return available.filter(f =>
      f.label.toLowerCase().includes(q) ||
      f.path.toLowerCase().includes(q) ||
      (f.description || '').toLowerCase().includes(q) ||
      f.section.toLowerCase().includes(q)
    ).slice(0, 30)
  }, [available, query])

  const enterTargetIndex = focusIndex >= 0 ? focusIndex
    : filtered.length > 0 ? 0
    : -1

  const handleSelect = (field) => {
    onAddField(field)
    setQuery('')
    setOpen(false)
    setFocusIndex(-1)
  }

  const handleKeyDown = (e) => {
    if (!open && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      setOpen(true)
      return
    }
    if (!open) return

    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setFocusIndex(i => Math.min(i + 1, filtered.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setFocusIndex(i => Math.max(i - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (enterTargetIndex >= 0) {
        handleSelect(filtered[enterTargetIndex])
      }
    } else if (e.key === 'Escape') {
      setOpen(false)
      setFocusIndex(-1)
    }
  }

  useEffect(() => {
    if (focusIndex >= 0 && listRef.current) {
      const item = listRef.current.children[focusIndex]
      if (item) item.scrollIntoView({ block: 'nearest' })
    }
  }, [focusIndex])

  const sectionColors = {
    general: 'var(--color-primary)',
    llm: 'var(--color-accent)',
    parameters: 'var(--color-success)',
    templates: 'var(--color-warning)',
    functions: 'var(--color-info, var(--color-primary))',
    reasoning: 'var(--color-accent)',
    diffusers: 'var(--color-warning)',
    tts: 'var(--color-success)',
    pipeline: 'var(--color-accent)',
    grpc: 'var(--color-text-muted)',
    agent: 'var(--color-primary)',
    mcp: 'var(--color-accent)',
    other: 'var(--color-text-muted)',
  }

  return (
    <div ref={wrapperRef} style={{ position: 'relative', marginBottom: 'var(--spacing-md)' }}>
      <div style={{ position: 'relative' }}>
        <i className="fas fa-search" style={{
          position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)',
          color: 'var(--color-text-muted)', fontSize: '0.75rem', pointerEvents: 'none',
        }} />
        <input
          className="input"
          value={query}
          onChange={e => { setQuery(e.target.value); setOpen(true); setFocusIndex(-1) }}
          onFocus={() => setOpen(true)}
          onKeyDown={handleKeyDown}
          placeholder="Search fields to add..."
          style={{ width: '100%', paddingLeft: 32, fontSize: '0.8125rem' }}
        />
      </div>
      {open && (
        <div
          ref={listRef}
          style={{
            position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 100, marginTop: 4,
            maxHeight: 320, overflowY: 'auto',
            background: 'var(--color-bg-secondary)', border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-md)', boxShadow: 'var(--shadow-md)',
            animation: 'dropdownIn 120ms ease-out',
          }}
        >
          {filtered.length === 0 ? (
            <div style={{ padding: '12px 16px', fontSize: '0.8125rem', color: 'var(--color-text-muted)', fontStyle: 'italic' }}>
              {query ? 'No matching fields' : 'All fields are already configured'}
            </div>
          ) : (
            filtered.map((field, i) => {
              const isEnterTarget = i === enterTargetIndex
              const isFocused = i === focusIndex || isEnterTarget
              return (
                <div
                  key={field.path}
                  style={{
                    padding: '8px 12px', cursor: 'pointer',
                    background: isFocused ? 'var(--color-bg-tertiary)' : 'transparent',
                    borderBottom: '1px solid var(--color-border-subtle)',
                  }}
                  onMouseEnter={() => setFocusIndex(i)}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    handleSelect(field)
                  }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{
                      fontSize: '0.625rem', padding: '1px 6px', borderRadius: 'var(--radius-sm)',
                      background: `color-mix(in srgb, ${sectionColors[field.section] || 'var(--color-text-muted)'} 15%, transparent)`,
                      color: sectionColors[field.section] || 'var(--color-text-muted)',
                      fontWeight: 600, whiteSpace: 'nowrap',
                    }}>
                      {field.section}
                    </span>
                    <span style={{ fontSize: '0.8125rem', fontWeight: 500 }}>{field.label}</span>
                    {isEnterTarget && (
                      <span style={{ marginLeft: 'auto', color: 'var(--color-text-muted)', fontSize: '0.75rem' }}>↵</span>
                    )}
                  </div>
                  {field.description && (
                    <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 2, marginLeft: 0 }}>
                      {field.description}
                    </div>
                  )}
                  <div style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', marginTop: 1, fontFamily: 'var(--font-mono)' }}>
                    {field.path}
                  </div>
                </div>
              )
            })
          )}
        </div>
      )}
    </div>
  )
}
