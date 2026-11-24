import { useCallback, useEffect, useRef, useState } from 'react'
import { modelsApi, systemApi } from '../utils/api'
import { rememberSize } from './useModelSizes'

// The contexts the Fit tab draws, the same set the Explore table asks for.
export const PAGE_CONTEXTS = [8192, 16384, 32768, 65536, 131072, 262144]

// Gallery rows to read when looking one entry up by exact name. The term is the
// whole name, so the entry is always in the match set; the size only has to
// outlast the fuzzy matches that share its prefix.
const LOOKUP_ITEMS = 100

// One gallery entry by its name.
//
//   status  'idle' (no name), 'loading', 'ready', 'missing' (the gallery answered
//           and holds no such entry) or 'error' (it could not be read).
//   entry   the listing row, with its files, tags, links and licence.
//   matches the other entries the lookup returned, for a "did you mean".
//
// Read once per name; `reload` asks again after an error.
export function useGalleryEntry(name) {
  const [state, setState] = useState({ status: name ? 'loading' : 'idle', entry: null, matches: [] })
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    if (!name) {
      setState({ status: 'idle', entry: null, matches: [] })
      return undefined
    }
    let cancelled = false
    setState(prev => (prev.entry && (prev.entry.name || prev.entry.id) === name ? prev : { status: 'loading', entry: null, matches: [] }))
    modelsApi.list({ term: name, items: LOOKUP_ITEMS })
      .then(data => {
        if (cancelled) return
        const rows = data?.models || []
        const entry = rows.find(m => (m.name || m.id) === name || m.id === name) || null
        setState(entry
          ? { status: 'ready', entry, matches: [] }
          : { status: 'missing', entry: null, matches: rows.slice(0, 5) })
      })
      .catch(() => {
        if (!cancelled) setState({ status: 'error', entry: null, matches: [] })
      })
    return () => { cancelled = true }
  }, [name, nonce])

  const reload = useCallback(() => setNonce(n => n + 1), [])
  return { ...state, reload }
}

// Size and memory by context for one model or build.
//
// The gallery endpoint knows every listed entry. An installed model the gallery
// does not list (an import, a hand-written config) has no entry, so it is
// estimated from its own files, once per context.
//
//   status  'idle', 'loading', 'ready' or 'unavailable'.
//   data    { sizeBytes, estimates: { [context]: { vramBytes } }, modelMaxContext }
export function useModelEstimate(name, { installed = false, enabled = true } = {}) {
  const [held, setHeld] = useState({ status: 'idle', data: null, name: null })
  // The reading held is for one name. For any other it is not an answer yet.
  const state = held.name === name ? held : { status: name && enabled ? 'loading' : 'idle', data: null }
  const setState = useCallback(next => setHeld({ ...next, name }), [name])
  const installedRef = useRef(installed)
  installedRef.current = installed

  useEffect(() => {
    if (!name || !enabled) {
      setState({ status: 'idle', data: null })
      return undefined
    }
    let cancelled = false
    setState({ status: 'loading', data: null })
    const finish = data => {
      if (cancelled) return
      if (data && (data.sizeBytes || Object.keys(data.estimates || {}).length > 0)) {
        if (data.sizeBytes) rememberSize(name, data.sizeBytes)
        setState({ status: 'ready', data })
      } else {
        setState({ status: 'unavailable', data: null })
      }
    }
    modelsApi.estimate(name, PAGE_CONTEXTS)
      .then(finish)
      .catch(async () => {
        if (!installedRef.current) { finish(null); return }
        try {
          const readings = await Promise.all(PAGE_CONTEXTS.map(ctx => modelsApi.estimateVram({ model: name, context_size: ctx })))
          const estimates = {}
          readings.forEach((r, i) => {
            if (Number(r?.vram_bytes) > 0) estimates[String(PAGE_CONTEXTS[i])] = { vramBytes: Number(r.vram_bytes) }
          })
          finish({
            sizeBytes: Number(readings.find(r => r?.size_bytes)?.size_bytes) || 0,
            estimates,
            modelMaxContext: Number(readings.find(r => r?.model_max_context)?.model_max_context) || 0,
          })
        } catch {
          finish(null)
        }
      })
    return () => { cancelled = true }
  }, [name, enabled, setState])

  return state
}

// The build list of a gallery entry that offers several. An entry that declares
// none returns { status: 'none' } without asking.
export function useVariants(name, declared) {
  const [state, setState] = useState({ status: declared ? 'loading' : 'none', data: null })
  useEffect(() => {
    if (!name || !declared) {
      setState({ status: 'none', data: null })
      return undefined
    }
    let cancelled = false
    setState({ status: 'loading', data: null })
    modelsApi.variants(name)
      .then(data => { if (!cancelled) setState({ status: Array.isArray(data?.variants) && data.variants.length > 0 ? 'ready' : 'none', data }) })
      .catch(() => { if (!cancelled) setState({ status: 'error', data: null }) })
    return () => { cancelled = true }
  }, [name, declared])
  return state
}

// Which models the server holds in memory right now.
export function useLoadedModels() {
  const [loaded, setLoaded] = useState(() => new Set())
  const refresh = useCallback(async () => {
    try {
      const info = await systemApi.info()
      setLoaded(new Set((Array.isArray(info?.loaded_models) ? info.loaded_models : []).map(m => m.id)))
    } catch {
      setLoaded(new Set())
    }
  }, [])
  useEffect(() => { refresh() }, [refresh])
  return { loaded, refresh }
}
