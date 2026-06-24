import { useEffect, useRef, useCallback } from 'react'

// usePolling runs `fn` immediately and then on a fixed interval, with two
// behaviours every hand-rolled setInterval in this app was missing:
//
//   1. Visibility-aware: the timer pauses while the tab is hidden
//      (document.hidden) and fires an immediate catch-up poll when the tab
//      becomes visible again. A backgrounded dashboard no longer hammers the
//      server every few seconds for data nobody is looking at.
//   2. Non-overlapping: if `fn` returns a promise that takes longer than the
//      interval, the next tick waits for it instead of stacking requests.
//
// `enabled: false` stops polling entirely (one-shot or gated polls). The
// returned `refetch` runs `fn` on demand and is stable across renders.
export function usePolling(fn, intervalMs = 5000, { enabled = true, immediate = true } = {}) {
  const fnRef = useRef(fn)
  fnRef.current = fn

  const runningRef = useRef(false)
  const refetch = useCallback(async () => {
    // Guard against overlap: a slow poll shouldn't pile up behind a fast timer.
    if (runningRef.current) return
    runningRef.current = true
    try {
      return await fnRef.current()
    } finally {
      runningRef.current = false
    }
  }, [])

  useEffect(() => {
    if (!enabled) return
    let timer = null

    const tick = () => { refetch() }

    const start = () => {
      if (timer != null) return
      timer = setInterval(tick, intervalMs)
    }
    const stop = () => {
      if (timer != null) { clearInterval(timer); timer = null }
    }

    const onVisibility = () => {
      if (document.hidden) {
        stop()
      } else {
        // Catch up immediately on return, then resume the cadence.
        tick()
        start()
      }
    }

    if (immediate) tick()
    if (!document.hidden) start()
    document.addEventListener('visibilitychange', onVisibility)

    return () => {
      stop()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [enabled, intervalMs, immediate, refetch])

  return { refetch }
}
