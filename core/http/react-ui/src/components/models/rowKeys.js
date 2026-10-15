import { useEffect, useRef } from 'react'

// Keyboard for a ledger table. Shared by Explore and Installed so both answer
// to the same keys.

export function isTypingTarget(el) {
  if (!el || !el.tagName) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

// Key handling for one focused row.
//   names    the navigable row names in screen order
//   current  the name of the row that has focus
//   move     (name) => void, moves selection and focus
//   enter    (name) => void, the row's primary action
//   close    () => void
// Only keys aimed at the row itself count: an Enter on a button inside the row
// belongs to that button.
export function rowKeyDown(e, { names, current, move, enter, close }) {
  if (e.altKey || e.ctrlKey || e.metaKey) return
  const index = names.indexOf(current)
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    if (e.target !== e.currentTarget) return
    e.preventDefault()
    const next = names[Math.max(0, Math.min(names.length - 1, index + (e.key === 'ArrowDown' ? 1 : -1)))]
    if (next && next !== current) move(next)
  } else if (e.key === 'Home' || e.key === 'End') {
    if (e.target !== e.currentTarget) return
    e.preventDefault()
    const next = e.key === 'Home' ? names[0] : names[names.length - 1]
    if (next && next !== current) move(next)
  } else if (e.key === 'Enter') {
    if (e.target !== e.currentTarget) return
    e.preventDefault()
    enter?.(current)
  } else if (e.key === 'Escape') {
    close?.()
  }
}

// When the selection clears (the inspector's close button, Escape, or the
// browser's Back), put keyboard focus back on the row that opened it, so a
// narrow layout that hid the table while the inspector was open does not drop
// the user on the page body.
export function useRestoreRowFocus(selectedId, containerRef) {
  const last = useRef(null)
  useEffect(() => {
    if (selectedId) {
      last.current = selectedId
      return undefined
    }
    const id = last.current
    if (!id) return undefined
    const frame = window.requestAnimationFrame(() => {
      const row = containerRef.current?.querySelector(`[data-entity="${CSS.escape(id)}"]`)
      row?.scrollIntoView({ block: 'nearest' })
      row?.focus({ preventScroll: true })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [selectedId, containerRef])
}

// Keys that work anywhere on a ledger page: "/" jumps to search, "d" flips the
// row density, "o" opens the selected model's own page, Escape closes the
// inspector. They stand down while the user is
// typing and while a dialog (the cleanup sheet, a confirmation) is open.
export function useLedgerKeys({ enabled, searchRef, onToggleDensity, hasSelection, onClose, onOpen }) {
  const latest = useRef({})
  latest.current = { onToggleDensity, hasSelection, onClose, onOpen }
  useEffect(() => {
    if (!enabled) return undefined
    const onKey = (e) => {
      if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
      if (isTypingTarget(e.target)) return
      if (document.querySelector('[role="dialog"][aria-modal="true"], [role="alertdialog"]')) return
      if (e.key === '/') {
        e.preventDefault()
        searchRef.current?.focus()
        searchRef.current?.select()
      } else if (e.key === 'd' || e.key === 'D') {
        latest.current.onToggleDensity()
      } else if ((e.key === 'o' || e.key === 'O') && latest.current.hasSelection && latest.current.onOpen) {
        latest.current.onOpen()
      } else if (e.key === 'Escape' && latest.current.hasSelection) {
        latest.current.onClose()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [enabled, searchRef])
}
