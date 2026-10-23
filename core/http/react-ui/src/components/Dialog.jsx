import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import Icon from './Icon'

// The kit's dialog as one component: portal, dialog roles, focus moved in and
// returned, Tab kept inside, Escape and a click on the veil to close. Use
// role="alertdialog" for a confirmation.
//
//   title, description   the head
//   foot                 buttons for the foot (primary action last)
//   pending              true while the action runs: it cannot be dismissed
//   closeLabel           accessible name of the close button
export default function Dialog({ title, description, foot, onClose, role = 'dialog', pending = false, closeLabel = 'Close', testId, labelId = 'dialog-title', children }) {
  const ref = useRef(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  const pendingRef = useRef(pending)
  pendingRef.current = pending

  useEffect(() => {
    const opener = document.activeElement
    const dialog = ref.current
    const field = dialog?.querySelector('.dk-dialog-body input:not([disabled]), .dk-dialog-body select, .dk-dialog-body textarea')
    ;(field || dialog?.querySelector('.dk-dialog-foot .dk-btn--primary, .dk-dialog-foot .dk-btn, button'))?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') {
        e.preventDefault(); e.stopPropagation()
        if (!pendingRef.current) closeRef.current()
        return
      }
      if (e.key !== 'Tab' || !dialog) return
      const focusable = Array.from(dialog.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])'))
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
    <div className="dk-veil" data-state="open" onMouseDown={() => { if (!pending) onClose() }}>
      <div
        ref={ref} className="dk-dialog" role={role} aria-modal="true" aria-labelledby={labelId}
        data-state="open" data-testid={testId} onMouseDown={e => e.stopPropagation()}
      >
        <div className="dk-dialog-head">
          <div>
            <h3 className="dk-dialog-title" id={labelId}>{title}</h3>
            {description && <p className="dk-dialog-desc">{description}</p>}
          </div>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={closeLabel} disabled={pending} onClick={onClose}>
            <Icon name="close" />
          </button>
        </div>
        <div className="dk-dialog-body">{children}</div>
        {foot && <div className="dk-dialog-foot">{foot}</div>}
      </div>
    </div>,
    document.body,
  )
}
