import { createContext, useContext, useState, useEffect, useCallback, useMemo, useRef } from 'react'
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
  const [history, setHistory] = useState([])
  const [historyLoading, setHistoryLoading] = useState(false)
  const { isAdmin } = useAuth()
  const intervalRef = useRef(null)
  const lastSerializedRef = useRef('[]')
  const liveCountRef = useRef(0)

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
      if (ops.length < liveCountRef.current) {
        fetchHistory()
      }
      liveCountRef.current = ops.length

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

  // Time remaining is derived, not reported. We keep the previous
  // (bytes, timestamp) sample per job and estimate from the delta.
  //
  // All or nothing on purpose: an estimate needs two samples, and one card
  // showing "11 min left" while its neighbours show nothing reads as a
  // rendering bug rather than as missing data.
  const samplesRef = useRef(new Map())
  const operationsWithEta = useMemo(() => {
    const now = Date.now()
    const samples = samplesRef.current
    const seen = new Set()

    const withEta = operations.map((op) => {
      const key = op.jobID || op.id
      seen.add(key)
      const current = op.currentBytes
      const total = op.totalBytes
      if (!Number.isFinite(current) || !Number.isFinite(total) || total <= 0) return op

      const previous = samples.get(key)
      samples.set(key, { bytes: current, at: now })
      if (!previous || current <= previous.bytes) return op

      const bytesPerMs = (current - previous.bytes) / Math.max(1, now - previous.at)
      if (bytesPerMs <= 0) return op
      return { ...op, etaSeconds: Math.round((total - current) / bytesPerMs / 1000) }
    })

    // Drop samples for jobs that finished, so the map cannot grow forever.
    for (const key of samples.keys()) {
      if (!seen.has(key)) samples.delete(key)
    }

    // All or nothing: if any byte-tracked operation has no estimate yet,
    // nobody shows one this tick.
    const tracked = withEta.filter((op) => Number.isFinite(op.totalBytes) && op.totalBytes > 0)
    if (tracked.length > 0 && tracked.some((op) => op.etaSeconds === undefined)) {
      return withEta.map(({ etaSeconds: _etaSeconds, ...op }) => op)
    }
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
