import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { pushLayer } from '../utils/modalLayers'

export default function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel,
  pendingLabel,
  cancelLabel,
  danger = false,
  pending = false,
  confirmDisabled = false,
  onConfirm,
  onCancel,
}) {
  const { t } = useTranslation('common')
  const titleText = title ?? t('actions.confirm')
  const confirmText = confirmLabel ?? t('actions.confirm')
  const cancelText = cancelLabel ?? t('actions.cancel')
  const dialogRef = useRef(null)
  const confirmRef = useRef(null)
  const layerRef = useRef(null)
  const cancelRef = useRef(onCancel)
  cancelRef.current = onCancel

  // The dialog is a modal layer for as long as it is open: only the topmost
  // layer handles Escape, and focus goes back to the control that opened it.
  useEffect(() => {
    if (!open) return
    const opener = document.activeElement
    const layer = pushLayer()
    layerRef.current = layer
    return () => {
      layer.pop()
      layerRef.current = null
      if (opener && opener !== document.body && document.contains(opener)) opener.focus?.()
    }
  }, [open])

  useEffect(() => {
    if (!open) return

    confirmRef.current?.focus()

    const dialog = dialogRef.current
    if (!dialog) return

    const focusableSelector = 'button, a[href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    const getFocusable = () => dialog.querySelectorAll(focusableSelector)

    const handleKeyDown = (e) => {
      if (layerRef.current && !layerRef.current.isTop()) return
      if (e.key === 'Escape') {
        if (pending) return
        e.preventDefault()
        cancelRef.current?.()
        return
      }
      if (e.key !== 'Tab') return
      const focusable = getFocusable()
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey) {
        if (document.activeElement === first) {
          e.preventDefault()
          last.focus()
        }
      } else {
        if (document.activeElement === last) {
          e.preventDefault()
          first.focus()
        }
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [open, pending])

  if (!open) return null

  const titleId = 'confirm-dialog-title'
  const bodyId = 'confirm-dialog-body'

  return (
    <div className="confirm-dialog-backdrop" onClick={pending ? undefined : onCancel}>
      <div
        ref={dialogRef}
        className="confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={message ? bodyId : undefined}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="confirm-dialog-header">
          {danger && <Icon name="warning" className="confirm-dialog-danger-icon" />}
          <span id={titleId} className="confirm-dialog-title">{titleText}</span>
        </div>
        {message && <div id={bodyId} className="confirm-dialog-body">{message}</div>}
        <div className="confirm-dialog-actions">
          <button className="btn btn-secondary btn-sm" disabled={pending} onClick={onCancel}>
            {cancelText}
          </button>
          <button
            ref={confirmRef}
            className={`btn btn-sm ${danger ? 'btn-danger' : 'btn-primary'}`}
            disabled={pending || confirmDisabled}
            onClick={onConfirm}
          >
            {pending ? (pendingLabel ?? confirmText) : confirmText}
          </button>
        </div>
      </div>
    </div>
  )
}
