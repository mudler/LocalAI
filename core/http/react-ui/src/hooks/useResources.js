import { useState, useEffect, useCallback, useRef } from 'react'
import { resourcesApi } from '../utils/api'

export function useResources(pollInterval = 5000) {
  const [resources, setResources] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const intervalRef = useRef(null)

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

  useEffect(() => {
    fetchResources()
    intervalRef.current = setInterval(fetchResources, pollInterval)
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [fetchResources, pollInterval])

  return { resources, loading, error, refetch: fetchResources }
}
