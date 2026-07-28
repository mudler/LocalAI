import { useState, useCallback } from 'react'
import { resourcesApi } from '../utils/api'
import { usePolling } from './usePolling'

export function useResources(pollInterval = 5000) {
  const [resources, setResources] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  const fetchResources = useCallback(async () => {
    try {
      const data = await resourcesApi.get()
      setResources(data)
      setError(null)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [])

  // Visibility-aware polling: pauses while the tab is hidden and catches up on
  // return (see usePolling). Resource stats are pure dashboard data, so there's
  // no reason to keep fetching them for a backgrounded tab.
  const { refetch } = usePolling(fetchResources, pollInterval)

  return { resources, loading, error, refetch }
}
