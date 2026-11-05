import { useEffect, useRef } from 'react'

// Focus handling of a modal sheet of the agent form: focus goes to the close
// button on open, Tab stays inside, Escape closes, and focus returns to the
// opener on close. Attach `sheetRef` to the dialog and `closeRef` to its
// close button.
export function useSheetFocus(onClose) {
  const sheetRef = useRef(null)
  const closeRef = useRef(null)
  const openerRef = useRef(null)
  // The parent passes a fresh onClose on every render. Read it through a ref
  // so the effect below runs once per open: re-running it would move focus to
  // the opener and back to Close, pulling it out of the control in use.
  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose })

  useEffect(() => {
    openerRef.current = document.activeElement
    closeRef.current?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onCloseRef.current(); return }
      if (e.key !== 'Tab' || !sheetRef.current) return
      const focusable = Array.from(sheetRef.current.querySelectorAll('button:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'))
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', onKey, true)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      const el = openerRef.current
      if (el && document.contains(el)) el.focus?.()
    }
  }, [])
  return { sheetRef, closeRef }
}
