import { useState, useEffect, useRef, useCallback } from 'react'
import { useModels } from '../hooks/useModels'

export default function SearchableModelSelect({ value, onChange, capability, placeholder = 'Type or select a model...', style }) {
  const { models, loading } = useModels(capability)
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [focusIndex, setFocusIndex] = useState(-1)
  const wrapperRef = useRef(null)
  const listRef = useRef(null)

  // Sync external value into the input
  useEffect(() => {
    setQuery(value || '')
  }, [value])

  // Close on outside click
  useEffect(() => {
    const handler = (e) => {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const filtered = models.filter(m =>
    m.id.toLowerCase().includes(query.toLowerCase())
  )

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
    if (!open) return

    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setFocusIndex(i => Math.min(i + 1, filtered.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setFocusIndex(i => Math.max(i - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (focusIndex >= 0 && focusIndex < filtered.length) {
        commit(filtered[focusIndex].id)
      } else {
        commit(query)
      }
    } else if (e.key === 'Escape') {
      setOpen(false)
      setFocusIndex(-1)
    }
  }

  // Scroll focused item into view
  useEffect(() => {
    if (focusIndex >= 0 && listRef.current) {
      const item = listRef.current.children[focusIndex]
      if (item) item.scrollIntoView({ block: 'nearest' })
    }
  }, [focusIndex])

  return (
    <div ref={wrapperRef} className="searchable-model-select" style={style}>
      <style>{`
        .searchable-model-select {
          position: relative;
          width: 280px;
        }
        .searchable-model-select input {
          width: 100%;
        }
        .sms-dropdown {
          position: absolute;
          top: 100%;
          left: 0;
          right: 0;
          z-index: 50;
          max-height: 220px;
          overflow-y: auto;
          background: var(--color-bg-primary);
          border: 1px solid var(--color-border);
          border-radius: var(--radius-md);
          box-shadow: var(--shadow-md);
          animation: dropdownIn 120ms ease-out;
          margin-top: 2px;
        }
        .sms-item {
          padding: 6px 10px;
          font-size: 0.8125rem;
          cursor: pointer;
          white-space: nowrap;
          overflow: hidden;
          text-overflow: ellipsis;
        }
        .sms-item:hover, .sms-item.sms-focused {
          background: var(--color-bg-tertiary);
        }
        .sms-item.sms-active {
          color: var(--color-primary);
          font-weight: 600;
        }
        .sms-empty {
          padding: 8px 10px;
          font-size: 0.8125rem;
          color: var(--color-text-muted);
        }
      `}</style>
      <input
        className="input"
        aria-haspopup="listbox"
        aria-expanded={open}
        value={query}
        onChange={(e) => {
          setQuery(e.target.value)
          setOpen(true)
          setFocusIndex(-1)
          // Commit on every keystroke so the parent always has current value
          onChange(e.target.value)
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={handleKeyDown}
        placeholder={loading ? 'Loading models...' : placeholder}
      />
      {open && !loading && (
        <div className="sms-dropdown" ref={listRef} role="listbox">
          {filtered.length === 0 ? (
            <div className="sms-empty">
              {query ? 'No matching models — value will be used as-is' : 'No models available'}
            </div>
          ) : (
            filtered.map((m, i) => (
              <div
                key={m.id}
                role="option"
                aria-selected={m.id === value}
                className={`sms-item${i === focusIndex ? ' sms-focused' : ''}${m.id === value ? ' sms-active' : ''}`}
                onMouseEnter={() => setFocusIndex(i)}
                onMouseDown={(e) => {
                  e.preventDefault()
                  commit(m.id)
                }}
              >
                {m.id}
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}
