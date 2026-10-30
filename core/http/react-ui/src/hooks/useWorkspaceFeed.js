// Placeholder for the workspace change feed. The real hook (an SSE stream of
// GET /api/workspace/events) replaces this file; keep its side of a merge.
// Until then a change reaches the subscribers of this tab only: through
// invalidate(), and when the window gets the focus back.
import { useEffect, useRef } from 'react'

const listeners = new Set()

function matches(pattern, path) {
  const p = String(path).replace(/^\/+/, '')
  const pat = String(pattern).replace(/^\/+/, '')
  if (pat.endsWith('/*')) return p.startsWith(pat.slice(0, -1))
  return p === pat
}

// invalidate tells the subscribers of this tab that a path changed, for
// example after a save of their own.
export function invalidate(path) {
  for (const l of [...listeners]) l([path])
}

// useWorkspaceFeed calls onChange(paths) when a path that matches one of the
// patterns ('persona.json', 'pages/*') changes.
export function useWorkspaceFeed(patterns, onChange) {
  const ref = useRef({ patterns, onChange })
  ref.current = { patterns, onChange }
  useEffect(() => {
    const listener = (paths) => {
      const { patterns: pats, onChange: cb } = ref.current
      const hit = paths.filter(p => p === '*' || (pats || []).some(pat => matches(pat, p)))
      if (hit.length) cb(hit)
    }
    const onFocus = () => listener(['*'])
    listeners.add(listener)
    window.addEventListener('focus', onFocus)
    return () => {
      listeners.delete(listener)
      window.removeEventListener('focus', onFocus)
    }
  }, [])
}
