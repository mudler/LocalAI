import { useEffect, useRef, useState } from 'react'
import { modelsApi } from '../utils/api'

const CONCURRENCY = 4
const SETTLE_MS = 400

// How many gallery entries each capability facet matches, as the server counts
// them. The listing paginates and filters on the server, so the only honest
// count is the server's own: one request per facet that asks for a single item
// and reads `availableModels` from the reply. Nothing is counted in the browser.
//
// The counts follow the search term, the backend and the one-row-per-model
// choice, which change what a facet means, but not the facets that are switched
// on, which would make every chip but the pressed ones read zero.
//
// Returns { counts, stale }. counts is { [facetKey]: number } for the facets
// answered so far; a key that has not been answered is absent, so the chip shows
// no count instead of a wrong one. When the scope changes the previous counts
// stay on screen, marked stale, until the new ones arrive: chips that vanish and
// return on every keystroke are worse than numbers that are briefly one search
// behind.
export function useFacetCounts({ keys, term, backend, collapse, ready }) {
  const [counts, setCounts] = useState({})
  const [answeredScope, setAnsweredScope] = useState(null)
  const cache = useRef(new Map())
  const scope = `${term}\u0000${backend}\u0000${collapse ? 1 : 0}`
  const keyList = keys.join(',')

  useEffect(() => {
    if (!ready) return undefined
    const known = cache.current.get(scope) || {}
    if (Object.keys(known).length > 0) {
      setCounts(known)
      setAnsweredScope(scope)
    }
    const todo = keys.filter(key => !(key in known))
    if (todo.length === 0) return undefined

    let cancelled = false
    const timer = setTimeout(() => {
      let cursor = 0
      const worker = async () => {
        while (!cancelled && cursor < todo.length) {
          const key = todo[cursor++]
          try {
            const params = { page: 1, items: 1 }
            if (key) params.tag = key
            if (term) params.term = term
            if (backend) params.backend = backend
            if (collapse) params.collapse_variants = 'true'
            const data = await modelsApi.list(params)
            if (typeof data?.availableModels === 'number') {
              const next = { ...(cache.current.get(scope) || {}), [key]: data.availableModels }
              cache.current.set(scope, next)
              if (!cancelled) {
                setCounts(next)
                setAnsweredScope(scope)
              }
            }
          } catch {
            // No count is better than a wrong one.
          }
        }
      }
      for (let i = 0; i < Math.min(CONCURRENCY, todo.length); i++) worker()
    }, SETTLE_MS)
    return () => { cancelled = true; clearTimeout(timer) }
    // keys is summarised by keyList.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope, keyList, ready])

  return { counts, stale: answeredScope !== scope }
}
