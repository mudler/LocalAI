import { createContext, useContext, useState, useEffect, useCallback, useRef } from 'react'
import { operationsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'

// Serialize ops into a stable comparison key. Each op is a flat map of
// primitives, so JSON.stringify is good enough and stable as long as the
// server emits keys in the same order (Go's map iteration into JSON happens
// to be stable here because we build an explicit map[string]any).
function serializeOps(ops) {
  return JSON.stringify(ops)
}

const OperationsContext = createContext(null)

// Single shared poller for /api/operations. Before this provider existed,
// each useOperations() call ran its own setInterval; with OperationsBar
// always mounted plus the per-page consumers (Models, Backends, Chat), the
// browser was firing 2-3 polls per second against the API for the lifetime
// of the session.
export function OperationsProvider({ children, pollInterval = 1000 }) {
  const [operations, setOperations] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const { isAdmin } = useAuth()
  const intervalRef = useRef(null)
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

      setError((prev) => (prev === null ? prev : null))
    } catch (err) {
      setError((prev) => (prev === err.message ? prev : err.message))
    } finally {
      setLoading((prev) => (prev ? false : prev))
    }
  }, [isAdmin])

  useEffect(() => {
    if (!isAdmin) return
    fetchOperations()
    intervalRef.current = setInterval(fetchOperations, pollInterval)
    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current)
        intervalRef.current = null
      }
    }
  }, [fetchOperations, pollInterval, isAdmin])

  const cancelOperation = useCallback(async (jobID) => {
    try {
      await operationsApi.cancel(jobID)
      await fetchOperations()
    } catch (err) {
      setError(err.message)
    }
  }, [fetchOperations])

  const dismissFailedOp = useCallback(async (opId) => {
    try {
      const op = operations.find((o) => o.id === opId)
      if (op?.jobID) {
        await operationsApi.dismiss(op.jobID)
        await fetchOperations()
      }
    } catch {
      // Ignore dismiss errors
    }
  }, [operations, fetchOperations])

  const value = {
    operations,
    loading,
    error,
    cancelOperation,
    dismissFailedOp,
    refetch: fetchOperations,
  }

  return <OperationsContext.Provider value={value}>{children}</OperationsContext.Provider>
}

export function useOperations() {
  const ctx = useContext(OperationsContext)
  if (!ctx) throw new Error('useOperations must be used within OperationsProvider')
  return ctx
}
