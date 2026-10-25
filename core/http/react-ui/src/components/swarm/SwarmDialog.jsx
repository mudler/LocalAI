import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'

// The kit's dialog for the Swarm pages: a title, one sentence, a body and a
// footer. It traps focus, closes on Escape and on a click on the veil, and
// gives focus back to what opened it. `pending` holds it open while a call runs.
//
// Pass the buttons as `foot`; the first focusable control in the body gets focus
// unless `initialFocus` names a selector.
export default function SwarmDialog({
  title, desc, children, foot, onClose, pending = false, testId, role = 'dialog', wide = false, initialFocus,
}) {
  const { t } = useTranslation('swarm')
  const ref = useRef(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  const pendingRef = useRef(pending)
  pendingRef.current = pending
  const titleId = `sw-dialog-${testId || 'x'}-title`

  useEffect(() => {
    const opener = document.activeElement
    const dialog = ref.current
    const first = (initialFocus && dialog?.querySelector(initialFocus)) || dialog?.querySelector('input, select, textarea, .sw-dialog__primary') || dialog?.querySelector('button')
    first?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
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
  }, [initialFocus])

  return createPortal(
    <div className="dk-veil" data-state="open" onMouseDown={() => { if (!pending) onClose() }}>
      <div
        ref={ref}
        className={`dk-dialog sw-dialog${wide ? ' dk-dialog--wide' : ''}`}
        role={role}
        aria-modal="true"
        aria-labelledby={titleId}
        data-state="open"
        data-testid={testId}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="dk-dialog-head">
          <div>
            <h3 className="dk-dialog-title" id={titleId}>{title}</h3>
            {desc && <p className="dk-dialog-desc">{desc}</p>}
          </div>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('dialog.close')} disabled={pending} onClick={onClose}>
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
