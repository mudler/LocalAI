import { useEffect, useState } from 'react'
import { modelsApi } from '../utils/api'
import { TYPE_INFO } from '../utils/studioWork'

// One gallery model to suggest for a Studio type that has none installed.
//
// The gallery has no "best for images" flag, so this reads what it does have:
// models carrying the type's tag, in the gallery's own order, not yet installed.
// It asks for the size of the first few and suggests the smallest download whose
// size is known; if no size is known it suggests the first. Whatever the server
// does not report stays null and the note says it is unknown, never a number
// made up to fill the line.
//
// Results are kept per tag for the session, so picking the chip again, or
// switching between two types, does not ask twice.
const cache = new Map()
const CANDIDATES = 4
const DEFAULT_CTX = 4096

async function lookup(tag) {
  const data = await modelsApi.list({ tag, items: 8, page: 1 })
  const candidates = (data?.models || []).filter(m => !m.installed).slice(0, CANDIDATES)
  if (candidates.length === 0) return null
  const sized = await Promise.all(candidates.map(async (m) => {
    const name = m.name || m.id
    try {
      const e = await modelsApi.estimate(name, [DEFAULT_CTX])
      const ctx = e?.estimates?.[String(DEFAULT_CTX)]
      return {
        name,
        description: m.description || '',
        sizeBytes: e?.sizeBytes ?? null,
        sizeDisplay: e?.sizeDisplay ?? null,
        vramBytes: ctx?.vramBytes ?? null,
        vramDisplay: ctx?.vramDisplay ?? null,
      }
    } catch {
      return { name, description: m.description || '', sizeBytes: null, sizeDisplay: null, vramBytes: null, vramDisplay: null }
    }
  }))
  const known = sized.filter(c => c.sizeBytes > 0).sort((a, b) => a.sizeBytes - b.sizeBytes)
  return known[0] || sized[0]
}

// state: 'idle' (not asked), 'loading', 'ready' (suggestion set), 'none' (the
// gallery has nothing for this type), 'error' (the gallery is out of reach).
export function useModelSuggestion(type, enabled) {
  const tag = TYPE_INFO[type]?.tag
  const [state, setState] = useState({ state: 'idle', suggestion: null })

  useEffect(() => {
    if (!enabled || !tag) { setState({ state: 'idle', suggestion: null }); return undefined }
    if (cache.has(tag)) {
      const hit = cache.get(tag)
      setState({ state: hit ? 'ready' : 'none', suggestion: hit })
      return undefined
    }
    let cancelled = false
    setState({ state: 'loading', suggestion: null })
    lookup(tag)
      .then((hit) => {
        cache.set(tag, hit)
        if (!cancelled) setState({ state: hit ? 'ready' : 'none', suggestion: hit })
      })
      .catch(() => { if (!cancelled) setState({ state: 'error', suggestion: null }) })
    return () => { cancelled = true }
  }, [tag, enabled])

  return state
}

// The memory an installed model needs, when the gallery can say. Installed
// models carry no size in the capabilities list, so this asks the same estimate
// endpoint by name; a model that is not a gallery entry answers with nothing and
// the chip simply shows no fit.
const needCache = new Map()

export function useModelNeed(modelId) {
  const [need, setNeed] = useState(() => needCache.get(modelId) ?? null)
  useEffect(() => {
    if (!modelId) { setNeed(null); return undefined }
    if (needCache.has(modelId)) { setNeed(needCache.get(modelId)); return undefined }
    let cancelled = false
    setNeed(null)
    modelsApi.estimate(modelId, [DEFAULT_CTX])
      .then((e) => {
        const ctx = e?.estimates?.[String(DEFAULT_CTX)]
        const value = ctx?.vramBytes > 0 ? ctx.vramBytes : null
        needCache.set(modelId, value)
        if (!cancelled) setNeed(value)
      })
      .catch(() => { needCache.set(modelId, null) })
    return () => { cancelled = true }
  }, [modelId])
  return need
}

// Test hook: forget what was looked up.
export function resetSuggestionCache() {
  cache.clear()
  needCache.clear()
}
