// The one place that shows a toast about a change to the user's files: "Added
// to your composer", "Task board is in Pages", "Examples hidden". It wraps the
// app's toast today, so the toast it uses can change in one file later.
//
// A toast here only says what happened outside the page the user looks at;
// every action it offers has a twin on the page (the change note in the chat,
// the switch in Account), so nobody depends on catching it.

const DURATION = 6000

// showChange shows a toast. action is { label, onClick } or nothing.
export function showChange(addToast, { message, action, testId = 'change-toast', tone = 'info', duration = DURATION }) {
  if (typeof addToast !== 'function' || !message) return null
  return addToast(message, tone, duration, { action, testId })
}

// anchorToasts keeps the toast stack above el (the chat composer): it sets two
// CSS variables on the document while el is on the page. It returns the
// function that stops.
export function anchorToasts(el) {
  if (!el || typeof window === 'undefined') return () => {}
  const root = document.documentElement
  const place = () => {
    const r = el.getBoundingClientRect()
    if (!r.width) return
    root.style.setProperty('--toast-anchor-x', `${Math.round(r.left + r.width / 2)}px`)
    root.style.setProperty('--toast-anchor-bottom', `${Math.max(16, Math.round(window.innerHeight - r.top + 12))}px`)
  }
  place()
  const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(place) : null
  ro?.observe(el)
  window.addEventListener('resize', place)
  return () => {
    ro?.disconnect()
    window.removeEventListener('resize', place)
    root.style.removeProperty('--toast-anchor-x')
    root.style.removeProperty('--toast-anchor-bottom')
  }
}
