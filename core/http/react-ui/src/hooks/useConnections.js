import { useCallback, useEffect, useState } from 'react'
import { connectionsApi } from '../utils/api'

// The connections of this server, for an administrator. `enabled: false` skips
// the request (the list is admin only, and nothing else needs it).
// refetch keeps the old list on screen while it loads again.
export function useConnections({ enabled = true } = {}) {
  const [connections, setConnections] = useState([])
  const [loading, setLoading] = useState(enabled)
  const [error, setError] = useState(null)

  const load = useCallback(async ({ silent = false } = {}) => {
    if (!silent) setLoading(true)
    try {
      const data = await connectionsApi.list()
      setConnections(Array.isArray(data?.data) ? data.data : [])
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
  const replace = useCallback((connection) => {
    setConnections(list => (list.some(c => c.id === connection.id)
      ? list.map(c => (c.id === connection.id ? connection : c))
      : [...list, connection]))
  }, [])

  return { connections, loading, error, refetch, replace }
}
