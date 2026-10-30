import { useEffect, useRef } from 'react'

// SHIM: the change feed of the workspace lives in useWorkspaceFeed.js, which
// another change adds (the hook useWorkspaceFeed(patterns, onChange) over GET
// /api/workspace/events, and invalidate(path)). Until that file exists this
// module falls back to a feed inside the tab: invalidate(path) reaches the
// listeners of this tab, and a focus of the window counts as a resync (the
// listener gets no paths and reads again what it shows). Remove the fallback
// once the real hook is merged; the callers import from here and need no change.
const found = import.meta.glob('./useWorkspaceFeed.js', { eager: true })
const real = found['./useWorkspaceFeed.js']

const listeners = new Set()

// A pattern is a path inside the home, with an optional trailing "/**" for
// everything below a folder.
function matches(pattern, path) {
  const p = `/${String(path).replace(/^\/+/, '')}`
  if (pattern.endsWith('/**')) {
    const base = pattern.slice(0, -3)
    return p === base || p.startsWith(`${base}/`)
  }
  return p === pattern
}

function localInvalidate(path) {
  const paths = [`/${String(path).replace(/^\/+/, '')}`]
  listeners.forEach((l) => l(paths))
}

function useLocalFeed(patterns, onChange) {
  const ref = useRef(onChange)
  ref.current = onChange
  const key = JSON.stringify(patterns || [])
  useEffect(() => {
    const pats = JSON.parse(key)
    const listener = (paths) => {
      const hit = paths.filter((p) => pats.some((pt) => matches(pt, p)))
      if (hit.length) ref.current?.({ paths: hit })
    }
    const onFocus = () => ref.current?.({ resync: true })
    listeners.add(listener)
    window.addEventListener('focus', onFocus)
    return () => {
      listeners.delete(listener)
      window.removeEventListener('focus', onFocus)
    }
  }, [key])
}

export const useWorkspaceFeed = real?.useWorkspaceFeed || useLocalFeed
export const invalidate = real?.invalidate || localInvalidate
