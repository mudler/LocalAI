import { useState, useEffect, useCallback } from 'react'
import { modelsApi, backendsApi } from '../utils/api'

// useGalleryEnrichment fetches the full model + backend gallery once and
// returns lookup helpers used by the Manage page. The Manage list APIs only
// know name/version/alias — descriptions, icons, licenses, tags, and links
// live on the gallery side. Cross-referencing here lets us light up the
// installed lists with the same metadata the Install pages show, instead of
// rendering them as bare names.
//
// Items not present in the gallery (custom imports, external OCI installs)
// resolve to `null` — callers fall back to a neutral icon + "no description".
export function useGalleryEnrichment() {
  const [modelMap, setModelMap] = useState(() => new Map())
  const [backendMap, setBackendMap] = useState(() => new Map())
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    let cancelled = false
    Promise.allSettled([
      modelsApi.list({ items: 9999, page: 1 }),
      backendsApi.list({ items: 9999, page: 1 }),
    ]).then(([m, b]) => {
      if (cancelled) return
      const mm = new Map()
      if (m.status === 'fulfilled') {
        const list = m.value?.models || []
        for (const x of list) {
          const key = x.name || x.id
          if (key) mm.set(key, x)
        }
      }
      const bm = new Map()
      if (b.status === 'fulfilled') {
        const raw = b.value
        const list = Array.isArray(raw?.backends) ? raw.backends : Array.isArray(raw) ? raw : []
        for (const x of list) {
          const key = x.name || x.id
          if (key) bm.set(key, x)
        }
      }
      setModelMap(mm)
      setBackendMap(bm)
      setLoaded(true)
    })
    return () => { cancelled = true }
  }, [])

  const enrichModel = useCallback((name) => (name ? modelMap.get(name) || null : null), [modelMap])
  const enrichBackend = useCallback((name) => (name ? backendMap.get(name) || null : null), [backendMap])

  return { enrichModel, enrichBackend, loaded }
}
