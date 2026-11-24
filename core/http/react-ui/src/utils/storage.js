// The one place the app keeps small per-browser choices (the sidebar, the
// theme, a list view). Every key has the prefix `localai.`; a caller gives
// the name without it. Browsers may refuse storage (a private window, blocked
// site data), so every call is guarded: a read gives null and a write does
// nothing, and the page works the same without it.
//
// Older builds used four spellings (`localai-x`, `localai_x`, `localai.x` and
// bare names). A read can name those old keys: the first one that holds a
// value is carried over to the new key and removed, so nobody loses a choice.

export const PREFIX = 'localai.'

const keyOf = (name) => PREFIX + name

function area(storage) {
  if (storage) return storage
  try { return window.localStorage } catch { return null }
}

export function readStored(name, legacy = [], storage) {
  const s = area(storage)
  if (!s) return null
  try {
    const value = s.getItem(keyOf(name))
    if (value !== null) return value
    for (const old of legacy) {
      const v = s.getItem(old)
      if (v === null) continue
      s.setItem(keyOf(name), v)
      s.removeItem(old)
      return v
    }
  } catch { /* storage is not available */ }
  return null
}

export function writeStored(name, value, storage) {
  const s = area(storage)
  if (!s) return
  try { s.setItem(keyOf(name), String(value)) } catch { /* storage is not available */ }
}

export function removeStored(name, storage) {
  const s = area(storage)
  if (!s) return
  try { s.removeItem(keyOf(name)) } catch { /* storage is not available */ }
}

// onStored calls fn(value) when another tab changes the stored name. The
// event does not fire in the tab that wrote, so a writer is never told of its
// own change.
export function onStored(name, fn) {
  const handler = (e) => {
    if (e.key === keyOf(name)) fn(e.newValue)
  }
  window.addEventListener('storage', handler)
  return () => window.removeEventListener('storage', handler)
}
