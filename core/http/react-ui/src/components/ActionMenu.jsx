import { useRef, useState, useEffect, useCallback } from 'react'
import Popover from './Popover'

// ActionMenu renders a kebab (three-dot) button that opens a popover with a
// list of row actions. Replaces the inline cluster of icon buttons that made
// dense tables feel like a control panel — actions stay out of the way until
// the user reaches for them, the way Linear/Vercel/Notion handle row menus.
//
// Items shape:
//   { key, icon?, label, onClick, danger?, disabled?, hidden?, shortcut? }
//   { divider: true }                       // visual separator
//   { type: 'badge', icon?, label }         // non-interactive badge row
//
// Hidden items are filtered out so callers can write conditional menus
// inline (`{ key: 'stop', visible: isRunning, ... }` style) without ternaries.
//
// Keyboard:
//   ArrowUp / ArrowDown  — move highlight (skipping dividers + badges)
//   Enter / Space        — activate
//   Escape               — close, return focus to trigger
export default function ActionMenu({ items, ariaLabel = 'Actions', triggerLabel, compact = false }) {
  const triggerRef = useRef(null)
  const [open, setOpen] = useState(false)
  const [activeIdx, setActiveIdx] = useState(-1)

  const interactive = (Array.isArray(items) ? items : []).filter(it => it && !it.divider && it.type !== 'badge' && !it.hidden)
  const visible = (Array.isArray(items) ? items : []).filter(it => it && !it.hidden)

  const close = useCallback(() => {
    setOpen(false)
    setActiveIdx(-1)
  }, [])

  // Move highlight to the first interactive item when opening, so keyboard
  // users land somewhere meaningful instead of having to arrow into the menu.
  useEffect(() => {
    if (open && activeIdx === -1 && interactive.length > 0) {
      setActiveIdx(0)
    }
  }, [open, activeIdx, interactive.length])

  const handleTriggerKeyDown = (e) => {
    if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      e.stopPropagation()
      setOpen(true)
    }
  }

  const handleMenuKeyDown = (e) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIdx(i => Math.min(interactive.length - 1, (i < 0 ? -1 : i) + 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIdx(i => Math.max(0, (i < 0 ? interactive.length : i) - 1))
    } else if (e.key === 'Home') {
      e.preventDefault()
      setActiveIdx(0)
    } else if (e.key === 'End') {
      e.preventDefault()
      setActiveIdx(interactive.length - 1)
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      const item = interactive[activeIdx]
      if (item && !item.disabled) {
        close()
        item.onClick?.()
      }
    }
  }

  if (interactive.length === 0 && !visible.some(it => it.type === 'badge')) {
    return null
  }

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        className={`action-menu__trigger${compact ? ' action-menu__trigger--compact' : ''}${open ? ' is-open' : ''}`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={triggerLabel || ariaLabel}
        onClick={(e) => { e.stopPropagation(); setOpen(v => !v) }}
        onKeyDown={handleTriggerKeyDown}
      >
        <i className="fas fa-ellipsis-vertical" />
      </button>
      <Popover anchor={triggerRef} open={open} onClose={close} ariaLabel={ariaLabel}>
        <div
          role="menu"
          aria-label={ariaLabel}
          className="action-menu"
          onKeyDown={handleMenuKeyDown}
          // Capture focus when the menu opens so arrow keys work without the
          // user clicking inside first.
          tabIndex={-1}
          ref={el => { if (el && open) el.focus() }}
        >
          {visible.map((item, i) => {
            if (item.divider) {
              return <div key={`d-${i}`} className="action-menu__divider" role="separator" />
            }
            if (item.type === 'badge') {
              return (
                <div key={item.key || `b-${i}`} className="action-menu__badge" role="presentation">
                  {item.icon && <i className={`fas ${item.icon}`} aria-hidden="true" />}
                  <span>{item.label}</span>
                </div>
              )
            }
            const idx = interactive.indexOf(item)
            const active = idx === activeIdx
            return (
              <button
                key={item.key}
                type="button"
                role="menuitem"
                disabled={item.disabled}
                className={`action-menu__item${item.danger ? ' is-danger' : ''}${active ? ' is-active' : ''}`}
                onMouseEnter={() => setActiveIdx(idx)}
                onClick={(e) => {
                  e.stopPropagation()
                  if (item.disabled) return
                  close()
                  item.onClick?.()
                }}
              >
                {item.icon && <i className={`fas ${item.icon} action-menu__icon`} aria-hidden="true" />}
                <span className="action-menu__label">{item.label}</span>
                {item.shortcut && <span className="action-menu__shortcut">{item.shortcut}</span>}
              </button>
            )
          })}
        </div>
      </Popover>
    </>
  )
}
