import { useEffect, useState } from 'react'
import { resourcesApi } from '../utils/api'

// The memory kind of this server's GPU (unified, discrete or system) and the
// memory a model can use, from the `memory` block of GET /api/resources:
// { kind, device, total }. Null until it answers, or when it fails; the
// quick setup then uses the schema's fallback kind.
export function useHostMemory() {
  const [memory, setMemory] = useState(null)
  useEffect(() => {
    let cancelled = false
    resourcesApi.get()
      .then(r => { if (!cancelled && r?.memory?.kind) setMemory(r.memory) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [])
  return memory
}
