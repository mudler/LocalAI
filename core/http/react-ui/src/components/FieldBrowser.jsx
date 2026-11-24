import { useState, useEffect, useRef, useMemo } from 'react'
import Icon from './Icon'
import './fieldbrowser.css'

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
    <div ref={wrapperRef} className="fb">
      <div className="fb__box">
        <Icon name="search" className="fb__icon" />
        <input
          className="dk-input fb__input"
          value={query}
          onChange={e => { setQuery(e.target.value); setOpen(true); setFocusIndex(-1) }}
          onFocus={() => setOpen(true)}
          onKeyDown={handleKeyDown}
          placeholder="Search fields to add..."
        />
      </div>
      {open && (
        <div
          ref={listRef}
          className="fb__list"
        >
          {filtered.length === 0 ? (
            <div className="fb__empty">
              {query ? 'No matching fields' : 'All fields are already configured'}
            </div>
          ) : (
            filtered.map((field, i) => {
              const isEnterTarget = i === enterTargetIndex
              const isFocused = i === focusIndex || isEnterTarget
              return (
                <div
                  key={field.path}
                  className={`fb__item${isFocused ? ' is-focused' : ''}`}
                  onMouseEnter={() => setFocusIndex(i)}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    handleSelect(field)
                  }}
                >
                  <div className="fb__head">
                    <span className="fb__section" style={{ '--fb-ink': sectionColors[field.section] || 'var(--dk-muted)' }}>
                      {field.section}
                    </span>
                    <span className="fb__label">{field.label}</span>
                    {isEnterTarget && (
                      <span className="fb__enter">↵</span>
                    )}
                  </div>
                  {field.description && (
                    <div className="fb__desc">
                      {field.description}
                    </div>
                  )}
                  <div className="fb__path">
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
