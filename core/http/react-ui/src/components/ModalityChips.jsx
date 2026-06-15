import { useRef } from 'react'

// ModalityChips — horizontal pill row that filters the Backend dropdown
// by modality. The empty string key acts as "Any" (no filter). Arrow-key
// navigation follows the WAI-ARIA radiogroup pattern: ArrowLeft/Up moves
// focus to the previous chip, ArrowRight/Down to the next, Home/End jump
// to the ends. Space/Enter selects — the chip-button's native click
// handler covers both.
//
// Parent owns the filter state and wires it into buildBackendOptions.
// Keeping this component dumb means it never has to know about backends.

const CHIPS = [
  { key: '', label: 'Any' },
  { key: 'text', label: 'Text' },
  { key: 'asr', label: 'Speech' },
  { key: 'tts', label: 'TTS' },
  { key: 'image', label: 'Image' },
  { key: 'embeddings', label: 'Embeddings' },
  { key: 'reranker', label: 'Rerankers' },
  { key: 'detection', label: 'Detection' },
  { key: 'vad', label: 'VAD' },
]

export default function ModalityChips({ value = '', onChange, disabled = false }) {
  const refs = useRef([])

  const setRef = (i) => (el) => { refs.current[i] = el }

  const focusAt = (i) => {
    const n = CHIPS.length
    const idx = ((i % n) + n) % n
    refs.current[idx]?.focus()
  }

  const handleKeyDown = (e, i) => {
    switch (e.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        e.preventDefault()
        focusAt(i + 1)
        break
      case 'ArrowLeft':
      case 'ArrowUp':
        e.preventDefault()
        focusAt(i - 1)
        break
      case 'Home':
        e.preventDefault()
        focusAt(0)
        break
      case 'End':
        e.preventDefault()
        focusAt(CHIPS.length - 1)
        break
      default:
        break
    }
  }

  const pick = (key) => {
    if (disabled) return
    onChange?.(key)
  }

  return (
    <div
      role="radiogroup"
      aria-label="Filter by modality"
      data-testid="modality-chips"
      style={{
        display: 'flex',
        gap: '6px',
        overflowX: 'auto',
        paddingBottom: '2px',
        marginBottom: 'var(--spacing-sm)',
        // Keep the row from vertically wrapping — on narrow viewports it
        // scrolls horizontally per spec.
        whiteSpace: 'nowrap',
      }}
    >
      {CHIPS.map((c, i) => {
        const checked = value === c.key
        // Roving tabindex: only the active chip (or the first one when the
        // filter is Any / unknown) is in the tab order. This is the
        // standard radiogroup pattern — Tab lands once, arrows move.
        const activeIdx = CHIPS.findIndex(x => x.key === value)
        const tabIndex = (activeIdx === -1 ? i === 0 : i === activeIdx) ? 0 : -1
        return (
          <button
            key={c.key || 'any'}
            ref={setRef(i)}
            type="button"
            role="radio"
            aria-checked={checked}
            tabIndex={tabIndex}
            data-testid={`modality-chip-${c.key}`}
            disabled={disabled}
            onClick={() => pick(c.key)}
            onKeyDown={(e) => handleKeyDown(e, i)}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              padding: '4px 12px',
              borderRadius: '999px',
              fontSize: '0.8125rem',
              fontWeight: checked ? 600 : 500,
              cursor: disabled ? 'not-allowed' : 'pointer',
              background: checked ? 'var(--color-primary)' : 'var(--color-bg-primary)',
              color: checked ? 'var(--color-bg-primary)' : 'var(--color-text-primary)',
              border: `1px solid ${checked ? 'var(--color-primary)' : 'var(--color-border-default)'}`,
              flexShrink: 0,
              transition: 'background 120ms, color 120ms, border-color 120ms',
            }}
          >
            {c.label}
          </button>
        )
      })}
    </div>
  )
}
