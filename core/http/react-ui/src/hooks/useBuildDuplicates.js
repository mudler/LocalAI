import { useEffect, useState } from 'react'
import { modelsApi } from '../utils/api'
import { findDuplicates } from '../utils/cleanupPlan'

const CONCURRENCY = 3

// Which installed models are an extra build of another installed model.
//
// The gallery says, for an entry that declares variants, which builds exist and
// which one it would pick on this host. Only entries that are installed
// themselves are asked, so a gallery of a thousand models costs a handful of
// requests. A build installed without its parent entry is not found this way;
// that is a known gap, not a claim.
//
// Returns { duplicates, loading }.
export function useBuildDuplicates(installedIds, hasVariants, enabled) {
  const [described, setDescribed] = useState({})
  const [loading, setLoading] = useState(false)
  const candidates = enabled ? installedIds.filter(id => hasVariants(id)) : []
  const key = candidates.join('\n')

  useEffect(() => {
    if (!enabled) return undefined
    const todo = candidates.filter(id => !(id in described))
    if (todo.length === 0) return undefined
    let cancelled = false
    let cursor = 0
    setLoading(true)
    const worker = async () => {
      while (!cancelled && cursor < todo.length) {
        const id = todo[cursor++]
        let value = null
        try {
          value = await modelsApi.variants(id)
        } catch {
          // Unknown: this entry simply contributes no duplicates.
        }
        if (!cancelled) setDescribed(prev => ({ ...prev, [id]: value }))
      }
    }
    Promise.all(Array.from({ length: Math.min(CONCURRENCY, todo.length) }, worker))
      .then(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
    // candidates is summarised by key.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, enabled])

  const usable = {}
  for (const id of candidates) if (described[id]) usable[id] = described[id]
  return { duplicates: findDuplicates(installedIds, usable), loading }
}
