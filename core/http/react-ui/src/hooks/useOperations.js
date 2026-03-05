import { useState, useEffect, useCallback, useRef } from 'react'
import { operationsApi } from '../utils/api'

export function useOperations(pollInterval = 1000) {
  const [operations, setOperations] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const intervalRef = useRef(null)

  const previousCountRef = useRef(0)

  const fetchOperations = useCallback(async () => {
    try {
      const data = await operationsApi.list()
      const ops = data?.operations || (Array.isArray(data) ? data : [])
      setOperations(ops)
      // Auto-refresh the page when all operations complete (mirrors original behavior)
      if (previousCountRef.current > 0 && ops.length === 0) {
        setTimeout(() => window.location.reload(), 1000)
      }
      previousCountRef.current = ops.length
      setError(null)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [])

  const cancelOperation = useCallback(async (jobID) => {
    try {
      await operationsApi.cancel(jobID)
      await fetchOperations()
    } catch (err) {
      setError(err.message)
    }
  }, [fetchOperations])

  useEffect(() => {
    fetchOperations()
    intervalRef.current = setInterval(fetchOperations, pollInterval)
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [fetchOperations, pollInterval])

  return { operations, loading, error, cancelOperation, refetch: fetchOperations }
}
