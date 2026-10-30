import { useState, useCallback, useRef, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { toastDuration, pushToast, markLeaving, toastLift, TOAST_EXIT_MS } from './ui/toastModel'

let toastId = 0

// The toast stack. Toasts are for the result of an action the person just
// took ("Deleted daily-digest", Undo). A page that failed to load shows
// <LoadError> in place, not a toast.
//
//   addToast(message, type, duration, options) -> id
//     type       info (default) | success | warning | error
//     duration   ms; leave it out for the default: 4 s, 8 s with an action
//                or a warning, never for an error (it stays until closed)
//     options    { action: { label, onClick }, link: { href, text }, testId }
//
// At most 3 show at once; a new one pushes the oldest out.
export function useToast() {
  const [toasts, setToasts] = useState([])

  const addToast = useCallback((message, type = 'info', duration, options = {}) => {
    const id = ++toastId
    const toast = {
      id, message, type,
      link: options.link,
      action: options.action,
      testId: options.testId,
      duration: toastDuration({ type, action: options.action, duration }),
    }
    setToasts(prev => pushToast(prev, toast))
    return id
  }, [])

  const removeToast = useCallback((id) => {
    setToasts(prev => markLeaving(prev, id))
  }, [])

  const dropToast = useCallback((id) => {
    setToasts(prev => prev.filter(t => t.id !== id))
  }, [])

  return { toasts, addToast, removeToast, dropToast }
}

const iconMap = {
  success: 'check-circle',
  error: 'alert-circle',
  warning: 'warning',
  info: 'info',
}

const colorMap = {
  success: 'toast-success',
  error: 'toast-error',
  warning: 'toast-warning',
  info: 'toast-info',
}

// The element the stack must stay above: a bar fixed at the bottom of the
// window, such as the chat composer. A bar registers itself with
// useToastAnchor(ref); on a chat page the stack also finds the element marked
// data-toast-anchor (the chat dock), or else the composer (.home-cmd), by
// itself.
const CHAT_ANCHOR = '[data-toast-anchor], .home-cmd'
const anchors = new Set()
const anchorListeners = new Set()
function anchorsChanged() { anchorListeners.forEach(fn => fn()) }

// Keeps the toast stack above the element in `ref` while it is mounted.
export function useToastAnchor(ref) {
  useEffect(() => {
    const el = ref.current
    if (!el) return undefined
    anchors.add(el)
    anchorsChanged()
    return () => { anchors.delete(el); anchorsChanged() }
  }, [ref])
}

function useAnchorCount() {
  const [count, setCount] = useState(anchors.size)
  useEffect(() => {
    const on = () => setCount(anchors.size)
    anchorListeners.add(on)
    on()
    return () => anchorListeners.delete(on)
  }, [])
  return count
}

// Raises the stack (and the kit's undo toasts, which read the same variable)
// above the anchor. The value is a CSS variable on <html>.
function useToastLift(chatPage) {
  const registered = useAnchorCount()
  const active = chatPage || registered > 0
  useEffect(() => {
    const root = document.documentElement
    if (!active) {
      root.style.removeProperty('--toast-lift')
      return undefined
    }
    let frame = 0
    let observed = null
    const resize = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(() => schedule()) : null
    const find = () => anchors.values().next().value || (chatPage ? document.querySelector(CHAT_ANCHOR) : null)
    const measure = () => {
      frame = 0
      const anchor = find()
      if (anchor !== observed) {
        if (observed) resize?.unobserve(observed)
        if (anchor) resize?.observe(anchor)
        observed = anchor
      }
      const lift = anchor ? toastLift(window.innerHeight, anchor.getBoundingClientRect()) : 0
      root.style.setProperty('--toast-lift', `${lift}px`)
    }
    function schedule() {
      if (!frame) frame = requestAnimationFrame(measure)
    }
    // On a chat page the composer mounts after the chat chunk loads and grows
    // with its chips, so watch the page as well as the window.
    const mutations = chatPage ? new MutationObserver(schedule) : null
    mutations?.observe(document.querySelector('.main-content') || document.body, { childList: true, subtree: true })
    window.addEventListener('resize', schedule)
    schedule()
    return () => {
      cancelAnimationFrame(frame)
      mutations?.disconnect()
      resize?.disconnect()
      window.removeEventListener('resize', schedule)
      root.style.removeProperty('--toast-lift')
    }
  }, [active, chatPage, registered])
}

export function ToastContainer({ toasts, removeToast, dropToast, lift = false }) {
  useToastLift(lift)
  return (
    <div className="toast-container" aria-live="polite" role="status">
      {toasts.map(toast => (
        <ToastItem key={toast.id} toast={toast} onRemove={removeToast} onGone={dropToast || removeToast} />
      ))}
    </div>
  )
}

function ToastItem({ toast, onRemove, onGone }) {
  const { t } = useTranslation('common')
  const [phase, setPhase] = useState('enter')
  const left = useRef(toast.duration)
  const startedAt = useRef(0)
  const timer = useRef(null)
  const removeRef = useRef(onRemove)
  removeRef.current = onRemove

  const run = useCallback(() => {
    if (!(left.current > 0) || timer.current) return
    startedAt.current = Date.now()
    timer.current = setTimeout(() => removeRef.current(toast.id), left.current)
  }, [toast.id])

  const pause = useCallback(() => {
    if (!timer.current) return
    clearTimeout(timer.current)
    timer.current = null
    left.current = Math.max(500, left.current - (Date.now() - startedAt.current))
  }, [])

  useEffect(() => {
    let second = 0
    const first = requestAnimationFrame(() => { second = requestAnimationFrame(() => setPhase('open')) })
    run()
    return () => { cancelAnimationFrame(first); cancelAnimationFrame(second); clearTimeout(timer.current) }
  }, [run])

  useEffect(() => {
    if (!toast.exiting) return undefined
    clearTimeout(timer.current)
    const gone = setTimeout(() => onGone(toast.id), TOAST_EXIT_MS)
    return () => clearTimeout(gone)
  }, [toast.exiting, toast.id, onGone])

  const state = toast.exiting ? 'exit' : phase
  const timed = toast.duration > 0
  return (
    <div
      className={`toast ${colorMap[toast.type] || 'toast-info'}${toast.action ? ' toast--action' : ''}`}
      data-state={state}
      data-testid={toast.testId}
      style={timed ? { '--toast-ms': `${toast.duration}ms` } : undefined}
      onMouseEnter={pause}
      onMouseLeave={run}
      onFocus={pause}
      onBlur={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) run() }}
    >
      <Icon name={iconMap[toast.type] || 'info'} className="toast__icon" />
      <span className="toast__text">{toast.message}</span>
      {toast.link && (
        <a href={toast.link.href} className="toast-link">{toast.link.text}</a>
      )}
      {toast.action && (
        <button
          type="button"
          className="toast-action"
          onClick={() => { toast.action.onClick?.(); onRemove(toast.id) }}
        >
          {toast.action.label || t('ui.undo')}
        </button>
      )}
      <button
        type="button"
        onClick={() => onRemove(toast.id)}
        className="toast-close"
        aria-label={t('actions.close')}
      >
        <Icon name="close" />
      </button>
      {timed && toast.action && <span className="toast__bar" aria-hidden="true" />}
    </div>
  )
}
