import { useCallback, useEffect, useRef, useState } from 'react'

// How long a cancel waits before anything is stopped. The server has no hold on
// a cancel (it ends the job and removes its partial data), so this wait in the
// browser is the whole undo. Pause is the call that keeps the data.
export const CANCEL_UNDO_MS = 8000

// A cancel that waits for its undo window, then runs.
//
//   operations    the live operations, so a job that finished while the window
//                 was open is not cancelled afterwards (that would only fail)
//   cancel        the real call, taking a jobID
//
// Only one waits at a time: asking for a second ends the first window, so a
// toast never hides another. Leaving the page runs the waiting cancel, because
// the person asked for it.
export function useUndoableCancel({ operations, cancel }) {
  const [waitingFor, setWaitingFor] = useState(null)
  const waiting = useRef(null)
  const live = useRef(operations)
  live.current = operations
  const cancelRef = useRef(cancel)
  cancelRef.current = cancel

  const commit = useCallback(() => {
    const entry = waiting.current
    if (!entry) return
    waiting.current = null
    setWaitingFor(null)
    if (live.current.some(op => op.jobID === entry.jobID)) cancelRef.current(entry.jobID)
  }, [])

  const request = useCallback((jobID) => {
    const op = live.current.find(o => o.jobID === jobID)
    if (!op) return
    commit()
    waiting.current = { jobID, name: op.name || op.id }
    setWaitingFor(waiting.current)
  }, [commit])

  const undo = useCallback(() => {
    waiting.current = null
    setWaitingFor(null)
  }, [])

  useEffect(() => () => {
    const entry = waiting.current
    if (entry && live.current.some(op => op.jobID === entry.jobID)) cancelRef.current(entry.jobID)
  }, [])

  return { waitingFor, request, undo, commit }
}
