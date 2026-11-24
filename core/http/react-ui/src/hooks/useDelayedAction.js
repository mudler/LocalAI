import { useCallback, useEffect, useRef, useState } from 'react'

// How long a delayed action waits before anything is sent.
export const UNDO_WINDOW_MS = 10000

// An action that waits for its undo window, then runs. Nothing goes to the
// server until the window ends, so Undo is not a second call: the first was
// never made.
//
//   run       the real call, given the key it was scheduled under
//
// Several actions can wait at once, each under its own key. Leaving the page
// runs the ones still waiting, because the person asked for them.
export function useDelayedAction(run, ms = UNDO_WINDOW_MS) {
  const [waiting, setWaiting] = useState({})
  const timers = useRef(new Map())
  const runRef = useRef(run)
  runRef.current = run

  const settle = useCallback((key, doRun) => {
    const entry = timers.current.get(key)
    if (!entry) return
    clearTimeout(entry.timer)
    timers.current.delete(key)
    setWaiting(prev => { const next = { ...prev }; delete next[key]; return next })
    if (doRun) runRef.current(key, entry.payload)
  }, [])

  const schedule = useCallback((key, payload) => {
    if (timers.current.has(key)) return
    const timer = setTimeout(() => settle(key, true), ms)
    timers.current.set(key, { timer, payload })
    setWaiting(prev => ({ ...prev, [key]: { payload, until: Date.now() + ms } }))
  }, [ms, settle])

  const cancel = useCallback((key) => settle(key, false), [settle])
  const flush = useCallback((key) => settle(key, true), [settle])

  useEffect(() => {
    const live = timers.current
    return () => {
      for (const [key, entry] of live) {
        clearTimeout(entry.timer)
        runRef.current(key, entry.payload)
      }
      live.clear()
    }
  }, [])

  return { waiting, schedule, cancel, flush }
}
