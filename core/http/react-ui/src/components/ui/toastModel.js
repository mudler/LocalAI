// Rules of the toast stack, kept out of the component so they can be tested
// without a browser.

// How long a toast stays. Short news goes in 4 s. A toast with an action
// (Undo) and a warning stay 8 s, so there is time to read and act. An error
// stays until it is closed: it may need copying or acting on, and a page
// load error belongs inline (LoadError), not in a toast.
export const TOAST_MS = { short: 4000, action: 8000 }

// The time a leaving toast needs for its exit, before it is removed.
export const TOAST_EXIT_MS = 160

// At most this many toasts show at once; a new one pushes the oldest out.
export const TOAST_MAX = 3

export function toastDuration({ type = 'info', action, duration } = {}) {
  if (typeof duration === 'number' && Number.isFinite(duration)) return Math.max(0, duration)
  if (type === 'error') return 0
  if (action || type === 'warning') return TOAST_MS.action
  return TOAST_MS.short
}

// Adds a toast and marks the oldest ones as leaving when the stack is full.
// Leaving toasts stay in the list until their exit ends, so they do not count.
export function pushToast(list, toast, max = TOAST_MAX) {
  const next = [...list, toast]
  let live = next.filter(t => !t.exiting).length
  return next.map(t => {
    if (live > max && !t.exiting && t !== toast) {
      live -= 1
      return { ...t, exiting: true }
    }
    return t
  })
}

export function markLeaving(list, id) {
  return list.map(t => (t.id === id && !t.exiting ? { ...t, exiting: true } : t))
}

// How far the stack must rise to clear an element fixed at the bottom of the
// window (the chat composer). Nothing to clear, or an element above the fold
// bottom by more than half the window, gives 0: then it is not a bottom bar.
export function toastLift(viewportHeight, anchorRect) {
  if (!anchorRect || !(viewportHeight > 0)) return 0
  const { top, height } = anchorRect
  if (!(height > 0) || top < viewportHeight / 2 || top >= viewportHeight) return 0
  return Math.round(viewportHeight - top)
}
