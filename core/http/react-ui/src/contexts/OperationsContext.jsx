import { createContext, useContext, useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { operationsApi } from '../utils/api'
import { createTransferRateSampler } from '../utils/transferRate'
import { addonUpgrades } from '../utils/addonUpgrades'
import { createAddonsApi } from '../utils/addonsApi'
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
// each useOperations() call ran its own setInterval; with the inbox
// always mounted plus the per-page consumers (Models, Backends, Chat), the
// browser was firing 2-3 polls per second against the API for the lifetime
// of the session.
export function OperationsProvider({ children, pollInterval = 1000 }) {
  const [operations, setOperations] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [history, setHistory] = useState([])
  const [historyLoading, setHistoryLoading] = useState(false)
  const { isAdmin, user, authEnabled, loading: authLoading } = useAuth()
  useEffect(() => {
    if (authLoading) return
    const principal = isAdmin ? (user?.id || (!authEnabled ? "local-owner" : "")) : ""
    addonUpgrades.bind(createAddonsApi(), principal, window.sessionStorage)
  }, [isAdmin, user?.id, authEnabled, authLoading])
  const intervalRef = useRef(null)
  const lastSerializedRef = useRef('[]')
  const liveIDsRef = useRef(new Set())
  // History is fetched on demand, never on the poll interval: it only changes
  // when an operation finishes, and the Activity page is the only consumer.
  const fetchHistory = useCallback(async () => {
    if (!isAdmin) return
    setHistoryLoading(true)
    try {
      const data = await operationsApi.history()
      setHistory(data?.operations || [])
    } catch (err) {
      setError((prev) => (prev === err.message ? prev : err.message))
    } finally {
      setHistoryLoading(false)
    }
  }, [isAdmin])

  const clearHistory = useCallback(async () => {
    try {
      await operationsApi.clearHistory()
      setHistory([])
    } catch (err) {
      setError(err.message)
    }
  }, [])

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

      // An operation leaving the live list is the one moment the record can
      // have changed. Refetching here keeps the page correct without polling
      // a second endpoint every second.
      //
      // Tracked by identity rather than by count: during a batch install one
      // operation finishing in the same second another starts leaves the
      // length unchanged, and a count comparison would miss the completion.
      const liveIDs = new Set(ops.map((op) => op.jobID || op.id))
      let departed = false
      for (const id of liveIDsRef.current) {
        if (!liveIDs.has(id)) {
          departed = true
          break
        }
      }
      liveIDsRef.current = liveIDs
      if (departed) {
        fetchHistory()
      }

      setError((prev) => (prev === null ? prev : null))
    } catch (err) {
      setError((prev) => (prev === err.message ? prev : err.message))
    } finally {
      setLoading((prev) => (prev ? false : prev))
    }
  }, [isAdmin, fetchHistory])

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

  const pauseOperation = useCallback(async (jobID) => {
    try {
      await operationsApi.pause(jobID)
      await fetchOperations()
    } catch (err) {
      setError(err.message)
    }
  }, [fetchOperations])

  // Takes the jobID, never the display id. /api/operations strips the
  // "node:<nodeID>:" prefix before emitting, so a local install and a
  // node-scoped install of the same backend arrive as two distinct jobs
  // sharing one id: looking the job up by id could dismiss the wrong one,
  // leaving the failure the user acted on live and silently retiring another.
  const dismissFailedOp = useCallback(async (jobID) => {
    if (!jobID) return
    try {
      await operationsApi.dismiss(jobID)
      await fetchOperations()
    } catch {
      // Ignore dismiss errors
    }
  }, [fetchOperations])

  const transferRateRef = useRef(createTransferRateSampler())
  const operationsWithEta = useMemo(() => {
    const now = Date.now()
    const seen = new Set()

    const withEta = operations.map((op) => {
      const key = op.jobID || op.id
      seen.add(key)
      const metrics = transferRateRef.current.sample(key, op.currentBytes, op.totalBytes, now)
      return Object.keys(metrics).length > 0 ? { ...op, ...metrics } : op
    })

    transferRateRef.current.retain(seen)
    return withEta
  }, [operations])

  const value = {
    operations: operationsWithEta,
    loading,
    error,
    history,
    historyLoading,
    fetchHistory,
    clearHistory,
    cancelOperation,
    pauseOperation,
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
