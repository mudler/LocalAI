import { useRef } from 'react'
import { useTranslation } from 'react-i18next'

// ModalityChips — chip row that filters the Backend dropdown by modality.
// The empty string key acts as "Any" (no filter). Arrow-key navigation
// follows the WAI-ARIA radiogroup pattern: ArrowLeft/Up moves focus to the
// previous chip, ArrowRight/Down to the next, Home/End jump to the ends.
// Space/Enter selects — the chip-button's native click handler covers both.
//
// Labels resolve through the same `modality.*` keys the Backend dropdown uses
// for its group headers. They used to be hardcoded English shorthand, so one
// modality carried two names on the same screen ("Speech" on the chip,
// "Speech recognition" on the group it filtered to) and no locale but English
// had either.
//
// Parent owns the filter state and wires it into buildBackendOptions.
// Keeping this component dumb means it never has to know about backends.

const CHIP_KEYS = ['', 'text', 'asr', 'tts', 'image', 'video', 'embeddings', 'reranker', 'detection', 'vad']

export default function ModalityChips({ value = '', onChange, disabled = false }) {
  const { t } = useTranslation('importModel')
  const refs = useRef([])

  const setRef = (i) => (el) => { refs.current[i] = el }

  const focusAt = (i) => {
    const n = CHIP_KEYS.length
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
        focusAt(CHIP_KEYS.length - 1)
        break
      default:
        break
    }
  }

  const pick = (key) => {
    if (disabled) return
    onChange?.(key)
  }

  // Roving tabindex: only the active chip (or the first one when the filter is
  // Any / unknown) is in the tab order. Standard radiogroup pattern — Tab
  // lands once, arrows move.
  const activeIdx = CHIP_KEYS.indexOf(value)

  return (
    <div
      role="radiogroup"
      aria-label={t('form.modalityFilter')}
      data-testid="modality-chips"
      className="modality-chips"
    >
      {CHIP_KEYS.map((key, i) => {
        const checked = value === key
        const tabIndex = (activeIdx === -1 ? i === 0 : i === activeIdx) ? 0 : -1
        return (
          <button
            key={key || 'any'}
            ref={setRef(i)}
            type="button"
            role="radio"
            aria-checked={checked}
            tabIndex={tabIndex}
            data-testid={`modality-chip-${key}`}
            disabled={disabled}
            onClick={() => pick(key)}
            onKeyDown={(e) => handleKeyDown(e, i)}
            className={`modality-chip${checked ? ' is-active' : ''}`}
          >
            {key ? t(`modality.${key}`) : t('form.modalityAny')}
          </button>
        )
      })}
    </div>
  )
}
