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

      // Separate active (non-failed) operations from failed ones
      const activeOps = ops.filter(op => !op.error)
      const failedOps = ops.filter(op => op.error)

      // Auto-refresh the page when all active operations complete (mirrors original behavior)
      // but not when there are still failed operations being shown
      if (previousCountRef.current > 0 && activeOps.length === 0 && failedOps.length === 0) {
        setTimeout(() => window.location.reload(), 1000)
      }
      previousCountRef.current = activeOps.length

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

  // Dismiss a failed operation (acknowledge the error and remove it)
  const dismissFailedOp = useCallback(async (opId) => {
    try {
      const op = operations.find(o => o.id === opId)
      if (op?.jobID) {
        await operationsApi.dismiss(op.jobID)
        await fetchOperations()
      }
    } catch {
      // Ignore dismiss errors
    }
  }, [operations, fetchOperations])

  useEffect(() => {
    fetchOperations()
    intervalRef.current = setInterval(fetchOperations, pollInterval)
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [fetchOperations, pollInterval])

  return { operations, loading, error, cancelOperation, dismissFailedOp, refetch: fetchOperations }
}
