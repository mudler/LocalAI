import { useEffect, useLayoutEffect, useRef, useState, useCallback } from 'react'
import { createPortal } from 'react-dom'

// Minimal popover: positions itself below-right of the trigger's bounding box,
// flips above when there isn't room below, closes on outside click or Escape,
// returns focus to the trigger. Uses the existing .card surface so it picks
// up theme/border/shadow automatically — no new theming work.
//
// Rendered through a portal on document.body: the popover is position:fixed and
// positioned from the trigger's viewport rect, so it must escape any ancestor
// that establishes a containing block (a row/card with a hover `transform`
// would otherwise re-anchor `position:fixed` to itself, throwing the menu to
// the wrong spot and making it unusable).
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

  // useLayoutEffect so we measure + place the popover before the browser
  // paints — otherwise it flashes at its initial {0,0} for a frame.
  useLayoutEffect(() => {
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
      // preventScroll: focusing the trigger must not yank the page scroll.
      const raf = requestAnimationFrame(() => anchor.current?.focus?.({ preventScroll: true }))
      return () => cancelAnimationFrame(raf)
    }
  }, [open, anchor])

  if (!open) return null

  return createPortal(
    <div
      ref={popoverRef}
      role="dialog"
      aria-label={ariaLabel}
      className="popover card"
      style={{ top: pos.top, left: pos.left }}
    >
      {children}
    </div>,
    document.body
  )
}
