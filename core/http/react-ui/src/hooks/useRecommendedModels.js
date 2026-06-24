import { useState, useEffect } from 'react'
import { modelsApi } from '../utils/api'
import { useResources } from './useResources'

// Data-driven "recommended for your hardware" model picks. The gallery exposes
// no popularity/download signal and the list response carries no size, so we:
//   1. ask the server for chat-capable models in their natural (curated) order,
//   2. estimate size/VRAM for the top candidates (same endpoint the Models page
//      uses), and
//   3. rank by hardware fit — smallest on CPU-only boxes, largest-that-fits on
//      GPUs (bigger == better quality while still fitting VRAM).
//
// Returns `recommended === null` while loading, `[]` when nothing could be
// resolved (gallery/estimates unavailable) so callers can fall back.

const GB = 1024 * 1024 * 1024
const DEFAULT_CTX = 4096

export function recommendTier(resources) {
  const isGpu = resources?.type === 'gpu'
  const vram = resources?.aggregate?.total_memory || 0
  if (!isGpu || vram <= 0) return { id: 'cpu', vram: 0 }
  if (vram < 8 * GB) return { id: 'gpu-small', vram }
  return { id: 'gpu-large', vram }
}

function rank(candidates, tier, count) {
  const withSize = candidates.filter(c => c.sizeBytes != null)
  if (tier.id === 'cpu') {
    // No GPU: smallest models stay responsive on CPU.
    return [...withSize].sort((a, b) => a.sizeBytes - b.sizeBytes).slice(0, count)
  }
  const limit = tier.vram * 0.95
  const fits = withSize.filter(c => c.vramBytes != null && c.vramBytes <= limit)
  if (fits.length > 0) {
    // Largest model that still fits VRAM is the best quality/speed trade.
    return [...fits].sort((a, b) => b.sizeBytes - a.sizeBytes).slice(0, count)
  }
  // Tiny GPU where nothing in the pool fits — offer the smallest instead.
  return [...withSize].sort((a, b) => a.sizeBytes - b.sizeBytes).slice(0, count)
}

export function useRecommendedModels({ count = 4, candidatePool = 10 } = {}) {
  const { resources } = useResources()
  const [recommended, setRecommended] = useState(null)
  const [error, setError] = useState(null)

  const resReady = resources !== null
  const tier = recommendTier(resources)

  useEffect(() => {
    if (!resReady) return
    let cancelled = false
    setRecommended(null)
    setError(null)
    ;(async () => {
      try {
        const data = await modelsApi.list({ tag: 'chat', items: candidatePool, page: 1 })
        // Recommend models the user hasn't installed yet.
        const models = (data?.models || []).filter(m => !m.installed)
        const estimated = await Promise.all(models.map(async (m) => {
          const name = m.name || m.id
          try {
            const e = await modelsApi.estimate(name, [DEFAULT_CTX])
            const ctx = e?.estimates?.[String(DEFAULT_CTX)]
            return {
              name,
              description: m.description,
              sizeBytes: e?.sizeBytes ?? null,
              sizeDisplay: e?.sizeDisplay ?? null,
              vramBytes: ctx?.vramBytes ?? null,
              vramDisplay: ctx?.vramDisplay ?? null,
            }
          } catch {
            return { name, sizeBytes: null }
          }
        }))
        if (cancelled) return
        setRecommended(rank(estimated, tier, count))
      } catch (e) {
        if (cancelled) return
        setError(e.message)
        setRecommended([])
      }
    })()
    return () => { cancelled = true }
    // tier.id / tier.vram are primitives, so resource polling doesn't re-run this.
  }, [resReady, tier.id, tier.vram, count, candidatePool])

  return { recommended, tier, error, loading: recommended === null }
}
