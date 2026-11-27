import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
// The real path of the compat stand-in, not '../Icon': the Talking Head add-on
// bundles this file too, and its build answers stand-ins only for its own src/.
import Icon from '../../../../../internal/host-compat/src/components/Icon.jsx'
import { getSdk } from '../../../../../internal/host-compat/src/runtime.js'

// A side sheet over the page: the veil, the head with a title, a line under it
// and Close, and the body. It takes the focus when it opens, keeps Tab inside,
// closes on Escape or a click on the veil, and gives the focus back to the
// control that opened it. The words come from the caller, so the Talk and the
// Talking Head pages share it with their own locale files.
//
// The sheet is drawn in document.body, outside the add-on's root element, and
// the host scopes an add-on style sheet to [data-addon="<id>"]. The wrapper
// carries the same attribute, so the add-on's own sheet styles reach it.
export default function Sheet({ id, title, desc, closeLabel, onClose, testId, children }) {
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
      const focusable = Array.from(sheetRef.current.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'))
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

  return createPortal(
    <div data-addon={getSdk().addonId}>
      <div className="dk-sheet-veil talk-sheet-veil" data-state="open" onMouseDown={onClose}>
        <div
          ref={sheetRef}
          className="dk-sheet talk-sheet"
          role="dialog"
          aria-modal="true"
          aria-labelledby={`${id}-title`}
          data-state="open"
          data-testid={testId}
          onMouseDown={(e) => e.stopPropagation()}
        >
          <span className="dk-sheet-grip" aria-hidden="true" />
          <div className="dk-sheet-head">
            <div>
              <h2 className="dk-sheet-title" id={`${id}-title`}>{title}</h2>
              {desc && <p className="dk-sheet-desc">{desc}</p>}
            </div>
            <button ref={closeRef} type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={closeLabel} onClick={onClose}>
              <Icon name="close" />
            </button>
          </div>
          <div className="dk-sheet-body talk-sheet__body">
            {children}
          </div>
        </div>
      </div>
    </div>,
    document.body,
  )
}
