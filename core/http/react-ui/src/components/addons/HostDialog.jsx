/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useRef } from 'react'
import { pushLayer } from '../../utils/modalLayers'
import Button from '../ui/Button'

// The modal of the host access screens: a title, a body that scrolls, and the
// actions. Escape closes it unless `busy`; Tab stays inside; focus goes to the
// first control and comes back to the opener. The same behavior as the install
// dialog, for dialogs that carry a list or a field.
//
// `dismissWhenBusy` is for a dialog whose work goes on in the background: it
// closes at any moment (Escape, the veil, the close button) and closing never
// stops that work. `closeLabel` adds the close button to the head.
export default function HostDialog({ title, children, actions, onClose, busy = false, dismissWhenBusy = false, closeLabel, testId }) {
  const ref = useRef(null)
  const titleId = useId()
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  const locked = busy && !dismissWhenBusy
  const busyRef = useRef(locked)
  busyRef.current = locked
  useEffect(() => {
    const opener = document.activeElement
    const layer = pushLayer()
    const dialog = ref.current
    const focusables = () => Array.from(dialog.querySelectorAll('button:not([disabled]), a[href], input:not([disabled])'))
    focusables().find(el => el.dataset.autofocus === 'true')?.focus()
    if (!dialog.contains(document.activeElement)) dialog.focus()
    const onKey = (e) => {
      if (!layer.isTop()) return
      if (e.key === 'Escape') {
        if (busyRef.current) return
        e.preventDefault(); closeRef.current(); return
      }
      if (e.key !== 'Tab') return
      const items = focusables()
      if (items.length === 0) { e.preventDefault(); return }
      const first = items[0]
      const last = items[items.length - 1]
      if (e.shiftKey && (document.activeElement === first || document.activeElement === dialog)) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      layer.pop()
      if (opener && opener !== document.body && document.contains(opener)) opener.focus?.()
    }
  }, [])
  return (
    <div className="confirm-dialog-backdrop" onClick={locked ? undefined : onClose}>
      <div ref={ref} tabIndex={-1} className="confirm-dialog ad-dialog" role="dialog" aria-modal="true" aria-labelledby={titleId} data-testid={testId} onClick={(e) => e.stopPropagation()}>
        <div className="confirm-dialog-header">
          <span id={titleId} className="confirm-dialog-title">{title}</span>
          {closeLabel && <Button variant="ghost" size="sm" icon="close" className="ad-dialog-close" aria-label={closeLabel} onClick={onClose} data-testid="dialog-close" />}
        </div>
        <div className="confirm-dialog-body ad-confirm">{children}</div>
        <div className="confirm-dialog-actions">{actions}</div>
      </div>
    </div>
  )
}
