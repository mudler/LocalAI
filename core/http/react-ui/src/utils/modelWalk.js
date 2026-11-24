// The list a model page was opened from, so "previous" and "next" walk the
// same rows the reader was looking at, in the order they saw them.
//
// The Models list publishes its visible order whenever it changes. The page
// reads it back. It lives in memory, and in session storage so a reload of the
// page keeps the walker; nothing is written to local storage because the order
// is a view, not a preference. A page opened from a pasted link finds a list
// that does not contain its model, and shows no walker rather than a wrong one.

const KEY = 'localai.modelWalk'

let memory = null

export function publishWalk(view, ids) {
  memory = { view, ids: Array.isArray(ids) ? ids : [] }
  try {
    sessionStorage.setItem(KEY, JSON.stringify(memory))
  } catch {
    // No session storage (private mode, blocked): memory alone serves the tab.
  }
}

function readWalk() {
  if (memory) return memory
  try {
    const stored = JSON.parse(sessionStorage.getItem(KEY) || 'null')
    if (stored && Array.isArray(stored.ids)) {
      memory = { view: stored.view === 'installed' ? 'installed' : 'explore', ids: stored.ids.filter(id => typeof id === 'string') }
      return memory
    }
  } catch {
    // Unreadable value: treated as no list.
  }
  return null
}

// Where `id` sits in the published list, or null when the list does not hold
// it (or has only that one model, which has nothing to walk to).
export function walkFor(id) {
  const walk = readWalk()
  if (!walk) return null
  const index = walk.ids.indexOf(id)
  if (index < 0 || walk.ids.length < 2) return null
  return {
    view: walk.view,
    index,
    total: walk.ids.length,
    previous: index > 0 ? walk.ids[index - 1] : null,
    next: index < walk.ids.length - 1 ? walk.ids[index + 1] : null,
  }
}

export function resetWalk() {
  memory = null
  try {
    sessionStorage.removeItem(KEY)
  } catch {
    // Nothing to clear.
  }
}

export function modelPath(id) {
  return `/app/models/${encodeURIComponent(id)}`
}
