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

// NVFP4 is a Blackwell/NVIDIA-specific 4-bit format — only worth suggesting on
// NVIDIA hardware, and to be filtered out elsewhere.
export const isNvfp4Name = (name) => /nvfp4/i.test(name || '')

export function hasNvidiaGpu(resources) {
  return Array.isArray(resources?.gpus) &&
    resources.gpus.some(g => (g?.vendor || '').toLowerCase() === 'nvidia')
}

export function recommendTier(resources) {
  const isGpu = resources?.type === 'gpu'
  const vram = resources?.aggregate?.total_memory || 0
  if (!isGpu || vram <= 0) return { id: 'cpu', vram: 0 }
  if (vram < 8 * GB) return { id: 'gpu-small', vram }
  if (vram < 24 * GB) return { id: 'gpu-mid', vram }
  return { id: 'gpu-large', vram }
}

function rank(candidates, tier, count, isNvidia) {
  // NVFP4 only runs on NVIDIA (Blackwell) — drop it everywhere else, and prefer
  // it on NVIDIA boxes where it's the fastest path.
  const pool = candidates.filter(c => c.sizeBytes != null && (isNvidia || !isNvfp4Name(c.name)))
  if (tier.id === 'cpu') {
    // No GPU: smallest models stay responsive on CPU.
    return [...pool].sort((a, b) => a.sizeBytes - b.sizeBytes).slice(0, count)
  }
  const limit = tier.vram * 0.95
  const fits = pool.filter(c => c.vramBytes != null && c.vramBytes <= limit)
  const base = fits.length > 0 ? fits : pool // tiny GPU where nothing fits → fall through to smallest
  const byPreference = (a, b) => {
    // On NVIDIA, surface NVFP4 first; then largest-that-fits (best quality).
    if (isNvidia) {
      const an = isNvfp4Name(a.name), bn = isNvfp4Name(b.name)
      if (an !== bn) return an ? -1 : 1
    }
    return fits.length > 0 ? b.sizeBytes - a.sizeBytes : a.sizeBytes - b.sizeBytes
  }
  return [...base].sort(byPreference).slice(0, count)
}

export function useRecommendedModels({ count = 4, candidatePool = 10 } = {}) {
  const { resources } = useResources()
  const [recommended, setRecommended] = useState(null)
  const [error, setError] = useState(null)

  const resReady = resources !== null
  const tier = recommendTier(resources)
  const isNvidia = hasNvidiaGpu(resources)

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
        setRecommended(rank(estimated, tier, count, isNvidia))
      } catch (e) {
        if (cancelled) return
        setError(e.message)
        setRecommended([])
      }
    })()
    return () => { cancelled = true }
    // tier.id / tier.vram / isNvidia are primitives, so resource polling doesn't re-run this.
  }, [resReady, tier.id, tier.vram, isNvidia, count, candidatePool])

  return { recommended, tier, isNvidia, error, loading: recommended === null }
}
