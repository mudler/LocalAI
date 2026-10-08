import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'

// Search in this chat: a bar under the header with the count and the arrows.
// Enter goes to the next match, Shift+Enter to the previous one, Esc closes.
export default function FindBar({ query, onQuery, index, count, onStep, onClose, focusToken }) {
  const { t } = useTranslation('chat')
  const inputRef = useRef(null)

  // Opening it again, from the keyboard, puts the cursor back in the box.
  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [focusToken])

  return (
    <div className="cx-find" role="search" data-testid="chat-find">
      <Icon name="search" />
      <input
        ref={inputRef}
        type="text"
        value={query}
        onChange={(e) => onQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') { e.preventDefault(); onStep(e.shiftKey ? -1 : 1) }
          else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
        }}
        placeholder={t('find.placeholder')}
        aria-label={t('find.placeholder')}
        data-testid="chat-find-input"
      />
      <span className="cx-find__count" role="status" aria-live="polite" data-testid="chat-find-count">
        {query ? (count > 0 ? t('find.count', { current: index + 1, total: count }) : t('find.none')) : ''}
      </span>
      <button type="button" className="cx-icobtn" onClick={() => onStep(-1)} disabled={count === 0} title={t('find.previous')} aria-label={t('find.previous')}>
        <Icon name="chevron-up" />
      </button>
      <button type="button" className="cx-icobtn" onClick={() => onStep(1)} disabled={count === 0} title={t('find.next')} aria-label={t('find.next')}>
        <Icon name="chevron-down" />
      </button>
      <button type="button" className="cx-icobtn" onClick={onClose} title={t('find.close')} aria-label={t('find.close')}>
        <Icon name="close" />
      </button>
    </div>
  )
}
