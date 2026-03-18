import { useEffect, useRef } from 'react'

export default function ConfirmDialog({
  open,
  title = 'Confirm',
  message,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  danger = false,
  onConfirm,
  onCancel,
}) {
  const confirmRef = useRef(null)

  useEffect(() => {
    if (open) {
      confirmRef.current?.focus()
      const handleKey = (e) => {
        if (e.key === 'Escape') onCancel?.()
      }
      document.addEventListener('keydown', handleKey)
      return () => document.removeEventListener('keydown', handleKey)
    }
  }, [open, onCancel])

  if (!open) return null

  return (
    <div className="confirm-dialog-backdrop" onClick={onCancel}>
      <div className="confirm-dialog" onClick={(e) => e.stopPropagation()}>
        <div className="confirm-dialog-header">
          {danger && <i className="fas fa-exclamation-triangle confirm-dialog-danger-icon" />}
          <span className="confirm-dialog-title">{title}</span>
        </div>
        {message && <div className="confirm-dialog-body">{message}</div>}
        <div className="confirm-dialog-actions">
          <button className="btn btn-secondary btn-sm" onClick={onCancel}>
            {cancelLabel}
          </button>
          <button
            ref={confirmRef}
            className={`btn btn-sm ${danger ? 'btn-danger' : 'btn-primary'}`}
            onClick={onConfirm}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
