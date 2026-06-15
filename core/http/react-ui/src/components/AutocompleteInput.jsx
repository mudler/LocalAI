import { useState, useEffect, useRef, useCallback } from 'react'
import { useAutocomplete } from '../hooks/useAutocomplete'

export default function AutocompleteInput({ value, onChange, provider, placeholder = 'Type or select...', style }) {
  const { values, loading } = useAutocomplete(provider)
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [focusIndex, setFocusIndex] = useState(-1)
  const wrapperRef = useRef(null)
  const listRef = useRef(null)

  useEffect(() => {
    setQuery(value || '')
  }, [value])

  useEffect(() => {
    const handler = (e) => {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const filtered = values.filter(v =>
    v.toLowerCase().includes(query.toLowerCase())
  )

  const enterTargetIndex = focusIndex >= 0 ? focusIndex
    : filtered.length > 0 ? 0
    : -1

  const commit = useCallback((val) => {
    setQuery(val)
    onChange(val)
    setOpen(false)
    setFocusIndex(-1)
  }, [onChange])

  const handleKeyDown = (e) => {
    if (!open && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      setOpen(true)
      return
    }
    if (!open && e.key === 'Enter') {
      e.preventDefault()
      commit(query)
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
        commit(filtered[enterTargetIndex])
      } else {
        commit(query)
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

  return (
    <div ref={wrapperRef} style={{ position: 'relative', ...style }}>
      <input
        className="input"
        aria-haspopup="listbox"
        aria-expanded={open}
        value={query}
        onChange={(e) => {
          setQuery(e.target.value)
          setOpen(true)
          setFocusIndex(-1)
          onChange(e.target.value)
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={handleKeyDown}
        placeholder={loading ? 'Loading...' : placeholder}
        style={{ width: '100%', fontSize: '0.8125rem' }}
      />
      {open && !loading && filtered.length > 0 && (
        <div
          ref={listRef}
          role="listbox"
          style={{
            position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 50,
            maxHeight: 220, overflowY: 'auto', marginTop: 2,
            background: 'var(--color-bg-primary)', border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-md)', boxShadow: 'var(--shadow-md)',
            animation: 'dropdownIn 120ms ease-out',
          }}
        >
          {filtered.map((v, i) => {
            const isEnterTarget = i === enterTargetIndex
            return (
              <div
                key={v}
                role="option"
                aria-selected={v === value}
                style={{
                  padding: '6px 10px', fontSize: '0.8125rem', cursor: 'pointer',
                  display: 'flex', alignItems: 'center', gap: '6px',
                  color: v === value ? 'var(--color-primary)' : 'var(--color-text-primary)',
                  fontWeight: v === value ? 600 : 400,
                  background: (i === focusIndex || isEnterTarget) ? 'var(--color-bg-tertiary)' : 'transparent',
                }}
                onMouseEnter={() => setFocusIndex(i)}
                onMouseDown={(e) => {
                  e.preventDefault()
                  commit(v)
                }}
              >
                <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{v}</span>
                {isEnterTarget && (
                  <span style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem', flexShrink: 0 }}>↵</span>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
