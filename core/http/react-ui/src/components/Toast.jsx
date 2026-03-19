import { useState, useCallback, useRef, useEffect } from 'react'

let toastId = 0

export function useToast() {
  const [toasts, setToasts] = useState([])

  const addToast = useCallback((message, type = 'info', duration = 5000) => {
    const id = ++toastId
    setToasts(prev => [...prev, { id, message, type }])
    if (duration > 0) {
      setTimeout(() => {
        setToasts(prev => prev.map(t => t.id === id ? { ...t, exiting: true } : t))
        setTimeout(() => {
          setToasts(prev => prev.filter(t => t.id !== id))
        }, 150)
      }, duration)
    }
    return id
  }, [])

  const removeToast = useCallback((id) => {
    setToasts(prev => prev.map(t => t.id === id ? { ...t, exiting: true } : t))
    setTimeout(() => {
      setToasts(prev => prev.filter(t => t.id !== id))
    }, 150)
  }, [])

  return { toasts, addToast, removeToast }
}

const iconMap = {
  success: 'fa-circle-check',
  error: 'fa-circle-exclamation',
  warning: 'fa-triangle-exclamation',
  info: 'fa-circle-info',
}

const colorMap = {
  success: 'toast-success',
  error: 'toast-error',
  warning: 'toast-warning',
  info: 'toast-info',
}

export function ToastContainer({ toasts, removeToast }) {
  return (
    <div className="toast-container" aria-live="polite" role="status">
      {toasts.map(toast => (
        <ToastItem key={toast.id} toast={toast} onRemove={removeToast} />
      ))}
    </div>
  )
}

function ToastItem({ toast, onRemove }) {
  const ref = useRef(null)

  useEffect(() => {
    if (ref.current) {
      ref.current.classList.add('toast-enter')
      requestAnimationFrame(() => {
        ref.current?.classList.remove('toast-enter')
      })
    }
  }, [])

  return (
    <div ref={ref} className={`toast ${colorMap[toast.type] || 'toast-info'} ${toast.exiting ? 'toast-exit' : ''}`}>
      <i className={`fas ${iconMap[toast.type] || 'fa-circle-info'}`} />
      <span>{toast.message}</span>
      <button onClick={() => onRemove(toast.id)} className="toast-close" aria-label="Dismiss notification">
        <i className="fas fa-xmark" />
      </button>
    </div>
  )
}
