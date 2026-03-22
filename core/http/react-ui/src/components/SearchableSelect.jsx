import { useState, useEffect, useRef, useMemo } from 'react'

export default function SearchableSelect({
  value, onChange, options, placeholder = 'Select...',
  allOption, searchPlaceholder = 'Search...',
  disabled = false, style, className = '',
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [focusIndex, setFocusIndex] = useState(-1)
  const ref = useRef(null)
  const buttonRef = useRef(null)
  const listRef = useRef(null)

  const items = useMemo(() =>
    options.map(o => typeof o === 'string' ? { value: o, label: o } : o),
    [options]
  )

  useEffect(() => {
    const handler = (e) => { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const filtered = query
    ? items.filter(o => o.label.toLowerCase().includes(query.toLowerCase()))
    : items

  // Determine which item Enter will select
  const enterTarget = focusIndex >= 0
    ? { type: 'item', index: focusIndex }
    : filtered.length > 0
      ? { type: 'item', index: 0 }
      : allOption
        ? { type: 'all' }
        : null

  const select = (val) => {
    onChange(val)
    setOpen(false)
    setQuery('')
    setFocusIndex(-1)
    buttonRef.current?.focus()
  }

  const handleKeyDown = (e) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setFocusIndex(i => Math.min(i + 1, filtered.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setFocusIndex(i => Math.max(i - 1, -1))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (!enterTarget) return
      if (enterTarget.type === 'all') {
        select('')
      } else {
        select(filtered[enterTarget.index].value)
      }
    } else if (e.key === 'Escape') {
      setOpen(false)
      setQuery('')
      setFocusIndex(-1)
      buttonRef.current?.focus()
    }
  }

  // Scroll focused item into view
  useEffect(() => {
    if (focusIndex >= 0 && listRef.current) {
      const offset = allOption ? focusIndex + 1 : focusIndex
      const item = listRef.current.children[offset]
      if (item) item.scrollIntoView({ block: 'nearest' })
    }
  }, [focusIndex, allOption])

  const displayLabel = !value ? placeholder
    : (items.find(o => o.value === value)?.label ?? value)

  const itemStyle = (isActive, isFocused) => ({
    padding: '6px 10px', fontSize: '0.8125rem', cursor: 'pointer',
    display: 'flex', alignItems: 'center', gap: '6px',
    color: isActive ? 'var(--color-primary)' : 'var(--color-text-primary)',
    fontWeight: isActive ? 600 : 400,
    background: isFocused ? 'var(--color-bg-tertiary)' : (isActive ? 'var(--color-bg-tertiary)' : 'transparent'),
  })

  return (
    <div ref={ref} className={className} style={{ position: 'relative', minWidth: 160, ...style }}>
      <button
        ref={buttonRef}
        type="button"
        className="input"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => { if (!disabled) { setOpen(!open); setQuery(''); setFocusIndex(-1) } }}
        style={{
          width: '100%', padding: '4px 8px', fontSize: '0.8125rem',
          cursor: disabled ? 'not-allowed' : 'pointer',
          display: 'flex', alignItems: 'center', gap: '6px',
          background: 'var(--color-bg-primary)', border: '1px solid var(--color-border)',
          borderRadius: 'var(--radius-md)',
          color: value ? 'var(--color-text-primary)' : 'var(--color-text-muted)',
          opacity: disabled ? 0.5 : 1,
        }}
      >
        <span style={{ flex: 1, textAlign: 'left' }}>{displayLabel}</span>
        <i className="fas fa-chevron-down" style={{ fontSize: '0.5rem', color: 'var(--color-text-muted)' }} />
      </button>
      {open && (
        <div style={{
          position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 100, marginTop: 4,
          minWidth: 200, maxHeight: 260, background: 'var(--color-bg-secondary)',
          border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)',
          boxShadow: 'var(--shadow-md)', display: 'flex', flexDirection: 'column',
          animation: 'dropdownIn 120ms ease-out',
        }}>
          <div style={{ padding: '6px', borderBottom: '1px solid var(--color-border-subtle)' }}>
            <input
              autoFocus
              className="input"
              type="text"
              placeholder={searchPlaceholder}
              value={query}
              onChange={(e) => { setQuery(e.target.value); setFocusIndex(-1) }}
              onKeyDown={handleKeyDown}
              style={{ width: '100%', padding: '4px 8px', fontSize: '0.8125rem' }}
            />
          </div>
          <div ref={listRef} role="listbox" style={{ overflowY: 'auto', maxHeight: 200 }}>
            {allOption && (
              <div
                role="option"
                aria-selected={!value}
                onClick={() => select('')}
                style={itemStyle(!value, focusIndex === -1 && enterTarget?.type === 'all')}
                onMouseEnter={() => setFocusIndex(-1)}
              >
                <span style={{ flex: 1 }}>{allOption}</span>
                {enterTarget?.type === 'all' && (
                  <span style={{ marginLeft: 'auto', color: 'var(--color-text-muted)', fontSize: '0.75rem' }}>↵</span>
                )}
              </div>
            )}
            {filtered.map((o, i) => {
              const isActive = value === o.value
              const isEnterTarget = enterTarget?.type === 'item' && enterTarget.index === i
              const isFocused = focusIndex === i || isEnterTarget
              return (
                <div
                  key={o.value}
                  role="option"
                  aria-selected={isActive}
                  onClick={() => select(o.value)}
                  style={itemStyle(isActive, isFocused)}
                  onMouseEnter={() => setFocusIndex(i)}
                >
                  <span style={{ flex: 1 }}>{o.label}</span>
                  {isEnterTarget && (
                    <span style={{ marginLeft: 'auto', color: 'var(--color-text-muted)', fontSize: '0.75rem' }}>↵</span>
                  )}
                </div>
              )
            })}
            {filtered.length === 0 && !allOption && (
              <div style={{ padding: '6px 10px', fontSize: '0.8125rem', color: 'var(--color-text-muted)', fontStyle: 'italic' }}>
                No matches
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
