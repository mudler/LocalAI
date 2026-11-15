import { useCallback, useEffect, useState } from 'react'
import { remotesApi } from '../utils/api'

// The remotes of this server, for an administrator. `enabled: false` skips
// the request (the list is admin only, and nothing else needs it).
// refetch keeps the old list on screen while it loads again.
export function useRemotes({ enabled = true } = {}) {
  const [remotes, setRemotes] = useState([])
  const [loading, setLoading] = useState(enabled)
  const [error, setError] = useState(null)

  const load = useCallback(async ({ silent = false } = {}) => {
    if (!silent) setLoading(true)
    try {
      const data = await remotesApi.list()
      setRemotes(Array.isArray(data?.data) ? data.data : [])
      setError(null)
    } catch (err) {
      setError(err.message || 'Failed')
    } finally {
      if (!silent) setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!enabled) { setLoading(false); return }
    load()
  }, [enabled, load])

  const refetch = useCallback(() => load({ silent: true }), [load])
  // A card that was changed replaces its old self in the list at once.
  const replace = useCallback((remote) => {
    setRemotes(list => (list.some(c => c.id === remote.id)
      ? list.map(c => (c.id === remote.id ? remote : c))
      : [...list, remote]))
  }, [])

  return { remotes, loading, error, refetch, replace }
}
