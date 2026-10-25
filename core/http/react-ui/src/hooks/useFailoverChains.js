import { useState, useEffect, useCallback, useMemo } from 'react'
import { failoverApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'

// Resync interval. SSE carries the live changes; the poll repairs anything
// missed while the stream was reconnecting.
const POLL_MS = 15_000

function parse(e) {
  try {
    return JSON.parse(e.data)
  } catch {
    return null
  }
}

// useFailoverChains returns the live health of every failover chain:
// { chains, byName, loading, error, refresh }. It seeds from GET /api/failover,
// applies /api/failover/events as they arrive, and re-lists every 15 s.
export default function useFailoverChains() {
  const [chains, setChains] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  const refresh = useCallback(async () => {
    try {
      const data = await failoverApi.list()
      setChains(data?.chains || [])
      setError(null)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, POLL_MS)
    return () => clearInterval(interval)
  }, [refresh])

  useEffect(() => {
    const es = new EventSource(apiUrl(failoverApi.eventsUrl()))

    es.addEventListener('snapshot', (e) => {
      const data = parse(e)
      if (data) setChains(data.chains || [])
    })

    es.addEventListener('chain.switched', (e) => {
      const data = parse(e)
      if (!data) return
      setChains(prev => prev.map(c => c.name === data.chain
        ? { ...c, active: data.to, state: data.state, active_since: data.at }
        : c))
    })

    // A model can be a target of several chains, so the patch applies to
    // every chain that lists it.
    es.addEventListener('target.state', (e) => {
      const data = parse(e)
      if (!data) return
      setChains(prev => prev.map(c => {
        if (!c.targets?.some(t => t.model === data.target)) return c
        return {
          ...c,
          targets: c.targets.map(t => t.model === data.target
            ? { ...t, state: data.to, last_error: data.error }
            : t),
        }
      }))
    })

    es.onerror = () => { /* the browser reconnects; the poll covers the gap */ }
    return () => es.close()
  }, [])

  const byName = useMemo(() => Object.fromEntries(chains.map(c => [c.name, c])), [chains])

  return { chains, byName, loading, error, refresh }
}
