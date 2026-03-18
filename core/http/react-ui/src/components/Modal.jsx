import { useEffect, useRef } from 'react'

export default function Modal({ onClose, children, maxWidth = '600px' }) {
  const dialogRef = useRef(null)
  const lastFocusRef = useRef(null)

  useEffect(() => {
    lastFocusRef.current = document.activeElement

    // Focus trap
    const dialog = dialogRef.current
    if (!dialog) return

    const focusableSelector = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    const getFocusable = () => dialog.querySelectorAll(focusableSelector)

    const firstFocusable = getFocusable()[0]
    firstFocusable?.focus()

    const handleKeyDown = (e) => {
      if (e.key === 'Escape') {
        onClose?.()
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
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      lastFocusRef.current?.focus()
    }
  }, [onClose])

  return (
    <div
      role="dialog"
      aria-modal="true"
      style={{
        position: 'fixed', inset: 0, zIndex: 1000,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: 'var(--color-modal-backdrop)', backdropFilter: 'blur(4px)',
        animation: 'fadeIn 150ms ease',
      }}
      onClick={onClose}
    >
      <div
        ref={dialogRef}
        style={{
          background: 'var(--color-bg-secondary)',
          border: '1px solid var(--color-border-subtle)',
          borderRadius: 'var(--radius-lg)',
          maxWidth, width: '90%', maxHeight: '80vh',
          display: 'flex', flexDirection: 'column',
          overflow: 'auto',
          animation: 'slideUp 150ms ease',
        }}
        onClick={e => e.stopPropagation()}
      >
        {children}
      </div>
    </div>
  )
}
