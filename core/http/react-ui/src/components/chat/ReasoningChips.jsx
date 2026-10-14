import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { REASONING_EFFORTS } from './chatActions'

// The two reasoning controls in the Chat composer, both stored per chat:
//   the effort chip   opens a menu. "Model default" sends no reasoning_effort.
//   the traces chip   a toggle. On, the chat asks for reasoning_effort "none" and
//                     the thread drops any reasoning the model still sends.
// The ref gives the page open() for the /effort action.
const ReasoningChips = forwardRef(function ReasoningChips({ effort, off, onEffort, onOff }, ref) {
  const { t } = useTranslation('chat')
  const [open, setOpen] = useState(false)
  const rootRef = useRef(null)
  const chipRef = useRef(null)
  const menuRef = useRef(null)
  const choices = ['', ...REASONING_EFFORTS]
  const current = off ? 'none' : (effort || '')
  const nameOf = (v) => (v ? t(`reasoning.level.${v}`) : t('reasoning.default'))

  const close = useCallback((restoreFocus = false) => {
    setOpen(false)
    if (restoreFocus) chipRef.current?.focus()
  }, [])
  const openMenu = useCallback(() => { if (!off) setOpen(true) }, [off])
  useImperativeHandle(ref, () => ({ open: openMenu }), [openMenu])

  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => { if (rootRef.current && !rootRef.current.contains(e.target)) close() }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open, close])

  useEffect(() => {
    if (!open) return
    const items = menuRef.current?.querySelectorAll('[role="menuitemradio"]')
    items?.[Math.max(0, choices.indexOf(current))]?.focus()
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const pick = (v) => { onEffort(v); close(true) }

  const onMenuKey = (e) => {
    const items = Array.from(menuRef.current?.querySelectorAll('[role="menuitemradio"]') || [])
    const at = items.indexOf(document.activeElement)
    if (e.key === 'ArrowDown') { e.preventDefault(); items[(at + 1) % items.length]?.focus() }
    else if (e.key === 'ArrowUp') { e.preventDefault(); items[(at + items.length - 1) % items.length]?.focus() }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(true) }
    else if (e.key === 'Tab') close()
  }

  return (
    <span className="cx-chipset">
      <span className="home-pick" ref={rootRef}>
        <button
          ref={chipRef}
          type="button"
          className="home-chip"
          aria-haspopup="menu"
          aria-expanded={open}
          disabled={off}
          title={t('reasoning.effortTitle')}
          onClick={() => (open ? close() : openMenu())}
          data-testid="chat-effort-chip"
        >
          <Icon name="brain" />
          <span className="home-chip__text">{t('reasoning.effortLabel')}: {nameOf(current)}</span>
          <Icon name="chevron-down" className="home-chip__caret" />
        </button>
        {open && (
          <div
            ref={menuRef}
            className="home-menu cx-effort-menu"
            role="menu"
            aria-label={t('reasoning.effortTitle')}
            onKeyDown={onMenuKey}
            data-testid="chat-effort-menu"
          >
            {choices.map(v => (
              <button
                key={v || 'default'}
                type="button"
                role="menuitemradio"
                aria-checked={v === current}
                className="home-menu__item cx-effort-item"
                onClick={() => pick(v)}
                data-testid={`chat-effort-${v || 'default'}`}
              >
                <span>{nameOf(v)}</span>
                {v === current && <Icon name="check" className="home-menu__check" />}
              </button>
            ))}
          </div>
        )}
      </span>
      <button
        type="button"
        className="home-chip cx-chip-toggle"
        aria-pressed={!!off}
        onClick={() => { close(); onOff(!off) }}
        title={t('reasoning.offTitle')}
        data-testid="chat-reasoning-off"
      >
        <Icon name="eye-off" />
        <span className="home-chip__text">{t('reasoning.offLabel')}</span>
      </button>
    </span>
  )
})

export default ReasoningChips
