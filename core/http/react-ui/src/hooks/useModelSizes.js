import { useEffect, useState } from 'react'
import { modelsApi } from '../utils/api'

// How many estimates to ask for at once. The same reason as the gallery page:
// an estimate can take seconds on a cold server and must not take every
// connection the browser has.
const CONCURRENCY = 4

// Download size of a gallery model, in bytes, or null when the gallery cannot
// say. The server reports no size for a model on disk, so the size of the files
// the gallery lists is the best figure there is. Kept for the page session: the
// gallery does not change under it, and the Installed table, the cleanup sheet
// and the Explore rows all ask about the same models.
const cache = new Map()
const inflight = new Set()

export function rememberSize(id, bytes) {
  if (id && typeof bytes === 'number' && bytes > 0) cache.set(id, bytes)
}

// useModelSizes returns { id: bytes } for the ids it knows so far, filling in
// the rest in the background. Only ids the gallery lists are asked about: any
// other would answer 404 and waste a slot.
export function useModelSizes(ids, enabled = true) {
  const [, bump] = useState(0)
  const key = enabled ? ids.join('\n') : ''

  useEffect(() => {
    if (!enabled) return undefined
    const queue = ids.filter(id => !cache.has(id) && !inflight.has(id))
    if (queue.length === 0) return undefined
    let cancelled = false
    let cursor = 0
    const worker = async () => {
      while (!cancelled && cursor < queue.length) {
        const id = queue[cursor++]
        inflight.add(id)
        try {
          const est = await modelsApi.estimate(id, [8192])
          if (typeof est?.sizeBytes === 'number' && est.sizeBytes > 0) cache.set(id, est.sizeBytes)
        } catch {
          // No figure is a legitimate answer: the row shows "unknown".
        } finally {
          inflight.delete(id)
        }
        if (!cancelled) bump(n => n + 1)
      }
    }
    for (let i = 0; i < Math.min(CONCURRENCY, queue.length); i++) worker()
    return () => { cancelled = true }
    // ids is summarised by key so a new array with the same members does not
    // restart the queue.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, enabled])

  const sizes = {}
  for (const id of ids) if (cache.has(id)) sizes[id] = cache.get(id)
  return sizes
}
