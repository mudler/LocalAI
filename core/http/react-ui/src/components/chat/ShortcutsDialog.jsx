import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'

const MOD = () => (typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || '') ? '⌘' : 'Ctrl')

// The keys the chat page answers to. Every row is a key that does something.
export default function ShortcutsDialog({ onClose, canFind }) {
  const { t } = useTranslation('chat')
  const ref = useRef(null)
  const mod = MOD()

  useEffect(() => {
    const el = ref.current
    el?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [onClose])

  const rows = [
    [t('shortcuts.conversations'), [mod, 'K']],
    ...(canFind ? [[t('shortcuts.find'), [mod, 'Shift', 'F']]] : []),
    [t('shortcuts.actions'), ['/']],
    [t('shortcuts.send'), ['Enter']],
    [t('shortcuts.newline'), ['Shift', 'Enter']],
    [t('shortcuts.editLast'), ['↑'], t('shortcuts.inEmptyBox')],
    [t('shortcuts.stop'), ['Esc']],
    [t('shortcuts.moveMessages'), ['↑', '↓']],
    [t('shortcuts.onMessage'), ['C', 'E', 'R', 'B']],
  ]

  return createPortal(
    <>
      <div className="cx-veil" onMouseDown={onClose} />
      <div className="cx-keys" role="dialog" aria-modal="true" aria-label={t('shortcuts.title')} tabIndex={-1} ref={ref} data-testid="chat-shortcuts">
        <h2>{t('shortcuts.title')}</h2>
        <dl>
          {rows.map(([label, keys, note]) => (
            <div key={label} className="cx-keys__row">
              <dt>{label}</dt>
              <dd>
                {keys.map(k => <kbd key={k} className="dk-kbd">{k}</kbd>)}
                {note && <span className="cx-keys__note">{note}</span>}
              </dd>
            </div>
          ))}
        </dl>
        <div className="cx-keys__foot">
          <button type="button" className="cx-btn" onClick={onClose}>{t('shortcuts.close')}</button>
        </div>
      </div>
    </>,
    document.body,
  )
}
