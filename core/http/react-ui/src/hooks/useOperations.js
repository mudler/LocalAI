import { useState, useEffect, useCallback, useRef } from 'react'
import { operationsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'

// Serialize ops into a stable comparison key. Each op is a flat map of
// primitives, so JSON.stringify is good enough and stable as long as the
// server emits keys in the same order (Go's map iteration into JSON happens
// to be stable here because we build an explicit map[string]any).
function serializeOps(ops) {
  return JSON.stringify(ops)
}

export function useOperations(pollInterval = 1000) {
  const [operations, setOperations] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const intervalRef = useRef(null)
  const { isAdmin } = useAuth()

  const previousCountRef = useRef(0)
  const onAllCompleteRef = useRef(null)
  // Track the last payload we wrote into state. Each poll otherwise produces
  // a fresh array reference even when nothing changed, and that re-render
  // ripples into the Chat page — wiping the user's text selection mid-read
  // (#9904).
  const lastSerializedRef = useRef('[]')

  const fetchOperations = useCallback(async () => {
    if (!isAdmin) {
      setLoading((prev) => (prev ? false : prev))
      return
    }
    try {
      const data = await operationsApi.list()
      const ops = data?.operations || (Array.isArray(data) ? data : [])

      const serialized = serializeOps(ops)
      if (serialized !== lastSerializedRef.current) {
        lastSerializedRef.current = serialized
        setOperations(ops)
      }

      // Separate active (non-failed) operations from failed ones
      const activeOps = ops.filter(op => !op.error)
      const failedOps = ops.filter(op => op.error)

      // Notify when all operations complete (no active or failed remaining)
      if (previousCountRef.current > 0 && activeOps.length === 0 && failedOps.length === 0) {
        onAllCompleteRef.current?.()
      }
      previousCountRef.current = activeOps.length

      setError((prev) => (prev === null ? prev : null))
    } catch (err) {
      setError((prev) => (prev === err.message ? prev : err.message))
    } finally {
      setLoading((prev) => (prev ? false : prev))
    }
  }, [isAdmin])

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
    if (!isAdmin) return
    fetchOperations()
    intervalRef.current = setInterval(fetchOperations, pollInterval)
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [fetchOperations, pollInterval, isAdmin])

  // Allow callers to register a callback for when all operations finish
  const onAllComplete = useCallback((cb) => {
    onAllCompleteRef.current = cb
  }, [])

  return { operations, loading, error, cancelOperation, dismissFailedOp, refetch: fetchOperations, onAllComplete }
}
