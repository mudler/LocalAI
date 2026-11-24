import { useCallback, useMemo, useState } from 'react'
import { systemApi } from '../utils/api'
import { usePolling } from './usePolling'

// The models the server holds in memory right now, polled the way Home polls
// them. A model in this set is "warm": a message to it starts at once. Any other
// installed model is "not loaded" and has to load first.
export function useLoadedModels(intervalMs = 5000) {
  const [loaded, setLoaded] = useState([])
  const read = useCallback(async () => {
    try {
      const info = await systemApi.info()
      setLoaded(Array.isArray(info?.loaded_models) ? info.loaded_models : [])
    } catch {
      // Keep the last reading: a missed poll must not turn every model cold.
    }
  }, [])
  const { refetch } = usePolling(read, intervalMs)
  const ids = useMemo(() => new Set(loaded.map(m => m.id)), [loaded])
  return { loaded, ids, refetch }
}
