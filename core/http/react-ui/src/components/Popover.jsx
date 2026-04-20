import { useEffect, useRef, useState, useCallback } from 'react'

// Minimal popover: positions itself below-right of the trigger's bounding box,
// flips above when there isn't room below, closes on outside click or Escape,
// returns focus to the trigger. Uses the existing .card surface so it picks
// up theme/border/shadow automatically — no new theming work.
//
// Props:
//   anchor:    ref to the trigger DOMElement (required)
//   open:      boolean
//   onClose:   () => void
//   children:  popover body
//   ariaLabel: accessible label for the dialog
export default function Popover({ anchor, open, onClose, children, ariaLabel }) {
  const popoverRef = useRef(null)
  const [pos, setPos] = useState({ top: 0, left: 0, flipped: false })

  // Compute position from the anchor's bounding box whenever we open or the
  // viewport changes. 240px is the minimum width we'll reserve; bigger content
  // grows naturally.
  const reposition = useCallback(() => {
    if (!anchor?.current) return
    const rect = anchor.current.getBoundingClientRect()
    const popoverHeight = popoverRef.current?.offsetHeight ?? 0
    const spaceBelow = window.innerHeight - rect.bottom
    const flipped = popoverHeight > spaceBelow - 16 && rect.top > popoverHeight
    const top = flipped ? rect.top - popoverHeight - 8 : rect.bottom + 8
    // Prefer left-aligned; clamp so we don't go off-screen right.
    const left = Math.min(rect.left, window.innerWidth - 320)
    setPos({ top, left: Math.max(8, left), flipped })
  }, [anchor])

  useEffect(() => {
    if (!open) return
    reposition()
    window.addEventListener('resize', reposition)
    window.addEventListener('scroll', reposition, true)
    return () => {
      window.removeEventListener('resize', reposition)
      window.removeEventListener('scroll', reposition, true)
    }
  }, [open, reposition])

  // Close on outside click or Escape. Mousedown (not click) so the close
  // happens before a parent handler could re-trigger us.
  useEffect(() => {
    if (!open) return
    const onMouseDown = (e) => {
      if (popoverRef.current && !popoverRef.current.contains(e.target) && !anchor?.current?.contains(e.target)) {
        onClose()
      }
    }
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, onClose, anchor])

  // Return focus to the trigger when the popover closes — keyboard users
  // shouldn't have to tab back through the whole page to find their spot.
  useEffect(() => {
    if (!open && anchor?.current) {
      // requestAnimationFrame so the close is painted before focus jumps;
      // otherwise screen readers announce the trigger mid-transition.
      const raf = requestAnimationFrame(() => anchor.current?.focus?.())
      return () => cancelAnimationFrame(raf)
    }
  }, [open, anchor])

  if (!open) return null

  return (
    <div
      ref={popoverRef}
      role="dialog"
      aria-label={ariaLabel}
      className="popover card"
      style={{ top: pos.top, left: pos.left }}
    >
      {children}
    </div>
  )
}
