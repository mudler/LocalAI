import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import Icon from './Icon'
import { pushLayer } from '../utils/modalLayers'

// The kit's side sheet (a bottom sheet on a phone) as one component: portal,
// dialog roles, focus moved in and returned, Tab kept inside, Escape and a
// click on the veil to close. The body scrolls; the footer holds the actions.
//
//   title, description   the head
//   headActions          controls drawn in the head, before the close button
//   footer               buttons for the foot (primary action last)
//   wide                 the 720 px width
//   closeLabel           accessible name of the close button
//   initialFocus         'close' puts the first focus on the close button (a
//                        sheet that opens to read before it acts); another
//                        string is a selector of the control to focus; by
//                        default the first control of the body (or one marked
//                        data-autofocus) takes it
//   pending              true while an action runs: the sheet cannot be closed
//   className, veilClass, bodyClass, footClass
//                        extra classes on the panel, the veil, the body and the
//                        foot, for a page that lays out its own content (never
//                        a width: use `wide`)
//   bodyProps, footProps extra attributes (aria-busy, data-*) on the body and
//                        the foot
export default function SideSheet({
  title, description, footer, headActions, onClose, wide = false, closeLabel = 'Close', testId,
  labelId = 'side-sheet-title', describedBy, initialFocus, pending = false,
  className = '', veilClass = '', bodyClass = '', footClass = '', bodyProps, footProps, children,
}) {
  const sheetRef = useRef(null)
  const closeBtnRef = useRef(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  const pendingRef = useRef(pending)
  pendingRef.current = pending
  const focusRef = useRef(initialFocus)

  useEffect(() => {
    const opener = document.activeElement
    const layer = pushLayer()
    const sheet = sheetRef.current
    const want = focusRef.current
    if (want === 'close') closeBtnRef.current?.focus()
    else {
      const first = (want && sheet?.querySelector(want)) || sheet?.querySelector('[data-autofocus], .dk-sheet-body input:not([disabled]), .dk-sheet-body button:not([disabled]), .dk-sheet-body select')
      ;(first || sheet?.querySelector('button'))?.focus()
    }
    const onKey = (e) => {
      // A dialog opened over this sheet is the topmost layer and owns the keys.
      if (!layer.isTop()) return
      if (e.key === 'Escape') {
        if (e.target?.closest?.('[role="listbox"], [role="menu"]') || e.target?.getAttribute?.('aria-expanded') === 'true') return
        e.preventDefault(); e.stopPropagation()
        if (!pendingRef.current) closeRef.current()
        return
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
      layer.pop()
      if (opener && document.contains(opener)) opener.focus?.()
    }
  }, [])

  const cls = (base, extra) => (extra ? `${base} ${extra}` : base)
  return createPortal(
    <div className={cls('dk-sheet-veil', veilClass)} data-state="open" onMouseDown={() => { if (!pending) onClose() }}>
      <div
        ref={sheetRef}
        className={cls(`dk-sheet${wide ? ' dk-sheet--wide' : ''}`, className)}
        role="dialog" aria-modal="true" aria-labelledby={labelId} aria-describedby={describedBy} data-state="open" data-testid={testId}
        onMouseDown={e => e.stopPropagation()}
      >
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id={labelId}>{title}</h2>
            {description && <p className="dk-sheet-desc">{description}</p>}
          </div>
          {headActions}
          <button ref={closeBtnRef} type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={closeLabel} disabled={pending} onClick={onClose}><Icon name="close" /></button>
        </div>
        <div className={cls('dk-sheet-body', bodyClass)} {...bodyProps}>{children}</div>
        {footer && <div className={cls('dk-sheet-foot', footClass)} {...footProps}>{footer}</div>}
      </div>
    </div>,
    document.body,
  )
}
