import { useCallback, useState } from 'react'

// One choice of a list (a sort order, a density) that survives a reload. The
// browser may refuse storage (a private window, blocked site data), so every
// read and write is guarded and the page works the same without it. A stored
// value that is not in `allowed` is ignored, so an old build's value cannot
// break the page.
export function useStoredChoice(key, fallback, allowed) {
  const [value, setValue] = useState(() => {
    try {
      const stored = window.localStorage.getItem(key)
      if (stored != null && (!allowed || allowed.includes(stored))) return stored
    } catch { /* storage is not available */ }
    return fallback
  })
  const set = useCallback((next) => {
    setValue(next)
    try { window.localStorage.setItem(key, next) } catch { /* storage is not available */ }
  }, [key])
  return [value, set]
}
