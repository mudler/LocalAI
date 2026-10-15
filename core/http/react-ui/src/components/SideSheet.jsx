import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import Icon from './Icon'

// The kit's side sheet (a bottom sheet on a phone) as one component: portal,
// dialog roles, focus moved in and returned, Tab kept inside, Escape and a
// click on the veil to close. The body scrolls; the footer holds the actions.
//
//   title, description   the head
//   footer               buttons for the foot (primary action last)
//   wide                 the 720 px width
//   closeLabel           accessible name of the close button
export default function SideSheet({ title, description, footer, onClose, wide = false, closeLabel = 'Close', testId, labelId = 'side-sheet-title', children }) {
  const sheetRef = useRef(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose

  useEffect(() => {
    const opener = document.activeElement
    const sheet = sheetRef.current
    const first = sheet?.querySelector('.dk-sheet-body input:not([disabled]), .dk-sheet-body button:not([disabled]), .dk-sheet-body select')
    ;(first || sheet?.querySelector('button'))?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') {
        if (e.target?.closest?.('[role="listbox"]') || e.target?.getAttribute?.('aria-expanded') === 'true') return
        e.preventDefault(); e.stopPropagation(); closeRef.current(); return
      }
      if (e.key !== 'Tab' || !sheet) return
      const focusable = Array.from(sheet.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])'))
      if (focusable.length === 0) return
      const head = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === head) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); head.focus() }
    }
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      if (opener && document.contains(opener)) opener.focus?.()
    }
  }, [])

  return createPortal(
    <div className="dk-sheet-veil" data-state="open" onMouseDown={onClose}>
      <div
        ref={sheetRef}
        className={`dk-sheet${wide ? ' dk-sheet--wide' : ''}`}
        role="dialog" aria-modal="true" aria-labelledby={labelId} data-state="open" data-testid={testId}
        onMouseDown={e => e.stopPropagation()}
      >
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id={labelId}>{title}</h2>
            {description && <p className="dk-sheet-desc">{description}</p>}
          </div>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={closeLabel} onClick={onClose}><Icon name="close" /></button>
        </div>
        <div className="dk-sheet-body">{children}</div>
        {footer && <div className="dk-sheet-foot">{footer}</div>}
      </div>
    </div>,
    document.body,
  )
}
