import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

export default function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel,
  cancelLabel,
  danger = false,
  onConfirm,
  onCancel,
}) {
  const { t } = useTranslation('common')
  const titleText = title ?? t('actions.confirm')
  const confirmText = confirmLabel ?? t('actions.confirm')
  const cancelText = cancelLabel ?? t('actions.cancel')
  const dialogRef = useRef(null)
  const confirmRef = useRef(null)

  useEffect(() => {
    if (!open) return

    confirmRef.current?.focus()

    const dialog = dialogRef.current
    if (!dialog) return

    const focusableSelector = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    const getFocusable = () => dialog.querySelectorAll(focusableSelector)

    const handleKeyDown = (e) => {
      if (e.key === 'Escape') {
        onCancel?.()
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
  }, [open, onCancel])

  if (!open) return null

  const titleId = 'confirm-dialog-title'
  const bodyId = 'confirm-dialog-body'

  return (
    <div className="confirm-dialog-backdrop" onClick={onCancel}>
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
          {danger && <i className="fas fa-exclamation-triangle confirm-dialog-danger-icon" />}
          <span id={titleId} className="confirm-dialog-title">{titleText}</span>
        </div>
        {message && <div id={bodyId} className="confirm-dialog-body">{message}</div>}
        <div className="confirm-dialog-actions">
          <button className="btn btn-secondary btn-sm" onClick={onCancel}>
            {cancelText}
          </button>
          <button
            ref={confirmRef}
            className={`btn btn-sm ${danger ? 'btn-danger' : 'btn-primary'}`}
            onClick={onConfirm}
          >
            {confirmText}
          </button>
        </div>
      </div>
    </div>
  )
}
